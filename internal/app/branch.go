package app

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/daviddwlee84/lazypg/internal/dbops"
	"github.com/daviddwlee84/lazypg/internal/env"
)

var branchRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_]*$`)

// maxIdent is PostgreSQL's identifier length limit (NAMEDATALEN - 1).
const maxIdent = 63

func (a *App) branchBase(t *env.Target) (*env.Target, []DBRef, error) {
	base := *t
	base.Branch = ""
	refs, err := a.Owned(&base, nil)
	return &base, refs, err
}

// BranchCreate copies every owned database (from the active branch, or main)
// into <db>__<name> with CREATE DATABASE … TEMPLATE, then switches to it.
func (a *App) BranchCreate(ctx context.Context, t *env.Target, name string, from string, switchTo bool) error {
	if !branchRe.MatchString(name) {
		return fmt.Errorf("branch name %q must match %s", name, branchRe)
	}
	if err := a.Check(t, env.OpBranch, "Create branch "+name); err != nil {
		return err
	}
	base, refs, err := a.branchBase(t)
	if err != nil {
		return err
	}
	if from == "" {
		from = t.Branch
	}
	for _, r := range refs {
		src := r.DBName
		if from != "" && from != "main" {
			src += env.BranchSep + from
		}
		dst := r.DBName + env.BranchSep + name
		if len(dst) > maxIdent {
			return fmt.Errorf("branch database name %s exceeds %d characters", dst, maxIdent)
		}
		ok, err := dbops.Exists(ctx, base, src)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("source database %s does not exist (run migrate first)", src)
		}
		a.Out.Info("branch %s → %s", src, dst)
		created, err := dbops.Create(ctx, base, dst, src)
		a.Audit(t, env.OpBranch, dst, "create from "+src, err)
		if err != nil {
			return err
		}
		if !created {
			return fmt.Errorf("branch database %s already exists", dst)
		}
	}
	if switchTo {
		return a.BranchUse(ctx, t, name)
	}
	return nil
}

// BranchUse selects the branch for t's env; "main" or "" returns to the base databases.
func (a *App) BranchUse(ctx context.Context, t *env.Target, name string) error {
	if name == "main" {
		name = ""
	}
	if name != "" {
		base, refs, err := a.branchBase(t)
		if err != nil {
			return err
		}
		for _, r := range refs {
			ok, err := dbops.Exists(ctx, base, r.DBName+env.BranchSep+name)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("branch %q does not exist for %s", name, r.DB.Name)
			}
		}
	}
	if a.State.Branches == nil {
		a.State.Branches = map[string]string{}
	}
	if name == "" {
		delete(a.State.Branches, t.Env)
	} else {
		a.State.Branches[t.Env] = name
	}
	t.Branch = name
	return a.SaveState()
}

// BranchInfo is one branch and whether it is active.
type BranchInfo struct {
	Name    string   `json:"name"`
	Current bool     `json:"current"`
	DBNames []string `json:"db_names"`
}

// BranchList lists branches present for the first owned database.
func (a *App) BranchList(ctx context.Context, t *env.Target) ([]BranchInfo, error) {
	base, refs, err := a.branchBase(t)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, nil
	}
	main := BranchInfo{Name: "main", Current: t.Branch == ""}
	byName := map[string]*BranchInfo{}
	var order []string
	for _, r := range refs {
		main.DBNames = append(main.DBNames, r.DBName)
		dbs, err := dbops.List(ctx, base, r.DBName+env.BranchSep)
		if err != nil {
			return nil, err
		}
		for _, d := range dbs {
			b := strings.TrimPrefix(d, r.DBName+env.BranchSep)
			if byName[b] == nil {
				byName[b] = &BranchInfo{Name: b, Current: b == t.Branch}
				order = append(order, b)
			}
			byName[b].DBNames = append(byName[b].DBNames, d)
		}
	}
	out := []BranchInfo{main}
	for _, b := range order {
		out = append(out, *byName[b])
	}
	return out, nil
}

// BranchDelete drops a branch's databases.
func (a *App) BranchDelete(ctx context.Context, t *env.Target, name string) error {
	if name == "" || name == "main" {
		return fmt.Errorf("cannot delete main; use reset instead")
	}
	if err := a.Check(t, env.OpBranch, "Delete branch "+name); err != nil {
		return err
	}
	base, refs, err := a.branchBase(t)
	if err != nil {
		return err
	}
	for _, r := range refs {
		d := r.DBName + env.BranchSep + name
		a.Out.Info("drop %s", d)
		err := dbops.Drop(ctx, base, d)
		a.Audit(t, env.OpBranch, d, "delete", err)
		if err != nil {
			return err
		}
	}
	if t.Branch == name {
		return a.BranchUse(ctx, t, "")
	}
	return nil
}
