package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/dbops"
	"github.com/daviddwlee84/lazypg/internal/deps"
	"github.com/daviddwlee84/lazypg/internal/engine"
	"github.com/daviddwlee84/lazypg/internal/env"
)

// DBRef is one database on a target: owned by this project or by a dependency.
type DBRef struct {
	Owner  *config.Project
	DB     *config.Database
	DBName string
	Dep    *deps.Resolved // nil for owned databases
}

// Owned returns this project's databases on t, optionally filtered by name.
func (a *App) Owned(t *env.Target, only []string) ([]DBRef, error) {
	var out []DBRef
	seen := map[string]bool{}
	for _, name := range only {
		if _, ok := a.Project.DB(name); !ok {
			return nil, fmt.Errorf("this project owns no database %q", name)
		}
	}
	for i := range a.Project.Databases {
		db := &a.Project.Databases[i]
		base := t.BaseDBName(a.Project, db)
		if err := config.ValidateDBName(base); err != nil {
			return nil, err
		}
		name := t.DBName(a.Project, db)
		if len(name) > 63 || seen[name] {
			return nil, fmt.Errorf("duplicate or overlong physical database name %q", name)
		}
		seen[name] = true
		if len(only) > 0 && !contains(only, db.Name) {
			continue
		}
		out = append(out, DBRef{Owner: a.Project, DB: db, DBName: name})
	}
	return out, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (a *App) engineCtx(t *env.Target, r DBRef, planHost *env.Target) *engine.Context {
	return &engine.Context{
		Project: r.Owner, DB: r.DB, Target: t, DBName: r.DBName,
		PlanHost: planHost, Log: a.Log(), Logf: a.Logf,
	}
}

func extensionsOf(refs []DBRef) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.DB.Extensions...)
	}
	return out
}

// prep is the pre-schema work for one database.
type prep struct {
	exists    bool
	bootstrap []string // missing schemas/extensions
	pre       []string // pending pre scripts
	preHuman  string
}

func (a *App) inspect(ctx context.Context, t *env.Target, r DBRef) (*prep, error) {
	p := &prep{}
	var err error
	if p.exists, err = dbops.Exists(ctx, t, r.DBName); err != nil {
		return nil, err
	}
	if !p.exists {
		// A new database is built from the desired state, so pre/post
		// scripts are recorded as applied instead of run (see prepare).
		for _, s := range r.DB.Schemas {
			if s != "public" {
				p.bootstrap = append(p.bootstrap, "schema "+s)
			}
		}
		for _, e := range r.DB.Extensions {
			p.bootstrap = append(p.bootstrap, "extension "+e)
		}
		return p, nil
	}
	if p.bootstrap, err = dbops.MissingBootstrap(ctx, t, r.DBName, r.DB.Schemas, r.DB.Extensions); err != nil {
		return nil, err
	}
	p.pre, p.preHuman, err = engine.PendingScripts(ctx, a.engineCtx(t, r, nil), engine.PhasePre)
	return p, err
}

// MigrateOptions controls Migrate.
type MigrateOptions struct {
	PlanFile  string // apply a saved bundle instead of planning now
	DryRun    bool
	Confirmed bool // policy already satisfied by the caller (e.g. reset)
}

// DBResult reports what happened to one database.
type DBResult struct {
	Database    string   `json:"database"`
	DBName      string   `json:"db_name"`
	Created     bool     `json:"created,omitempty"`
	Bootstrap   []string `json:"bootstrap,omitempty"`
	PreScripts  []string `json:"pre_scripts,omitempty"`
	Changes     bool     `json:"schema_changes"`
	PostScripts []string `json:"post_scripts,omitempty"`
	Applied     bool     `json:"applied"`
}

func (a *App) check(t *env.Target, op env.Op, what string, confirmed bool) error {
	if confirmed {
		return nil
	}
	return a.Check(t, op, what)
}

