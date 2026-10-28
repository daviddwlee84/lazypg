// Package dbops performs cluster- and database-level operations over pgx:
// readiness, CREATE/DROP DATABASE, bootstrap objects, SQL files, and branches.
package dbops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/daviddwlee84/lazypg/internal/env"
)

// Connect opens a connection to dbname on t.
func Connect(ctx context.Context, t *env.Target, dbname string) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(t.URL(dbname))
	if err != nil {
		return nil, err
	}
	cfg.RuntimeParams["application_name"] = "lazypg"
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(cctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", t.RedactedURL(dbname), err)
	}
	return conn, nil
}

// WaitReady polls until the maintenance database accepts connections.
func WaitReady(ctx context.Context, t *env.Target, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		conn, err := Connect(ctx, t, t.MaintDB)
		if err == nil {
			conn.Close(ctx)
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("database not ready after %s: %w", timeout, last)
}

func withMaint(ctx context.Context, t *env.Target, fn func(*pgx.Conn) error) error {
	conn, err := Connect(ctx, t, t.MaintDB)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	return fn(conn)
}

// Exists reports whether a database exists.
func Exists(ctx context.Context, t *env.Target, name string) (bool, error) {
	var ok bool
	err := withMaint(ctx, t, func(c *pgx.Conn) error {
		return c.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&ok)
	})
	return ok, err
}

// List returns databases whose names start with prefix.
func List(ctx context.Context, t *env.Target, prefix string) ([]string, error) {
	var out []string
	err := withMaint(ctx, t, func(c *pgx.Conn) error {
		rows, err := c.Query(ctx, "SELECT datname FROM pg_database WHERE NOT datistemplate AND starts_with(datname, $1) ORDER BY datname", prefix)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	return out, err
}

// Create creates a database, optionally as a copy of template. It returns
// false when the database already existed.
func Create(ctx context.Context, t *env.Target, name, template string) (bool, error) {
	created := false
	err := withMaint(ctx, t, func(c *pgx.Conn) error {
		var exists bool
		if err := c.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		sql := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize()
		if template != "" {
			// A template must have no other sessions while it is copied.
			if _, err := c.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()", template); err != nil {
				return err
			}
			sql += " TEMPLATE " + pgx.Identifier{template}.Sanitize()
		}
		if _, err := c.Exec(ctx, sql); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

// Drop drops a database, terminating its sessions (PostgreSQL 13+).
func Drop(ctx context.Context, t *env.Target, name string) error {
	return withMaint(ctx, t, func(c *pgx.Conn) error {
		_, err := c.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		return err
	})
}

// Bootstrap idempotently creates schemas and extensions in dbname. These
// cluster/database-level objects are exactly what pgschema does not manage.
func Bootstrap(ctx context.Context, t *env.Target, dbname string, schemas, extensions []string) error {
	conn, err := Connect(ctx, t, dbname)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	for _, s := range schemas {
		if s == "public" {
			continue
		}
		if _, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{s}.Sanitize()); err != nil {
			return fmt.Errorf("create schema %s: %w", s, err)
		}
	}
	for _, e := range extensions {
		if _, err := conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS "+pgx.Identifier{e}.Sanitize()); err != nil {
			return fmt.Errorf("create extension %s: %w", e, err)
		}
	}
	return nil
}

// ExecFile runs a SQL file in one implicit transaction using the simple query
// protocol, which accepts multiple statements. psql meta-commands are not
// supported.
func ExecFile(ctx context.Context, t *env.Target, dbname, path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(stripComments(string(b))) == "" {
		return nil
	}
	conn, err := Connect(ctx, t, dbname)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, string(b), pgx.QueryExecModeSimpleProtocol); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func stripComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// SQLFiles expands a list of files and directories into sorted .sql files.
// Missing entries are skipped.
func SQLFiles(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			out = append(out, p)
			continue
		}
		matches, err := filepath.Glob(filepath.Join(p, "*.sql"))
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		out = append(out, matches...)
	}
	return out, nil
}

// ServerVersion returns server_version_num / 10000 (the major version).
func ServerVersion(ctx context.Context, t *env.Target) (int, error) {
	var v int
	err := withMaint(ctx, t, func(c *pgx.Conn) error {
		return c.QueryRow(ctx, "SELECT current_setting('server_version_num')::int / 10000").Scan(&v)
	})
	return v, err
}

// MissingBootstrap returns the schemas and extensions not yet present in dbname.
func MissingBootstrap(ctx context.Context, t *env.Target, dbname string, schemas, extensions []string) ([]string, error) {
	conn, err := Connect(ctx, t, dbname)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	var missing []string
	for _, s := range schemas {
		var ok bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)", s).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			missing = append(missing, "schema "+s)
		}
	}
	for _, e := range extensions {
		var ok bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = $1)", e).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			missing = append(missing, "extension "+e)
		}
	}
	return missing, nil
}
