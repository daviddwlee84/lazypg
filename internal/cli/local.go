package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/daviddwlee84/lazypg/internal/app"
	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/dbops"
	"github.com/daviddwlee84/lazypg/internal/env"
	"github.com/daviddwlee84/lazypg/internal/instance"
	"github.com/daviddwlee84/lazypg/internal/output"
	"github.com/daviddwlee84/lazypg/internal/state"
)

func localOnly(cmd string) error {
	if opts.Env != "" && opts.Env != config.EnvLocal {
		return fmt.Errorf("`%s` manages the local instance only; drop --env", cmd)
	}
	return nil
}

func newStart() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the local database, apply schema, seed new databases, build deps",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := localOnly("start"); err != nil {
				return err
			}
			opts.Env = config.EnvLocal
			ctx := cmd.Context()
			a, t, err := target(ctx, true)
			if err != nil {
				return err
			}
			refs, err := a.Owned(t, nil)
			if err != nil {
				return err
			}
			a.Banner(t, dbNames(refs))
			results, err := a.Migrate(ctx, t, refs, app.MigrateOptions{})
			if err != nil {
				return err
			}
			var fresh []app.DBRef
			for i, r := range results {
				if r.Created {
					fresh = append(fresh, refs[i])
				}
			}
			if len(fresh) > 0 {
				if err := a.Seed(ctx, t, fresh, true); err != nil {
					return err
				}
			}
			resolved, err := a.ResolveDeps(ctx, false)
			if err != nil {
				return err
			}
			depRefs, err := a.DepRefs(t, resolved)
			if err != nil {
				return err
			}
			if err := a.ApplyDeps(ctx, t, depRefs, false); err != nil {
				return err
			}
			st := a.Status(ctx, t, depRefs)
			return a.Out.Result(st, func(w io.Writer) { printStatus(w, st, t, append(refs, depRefs...)) })
		},
	}
}

func newStop() *cobra.Command {
	var purge bool
	c := &cobra.Command{
		Use:   "stop",
		Short: "Stop the local container (data is kept unless --purge)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := localOnly("stop"); err != nil {
				return err
			}
			opts.Env = config.EnvLocal
			ctx := cmd.Context()
			a, err := load()
			if err != nil {
				return err
			}
			t, err := a.Target(ctx)
			if err != nil {
				return err
			}
			if t.Kind == env.KindServer {
				if purge {
					return fmt.Errorf("this project uses the shared server %q; drop its databases with `reset`/`branch delete`, or `%s server stop %s --purge`", a.Project.Local.Server, config.AppName, a.Project.Local.Server)
				}
				a.Out.Info("this project uses the shared server %q, which other projects may be using; stop it with `%s server stop %s`", a.Project.Local.Server, config.AppName, a.Project.Local.Server)
				return nil
			}
			if purge {
				// Local policy needs no prompt for resets, but deleting the
				// whole volume (all branches included) still deserves one.
				if err := env.ConfirmYN(a.Prompt, "Delete "+t.Compose.Project+" and ALL its data."); err != nil {
					return err
				}
			}
			if err := t.Compose.Down(ctx, purge); err != nil {
				return err
			}
			if purge {
				delete(a.State.Branches, config.EnvLocal)
				if err := a.SaveState(); err != nil {
					return err
				}
			}
			a.Out.Info("stopped %s", t.Compose.Project)
			return nil
		},
	}
	c.Flags().BoolVar(&purge, "purge", false, "also delete the data volume")
	return c
}

func newStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the selected environment, its instance, and databases",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			a, err := load()
			if err != nil {
				return err
			}
			t, err := a.Target(ctx)
			if err != nil {
				return err
			}
			var depRefs []app.DBRef
			if len(a.Project.Deps) > 0 {
				if res, err := a.ResolveDeps(ctx, false); err == nil {
					depRefs, _ = a.DepRefs(t, res)
				} else {
					a.Out.Warn("deps: %v", err)
				}
			}
			refs, _ := a.Owned(t, nil)
			st := a.Status(ctx, t, depRefs)
			return a.Out.Result(st, func(w io.Writer) { printStatus(w, st, t, append(refs, depRefs...)) })
		},
	}
}

