// Package env resolves an environment name (local, dev, stage, prod, …) into
// a concrete connection target, and enforces per-tier safety policy.
package env

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/instance"
	"github.com/daviddwlee84/lazypg/internal/state"
	"github.com/daviddwlee84/lazypg/internal/xdg"
)

// Kinds of targets.
const (
	KindProject  = "project"  // per-checkout container
	KindServer   = "server"   // machine-level shared container
	KindExternal = "external" // any reachable PostgreSQL server
)

// Target is a resolved environment: where to connect and how careful to be.
type Target struct {
	Env      string
	Tier     string
	Kind     string
	Instance string // alias shown in banners: "acme/prod", "server:default", "local"
	Host     string
	Port     int
	User     string
	Password string `json:"-"`
	SSLMode  string
	MaintDB  string
	Shared   bool   // physical database names are prefixed with the project name
	Branch   string // database branch suffix (local/dev only)

	DBNames map[string]string // env-specific physical name overrides

	// Compose is set for lazypg-managed containers.
	Compose *instance.Compose
}

// Managed reports whether lazypg owns the container behind this target.
func (t *Target) Managed() bool { return t.Compose != nil }

// Resolver turns env names into targets.
type Resolver struct {
	Project *config.Project
	User    *config.User
	Creds   config.Credentials
	State   *state.Project
}

// LoadResolver reads user config and credentials from XDG.
func LoadResolver(p *config.Project, st *state.Project) (*Resolver, error) {
	r := &Resolver{Project: p, User: &config.User{}, Creds: config.Credentials{}, State: st}
	if err := config.LoadTOML(UserConfigPath(), r.User); err != nil {
		return nil, err
	}
	if err := config.LoadTOML(CredentialsPath(), &r.Creds); err != nil {
		return nil, err
	}
	return r, nil
}

// UserConfigPath is ~/.config/lazypg/config.toml.
func UserConfigPath() string { return filepath.Join(xdg.ConfigDir(), "config.toml") }

// CredentialsPath is ~/.config/lazypg/credentials.toml.
func CredentialsPath() string { return filepath.Join(xdg.ConfigDir(), "credentials.toml") }

// OrgDir returns the directory holding an org's lazypg-org.toml.
func OrgDir(u *config.User, org string) (string, error) {
	ref, ok := u.Orgs[org]
	if !ok {
		return "", fmt.Errorf("org %q is not registered on this machine (run `%s org add %s --git <url>` or `--path <dir>`)", org, config.AppName, org)
	}
	if ref.Path != "" {
		return expandHome(ref.Path), nil
	}
	return filepath.Join(xdg.DataDir(), "orgs", org), nil
}

// Current returns the env to use when --env is not given: the sticky
// selection from `env use`, or local.
func (r *Resolver) Current() string {
	if r.State != nil && r.State.Env != "" {
		if _, ok := r.Project.Envs[r.State.Env]; ok {
			return r.State.Env
		}
	}
	return config.EnvLocal
}

// Resolve resolves an env name. An empty name means Current().
func (r *Resolver) Resolve(ctx context.Context, name string) (*Target, error) {
	if name == "" {
		name = r.Current()
	}
	if name == config.EnvLocal {
		return r.resolveLocal()
	}
	spec, ok := r.Project.Envs[name]
	if !ok {
		return nil, fmt.Errorf("unknown env %q (known: %s)", name, strings.Join(r.Project.EnvNames(), ", "))
	}
	t := &Target{Env: name, Tier: spec.Tier, Kind: KindExternal, Instance: spec.Instance, DBNames: spec.DBNames}
	if r.State != nil && spec.Tier == config.TierDev {
		t.Branch = r.State.Branches[name]
	}

	// CI escape hatch: LAZYPG_URL_<ENV> supplies the whole connection.
	if raw := os.Getenv(URLEnvVar(name)); raw != "" {
		if err := t.applyURL(raw); err != nil {
			return nil, fmt.Errorf("%s: %w", URLEnvVar(name), err)
		}
		return t, nil
	}

	inst, err := r.instance(spec.Instance)
	if err != nil {
		return nil, fmt.Errorf("env %q: %w", name, err)
	}
	if inst.Tier != "" && inst.Tier != spec.Tier {
		return nil, fmt.Errorf("env %q has tier %q but instance %q is declared as tier %q; refusing to guess which is right", name, spec.Tier, spec.Instance, inst.Tier)
	}
	inst = inst.WithDefaults()
	t.Host, t.Port, t.User, t.SSLMode, t.MaintDB, t.Shared = inst.Host, inst.Port, inst.User, inst.SSLMode, inst.MaintDB, inst.Shared
	if cred, ok := r.Creds[spec.Instance]; ok {
		if cred.User != "" {
			t.User = cred.User
		}
		pw, err := resolvePassword(ctx, cred)
		if err != nil {
			return nil, fmt.Errorf("credentials for %q: %w", spec.Instance, err)
		}
		t.Password = pw
	}
	return t, nil
}

