// Package deps resolves read-only database dependencies owned by other
// repositories: a git repository pinned by lazypg.lock, a local checkout, or
// a vendored schema file.
package deps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/proc"
	"github.com/daviddwlee84/lazypg/internal/xdg"
)

// Resolved is a dependency ready to be applied.
type Resolved struct {
	Dep       config.Dep
	Owner     *config.Project // nil for vendored deps
	Databases []*config.Database
	Commit    string
	Source    string // human-readable origin
	Vendor    string // absolute path of the vendored SQL file
}

// OwnerName is the project name used for physical database names.
func (r *Resolved) OwnerName() string {
	if r.Owner != nil {
		return r.Owner.Project
	}
	return r.Dep.Name
}

// Lock is lazypg.lock: the exact commit of every git dependency.
type Lock struct {
	Deps []LockedDep `toml:"deps"`
}

// LockedDep pins one git dependency.
type LockedDep struct {
	Name   string `toml:"name"`
	Git    string `toml:"git"`
	Ref    string `toml:"ref,omitempty"`
	Commit string `toml:"commit"`
}

// LoadLock reads lazypg.lock (missing file = empty lock).
func LoadLock(p *config.Project) (*Lock, error) {
	l := &Lock{}
	return l, config.LoadTOML(filepath.Join(p.Dir, config.LockFile), l)
}

// SaveLock writes lazypg.lock.
func SaveLock(p *config.Project, l *Lock) error {
	return config.SaveTOML(filepath.Join(p.Dir, config.LockFile), l, 0o644)
}

func (l *Lock) find(d config.Dep) (LockedDep, bool) {
	for _, e := range l.Deps {
		if e.Name == d.Name && e.Git == d.Git && e.Ref == d.Ref {
			return e, true
		}
	}
	return LockedDep{}, false
}

// Resolve resolves every dependency. With update, git refs are re-resolved
// and the returned lock reflects the new commits; otherwise locked commits win.
func Resolve(ctx context.Context, p *config.Project, u *config.User, lock *Lock, update bool) ([]*Resolved, *Lock, error) {
	newLock := &Lock{}
	var out []*Resolved
	overrides := u.Overrides[p.Project].DepPaths
	for _, d := range p.Deps {
		r := &Resolved{Dep: d}
		var dir string
		switch {
		case overrides[d.Name] != "":
			dir = expand(overrides[d.Name])
			r.Source = "override " + dir
			// A personal override must not rewrite the committed lock.
			if locked, ok := lock.find(d); ok && d.Git != "" {
				newLock.Deps = append(newLock.Deps, locked)
			}
		case d.Path != "":
			dir = p.Abs(expand(d.Path))
			r.Source = "path " + d.Path
		case d.Git != "":
			locked, ok := lock.find(d)
			commit, cdir, err := checkout(ctx, d, locked.Commit, ok && !update)
			if err != nil {
				return nil, nil, fmt.Errorf("dep %s: %w", d.Name, err)
			}
			dir, r.Commit = cdir, commit
			r.Source = fmt.Sprintf("git %s@%s (%s)", d.Git, refOr(d.Ref), short(commit))
			newLock.Deps = append(newLock.Deps, LockedDep{Name: d.Name, Git: d.Git, Ref: d.Ref, Commit: commit})
		case d.Vendor != "":
			r.Vendor = p.Abs(d.Vendor)
			r.Source = "vendor " + d.Vendor
			r.Databases = []*config.Database{{Name: d.Databases[0]}}
			out = append(out, r)
			continue
		}
		owner, err := config.LoadProject(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("dep %s: %w", d.Name, err)
		}
		r.Owner = owner
		for i := range owner.Databases {
			db := &owner.Databases[i]
			if len(d.Databases) == 0 || contains(d.Databases, db.Name) {
				r.Databases = append(r.Databases, db)
			}
		}
		for _, want := range d.Databases {
			if _, ok := owner.DB(want); !ok {
				return nil, nil, fmt.Errorf("dep %s: %s owns no database %q", d.Name, owner.Project, want)
			}
		}
		out = append(out, r)
	}
	return out, newLock, nil
}

// checkout clones (or reuses) a git dependency in the cache and checks out
// either the locked commit or the current commit of its ref.
func checkout(ctx context.Context, d config.Dep, locked string, useLock bool) (string, string, error) {
	sum := sha256.Sum256([]byte(d.Git))
	dir := filepath.Join(xdg.CacheDir(), "deps", d.Name+"-"+hex.EncodeToString(sum[:])[:8])
	git := func(args ...string) (string, error) {
		out, err := proc.Output(ctx, proc.Cmd{Name: "git", Args: append([]string{"-C", dir}, args...)})
		return strings.TrimSpace(string(out)), err
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return "", "", err
		}
		if err := proc.Run(ctx, proc.Cmd{Name: "git", Args: []string{"clone", "--quiet", "--", d.Git, dir}}); err != nil {
			return "", "", err
		}
	}
	target := locked
	if !useLock || locked == "" {
		if _, err := git("fetch", "--quiet", "--tags", "--force", "origin"); err != nil {
			return "", "", err
		}
		ref := refOr(d.Ref)
		var err error
		// Prefer the remote branch so a moving branch ref picks up new commits.
		target, err = git("rev-parse", "--verify", "--quiet", "origin/"+ref+"^{commit}")
		if err != nil || target == "" {
			if target, err = git("rev-parse", "--verify", ref+"^{commit}"); err != nil {
				return "", "", fmt.Errorf("cannot resolve ref %q", ref)
			}
		}
	} else if _, err := git("cat-file", "-e", target+"^{commit}"); err != nil {
		if _, err := git("fetch", "--quiet", "--tags", "--force", "origin"); err != nil {
			return "", "", err
		}
	}
	if _, err := git("checkout", "--quiet", "--detach", target); err != nil {
		return "", "", err
	}
	return target, dir, nil
}

func refOr(ref string) string {
	if ref == "" {
		return "HEAD"
	}
	return ref
}

func short(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}
