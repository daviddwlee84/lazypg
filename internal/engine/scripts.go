package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/dbops"
)

// Script phases. Pre scripts run before the schema engine (e.g. renames that
// pgschema cannot express); post scripts run after it (e.g. backfills).
const (
	PhasePre  = "pre"
	PhasePost = "post"
)

// scriptsTable keeps script history outside user schemas, so declarative
// engines never see (and never try to drop) it.
func scriptsTable(phase string) string { return config.MetaSchema + ".scripts_" + phase }

func scriptsDir(c *Context, phase string) (string, error) {
	if phase != PhasePre && phase != PhasePost {
		return "", fmt.Errorf("phase must be %q or %q", PhasePre, PhasePost)
	}
	return filepath.Join(c.Project.Abs(c.DB.Scripts), phase), nil
}

// PendingScripts lists scripts of phase that have not run on the target.
func PendingScripts(ctx context.Context, c *Context, phase string) ([]string, string, error) {
	dir, err := scriptsDir(c, phase)
	if err != nil {
		return nil, "", err
	}
	db, err := newDbmate(c, dir, scriptsTable(phase))
	if err != nil {
		return nil, "", err
	}
	ms, err := pending(db)
	if err != nil {
		return nil, "", err
	}
	var names []string
	for _, m := range ms {
		names = append(names, m.FileName)
	}
	human := ""
	if len(ms) > 0 {
		human = describe(fmt.Sprintf("%s %s scripts", c.DB.Name, phase), ms)
	}
	return names, human, nil
}

// RunScripts runs pending scripts of phase.
func RunScripts(ctx context.Context, c *Context, phase string) error {
	dir, err := scriptsDir(c, phase)
	if err != nil {
		return err
	}
	if !hasSQL(dir) {
		return nil
	}
	db, err := newDbmate(c, dir, scriptsTable(phase))
	if err != nil {
		return err
	}
	return db.Migrate()
}

// NewScript creates a script file for phase and returns its path.
func NewScript(c *Context, phase, name string) (string, error) {
	dir, err := scriptsDir(c, phase)
	if err != nil {
		return "", err
	}
	return newFile(dir, name)
}

var nameRe = regexp.MustCompile(`^[a-z0-9_]+$`)

func newFile(dir, name string) (string, error) {
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("name %q must match %s", name, nameRe)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, time.Now().UTC().Format("20060102150405")+"_"+name+".sql")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}
	return path, os.WriteFile(path, []byte("-- migrate:up\n\n\n-- migrate:down\n\n"), 0o644)
}

// BaselineScripts records every script of phase as applied without running
// it. Scripts move existing databases between schema versions; a database
// created from the current desired state is already past all of them.
func BaselineScripts(ctx context.Context, c *Context, phase string) ([]string, error) {
	dir, err := scriptsDir(c, phase)
	if err != nil {
		return nil, err
	}
	if !hasSQL(dir) {
		return nil, nil
	}
	db, err := newDbmate(c, dir, scriptsTable(phase))
	if err != nil {
		return nil, err
	}
	ms, err := pending(db)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, nil
	}
	conn, err := dbops.Connect(ctx, c.Target, c.DBName)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	table := pgx.Identifier{config.MetaSchema, "scripts_" + phase}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{config.MetaSchema}.Sanitize()+
		"; CREATE TABLE IF NOT EXISTS "+table+" (version varchar PRIMARY KEY)"); err != nil {
		return nil, err
	}
	var names []string
	for _, m := range ms {
		if _, err := conn.Exec(ctx, "INSERT INTO "+table+" (version) VALUES ($1) ON CONFLICT DO NOTHING", m.Version); err != nil {
			return nil, err
		}
		names = append(names, m.FileName)
	}
	return names, nil
}