// Migrate brings databases to their desired state in two phases:
//
//  1. create missing databases, bootstrap schemas/extensions, run pre scripts;
//  2. plan schema changes, apply them, run post scripts.
//
// Pre scripts must run before planning: a rename done by a pre script makes
// the declarative diff a no-op instead of a drop-and-add.
func (a *App) Migrate(ctx context.Context, t *env.Target, refs []DBRef, opts MigrateOptions) ([]*DBResult, error) {
	results := make([]*DBResult, len(refs))
	preps := make([]*prep, len(refs))
	var phaseA []string
	for i, r := range refs {
		results[i] = &DBResult{Database: r.DB.Name, DBName: r.DBName}
		p, err := a.inspect(ctx, t, r)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.DBName, err)
		}
		preps[i] = p
		if !p.exists {
			phaseA = append(phaseA, fmt.Sprintf("create database %s", r.DBName))
		}
		for _, b := range p.bootstrap {
			phaseA = append(phaseA, fmt.Sprintf("%s: create %s", r.DBName, b))
		}
		for _, s := range p.pre {
			phaseA = append(phaseA, fmt.Sprintf("%s: pre script %s", r.DBName, s))
		}
		if p.preHuman != "" && !a.Out.JSON {
			fmt.Fprint(a.Out.Err, p.preHuman)
		}
	}

	var bundle map[string]*engine.Plan
	if opts.PlanFile != "" {
		if len(phaseA) > 0 {
			return nil, fmt.Errorf("cannot apply saved plan: pending work must happen before planning (%s); apply it and re-plan", strings.Join(phaseA, "; "))
		}
		b, err := engine.ReadBundle(opts.PlanFile)
		if err != nil {
			return nil, err
		}
		if b.Project != a.Project.Project || b.Env != t.Env {
			return nil, fmt.Errorf("plan %s is for %s/%s, not %s/%s", opts.PlanFile, b.Project, b.Env, a.Project.Project, t.Env)
		}
		targetID, sourceID, err := a.planIdentity(t, refs)
		if err != nil {
			return nil, err
		}
		if b.TargetID != targetID || b.SourceID != sourceID {
			return nil, fmt.Errorf("plan target or migration/script inputs changed; re-run plan and review")
		}
		bundle = map[string]*engine.Plan{}
		for _, p := range b.Plans {
			if p == nil {
				return nil, fmt.Errorf("plan contains null database entry")
			}
			if _, duplicate := bundle[p.Database]; duplicate {
				return nil, fmt.Errorf("plan contains duplicate database %s", p.Database)
			}
			if len(p.PreScripts) > 0 || p.Missing {
				return nil, fmt.Errorf("plan %s was made while %s still needed creation or pre scripts; re-plan", opts.PlanFile, p.Database)
			}
			bundle[p.Database] = p
		}
	}

	if len(phaseA) > 0 {
		for _, l := range phaseA {
			a.Out.Info("  %s", l)
		}
		if opts.DryRun {
			return results, nil
		}
		if err := a.check(t, env.OpMigrate, fmt.Sprintf("Prepare %d item(s) before planning", len(phaseA)), opts.Confirmed); err != nil {
			return nil, err
		}
		for i, r := range refs {
			if err := a.prepare(ctx, t, r, preps[i], results[i]); err != nil {
				a.Audit(t, env.OpMigrate, r.DBName, "prepare", err)
				return nil, fmt.Errorf("%s: %w", r.DBName, err)
			}
			var done []string
			if results[i].Created {
				done = append(done, "create database")
			}
			done = append(append(done, results[i].Bootstrap...), results[i].PreScripts...)
			a.Audit(t, env.OpMigrate, r.DBName, "prepare: "+strings.Join(done, ", "), nil)
		}
	}

	var planHost *env.Target
	if bundle == nil {
		var err error
		if planHost, err = a.PlanHost(ctx, t, extensionsOf(refs)); err != nil {
			return nil, err
		}
	}
	plans := make([]*engine.Plan, len(refs))
	work := 0
	for i, r := range refs {
		c := a.engineCtx(t, r, planHost)
		e, err := engine.For(r.DB)
		if err != nil {
			return nil, err
		}
		if bundle != nil {
			p, ok := bundle[r.DB.Name]
			if !ok {
				return nil, fmt.Errorf("plan %s has no entry for database %s", opts.PlanFile, r.DB.Name)
			}
			if p.DBName != r.DBName || p.Engine != r.DB.Engine {
				return nil, fmt.Errorf("plan database or engine does not match %s", r.DB.Name)
			}
			plans[i] = p
		} else if plans[i], err = e.Plan(ctx, c); err != nil {
			return nil, fmt.Errorf("%s: %w", r.DBName, err)
		}
		post, postHuman, err := engine.PendingScripts(ctx, c, engine.PhasePost)
		if err != nil {
			return nil, err
		}
		plans[i].PostScripts = post
		results[i].Changes, results[i].PostScripts = plans[i].Changes, post
		if plans[i].Changes || len(post) > 0 {
			work++
			if !a.Out.JSON {
				fmt.Fprint(a.Out.Err, plans[i].Human, postHuman)
			}
		}
	}
	if work == 0 {
		a.Out.Info("%s", "up to date")
		return results, nil
	}
	if opts.DryRun {
		return results, nil
	}
	if err := a.check(t, env.OpMigrate, fmt.Sprintf("Apply changes to %d database(s)", work), opts.Confirmed); err != nil {
		return nil, err
	}
	for i, r := range refs {
		if !plans[i].Changes && len(plans[i].PostScripts) == 0 {
			continue
		}
		c := a.engineCtx(t, r, planHost)
		e, _ := engine.For(r.DB)
		err := e.Apply(ctx, c, plans[i])
		if err == nil {
			err = engine.RunScripts(ctx, c, engine.PhasePost)
		}
		a.Audit(t, env.OpMigrate, r.DBName, "apply", err)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.DBName, err)
		}
		results[i].Applied = true
	}
	return results, nil
}

