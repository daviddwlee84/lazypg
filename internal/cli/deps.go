package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/engine"
	"github.com/daviddwlee84/lazypg/internal/pgexec"
)

func newDeps() *cobra.Command {
	c := &cobra.Command{
		Use:   "deps",
		Short: "Databases owned by other repositories (read-only here)",
	}
	var update bool
	sync := &cobra.Command{
		Use:   "sync",
		Short: "Resolve deps and write lazypg.lock (--update moves git refs forward)",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			res, err := a.ResolveDeps(cmd.Context(), update)
			if err != nil {
				return err
			}
			type row struct {
				Name      string   `json:"name"`
				Source    string   `json:"source"`
				Commit    string   `json:"commit,omitempty"`
				Databases []string `json:"databases"`
			}
			var rows []row
			for _, r := range res {
				x := row{Name: r.Dep.Name, Source: r.Source, Commit: r.Commit}
				for _, d := range r.Databases {
					x.Databases = append(x.Databases, d.Name)
				}
				rows = append(rows, x)
			}
			return a.Out.Result(rows, func(w io.Writer) {
				if len(rows) == 0 {
					fmt.Fprintln(w, "no deps declared")
				}
				for _, r := range rows {
					fmt.Fprintf(w, "%s  %s  %v\n", r.Name, r.Source, r.Databases)
				}
			})
		},
	}
	sync.Flags().BoolVar(&update, "update", false, "re-resolve git refs instead of using lazypg.lock")

	var force bool
	apply := &cobra.Command{
		Use:   "apply",
		Short: "Build dependency databases on local (--force rebuilds existing ones)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := localOnly("deps apply"); err != nil {
				return err
			}
			opts.Env = config.EnvLocal
			ctx := cmd.Context()
			a, t, err := target(ctx, true)
			if err != nil {
				return err
			}
			res, err := a.ResolveDeps(ctx, false)
			if err != nil {
				return err
			}
			refs, err := a.DepRefs(t, res)
			if err != nil {
				return err
			}
			a.Banner(t, dbNames(refs))
			return a.ApplyDeps(ctx, t, refs, force)
		},
	}
	apply.Flags().BoolVar(&force, "force", false, "drop and rebuild dependency databases that already exist")

	var dir string
	vendor := &cobra.Command{
		Use:   "vendor",
		Short: "Snapshot local dependency databases into SQL files for offline use",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Env = config.EnvLocal
			ctx := cmd.Context()
			a, t, err := target(ctx, false)
			if err != nil {
				return err
			}
			res, err := a.ResolveDeps(ctx, false)
			if err != nil {
				return err
			}
			refs, err := a.DepRefs(t, res)
			if err != nil {
				return err
			}
			for _, r := range refs {
				var out bytes.Buffer
				if err := pgexec.Run(ctx, t, a.Project.Local.Image, r.DBName, false, nil, &out, nil,
					"pg_dump", "--schema-only", "--no-owner", "--no-privileges", "--exclude-schema="+config.MetaSchema); err != nil {
					return err
				}
				path := filepath.Join(a.Project.Abs(dir), r.Dep.Dep.Name+"_"+r.DB.Name+".sql")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(path, engine.CleanDump(out.Bytes()), 0o644); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n  to use it: [[deps]] name = %q, vendor = %q, databases = [%q]\n",
					rel(a.Project.Dir, path), r.Dep.Dep.Name, rel(a.Project.Dir, path), r.DB.Name)
			}
			return nil
		},
	}
	vendor.Flags().StringVar(&dir, "dir", "db/vendor", "output directory")
	c.AddCommand(sync, apply, vendor)
	return c
}
