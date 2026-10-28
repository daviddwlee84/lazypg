package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/dbops"
	"github.com/daviddwlee84/lazypg/internal/env"
	"github.com/daviddwlee84/lazypg/internal/output"
	"github.com/daviddwlee84/lazypg/internal/proc"
	"github.com/daviddwlee84/lazypg/internal/tools"
	"github.com/daviddwlee84/lazypg/internal/xdg"
)

func loadUser() (*config.User, error) {
	u := &config.User{}
	return u, config.LoadTOML(env.UserConfigPath(), u)
}

func newOrg() *cobra.Command {
	c := &cobra.Command{
		Use:   "org",
		Short: "Register org configs (shared instance definitions, no secrets)",
		Long: `An org config is a ` + config.OrgFile + ` committed in your team's infrastructure
repository. It names instances (host, port, sslmode, tier) so that projects can
say instance = "acme/prod" instead of copying hostnames into every repo.`,
	}
	var gitURL, ref, path string
	add := &cobra.Command{
		Use:   "add <name>",
		Short: "Register an org from a git repository or a local directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if (gitURL == "") == (path == "") {
				return fmt.Errorf("pass exactly one of --git or --path")
			}
			u, err := loadUser()
			if err != nil {
				return err
			}
			if u.Orgs == nil {
				u.Orgs = map[string]config.OrgRef{}
			}
			ref := config.OrgRef{Git: gitURL, Ref: ref}
			if path != "" {
				abs, err := filepath.Abs(path)
				if err != nil {
					return err
				}
				ref = config.OrgRef{Path: abs}
			}
			u.Orgs[name] = ref
			if gitURL != "" {
				if err := syncOrg(cmd.Context(), name, ref); err != nil {
					return err
				}
			}
			dir, err := env.OrgDir(u, name)
			if err != nil {
				return err
			}
			o, err := config.LoadOrg(dir)
			if err != nil {
				return err
			}
			if err := config.SaveTOML(env.UserConfigPath(), u, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "registered org %s with %d instance(s)\n", name, len(o.Instances))
			return nil
		},
	}
	add.Flags().StringVar(&gitURL, "git", "", "repository containing "+config.OrgFile)
	add.Flags().StringVar(&ref, "ref", "", "branch or tag (default: the repository's default branch)")
	add.Flags().StringVar(&path, "path", "", "local directory containing "+config.OrgFile)

	list := &cobra.Command{
		Use:   "list",
		Short: "List registered orgs and their instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			u, err := loadUser()
			if err != nil {
				return err
			}
			type inst struct {
				Alias string `json:"alias"`
				config.Instance
				HasCredential bool `json:"has_credential"`
			}
			creds := config.Credentials{}
			_ = config.LoadTOML(env.CredentialsPath(), &creds)
			var rows []inst
			var errs []string
			names := make([]string, 0, len(u.Orgs))
			for n := range u.Orgs {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				dir, err := env.OrgDir(u, n)
				if err == nil {
					var o *config.Org
					if o, err = config.LoadOrg(dir); err == nil {
						for in, i := range o.Instances {
							alias := n + "/" + in
							_, ok := creds[alias]
							rows = append(rows, inst{alias, i, ok})
						}
					}
				}
				if err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", n, err))
				}
			}
			for in, i := range u.Instances {
				_, ok := creds[in]
				rows = append(rows, inst{in, i, ok})
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].Alias < rows[j].Alias })
			return output.New(opts.JSON).Result(rows, func(w io.Writer) {
				tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
				fmt.Fprintln(tw, "INSTANCE\tTIER\tHOST\tCREDENTIAL")
				for _, r := range rows {
					cred := output.BadStyle.Render("missing (" + config.AppName + " login " + r.Alias + ")")
					if r.HasCredential {
						cred = output.OKStyle.Render("ok")
					}
					fmt.Fprintf(tw, "%s\t%s\t%s:%d\t%s\n", r.Alias, r.Tier, r.Host, r.WithDefaults().Port, cred)
				}
				tw.Flush()
				for _, e := range errs {
					fmt.Fprintln(w, output.BadStyle.Render(e))
				}
			})
		},
	}
	sync := &cobra.Command{
		Use:   "sync [name]",
		Short: "Pull the latest org config from git",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			u, err := loadUser()
			if err != nil {
				return err
			}
			for n, r := range u.Orgs {
				if len(args) == 1 && args[0] != n || r.Git == "" {
					continue
				}
				if err := syncOrg(cmd.Context(), n, r); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "synced %s\n", n)
			}
			return nil
		},
	}
	c.AddCommand(add, list, sync)
	return c
}

