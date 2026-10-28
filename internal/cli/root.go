// Package cli defines lazypg's Cobra command tree. Commands parse flags and
// delegate to internal/app.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"github.com/daviddwlee84/lazypg/internal/app"
	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/env"
	"github.com/daviddwlee84/lazypg/internal/output"
)

// Version is set at build time with -ldflags "-X …/cli.Version=v0.1.0".
var Version = "dev"

var opts app.Options

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   config.AppName,
		Short: "Supabase-CLI-style PostgreSQL workflow: local instances, safe environments, schema-as-code",
		Long: config.AppName + ` keeps PostgreSQL development reproducible and environment switching safe.

  local    lazypg-managed container (per project, or a shared machine server)
  dev      shared team database: changes ask for confirmation
  stage    no reset; changes ask for confirmation
  prod     no reset/seed/rollback/branch; migrations require --confirm prod

Schema engines per database: pgschema (declarative, default) or dbmate (versioned).
Committed config lives in lazypg.toml; machine config and credentials in ~/.config/lazypg.`,
		Version:       buildVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.StringVarP(&opts.Env, "env", "e", "", "environment: local, dev, stage, prod, … (default: the one chosen with \"env use\", else local)")
	pf.BoolVar(&opts.JSON, "json", false, "machine-readable output on stdout")
	pf.BoolVarP(&opts.Yes, "yes", "y", false, "answer yes to y/N confirmations (not sufficient for prod migrations)")
	pf.StringVar(&opts.Confirm, "confirm", "", "confirm an operation on the named env (required for prod in non-interactive use)")
	pf.StringVarP(&opts.Dir, "dir", "C", ".", "run as if started in this directory")

	root.AddGroup(
		&cobra.Group{ID: "local", Title: "Local development:"},
		&cobra.Group{ID: "schema", Title: "Schema and data:"},
		&cobra.Group{ID: "env", Title: "Environments and connections:"},
		&cobra.Group{ID: "setup", Title: "Setup:"},
	)
	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	add("setup", newInit(), newEject(), newOrg(), newLogin(), newDoctor())
	add("local", newStart(), newStop(), newStatus(), newServer(), newBranch(), newDeps())
	add("schema", newPlan(), newMigrate(), newRollback(), newMigrationNew(), newScript(), newSeed(), newReset(), newDB())
	add("env", newEnv(), newURL(), newExec(), newPsql(), newUI())
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root := newRoot()
	if err := root.ExecuteContext(ctx); err != nil {
		msg := err.Error()
		fmt.Fprintln(os.Stderr, output.BadStyle.Render("error:")+" "+msg)
		if errors.Is(err, env.ErrDenied) {
			return 3
		}
		if strings.HasPrefix(msg, "aborted") {
			return 130
		}
		return 1
	}
	return 0
}

func load() (*app.App, error) { return app.Load(opts) }

// target loads the app and resolves --env. Managed targets must be running
// unless start is true, in which case they are started.
func target(ctx context.Context, start bool) (*app.App, *env.Target, error) {
	a, err := load()
	if err != nil {
		return nil, nil, err
	}
	t, err := a.Target(ctx)
	if err != nil {
		return nil, nil, err
	}
	if t.Managed() {
		if start {
			err = a.EnsureLocal(ctx, t)
		} else {
			err = a.RequireRunning(ctx, t)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return a, t, nil
}

func dbNames(refs []app.DBRef) string {
	var names []string
	for _, r := range refs {
		names = append(names, r.DBName)
	}
	return strings.Join(names, ", ")
}
