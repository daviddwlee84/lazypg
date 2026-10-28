// Package app holds lazypg's operations. CLI commands are thin wrappers that
// parse flags and call into App, so a future TUI can share the same services.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/dbops"
	"github.com/daviddwlee84/lazypg/internal/env"
	"github.com/daviddwlee84/lazypg/internal/output"
	"github.com/daviddwlee84/lazypg/internal/state"
)

// App is a loaded project plus the user's machine context.
type App struct {
	Project  *config.Project
	State    *state.Project
	Resolver *env.Resolver
	Out      *output.Printer
	Prompt   env.Prompt
	// EnvFlag is the --env value ("" = sticky selection or local).
	EnvFlag string
}

// Options are the global flags.
type Options struct {
	Dir     string
	Env     string
	JSON    bool
	Yes     bool
	Confirm string
}

// Load finds the project from opts.Dir and loads all configuration.
func Load(opts Options) (*App, error) {
	dir, err := config.FindProject(opts.Dir)
	if err != nil {
		return nil, err
	}
	p, err := config.LoadProject(dir)
	if err != nil {
		return nil, err
	}
	st, err := state.LoadProject(p)
	if err != nil {
		return nil, err
	}
	r, err := env.LoadResolver(p, st)
	if err != nil {
		return nil, err
	}
	out := output.New(opts.JSON)
	var interactive *bool
	if opts.JSON {
		no := false
		interactive = &no
	}
	return &App{
		Project: p, State: st, Resolver: r, Out: out, EnvFlag: opts.Env,
		Prompt: env.Prompt{Yes: opts.Yes, Confirm: opts.Confirm, In: os.Stdin, Out: os.Stderr, Interactive: interactive},
	}, nil
}

// Target resolves the selected environment.
func (a *App) Target(ctx context.Context) (*env.Target, error) {
	return a.Resolver.Resolve(ctx, a.EnvFlag)
}

// Banner prints the target line for a database.
func (a *App) Banner(t *env.Target, db string) {
	a.Out.Banner(t.Env, t.Tier, t.Instance, db)
}

// Check enforces tier policy.
func (a *App) Check(t *env.Target, op env.Op, what string) error {
	return env.Check(t, op, what, a.Prompt)
}

// Audit records an operation against a non-local env. Audit failures are
// reported as warnings and never fail the operation.
func (a *App) Audit(t *env.Target, op env.Op, db, detail string, err error) {
	if t.Tier == config.TierLocal {
		return
	}
	e := state.AuditEntry{
		Project: a.Project.Project, Env: t.Env, Tier: t.Tier, Instance: t.Instance,
		Op: string(op), Database: db, Detail: t.Redact(detail), OK: err == nil,
	}
	if err != nil {
		e.Error = t.Redact(err.Error())
	}
	if aerr := state.Audit(e); aerr != nil {
		a.Out.Warn("audit log: %v", aerr)
	}
}

// Log is where engines and tools write progress (stderr, quiet in JSON mode).
func (a *App) Log() io.Writer {
	if a.Out.JSON {
		return io.Discard
	}
	return a.Out.Err
}

// Logf adapts Out.Info for packages that take a printf-style logger.
func (a *App) Logf(format string, args ...any) { a.Out.Info(format, args...) }

// SaveState persists project state.
func (a *App) SaveState() error { return state.SaveProject(a.Project, a.State) }

// RequireRunning makes sure a managed target's container is up before use.
func (a *App) RequireRunning(ctx context.Context, t *env.Target) error {
	if !t.Managed() {
		return nil
	}
	if t.Port == 0 {
		return fmt.Errorf("local database has never been started; run `%s start`", config.AppName)
	}
	s, err := t.Compose.State(ctx)
	if err != nil {
		return err
	}
	if s != "running" {
		return fmt.Errorf("local database is not running (state %q); run `%s start`", s, config.AppName)
	}
	return dbops.WaitReady(ctx, t, 30*time.Second)
}