func (a *App) prepare(ctx context.Context, t *env.Target, r DBRef, p *prep, res *DBResult) error {
	if !p.exists {
		created, err := dbops.Create(ctx, t, r.DBName, "")
		if err != nil {
			return err
		}
		res.Created = created
	}
	if len(p.bootstrap) > 0 || !p.exists {
		if err := dbops.Bootstrap(ctx, t, r.DBName, r.DB.Schemas, r.DB.Extensions); err != nil {
			return err
		}
		res.Bootstrap = p.bootstrap
	}
	if res.Created && r.DB.Bootstrap != "" {
		if err := dbops.ExecFile(ctx, t, r.DBName, r.Owner.Abs(r.DB.Bootstrap)); err != nil {
			return err
		}
	}
	if !p.exists {
		c := a.engineCtx(t, r, nil)
		for _, phase := range []string{engine.PhasePre, engine.PhasePost} {
			names, err := engine.BaselineScripts(ctx, c, phase)
			if err != nil {
				return fmt.Errorf("baseline %s scripts: %w", phase, err)
			}
			if len(names) > 0 {
				a.Out.Info("  %s: new database, marked %d %s script(s) as applied", r.DBName, len(names), phase)
			}
		}
	}
	if len(p.pre) > 0 {
		if err := engine.RunScripts(ctx, a.engineCtx(t, r, nil), engine.PhasePre); err != nil {
			return err
		}
		res.PreScripts = p.pre
	}
	return nil
}