func printStatus(w io.Writer, st *app.Status, t *env.Target, refs []app.DBRef) {
	where := st.Instance
	switch st.Kind {
	case env.KindProject:
		where = "lazypg container for this checkout"
	case env.KindServer:
		where = "shared " + st.Instance
	}
	fmt.Fprintf(w, "%s %s  %s\n", output.TierStyle(st.Tier).Render("●"), output.BoldStyle.Render(st.Env), output.Dim(where))
	if st.Container != "" {
		fmt.Fprintf(w, "  container  %s\n", st.Container)
	}
	if st.Branch != "" {
		fmt.Fprintf(w, "  branch     %s\n", st.Branch)
	}
	if t.Port != 0 {
		fmt.Fprintf(w, "  host       %s:%d (user %s)\n", t.Host, t.Port, t.User)
	}
	if st.Error != "" {
		fmt.Fprintf(w, "  error      %s\n", output.BadStyle.Render(st.Error))
	}
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "\n  DATABASE\tPHYSICAL\tENGINE\tOWNER\tEXISTS")
	for _, d := range st.Databases {
		exists := output.BadStyle.Render("no")
		if d.Exists {
			exists = output.OKStyle.Render("yes")
		} else if !st.Reachable {
			exists = output.Dim("?")
		}
		eng := d.Engine
		if eng == "" {
			eng = "-"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", d.Database, d.DBName, eng, d.Owner, exists)
	}
	tw.Flush()
	if st.Reachable && len(refs) > 0 && t.Tier == config.TierLocal {
		fmt.Fprintf(w, "\n  %s\n", t.URL(refs[0].DBName))
	}
}

func newServer() *cobra.Command {
	c := &cobra.Command{
		Use:   "server",
		Short: "Manage machine-level shared instances (local.mode = \"server\")",
	}
	var image string
	var port int
	start := &cobra.Command{
		Use:   "start [name]",
		Short: "Create and start a shared server (default name: default)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "default"
			if len(args) == 1 {
				name = args[0]
			}
			srv, err := app.EnsureServer(cmd.Context(), name, image, port, func(f string, a ...any) { fmt.Fprintf(cmd.ErrOrStderr(), f+"\n", a...) })
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "server %s running %s on 127.0.0.1:%d\n", srv.Name, srv.Image, srv.Port)
			return nil
		},
	}
	start.Flags().StringVar(&image, "image", "", "image for a new server (default "+config.DefaultImage+")")
	start.Flags().IntVar(&port, "port", 0, "host port for a new server (default: first free from 54320)")

	var purge bool
	stop := &cobra.Command{
		Use:   "stop [name]",
		Short: "Stop a shared server (--purge deletes all its databases)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "default"
			if len(args) == 1 {
				name = args[0]
			}
			srv, ok, err := state.LoadServer(name)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no server %q", name)
			}
			if purge {
				p := env.Prompt{Yes: opts.Yes, In: cmd.InOrStdin(), Out: cmd.ErrOrStderr()}
				if err := env.ConfirmYN(p, "Delete server "+name+" and ALL databases of ALL projects on it."); err != nil {
					return err
				}
			}
			if err := instance.ForServer(srv).Down(cmd.Context(), purge); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "server %s stopped\n", name)
			return nil
		},
	}
	stop.Flags().BoolVar(&purge, "purge", false, "delete the data volume")

	ls := &cobra.Command{
		Use:   "ls",
		Short: "List shared servers and their databases",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			servers, err := state.ListServers()
			if err != nil {
				return err
			}
			type row struct {
				Name      string   `json:"name"`
				Image     string   `json:"image"`
				Port      int      `json:"port"`
				State     string   `json:"state"`
				Databases []string `json:"databases,omitempty"`
			}
			var rows []row
			for _, s := range servers {
				r := row{Name: s.Name, Image: s.Image, Port: s.Port}
				r.State, _ = instance.ForServer(s).State(ctx)
				if r.State == "running" {
					t := &env.Target{Host: "127.0.0.1", Port: s.Port, User: instance.LocalUser, Password: instance.LocalPassword, SSLMode: "disable", MaintDB: "postgres"}
					all, _ := dbops.List(ctx, t, "")
					for _, d := range all {
						if d != "postgres" && d != config.PlanDB {
							r.Databases = append(r.Databases, d)
						}
					}
				}
				rows = append(rows, r)
			}
			return output.New(opts.JSON).Result(rows, func(w io.Writer) {
				if len(rows) == 0 {
					fmt.Fprintf(w, "no servers (create one with `%s server start`)\n", config.AppName)
					return
				}
				tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
				fmt.Fprintln(tw, "NAME\tIMAGE\tPORT\tSTATE\tDATABASES")
				for _, r := range rows {
					st := r.State
					if st == "" {
						st = "absent"
					}
					fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%d\n", r.Name, r.Image, r.Port, st, len(r.Databases))
				}
				tw.Flush()
				for _, r := range rows {
					for _, d := range r.Databases {
						fmt.Fprintf(w, "  %s/%s\n", r.Name, d)
					}
				}
			})
		},
	}
	c.AddCommand(start, stop, ls)
	return c
}