// URLEnvVar names the env var that overrides a whole env connection, e.g. LAZYPG_URL_PROD.
func URLEnvVar(env string) string {
	return strings.ToUpper(config.AppName) + "_URL_" + strings.ToUpper(env)
}

func (t *Target) applyURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return fmt.Errorf("scheme must be postgres://")
	}
	t.Host = u.Hostname()
	t.Port = 5432
	if p := u.Port(); p != "" {
		if t.Port, err = strconv.Atoi(p); err != nil {
			return err
		}
	}
	t.User = u.User.Username()
	t.Password, _ = u.User.Password()
	t.SSLMode = u.Query().Get("sslmode")
	if t.SSLMode == "" {
		t.SSLMode = "prefer"
	}
	t.MaintDB = "postgres"
	t.Instance = "$" + URLEnvVar(t.Env)
	return nil
}

func (r *Resolver) instance(alias string) (config.Instance, error) {
	org, name := config.SplitAlias(alias)
	if org == "" {
		inst, ok := r.User.Instances[name]
		if !ok {
			return config.Instance{}, fmt.Errorf("instance %q is not defined in %s", name, UserConfigPath())
		}
		return inst, nil
	}
	dir, err := OrgDir(r.User, org)
	if err != nil {
		return config.Instance{}, err
	}
	o, err := config.LoadOrg(dir)
	if err != nil {
		return config.Instance{}, err
	}
	inst, ok := o.Instances[name]
	if !ok {
		return config.Instance{}, fmt.Errorf("org %q has no instance %q", org, name)
	}
	return inst, nil
}

func resolvePassword(ctx context.Context, c config.Credential) (string, error) {
	switch {
	case c.PasswordCmd != "":
		out, err := exec.CommandContext(ctx, "sh", "-c", c.PasswordCmd).Output()
		if err != nil {
			return "", fmt.Errorf("password_cmd failed: %w", err)
		}
		return strings.TrimRight(string(out), "\r\n"), nil
	case c.PasswordEnv != "":
		v, ok := os.LookupEnv(c.PasswordEnv)
		if !ok {
			return "", fmt.Errorf("password_env %s is not set", c.PasswordEnv)
		}
		return v, nil
	}
	return c.Password, nil
}

func (r *Resolver) resolveLocal() (*Target, error) {
	p := r.Project
	t := &Target{
		Env: config.EnvLocal, Tier: config.TierLocal,
		Host: "127.0.0.1", User: instance.LocalUser, Password: instance.LocalPassword,
		SSLMode: "disable", MaintDB: "postgres",
	}
	if r.State != nil {
		t.Branch = r.State.Branches[config.EnvLocal]
	}
	if p.Local.Mode == config.ModeServer {
		srv, _, err := state.LoadServer(p.Local.Server)
		if err != nil {
			return nil, err
		}
		if srv.Image == "" {
			srv.Image = p.Local.Image
		}
		t.Kind, t.Shared = KindServer, true
		t.Instance = "server:" + srv.Name
		t.Compose = instance.ForServer(srv)
		t.Port = srv.Port
		return t, nil
	}
	t.Kind, t.Instance = KindProject, "local"
	t.Compose = instance.ForProject(p, r.State)
	t.Port = r.State.Port
	return t, nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}
