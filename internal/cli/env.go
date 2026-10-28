package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/daviddwlee84/lazypg/internal/app"
	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/env"
	"github.com/daviddwlee84/lazypg/internal/output"
	"github.com/daviddwlee84/lazypg/internal/pgexec"
	"github.com/daviddwlee84/lazypg/internal/proc"
)

func newEnv() *cobra.Command {
	c := &cobra.Command{
		Use:   "env",
		Short: "List, select, and export environments (replaces juggling .env files)",
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List environments and whether they resolve on this machine",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			envs := a.Envs(cmd.Context())
			return a.Out.Result(envs, func(w io.Writer) {
				tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
				fmt.Fprintln(tw, "  ENV\tTIER\tINSTANCE\tSTATUS")
				for _, e := range envs {
					mark := " "
					if e.Current {
						mark = "*"
					}
					status := output.OKStyle.Render("ok")
					if e.Error != "" {
						status = output.BadStyle.Render(e.Error)
					}
					fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\n", mark, e.Name, output.TierStyle(e.Tier).Render(e.Tier), e.Instance, status)
				}
				tw.Flush()
			})
		},
	}
	use := &cobra.Command{
		Use:   "use <env>",
		Short: "Make an env the default for this checkout (prod can never be the default)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			t, err := a.Resolver.Resolve(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := a.Check(t, env.OpUse, "use"); err != nil {
				return fmt.Errorf("%w; pass --env %s on each command instead", err, t.Env)
			}
			if t.Env == config.EnvLocal {
				a.State.Env = ""
			} else {
				a.State.Env = t.Env
			}
			if err := a.SaveState(); err != nil {
				return err
			}
			a.Out.Banner(t.Env, t.Tier, t.Instance, "now the default for "+a.Project.Dir)
			return nil
		},
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "Show the resolved connection of the selected env (password redacted)",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			t, err := a.Target(cmd.Context())
			if err != nil {
				return err
			}
			refs, _ := a.Owned(t, nil)
			type db struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			}
			out := struct {
				*env.Target
				Databases []db `json:"databases"`
			}{Target: t}
			for _, r := range refs {
				out.Databases = append(out.Databases, db{r.DB.Name, t.RedactedURL(r.DBName)})
			}
			return a.Out.Result(out, func(w io.Writer) {
				fmt.Fprintf(w, "%s %s (tier %s)  %s\n", output.TierStyle(t.Tier).Render("●"), t.Env, t.Tier, t.Instance)
				for _, d := range out.Databases {
					fmt.Fprintf(w, "  %-12s %s\n", d.Name, d.URL)
				}
			})
		},
	}
	var primary string
	shell := &cobra.Command{
		Use:   "shell",
		Short: "Print `export` lines for the selected env (for direnv: eval \"$(lazypg env shell)\")",
		RunE: func(cmd *cobra.Command, args []string) error {
			return printVars(cmd, primary, func(k, v string) string { return "export " + k + "=" + shellQuote(v) })
		},
	}
	dotenv := &cobra.Command{
		Use:   "dotenv",
		Short: "Print KEY=value lines for tools that insist on a .env file",
		RunE: func(cmd *cobra.Command, args []string) error {
			return printVars(cmd, primary, func(k, v string) string { return k + "=" + shellQuote(v) })
		},
	}
	for _, x := range []*cobra.Command{shell, dotenv} {
		x.Flags().StringVar(&primary, "db", "", "database for DATABASE_URL/PG* (default: first owned)")
	}
	c.AddCommand(list, use, show, shell, dotenv)
	return c
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// connTarget resolves the env for commands that hand credentials to other programs.
func connTarget(ctx context.Context) (*app.App, *env.Target, []app.DBRef, error) {
	a, err := load()
	if err != nil {
		return nil, nil, nil, err
	}
	t, err := a.Target(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if t.Managed() && t.Port == 0 {
		return nil, nil, nil, fmt.Errorf("local database has never been started; run `%s start`", config.AppName)
	}
	var depRefs []app.DBRef
	if len(a.Project.Deps) > 0 {
		res, err := a.ResolveDeps(ctx, false)
		if err != nil {
			return nil, nil, nil, err
		}
		if depRefs, err = a.DepRefs(t, res); err != nil {
			return nil, nil, nil, err
		}
	}
	return a, t, depRefs, nil
}

func printVars(cmd *cobra.Command, primary string, line func(k, v string) string) error {
	a, t, depRefs, err := connTarget(cmd.Context())
	if err != nil {
		return err
	}
	if err := a.Check(t, env.OpConnect, "Export credentials"); err != nil {
		return err
	}
	vars, err := a.ConnEnv(t, primary, depRefs)
	if err != nil {
		return err
	}
	for _, kv := range vars {
		fmt.Fprintln(cmd.OutOrStdout(), line(kv[0], kv[1]))
	}
	return nil
}

func newURL() *cobra.Command {
	var db string
	c := &cobra.Command{
		Use:   "url",
		Short: "Print the connection URL of a database on the selected env",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, t, depRefs, err := connTarget(cmd.Context())
			if err != nil {
				return err
			}
			if err := a.Check(t, env.OpConnect, "Print credentials"); err != nil {
				return err
			}
			vars, err := a.ConnEnv(t, db, depRefs)
			if err != nil {
				return err
			}
			for _, kv := range vars {
				if kv[0] == "DATABASE_URL" {
					fmt.Fprintln(cmd.OutOrStdout(), kv[1])
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&db, "db", "", "database (default: first owned)")
	return c
}

func newExec() *cobra.Command {
	var db string
	c := &cobra.Command{
		Use:   "exec [--db name] -- <command> [args...]",
		Short: "Run a command with DATABASE_URL, PG*, and <NAME>_DATABASE_URL set for the selected env",
		Example: `  lazypg exec -- python manage.py runserver
  lazypg exec --env stage -- pytest tests/integration
  lazypg exec --env prod --yes -- ./scripts/report.sh`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, t, depRefs, err := connTarget(cmd.Context())
			if err != nil {
				return err
			}
			vars, err := a.ConnEnv(t, db, depRefs)
			if err != nil {
				return err
			}
			a.Banner(t, "exec "+strings.Join(args, " "))
			if err := a.Check(t, env.OpConnect, "Run "+args[0]); err != nil {
				return err
			}
			var kv []string
			for _, v := range vars {
				kv = append(kv, v[0]+"="+v[1])
			}
			c := exec.CommandContext(cmd.Context(), args[0], args[1:]...)
			c.Env = append(os.Environ(), kv...)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			err = c.Run()
			if ee, ok := err.(*exec.ExitError); ok {
				os.Exit(ee.ExitCode())
			}
			return err
		},
	}
	c.Flags().StringVar(&db, "db", "", "database for DATABASE_URL/PG* (default: first owned)")
	return c
}

func pickDB(a *app.App, t *env.Target, depRefs []app.DBRef, name string) (string, error) {
	refs, err := a.Owned(t, nil)
	if err != nil {
		return "", err
	}
	all := append(refs, depRefs...)
	for _, r := range all {
		if name == "" || r.DB.Name == name {
			return r.DBName, nil
		}
	}
	if name == "" {
		return "", fmt.Errorf("no databases")
	}
	return "", fmt.Errorf("unknown database %q", name)
}

func newPsql() *cobra.Command {
	var db string
	c := &cobra.Command{
		Use:   "psql [--db name] [-- psql args...]",
		Short: "Open psql on the selected env (no local psql install needed)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a, t, depRefs, err := connTarget(ctx)
			if err != nil {
				return err
			}
			if err := a.RequireRunning(ctx, t); err != nil {
				return err
			}
			name, err := pickDB(a, t, depRefs, db)
			if err != nil {
				return err
			}
			a.Banner(t, name)
			if err := a.Check(t, env.OpConnect, "Open psql"); err != nil {
				return err
			}
			return pgexec.Interactive(ctx, t, a.Project.Local.Image, name, args...)
		},
	}
	c.Flags().StringVar(&db, "db", "", "database (default: first owned)")
	return c
}

func newUI() *cobra.Command {
	var db string
	c := &cobra.Command{
		Use:   "ui",
		Short: "Browse the selected env in pgtui (lazygit-style TUI by pgplex)",
		Long: `Launches pgtui with PG* variables for the selected env. pgtui lists the
connection under its discovered instances; pick it to connect.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if !proc.Has("pgtui") {
				return fmt.Errorf("pgtui is not installed: brew install pgplex/pgtui/pgtui (or go install github.com/pgplex/pgtui/cmd/pgtui@latest)")
			}
			a, t, depRefs, err := connTarget(ctx)
			if err != nil {
				return err
			}
			name, err := pickDB(a, t, depRefs, db)
			if err != nil {
				return err
			}
			a.Banner(t, name)
			if err := a.Check(t, env.OpConnect, "Open pgtui"); err != nil {
				return err
			}
			c := exec.CommandContext(ctx, "pgtui")
			c.Env = append(os.Environ(), t.PGEnv(name)...)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			return c.Run()
		},
	}
	c.Flags().StringVar(&db, "db", "", "database (default: first owned)")
	return c
}

func gitDirty(dir string) (bool, error) {
	out, err := proc.Output(context.Background(), proc.Cmd{Name: "git", Args: []string{"-C", dir, "status", "--porcelain"}})
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}
