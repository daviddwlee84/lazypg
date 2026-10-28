package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/amacneil/dbmate/v2/pkg/dbmate"
	_ "github.com/amacneil/dbmate/v2/pkg/driver/postgres"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/pgexec"
)

// Dbmate is the versioned-migration engine, embedded as a library.
type Dbmate struct{}

// newDbmate configures dbmate for one migrations directory and history table.
func newDbmate(c *Context, dir, table string) (*dbmate.DB, error) {
	u, err := url.Parse(c.Target.URL(c.DBName))
	if err != nil {
		return nil, err
	}
	db := dbmate.New(u)
	db.MigrationsDir = []string{dir}
	db.MigrationsTableName = table
	db.AutoDumpSchema = false
	db.Log = c.Log
	if db.Log == nil {
		db.Log = io.Discard
	}
	// Serialize concurrent deploys against shared environments.
	db.UseMigrationLock = !c.Target.Managed()
	return db, nil
}

func (Dbmate) db(c *Context) (*dbmate.DB, error) {
	return newDbmate(c, c.Project.Abs(c.DB.Migrations), "schema_migrations")
}

func pending(db *dbmate.DB) ([]dbmate.Migration, error) {
	if !hasSQL(db.MigrationsDir[0]) {
		return nil, nil
	}
	all, err := db.FindMigrations()
	if err != nil {
		return nil, err
	}
	var out []dbmate.Migration
	for _, m := range all {
		if !m.Applied {
			out = append(out, m)
		}
	}
	return out, nil
}

func hasSQL(dir string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, "*.sql"))
	return len(m) > 0
}

// upSection returns the migrate:up part of a dbmate file for previews.
func upSection(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := string(b)
	if i := strings.Index(s, "-- migrate:down"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "-- migrate:up"))
}

func describe(header string, ms []dbmate.Migration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "── %s ──\n", header)
	for _, m := range ms {
		fmt.Fprintf(&b, "+ %s\n", m.FileName)
		for _, line := range strings.Split(upSection(m.FilePath), "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	return b.String()
}

// Plan lists migrations that have not been applied.
func (e Dbmate) Plan(ctx context.Context, c *Context) (*Plan, error) {
	db, err := e.db(c)
	if err != nil {
		return nil, err
	}
	ms, err := pending(db)
	if err != nil {
		return nil, err
	}
	p := &Plan{Database: c.DB.Name, DBName: c.DBName, Engine: config.EngineDbmate}
	for _, m := range ms {
		p.Pending = append(p.Pending, m.FileName)
	}
	if len(ms) > 0 {
		p.Changes = true
		p.Human = describe(c.DB.Name+" migrations", ms)
	}
	return p, nil
}

// Apply runs pending migrations after checking that the pending set still
// matches the plan.
func (e Dbmate) Apply(ctx context.Context, c *Context, p *Plan) error {
	if !p.Changes {
		return nil
	}
	db, err := e.db(c)
	if err != nil {
		return err
	}
	ms, err := pending(db)
	if err != nil {
		return err
	}
	var now []string
	for _, m := range ms {
		now = append(now, m.FileName)
	}
	if !slices.Equal(now, p.Pending) {
		return fmt.Errorf("database %s changed since the plan was made: pending is now %v, plan has %v; re-run plan", c.DB.Name, now, p.Pending)
	}
	return db.Migrate()
}

// Rollback reverts the most recent migration.
func (e Dbmate) Rollback(c *Context) error {
	db, err := e.db(c)
	if err != nil {
		return err
	}
	return db.Rollback()
}

// NewMigration creates a timestamped migration file and returns its path.
func (e Dbmate) NewMigration(c *Context, name string) (string, error) {
	return newFile(c.Project.Abs(c.DB.Migrations), name)
}

// Pull writes a schema-only pg_dump to schema_file.
func (e Dbmate) Pull(ctx context.Context, c *Context) error {
	var out bytes.Buffer
	if err := pgexec.Run(ctx, c.Target, c.Project.Local.Image, c.DBName, false, nil, &out, nil,
		"pg_dump", "--schema-only", "--no-owner", "--no-privileges", "--exclude-schema="+config.MetaSchema); err != nil {
		return err
	}
	path := c.Project.Abs(c.DB.SchemaFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, CleanDump(out.Bytes()), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(c.Log, "wrote %s\n", relTo(c.Project.Dir, path))
	return nil
}

// CleanDump removes pg_dump's per-run \restrict tokens so snapshots diff cleanly.
func CleanDump(b []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.Split(b, []byte("\n")) {
		if bytes.HasPrefix(line, []byte(`\restrict `)) || bytes.HasPrefix(line, []byte(`\unrestrict `)) {
			continue
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	return bytes.TrimRight(out.Bytes(), "\n")
}
