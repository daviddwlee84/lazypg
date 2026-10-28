// Package engine adapts schema-management tools behind one interface so each
// database can choose its engine: declarative pgschema (default) or versioned
// dbmate migrations. One-off pre/post scripts complement both.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/env"
	"github.com/daviddwlee84/lazypg/internal/tools"
)

// Context is everything an engine needs for one database on one target.
type Context struct {
	Project *config.Project
	DB      *config.Database
	Target  *env.Target
	DBName  string
	// PlanHost is a lazypg-managed local target whose PlanDB database is used
	// by pgschema to validate desired state. Nil falls back to pgschema's
	// embedded PostgreSQL.
	PlanHost *env.Target
	Log      io.Writer
	Logf     tools.Logf
}

// Plan is the pending change for one database.
type Plan struct {
	Database string                     `json:"database"`
	DBName   string                     `json:"db_name"`
	Engine   string                     `json:"engine"`
	Changes  bool                       `json:"changes"`
	Missing  bool                       `json:"missing,omitempty"` // database does not exist yet
	Schemas  map[string]json.RawMessage `json:"schemas,omitempty"` // pgschema plan JSON per schema
	Pending  []string                   `json:"pending,omitempty"` // dbmate migrations not yet applied
	// PreScripts were pending when the plan was made; a schema diff computed
	// before they run may be wrong, so such a plan is not applicable.
	PreScripts  []string `json:"pre_scripts,omitempty"`
	PostScripts []string `json:"post_scripts,omitempty"`
	Human       string   `json:"human,omitempty"`
}

// Engine manages one database's schema.
type Engine interface {
	Plan(ctx context.Context, c *Context) (*Plan, error)
	// Apply executes p. Plans loaded from a file are re-validated: pgschema
	// checks its source fingerprint, dbmate compares the pending set.
	Apply(ctx context.Context, c *Context, p *Plan) error
	// Pull writes the live schema into the repository (pgschema: desired
	// state files; dbmate: schema_file snapshot).
	Pull(ctx context.Context, c *Context) error
}

// For returns the engine for a database.
func For(db *config.Database) (Engine, error) {
	switch db.Engine {
	case config.EnginePgschema:
		return Pgschema{}, nil
	case config.EngineDbmate:
		return Dbmate{}, nil
	}
	return nil, fmt.Errorf("unknown engine %q", db.Engine)
}

// Bundle is a saved plan for CI: generated in a PR, applied after merge.
type Bundle struct {
	TargetID  string    `json:"target_id"`
	SourceID  string    `json:"source_id"`
	Format    int       `json:"lazypg_plan"`
	Project   string    `json:"project"`
	Env       string    `json:"env"`
	CreatedAt time.Time `json:"created_at"`
	Plans     []*Plan   `json:"databases"`
}

// BundleFormat is the current plan bundle version.
const BundleFormat = 2

// WriteBundle saves a bundle to path.
func WriteBundle(path string, b *Bundle) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// ReadBundle loads a bundle from path.
func ReadBundle(path string) (*Bundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if b.Format != BundleFormat {
		return nil, fmt.Errorf("%s: unsupported plan format %d", path, b.Format)
	}
	return &b, nil
}
