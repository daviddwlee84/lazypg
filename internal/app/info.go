package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/daviddwlee84/lazypg/internal/dbops"
	"github.com/daviddwlee84/lazypg/internal/env"
)

// ConnEnv returns the variables `exec`, `env shell`, and `env dotenv` export:
// DATABASE_URL and PG* for the primary database (the first owned one, or
// primary), plus <NAME>_DATABASE_URL for every owned and dependency database.
func (a *App) ConnEnv(t *env.Target, primary string, depRefs []DBRef) ([][2]string, error) {
	refs, err := a.Owned(t, nil)
	if err != nil {
		return nil, err
	}
	all := append(refs, depRefs...)
	if len(all) == 0 {
		return nil, fmt.Errorf("no databases")
	}
	main := all[0]
	if primary != "" {
		found := false
		for _, r := range all {
			if r.DB.Name == primary {
				main, found = r, true
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown database %q", primary)
		}
	}
	vars := [][2]string{
		{"LAZYPG_ENV", t.Env},
		{"LAZYPG_TIER", t.Tier},
		{"DATABASE_URL", t.URL(main.DBName)},
	}
	for _, kv := range t.PGEnv(main.DBName) {
		k, v, _ := strings.Cut(kv, "=")
		vars = append(vars, [2]string{k, v})
	}
	for _, r := range all {
		vars = append(vars, [2]string{strings.ToUpper(r.DB.Name) + "_DATABASE_URL", t.URL(r.DBName)})
	}
	return vars, nil
}

// DBStatus describes one database on a target.
type DBStatus struct {
	Database string `json:"database"`
	DBName   string `json:"db_name"`
	Engine   string `json:"engine,omitempty"`
	Owner    string `json:"owner"`
	Exists   bool   `json:"exists"`
}

// Status is the result of `status`.
type Status struct {
	Env       string     `json:"env"`
	Tier      string     `json:"tier"`
	Instance  string     `json:"instance"`
	Kind      string     `json:"kind"`
	Branch    string     `json:"branch,omitempty"`
	Container string     `json:"container,omitempty"`
	URL       string     `json:"url,omitempty"`
	Reachable bool       `json:"reachable"`
	Error     string     `json:"error,omitempty"`
	Databases []DBStatus `json:"databases"`
}

// Status inspects t without changing anything.
func (a *App) Status(ctx context.Context, t *env.Target, depRefs []DBRef) *Status {
	s := &Status{Env: t.Env, Tier: t.Tier, Instance: t.Instance, Kind: t.Kind, Branch: t.Branch}
	refs, _ := a.Owned(t, nil)
	all := append(refs, depRefs...)
	if t.Managed() {
		if t.Port == 0 {
			s.Container = "never started"
		} else if st, err := t.Compose.State(ctx); err != nil {
			s.Error = err.Error()
		} else if st == "" {
			s.Container = "absent"
		} else {
			s.Container = st
		}
	}
	if len(all) > 0 && t.Port != 0 {
		s.URL = t.RedactedURL(all[0].DBName)
	}
	reachable := t.Port != 0 && (!t.Managed() || s.Container == "running")
	if reachable {
		if _, err := dbops.ServerVersion(ctx, t); err != nil {
			s.Error, reachable = err.Error(), false
		}
	}
	s.Reachable = reachable
	for _, r := range all {
		d := DBStatus{Database: r.DB.Name, DBName: r.DBName, Engine: r.DB.Engine, Owner: r.Owner.Project}
		if r.Dep != nil {
			d.Owner = r.Dep.OwnerName() + " (dep)"
		}
		if reachable {
			d.Exists, _ = dbops.Exists(ctx, t, r.DBName)
		}
		s.Databases = append(s.Databases, d)
	}
	return s
}

// EnvInfo is one row of `env list`.
type EnvInfo struct {
	Name     string `json:"name"`
	Tier     string `json:"tier"`
	Instance string `json:"instance"`
	Current  bool   `json:"current"`
	Error    string `json:"error,omitempty"`
}

// Envs lists environments with their resolution status.
func (a *App) Envs(ctx context.Context) []EnvInfo {
	cur := a.Resolver.Current()
	var out []EnvInfo
	for _, name := range a.Project.EnvNames() {
		info := EnvInfo{Name: name, Current: name == cur}
		t, err := a.Resolver.Resolve(ctx, name)
		if err != nil {
			info.Error = err.Error()
			if spec, ok := a.Project.Envs[name]; ok {
				info.Tier, info.Instance = spec.Tier, spec.Instance
			}
		} else {
			info.Tier, info.Instance = t.Tier, t.Instance
		}
		out = append(out, info)
	}
	return out
}
