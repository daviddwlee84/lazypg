package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/proc"
	"github.com/daviddwlee84/lazypg/internal/tools"
)

// Pgschema is the declarative engine: desired state lives in
// <schema_dir>/<schema>/main.sql and pgschema computes the DDL.
type Pgschema struct{}

// DesiredFile is the desired-state entry file for one schema.
func DesiredFile(p *config.Project, db *config.Database, schema string) string {
	return filepath.Join(p.Abs(db.SchemaDir), schema, "main.sql")
}

func (Pgschema) bin(ctx context.Context, c *Context) (string, error) {
	return tools.Pgschema(ctx, c.Project.Tools.Pgschema, c.Logf)
}

// connArgs are the target connection flags. The password travels in the
// environment, never on the command line.
func connArgs(c *Context, schema string) ([]string, []string) {
	t := c.Target
	args := []string{
		"--host", t.Host, "--port", strconv.Itoa(t.Port), "--db", c.DBName,
		"--user", t.User, "--sslmode", t.SSLMode, "--schema", schema,
	}
	env := []string{"PGPASSWORD=" + t.Password}
	return args, env
}

func planHostArgs(c *Context) ([]string, []string) {
	h := c.PlanHost
	if h == nil {
		return nil, nil
	}
	return []string{
			"--plan-host", h.Host, "--plan-port", strconv.Itoa(h.Port), "--plan-db", config.PlanDB,
			"--plan-user", h.User, "--plan-sslmode", h.SSLMode,
		},
		[]string{"PGSCHEMA_PLAN_PASSWORD=" + h.Password}
}

// run executes pgschema from a scratch directory so that its own .env loading
// can never pick up a repository's .env and silently change the target.
func run(ctx context.Context, bin string, args, env []string, stdout *bytes.Buffer) error {
	dir, err := os.MkdirTemp("", "lazypg-pgschema-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var stderr bytes.Buffer
	err = proc.Run(ctx, proc.Cmd{Name: bin, Args: args, Env: env, Dir: dir, Stdout: stdout, Stderr: &stderr})
	if err != nil {
		return fmt.Errorf("pgschema %s failed: %s", args[0], strings.TrimSpace(stderr.String()+"\n"+stdout.String()))
	}
	return nil
}

// Plan runs `pgschema plan` once per schema.
func (e Pgschema) Plan(ctx context.Context, c *Context) (*Plan, error) {
	bin, err := e.bin(ctx, c)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Database: c.DB.Name, DBName: c.DBName, Engine: config.EnginePgschema, Schemas: map[string]json.RawMessage{}}
	tmp, err := os.MkdirTemp("", "lazypg-plan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	var human strings.Builder
	for _, s := range c.DB.Schemas {
		file := DesiredFile(c.Project, c.DB, s)
		if _, err := os.Stat(file); err != nil {
			return nil, fmt.Errorf("database %s, schema %s: desired state %s not found (create it, or adopt an existing database with `%s db pull`)", c.DB.Name, s, file, config.AppName)
		}
		out := filepath.Join(tmp, s+".json")
		args, env := connArgs(c, s)
		args = append([]string{"plan"}, args...)
		args = append(args, "--file", file, "--output-human", "stdout", "--output-json", out, "--no-color")
		pa, pe := planHostArgs(c)
		args, env = append(args, pa...), append(env, pe...)
		var stdout bytes.Buffer
		if err := run(ctx, bin, args, env, &stdout); err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			return nil, err
		}
		var parsed struct {
			Groups []json.RawMessage `json:"groups"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("parse pgschema plan: %w", err)
		}
		if len(parsed.Groups) > 0 {
			plan.Changes = true
			plan.Schemas[s] = raw
			fmt.Fprintf(&human, "── %s.%s ──\n%s\n", c.DB.Name, s, strings.TrimSpace(stdout.String()))
		}
	}
	plan.Human = human.String()
	return plan, nil
}

// Apply runs `pgschema apply --plan` for every schema with changes.
func (e Pgschema) Apply(ctx context.Context, c *Context, p *Plan) error {
	if !p.Changes {
		return nil
	}
	bin, err := e.bin(ctx, c)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "lazypg-apply-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, s := range c.DB.Schemas {
		raw, ok := p.Schemas[s]
		if !ok {
			continue
		}
		f := filepath.Join(tmp, s+".json")
		if err := os.WriteFile(f, raw, 0o600); err != nil {
			return err
		}
		args, env := connArgs(c, s)
		args = append([]string{"apply"}, args...)
		args = append(args, "--plan", f, "--auto-approve", "--no-color")
		var stdout bytes.Buffer
		if err := run(ctx, bin, args, env, &stdout); err != nil {
			if strings.Contains(err.Error(), "fingerprint mismatch") {
				return fmt.Errorf("schema %s changed since the plan was made; re-run `%s plan` and review it again: %w", s, config.AppName, err)
			}
			return fmt.Errorf("schema %s: %w", s, err)
		}
		fmt.Fprintf(c.Log, "applied %s.%s\n", c.DB.Name, s)
	}
	return nil
}

// Pull replaces the desired state with a multi-file dump of the live schema.
func (e Pgschema) Pull(ctx context.Context, c *Context) error {
	bin, err := e.bin(ctx, c)
	if err != nil {
		return err
	}
	for _, s := range c.DB.Schemas {
		file := DesiredFile(c.Project, c.DB, s)
		dir := filepath.Dir(file)
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		staged, err := os.MkdirTemp(filepath.Dir(dir), ".pull-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(staged)
		args, env := connArgs(c, s)
		args = append([]string{"dump"}, args...)
		args = append(args, "--multi-file", "--file", filepath.Join(staged, "main.sql"))
		var stdout bytes.Buffer
		if err := run(ctx, bin, args, env, &stdout); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(staged, "main.sql")); err != nil {
			return fmt.Errorf("dump did not produce main.sql: %w", err)
		}
		backup, err := os.MkdirTemp(filepath.Dir(dir), ".pull-backup-*")
		if err != nil {
			return err
		}
		if err := os.Remove(backup); err != nil {
			return err
		}
		existed := false
		if _, err := os.Lstat(dir); err == nil {
			if err := os.Rename(dir, backup); err != nil {
				return err
			}
			existed = true
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(staged, dir); err != nil {
			if existed {
				if restoreErr := os.Rename(backup, dir); restoreErr != nil {
					return fmt.Errorf("install dump: %v; old schema preserved at %s: %w", err, backup, restoreErr)
				}
			}
			return err
		}
		if existed {
			if err := os.RemoveAll(backup); err != nil {
				return err
			}
		}
		fmt.Fprintf(c.Log, "wrote %s\n", relTo(c.Project.Dir, dir))
	}
	return nil
}

func relTo(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil {
		return r
	}
	return p
}