// Plan computes pending work without changing anything. Databases that do
// not exist yet, or have pending pre scripts, are reported but cannot be
// planned exactly.
func (a *App) Plan(ctx context.Context, t *env.Target, refs []DBRef) (*engine.Bundle, error) {
	targetID, sourceID, err := a.planIdentity(t, refs)
	if err != nil {
		return nil, err
	}
	b := &engine.Bundle{Format: engine.BundleFormat, Project: a.Project.Project, Env: t.Env, CreatedAt: time.Now().UTC(), TargetID: targetID, SourceID: sourceID}
	planHost, err := a.PlanHost(ctx, t, extensionsOf(refs))
	if err != nil {
		return nil, err
	}
	if planHost == nil && !t.Managed() {
		a.Out.Info("no local instance running; pgschema will validate with its embedded PostgreSQL (run `%s start` to use your local image)", config.AppName)
	}
	for _, r := range refs {
		p, err := a.inspect(ctx, t, r)
		if err != nil {
			return nil, err
		}
		if !p.exists {
			b.Plans = append(b.Plans, &engine.Plan{
				Database: r.DB.Name, DBName: r.DBName, Engine: r.DB.Engine, Missing: true, Changes: true,
				PreScripts: p.pre, Human: fmt.Sprintf("── %s ──\ndatabase %s does not exist; migrate will create it and apply the full desired state\n", r.DB.Name, r.DBName),
			})
			continue
		}
		c := a.engineCtx(t, r, planHost)
		e, err := engine.For(r.DB)
		if err != nil {
			return nil, err
		}
		plan, err := e.Plan(ctx, c)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.DBName, err)
		}
		plan.PreScripts = p.pre
		if len(p.pre) > 0 {
			plan.Human = p.preHuman + fmt.Sprintf("note: %d pre script(s) pending; the schema diff below is computed before they run and may differ\n", len(p.pre)) + plan.Human
		}
		if len(p.bootstrap) > 0 {
			plan.Human = fmt.Sprintf("── %s ──\nbootstrap: create %s\n", r.DB.Name, strings.Join(p.bootstrap, ", ")) + plan.Human
		}
		post, postHuman, err := engine.PendingScripts(ctx, c, engine.PhasePost)
		if err != nil {
			return nil, err
		}
		plan.PostScripts = post
		plan.Human += postHuman
		b.Plans = append(b.Plans, plan)
	}
	return b, nil
}

// Seed runs seed files for owned databases.
func (a *App) Seed(ctx context.Context, t *env.Target, refs []DBRef, confirmed bool) error {
	if err := a.check(t, env.OpSeed, "Run seed files", confirmed); err != nil {
		return err
	}
	for _, r := range refs {
		if err := a.seedOne(ctx, t, r); err != nil {
			a.Audit(t, env.OpSeed, r.DBName, "", err)
			return err
		}
		a.Audit(t, env.OpSeed, r.DBName, "", nil)
	}
	return nil
}

func (a *App) seedOne(ctx context.Context, t *env.Target, r DBRef) error {
	var paths []string
	for _, s := range r.DB.Seeds {
		paths = append(paths, r.Owner.Abs(s))
	}
	files, err := dbops.SQLFiles(paths)
	if err != nil {
		return err
	}
	for _, f := range files {
		a.Out.Info("seed %s ← %s", r.DBName, relPath(r.Owner.Dir, f))
		if err := dbops.ExecFile(ctx, t, r.DBName, f); err != nil {
			return err
		}
	}
	return nil
}

// Reset drops and rebuilds databases, then seeds them.
func (a *App) Reset(ctx context.Context, t *env.Target, refs []DBRef) ([]*DBResult, error) {
	var names []string
	for _, r := range refs {
		names = append(names, r.DBName)
	}
	if err := a.Check(t, env.OpReset, "Drop and rebuild "+strings.Join(names, ", ")); err != nil {
		return nil, err
	}
	for _, r := range refs {
		a.Out.Info("drop %s", r.DBName)
		err := dbops.Drop(ctx, t, r.DBName)
		a.Audit(t, env.OpReset, r.DBName, "drop", err)
		if err != nil {
			return nil, err
		}
	}
	res, err := a.Migrate(ctx, t, refs, MigrateOptions{Confirmed: true})
	if err != nil {
		return nil, err
	}
	return res, a.Seed(ctx, t, refs, true)
}

func relPath(base, p string) string {
	if strings.HasPrefix(p, base+string(os.PathSeparator)) {
		return p[len(base)+1:]
	}
	return p
}
