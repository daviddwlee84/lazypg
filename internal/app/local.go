package app

import (
	"context"
	"fmt"
	"time"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/dbops"
	"github.com/daviddwlee84/lazypg/internal/env"
	"github.com/daviddwlee84/lazypg/internal/instance"
	"github.com/daviddwlee84/lazypg/internal/state"
)

// BasePort is where local port allocation starts.
const BasePort = 54320

// EnsureLocal allocates a port if needed, starts the container, and waits
// until PostgreSQL accepts connections. t must be a managed target.
func (a *App) EnsureLocal(ctx context.Context, t *env.Target) error {
	if !t.Managed() {
		return fmt.Errorf("env %s is not a lazypg-managed instance", t.Env)
	}
	if t.Kind == env.KindServer {
		srv, err := EnsureServer(ctx, a.Project.Local.Server, a.Project.Local.Image, 0, a.Logf)
		if err != nil {
			return err
		}
		t.Compose, t.Port = instance.ForServer(srv), srv.Port
		return nil
	}
	if a.State.Port == 0 {
		port, err := state.FreePort(BasePort, state.ReservedPorts())
		if err != nil {
			return err
		}
		a.State.Port = port
		if err := a.SaveState(); err != nil {
			return err
		}
	}
	t.Port = a.State.Port
	t.Compose = instance.ForProject(a.Project, a.State)
	if s, _ := t.Compose.State(ctx); s != "running" {
		a.Out.Info("starting %s (%s) on 127.0.0.1:%d …", t.Compose.Project, t.Compose.Image, t.Port)
		if err := t.Compose.Up(ctx); err != nil {
			return err
		}
	}
	return dbops.WaitReady(ctx, t, 60*time.Second)
}

// EnsureServer creates (if needed) and starts a named machine-level server.
// image and port only apply when the server is first created.
func EnsureServer(ctx context.Context, name, image string, port int, logf func(string, ...any)) (*state.Server, error) {
	srv, existed, err := state.LoadServer(name)
	if err != nil {
		return nil, err
	}
	if !existed {
		if image == "" {
			image = config.DefaultImage
		}
		if port == 0 {
			if port, err = state.FreePort(BasePort, state.ReservedPorts()); err != nil {
				return nil, err
			}
		}
		srv.Image, srv.Port = image, port
		if err := state.SaveServer(srv); err != nil {
			return nil, err
		}
	} else if image != "" && image != srv.Image && logf != nil {
		logf("server %q runs %s; this project asked for %s (servers keep their image; use another server name for a different version)", name, srv.Image, image)
	}
	c := instance.ForServer(srv)
	if s, _ := c.State(ctx); s != "running" {
		if logf != nil {
			logf("starting server %q (%s) on 127.0.0.1:%d …", name, srv.Image, srv.Port)
		}
		if err := c.Up(ctx); err != nil {
			return nil, err
		}
	}
	t := &env.Target{Host: "127.0.0.1", Port: srv.Port, User: instance.LocalUser, Password: instance.LocalPassword, SSLMode: "disable", MaintDB: "postgres"}
	return srv, dbops.WaitReady(ctx, t, 60*time.Second)
}

// PlanHost returns a running lazypg-managed target that pgschema can use to
// validate desired state, ensuring the scratch database and the extensions
// it needs. It returns nil when no local instance is running; pgschema then
// falls back to its embedded PostgreSQL.
func (a *App) PlanHost(ctx context.Context, target *env.Target, exts []string) (*env.Target, error) {
	host := target
	if !target.Managed() {
		local, err := a.Resolver.Resolve(ctx, config.EnvLocal)
		if err != nil {
			return nil, err
		}
		if local.Port == 0 {
			return nil, nil
		}
		if s, _ := local.Compose.State(ctx); s != "running" {
			return nil, nil
		}
		host = local
	}
	if _, err := dbops.Create(ctx, host, config.PlanDB, ""); err != nil {
		return nil, err
	}
	if err := dbops.Bootstrap(ctx, host, config.PlanDB, nil, exts); err != nil {
		return nil, fmt.Errorf("prepare %s: %w", config.PlanDB, err)
	}
	return host, nil
}
