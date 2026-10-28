package app

import (
	"context"
	"fmt"
	"os"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/dbops"
	"github.com/daviddwlee84/lazypg/internal/deps"
	"github.com/daviddwlee84/lazypg/internal/env"
	"github.com/daviddwlee84/lazypg/internal/pgexec"
)

// ResolveDeps resolves dependencies, updating lazypg.lock when it changes.
func (a *App) ResolveDeps(ctx context.Context, update bool) ([]*deps.Resolved, error) {
	if len(a.Project.Deps) == 0 {
		return nil, nil
	}
	lock, err := deps.LoadLock(a.Project)
	if err != nil {
		return nil, err
	}
	res, newLock, err := deps.Resolve(ctx, a.Project, a.Resolver.User, lock, update)
	if err != nil {
		return nil, err
	}
	if !lockEqual(lock, newLock) {
		if err := deps.SaveLock(a.Project, newLock); err != nil {
			return nil, err
		}
		a.Out.Info("updated %s", config.LockFile)
	}
	return res, nil
}

func lockEqual(a, b *deps.Lock) bool {
	if len(a.Deps) != len(b.Deps) {
		return false
	}
	for i := range a.Deps {
		if a.Deps[i] != b.Deps[i] {
			return false
		}
	}
	return true
}

// DepRefs maps resolved dependencies to databases on t.
func (a *App) DepRefs(t *env.Target, res []*deps.Resolved) ([]DBRef, error) {
	owned := map[string]bool{}
	refs, err := a.Owned(t, nil)
	if err != nil {
		return nil, err
	}
	for _, r := range refs {
		owned[r.DBName] = true
	}
	var out []DBRef
	for _, d := range res {
		for _, db := range d.Databases {
			name := t.DepDBName(d.OwnerName(), db)
			if err := config.ValidateDBName(name); err != nil {
				return nil, err
			}
			if owned[name] {
				return nil, fmt.Errorf("dep %s database %s collides with a database this project owns; set db_name on one of them", d.Dep.Name, name)
			}
			owned[name] = true
			owner := d.Owner
			if owner == nil {
				owner = a.Project
			}
			out = append(out, DBRef{Owner: owner, DB: db, DBName: name, Dep: d})
		}
	}
	return out, nil
}

// ApplyDeps creates dependency databases on a local target from their owners'
// schema (or vendored SQL) plus the owners' seeds. Existing databases are left
// alone unless force is set: on a shared server the owner project may be
// developing a newer schema in the very same database.
func (a *App) ApplyDeps(ctx context.Context, t *env.Target, refs []DBRef, force bool) error {
	if t.Tier != config.TierLocal {
		return fmt.Errorf("dependencies are only built on local; on %s they are owned and migrated by their own repositories", t.Env)
	}
	for _, r := range refs {
		exists, err := dbops.Exists(ctx, t, r.DBName)
		if err != nil {
			return err
		}
		if exists && !force {
			a.Out.Info("dep %s: %s exists, leaving it as is", r.Dep.Dep.Name, r.DBName)
			continue
		}
		if exists {
			if err := dbops.Drop(ctx, t, r.DBName); err != nil {
				return err
			}
		}
		a.Out.Info("dep %s: building %s from %s", r.Dep.Dep.Name, r.DBName, r.Dep.Source)
		if r.Dep.Vendor != "" {
			if err := a.loadVendor(ctx, t, r); err != nil {
				return err
			}
			continue
		}
		if _, err := a.Migrate(ctx, t, []DBRef{r}, MigrateOptions{Confirmed: true}); err != nil {
			return fmt.Errorf("dep %s: %w", r.Dep.Dep.Name, err)
		}
		if err := a.seedOne(ctx, t, r); err != nil {
			return fmt.Errorf("dep %s seeds: %w", r.Dep.Dep.Name, err)
		}
	}
	return nil
}

func (a *App) loadVendor(ctx context.Context, t *env.Target, r DBRef) error {
	f, err := os.Open(r.Dep.Vendor)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := dbops.Create(ctx, t, r.DBName, ""); err != nil {
		return err
	}
	return pgexec.Run(ctx, t, a.Project.Local.Image, r.DBName, false, f, nil, nil,
		"psql", "-v", "ON_ERROR_STOP=1", "-q", "-f", "-")
}
