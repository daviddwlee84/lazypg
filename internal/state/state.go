// Package state persists per-machine runtime state (allocated ports, the
// selected environment and branch) under the XDG state directory, plus an
// append-only audit log of operations against non-local environments.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/xdg"
)

// Project is the runtime state of one project checkout. Distinct checkouts
// (including git worktrees) of the same project get distinct state.
type Project struct {
	Project string `toml:"project"`
	Path    string `toml:"path"`
	Port    int    `toml:"port,omitempty"`
	Env     string `toml:"env,omitempty"`
	// Branches maps an env name to its active database branch.
	Branches map[string]string `toml:"branches,omitempty"`
}

// Server is the runtime state of a named machine-level shared instance.
type Server struct {
	Name  string `toml:"name"`
	Image string `toml:"image"`
	Port  int    `toml:"port"`
}

// ProjectID identifies a checkout: project name plus a hash of its absolute path.
func ProjectID(p *config.Project) string {
	sum := sha256.Sum256([]byte(p.Dir))
	return p.Project + "-" + hex.EncodeToString(sum[:])[:8]
}

func projectPath(id string) string {
	return filepath.Join(xdg.StateDir(), "projects", id+".toml")
}

func serverPath(name string) string {
	return filepath.Join(xdg.StateDir(), "servers", name+".toml")
}

// LoadProject returns the state for p, initialized if absent.
func LoadProject(p *config.Project) (*Project, error) {
	s := &Project{Project: p.Project, Path: p.Dir}
	if err := config.LoadTOML(projectPath(ProjectID(p)), s); err != nil {
		return nil, err
	}
	return s, nil
}

// SaveProject persists project state.
func SaveProject(p *config.Project, s *Project) error {
	return config.SaveTOML(projectPath(ProjectID(p)), s, 0o644)
}

// LoadServer returns the named server's state; ok is false when it was never created.
func LoadServer(name string) (*Server, bool, error) {
	if err := config.ValidateComponent("server", name); err != nil {
		return nil, false, err
	}
	path := serverPath(name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &Server{Name: name}, false, nil
	}
	s := &Server{Name: name}
	if err := config.LoadTOML(path, s); err != nil {
		return nil, false, err
	}
	return s, true, nil
}

// SaveServer persists server state.
func SaveServer(s *Server) error {
	if err := config.ValidateComponent("server", s.Name); err != nil {
		return err
	}
	return config.SaveTOML(serverPath(s.Name), s, 0o644)
}

// ListServers returns all known servers.
func ListServers() ([]*Server, error) {
	matches, err := filepath.Glob(filepath.Join(xdg.StateDir(), "servers", "*.toml"))
	if err != nil {
		return nil, err
	}
	var out []*Server
	for _, m := range matches {
		s := &Server{}
		if err := config.LoadTOML(m, s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// FreePort returns the first port at or after start that can be bound on
// loopback and is not in the reserved set.
func FreePort(start int, reserved map[int]bool) (int, error) {
	for p := start; p < start+1000; p++ {
		if reserved[p] {
			continue
		}
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			continue
		}
		l.Close()
		return p, nil
	}
	return 0, fmt.Errorf("no free port in %d-%d", start, start+999)
}

// ReservedPorts lists ports already allocated to any project or server, so two
// stopped instances never get the same port.
func ReservedPorts() map[int]bool {
	out := map[int]bool{}
	for _, dir := range []string{"projects", "servers"} {
		matches, _ := filepath.Glob(filepath.Join(xdg.StateDir(), dir, "*.toml"))
		for _, m := range matches {
			var v struct {
				Port int `toml:"port"`
			}
			if config.LoadTOML(m, &v) == nil && v.Port != 0 {
				out[v.Port] = true
			}
		}
	}
	return out
}

// AuditEntry records one operation against a non-local environment.
type AuditEntry struct {
	Time     time.Time `json:"time"`
	Project  string    `json:"project"`
	Env      string    `json:"env"`
	Tier     string    `json:"tier"`
	Instance string    `json:"instance"`
	Op       string    `json:"op"`
	Database string    `json:"database,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	User     string    `json:"user,omitempty"`
}

// Audit appends an entry to audit.jsonl. Failures are returned but callers
// treat them as warnings: the audit log must never block an operation.
func Audit(e AuditEntry) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	if e.User == "" {
		e.User = os.Getenv("USER")
	}
	path := filepath.Join(xdg.StateDir(), "audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(e)
}
