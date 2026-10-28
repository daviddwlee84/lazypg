package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/spf13/cobra"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/instance"
	"github.com/daviddwlee84/lazypg/internal/state"
)

const projectTmpl = `# {{.App}} project config: commit this file.
# Machine-specific settings and credentials live in ~/.config/{{.App}}/.
{{- if eq .Mode "shared"}}
#
# This repository owns a shared schema. Other repositories consume it with:
#   [[deps]]
#   name = "{{.DB}}"
#   git  = "<this repository's URL>"
#   ref  = "<tag or branch>"
{{- end}}
version = 1
project = "{{.Project}}"
# org = "acme"              # resolve instances like "acme/prod" from your org's {{.OrgFile}}

[tools]
pgschema = "{{.Pgschema}}"

[local]
# "project": one container per checkout · "server": a shared machine instance
mode = "{{.LocalMode}}"
# e.g. "pgvector/pgvector:pg18" for pgvector
image = "{{.Image}}"

[[databases]]
name = "{{.DB}}"
# {{if eq .Engine "pgschema"}}pgschema: desired state in db/schema/<schema>/main.sql{{else}}dbmate: versioned migrations in db/migrations{{end}}
engine = "{{.Engine}}"
schemas = ["public"]
# extensions = ["vector"]   # created by {{.App}} before the engine runs
# scripts = "db/scripts"    # one-off pre/post scripts (renames, backfills)
# seeds = ["db/seeds"]      # development data; never run on prod

# Remote environments. Never put credentials here: use ` + "`{{.App}} login <instance>`" + `.
# [envs.dev]
# tier = "dev"
# instance = "acme/dev"
#
# [envs.prod]
# tier = "prod"
# instance = "acme/prod"

# Databases owned by other repositories (read-only here):
# [[deps]]
# name = "events"
# git = "git@github.com:acme/agent-events.git"
# ref = "v1.2.0"
`

const desiredTmpl = `-- Desired state of schema "public" (pgschema). Edit, then run:
--   {{.App}} plan      # preview the DDL
--   {{.App}} migrate   # apply it
-- Renames and data changes go in db/scripts/{pre,post} (` + "`{{.App}} script new`" + `).
--
-- CREATE TABLE notes (
--     id bigserial PRIMARY KEY,
--     body text NOT NULL,
--     created_at timestamptz NOT NULL DEFAULT now()
-- );
`

const seedTmpl = `-- Development seed data. Runs on ` + "`{{.App}} start`" + ` (new databases), ` + "`seed`" + `, and ` + "`reset`" + `.
-- Never runs on stage/prod.
-- INSERT INTO notes (body) VALUES ('hello');
`

var nonIdent = regexp.MustCompile(`[^a-z0-9_]+`)

func identFrom(s string) string {
	s = nonIdent.ReplaceAllString(strings.ToLower(s), "_")
	s = strings.Trim(s, "_")
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		s = "db_" + s
	}
	return s
}

func newInit() *cobra.Command {
	var name, db, engine, mode, localMode, image string
	var force bool
	c := &cobra.Command{
		Use:   "init",
		Short: "Create lazypg.toml and the db/ layout (touches no database)",
		Example: `  lazypg init                       # app-owned database in this repo
  lazypg init --mode shared         # a repo whose schema other repos consume
  lazypg init --engine dbmate       # versioned migrations instead of declarative`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := filepath.Abs(opts.Dir)
			if err != nil {
				return err
			}
			cfgPath := filepath.Join(dir, config.ProjectFile)
			if _, err := os.Stat(cfgPath); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", cfgPath)
			}
			if mode != "app" && mode != "shared" {
				return fmt.Errorf("--mode must be app or shared")
			}
			if name == "" {
				name = identFrom(filepath.Base(dir))
			}
			if db == "" {
				db = name
			}
			data := map[string]string{
				"App": config.AppName, "OrgFile": config.OrgFile, "Mode": mode, "Project": name, "DB": db,
				"Engine": engine, "LocalMode": localMode, "Image": image, "Pgschema": config.DefaultPgschemaVersion,
			}
			var buf strings.Builder
			if err := template.Must(template.New("p").Parse(projectTmpl)).Execute(&buf, data); err != nil {
				return err
			}
			if _, err := config.ParseProject([]byte(buf.String())); err != nil {
				return fmt.Errorf("generated config is invalid: %w", err)
			}
			files := map[string]string{config.ProjectFile: buf.String()}
			render := func(t string) string {
				var b strings.Builder
				template.Must(template.New("f").Parse(t)).Execute(&b, data)
				return b.String()
			}
			if engine == config.EnginePgschema {
				files["db/schema/public/main.sql"] = render(desiredTmpl)
			} else {
				files["db/migrations/.gitkeep"] = ""
			}
			files["db/seeds/seed.sql"] = render(seedTmpl)
			files["db/scripts/pre/.gitkeep"] = ""
			files["db/scripts/post/.gitkeep"] = ""
			var written []string
			for rel, content := range files {
				p := filepath.Join(dir, rel)
				if _, err := os.Stat(p); err == nil && rel != config.ProjectFile {
					continue
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
					return err
				}
				written = append(written, rel)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Initialized %s project %q in %s\n", config.AppName, name, dir)
			sort.Strings(written)
			for _, w := range written {
				fmt.Fprintf(cmd.OutOrStdout(), "  + %s\n", w)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nNext: %s start\n", config.AppName)
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&name, "name", "", "project name (default: directory name)")
	f.StringVar(&db, "db", "", "first database name (default: project name)")
	f.StringVar(&engine, "engine", config.EnginePgschema, "schema engine: pgschema or dbmate")
	f.StringVar(&mode, "mode", "app", "app (schema lives with its app) or shared (schema consumed by other repos)")
	f.StringVar(&localMode, "local", config.ModeProject, "local instance: project (own container) or server (shared machine instance)")
	f.StringVar(&image, "image", config.DefaultImage, "PostgreSQL image for local instances")
	f.BoolVar(&force, "force", false, "overwrite an existing lazypg.toml")
	return c
}

func newEject() *cobra.Command {
	return &cobra.Command{
		Use:   "eject",
		Short: "Copy the generated compose file into .lazypg/ for customization",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			if a.Project.Local.Mode != config.ModeProject {
				return fmt.Errorf("eject only applies to local.mode = %q", config.ModeProject)
			}
			path := filepath.Join(a.Project.Dir, config.EjectDir, "compose.yaml")
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("%s already exists", path)
			}
			b, err := instance.RenderTemplate(config.AppName+"-"+state.ProjectID(a.Project), a.Project.Local.Image)
			if err != nil {
				return err
			}
			// The compose project name is set by lazypg (-p); drop it from the
			// file so the ejected copy works from any checkout path.
			lines := strings.Split(string(b), "\n")
			var kept []string
			for _, l := range lines {
				if !strings.HasPrefix(l, "name: ") {
					kept = append(kept, l)
				}
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (keep the ${LAZYPG_PORT} port mapping and the %q service)\n", path, instance.Service)
			return nil
		},
	}
}
