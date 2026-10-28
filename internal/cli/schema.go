package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/daviddwlee84/lazypg/internal/app"
	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/engine"
	"github.com/daviddwlee84/lazypg/internal/env"
)

func newPlan() *cobra.Command {
	var dbs []string
	var out string
	c := &cobra.Command{
		Use:   "plan",
		Short: "Preview schema changes (and pending scripts) without applying them",
		Long: `Preview what ` + "`migrate`" + ` would do on the selected environment.

With --out, the plan is saved for CI: generate it in a pull request, review it,
then apply exactly that plan after merge with ` + "`migrate --plan <file>`" + `.
pgschema refuses a saved plan if the database changed in between.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a, t, err := target(ctx, false)
			if err != nil {
				return err
			}
			refs, err := a.Owned(t, dbs)
			if err != nil {
				return err
			}
			if err := a.Check(t, env.OpRead, "plan"); err != nil {
				return err
			}
			a.Banner(t, dbNames(refs))
			b, err := a.Plan(ctx, t, refs)
			if err != nil {
				return err
			}
			if out != "" {
				if err := engine.WriteBundle(out, b); err != nil {
					return err
				}
				a.Out.Info("saved plan to %s", out)
			}
			return a.Out.Result(b, func(w io.Writer) {
				changes := false
				for _, p := range b.Plans {
					if p.Human != "" {
						fmt.Fprint(w, p.Human)
					}
					changes = changes || p.Changes || len(p.PostScripts) > 0 || len(p.PreScripts) > 0
				}
				if !changes {
					fmt.Fprintln(w, "No changes: the database matches the desired state.")
				}
			})
		},
	}
	c.Flags().StringSliceVar(&dbs, "db", nil, "only these databases (repeatable)")
	c.Flags().StringVarP(&out, "out", "o", "", "save the plan as JSON (apply it later with migrate --plan)")
	return c
}

func newMigrate() *cobra.Command {
	var dbs []string
	var planFile string
	var dryRun bool
	c := &cobra.Command{
		Use:     "migrate",
		Aliases: []string{"apply", "up"},
		Short:   "Create/bootstrap databases, run pre scripts, apply schema changes, run post scripts",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a, t, err := target(ctx, true)
			if err != nil {
				return err
			}
			refs, err := a.Owned(t, dbs)
			if err != nil {
				return err
			}
			a.Banner(t, dbNames(refs))
			if t.Tier != config.TierLocal {
				if dirty, _ := gitDirty(a.Project.Dir); dirty {
					a.Out.Warn("working tree has uncommitted changes; %s will apply them as they are on disk", t.Env)
				}
			}
			res, err := a.Migrate(ctx, t, refs, app.MigrateOptions{PlanFile: planFile, DryRun: dryRun})
			if err != nil {
				return err
			}
			return a.Out.Result(res, nil)
		},
	}
	c.Flags().StringSliceVar(&dbs, "db", nil, "only these databases (repeatable)")
	c.Flags().StringVar(&planFile, "plan", "", "apply a plan saved by plan --out")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would happen and stop")
	return c
}

func singleDB(a *app.App, t *env.Target, name, engineName string) (app.DBRef, error) {
	var only []string
	if name != "" {
		only = []string{name}
	}
	refs, err := a.Owned(t, only)
	if err != nil {
		return app.DBRef{}, err
	}
	var match []app.DBRef
	for _, r := range refs {
		if engineName == "" || r.DB.Engine == engineName {
			match = append(match, r)
		}
	}
	switch {
	case len(match) == 1:
		return match[0], nil
	case len(match) == 0 && engineName != "":
		return app.DBRef{}, fmt.Errorf("no database uses the %s engine; with pgschema, edit the desired state instead (and use `branch`/`reset` to undo locally)", engineName)
	case len(match) == 0:
		return app.DBRef{}, fmt.Errorf("no databases")
	}
	return app.DBRef{}, fmt.Errorf("several databases match; pick one with --db")
}

func newRollback() *cobra.Command {
	var db string
	c := &cobra.Command{
		Use:   "rollback",
		Short: "Revert the last dbmate migration (local/dev only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a, t, err := target(ctx, false)
			if err != nil {
				return err
			}
			r, err := singleDB(a, t, db, config.EngineDbmate)
			if err != nil {
				return err
			}
			a.Banner(t, r.DBName)
			if err := a.Check(t, env.OpRollback, "Roll back the last migration of "+r.DBName); err != nil {
				return err
			}
			err = engine.Dbmate{}.Rollback(&engine.Context{Project: r.Owner, DB: r.DB, Target: t, DBName: r.DBName, Log: a.Log()})
			a.Audit(t, env.OpRollback, r.DBName, "", err)
			return err
		},
	}
	c.Flags().StringVar(&db, "db", "", "database (required when several use dbmate)")
	return c
}

func newMigrationNew() *cobra.Command {
	var db string
	c := &cobra.Command{
		Use:   "migration",
		Short: "Versioned migrations (dbmate engine)",
	}
	n := &cobra.Command{
		Use:   "new <name>",
		Short: "Create a timestamped dbmate migration file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			t, err := a.Target(cmd.Context())
			if err != nil {
				return err
			}
			r, err := singleDB(a, t, db, config.EngineDbmate)
			if err != nil {
				return err
			}
			path, err := engine.Dbmate{}.NewMigration(&engine.Context{Project: r.Owner, DB: r.DB}, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), rel(a.Project.Dir, path))
			return nil
		},
	}
	n.Flags().StringVar(&db, "db", "", "database (required when several use dbmate)")
	c.AddCommand(n)
	return c
}

func newScript() *cobra.Command {
	var db, phase string
	c := &cobra.Command{
		Use:   "script",
		Short: "One-off pre/post scripts for changes the schema engine cannot express",
	}
	n := &cobra.Command{
		Use:   "new <name>",
		Short: "Create a script file (pre: before the schema diff, e.g. renames; post: after, e.g. backfills)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			t, err := a.Target(cmd.Context())
			if err != nil {
				return err
			}
			r, err := singleDB(a, t, db, "")
			if err != nil {
				return err
			}
			path, err := engine.NewScript(&engine.Context{Project: r.Owner, DB: r.DB}, phase, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), rel(a.Project.Dir, path))
			return nil
		},
	}
	n.Flags().StringVar(&db, "db", "", "database (required when the project owns several)")
	n.Flags().StringVar(&phase, "phase", engine.PhasePost, "pre or post")
	c.AddCommand(n)
	return c
}

func newSeed() *cobra.Command {
	var dbs []string
	c := &cobra.Command{
		Use:   "seed",
		Short: "Run seed files (stage requires confirmation; prod is denied)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a, t, err := target(ctx, false)
			if err != nil {
				return err
			}
			refs, err := a.Owned(t, dbs)
			if err != nil {
				return err
			}
			a.Banner(t, dbNames(refs))
			return a.Seed(ctx, t, refs, false)
		},
	}
	c.Flags().StringSliceVar(&dbs, "db", nil, "only these databases (repeatable)")
	return c
}

func newReset() *cobra.Command {
	var dbs []string
	c := &cobra.Command{
		Use:   "reset",
		Short: "Drop and rebuild databases from the repository, then seed (local/dev only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a, t, err := target(ctx, true)
			if err != nil {
				return err
			}
			refs, err := a.Owned(t, dbs)
			if err != nil {
				return err
			}
			a.Banner(t, dbNames(refs))
			res, err := a.Reset(ctx, t, refs)
			if err != nil {
				return err
			}
			return a.Out.Result(res, nil)
		},
	}
	c.Flags().StringSliceVar(&dbs, "db", nil, "only these databases (repeatable)")
	return c
}

func newDB() *cobra.Command {
	c := &cobra.Command{Use: "db", Short: "Schema snapshots and adoption"}
	var dbs []string
	dump := &cobra.Command{
		Use:   "dump",
		Short: "Write a schema-only pg_dump to each database's schema_file (one file agents can read)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a, t, err := target(ctx, false)
			if err != nil {
				return err
			}
			refs, err := a.Owned(t, dbs)
			if err != nil {
				return err
			}
			a.Banner(t, dbNames(refs))
			for _, r := range refs {
				if err := (engine.Dbmate{}).Pull(ctx, &engine.Context{Project: r.Owner, DB: r.DB, Target: t, DBName: r.DBName, Log: a.Log()}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	dump.Flags().StringSliceVar(&dbs, "db", nil, "only these databases (repeatable)")
	pull := &cobra.Command{
		Use:   "pull",
		Short: "Adopt a live database: overwrite the desired state (pgschema) or schema_file (dbmate) from it",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a, t, err := target(ctx, false)
			if err != nil {
				return err
			}
			refs, err := a.Owned(t, dbs)
			if err != nil {
				return err
			}
			a.Banner(t, dbNames(refs))
			if err := a.Check(t, env.OpPull, "pull"); err != nil {
				return err
			}
			for _, r := range refs {
				if r.DB.Engine == config.EnginePgschema && hasFiles(r.Owner.Abs(r.DB.SchemaDir)) {
					if err := env.ConfirmYN(a.Prompt, fmt.Sprintf("Overwrite %s with the schema of %s.", r.DB.SchemaDir, r.DBName)); err != nil {
						return err
					}
				}
				e, err := engine.For(r.DB)
				if err != nil {
					return err
				}
				if err := e.Pull(ctx, &engine.Context{Project: r.Owner, DB: r.DB, Target: t, DBName: r.DBName, Log: a.Log(), Logf: a.Logf}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	pull.Flags().StringSliceVar(&dbs, "db", nil, "only these databases (repeatable)")
	c.AddCommand(dump, pull)
	return c
}

func hasFiles(dir string) bool {
	found := false
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(p) == ".sql" {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func rel(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil {
		return r
	}
	return p
}