func syncOrg(ctx context.Context, name string, r config.OrgRef) error {
	dir := filepath.Join(xdg.DataDir(), "orgs", name)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		args := []string{"clone", "--quiet", "--depth", "1"}
		if r.Ref != "" {
			args = append(args, "--branch", r.Ref)
		}
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		return proc.Run(ctx, proc.Cmd{Name: "git", Args: append(args, r.Git, dir)})
	}
	return proc.Run(ctx, proc.Cmd{Name: "git", Args: []string{"-C", dir, "pull", "--quiet", "--ff-only"}})
}

func newLogin() *cobra.Command {
	var user, cmdStr, envVar string
	c := &cobra.Command{
		Use:   "login <instance>",
		Short: "Store how to get the password for an instance (credentials.toml, mode 0600)",
		Example: `  lazypg login acme/prod                               # prompts for a password
  lazypg login acme/prod --password-cmd 'op read op://infra/pg-prod/password'
  lazypg login acme/dev --password-env ACME_DEV_PGPASSWORD`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := args[0]
			creds := config.Credentials{}
			if err := config.LoadTOML(env.CredentialsPath(), &creds); err != nil {
				return err
			}
			cred := config.Credential{User: user, PasswordCmd: cmdStr, PasswordEnv: envVar}
			if cmdStr == "" && envVar == "" {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return fmt.Errorf("no terminal to read a password from; use --password-cmd or --password-env")
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "Password for %s: ", alias)
				pw, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				cred.Password = string(pw)
			}
			creds[alias] = cred
			if err := config.SaveTOML(env.CredentialsPath(), creds, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "saved credentials for %s in %s\n", alias, env.CredentialsPath())
			return nil
		},
	}
	c.Flags().StringVar(&user, "user", "", "override the instance's user")
	c.Flags().StringVar(&cmdStr, "password-cmd", "", "shell command that prints the password (e.g. a password manager)")
	c.Flags().StringVar(&envVar, "password-env", "", "environment variable that holds the password")
	return c
}

type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

func newDoctor() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check Docker, configuration, tools, and every environment's connectivity",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var checks []check
			add := func(name string, err error, okDetail string) {
				c := check{Name: name, OK: err == nil, Detail: okDetail}
				if err != nil {
					c.Detail = err.Error()
				}
				checks = append(checks, c)
			}
			out, err := proc.Output(ctx, proc.Cmd{Name: "docker", Args: []string{"compose", "version", "--short"}})
			add("docker compose", err, string(trimNL(out)))
			if err == nil {
				_, err = proc.Output(ctx, proc.Cmd{Name: "docker", Args: []string{"info", "--format", "{{.ServerVersion}}"}})
				add("docker daemon", err, "running")
			}
			a, err := load()
			add("project config", err, func() string {
				if a != nil {
					return filepath.Join(a.Project.Dir, config.ProjectFile)
				}
				return ""
			}())
			if a != nil {
				usesPgschema := false
				for _, d := range a.Project.Databases {
					usesPgschema = usesPgschema || d.Engine == config.EnginePgschema
				}
				if usesPgschema {
					bin, err := tools.Pgschema(ctx, a.Project.Tools.Pgschema, a.Logf)
					add("pgschema "+a.Project.Tools.Pgschema, err, bin)
				}
				if len(a.Project.Deps) > 0 {
					_, err := a.ResolveDeps(ctx, false)
					add("deps", err, fmt.Sprintf("%d resolved", len(a.Project.Deps)))
				}
				for _, name := range a.Project.EnvNames() {
					t, err := a.Resolver.Resolve(ctx, name)
					if err == nil {
						if t.Managed() && t.Port == 0 {
							add("env "+name, nil, "not started yet")
							continue
						}
						cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
						var v int
						v, err = dbops.ServerVersion(cctx, t)
						cancel()
						if err == nil {
							add("env "+name, nil, fmt.Sprintf("%s · PostgreSQL %d", t.Instance, v))
							continue
						}
					}
					add("env "+name, err, "")
				}
			}
			failed := 0
			for _, c := range checks {
				if !c.OK {
					failed++
				}
			}
			err = output.New(opts.JSON).Result(checks, func(w io.Writer) {
				for _, c := range checks {
					mark := output.OKStyle.Render("✓")
					if !c.OK {
						mark = output.BadStyle.Render("✗")
					}
					fmt.Fprintf(w, "%s %-22s %s\n", mark, c.Name, output.Dim(c.Detail))
				}
			})
			if err == nil && failed > 0 {
				err = fmt.Errorf("%d check(s) failed", failed)
			}
			return err
		},
	}
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
