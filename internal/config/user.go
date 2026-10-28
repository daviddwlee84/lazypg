package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Instance describes a PostgreSQL server reachable over the network. It never
// contains secrets; passwords live in credentials.toml.
type Instance struct {
	Host        string `toml:"host"`
	Port        int    `toml:"port,omitempty"`
	User        string `toml:"user,omitempty"`
	SSLMode     string `toml:"sslmode,omitempty"`
	MaintDB     string `toml:"maintenance_db,omitempty"` // database used for CREATE/DROP DATABASE; default "postgres"
	Tier        string `toml:"tier,omitempty"`           // optional guard: must match the env's tier when set
	Shared      bool   `toml:"shared,omitempty"`         // prefix physical db names with the project name
	Description string `toml:"description,omitempty"`
}

// WithDefaults fills in port, user, sslmode, and maintenance database.
func (i Instance) WithDefaults() Instance {
	if i.Port == 0 {
		i.Port = 5432
	}
	if i.User == "" {
		i.User = "postgres"
	}
	if i.SSLMode == "" {
		i.SSLMode = "prefer"
	}
	if i.MaintDB == "" {
		i.MaintDB = "postgres"
	}
	return i
}

// OrgRef registers an org config source on this machine.
type OrgRef struct {
	Path string `toml:"path,omitempty"` // local directory containing lazypg-org.toml
	Git  string `toml:"git,omitempty"`  // repository URL; checked out under the data dir
	Ref  string `toml:"ref,omitempty"`
}

// Override holds per-project, per-user adjustments that must not be committed.
type Override struct {
	DepPaths map[string]string `toml:"dep_paths,omitempty"` // dep name -> local checkout path
}

// User is ~/.config/lazypg/config.toml.
type User struct {
	Orgs      map[string]OrgRef   `toml:"orgs,omitempty"`
	Instances map[string]Instance `toml:"instances,omitempty"`
	Overrides map[string]Override `toml:"overrides,omitempty"`
}

// Org is lazypg-org.toml, committed in an org infrastructure repository.
type Org struct {
	Org       string              `toml:"org"`
	Instances map[string]Instance `toml:"instances"`
}

// Credential resolves a password for an instance alias. Exactly one source is used,
// checked in order: PasswordCmd, PasswordEnv, Password.
type Credential struct {
	User        string `toml:"user,omitempty"`
	Password    string `toml:"password,omitempty"`
	PasswordCmd string `toml:"password_cmd,omitempty"`
	PasswordEnv string `toml:"password_env,omitempty"`
}

// Credentials is ~/.config/lazypg/credentials.toml (mode 0600), keyed by instance alias.
type Credentials map[string]Credential

// LoadTOML decodes path into v; a missing file leaves v untouched.
func LoadTOML(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := toml.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// SaveTOML atomically writes v to path with the given permissions.
func SaveTOML(path string, v any, perm os.FileMode) error {
	data, err := toml.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// LoadOrg reads lazypg-org.toml from dir.
func LoadOrg(dir string) (*Org, error) {
	var o Org
	path := filepath.Join(dir, OrgFile)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("org config: %w", err)
	}
	if err := LoadTOML(path, &o); err != nil {
		return nil, err
	}
	for name, inst := range o.Instances {
		if inst.Host == "" {
			return nil, fmt.Errorf("%s: instance %q has no host", path, name)
		}
		if inst.Tier != "" && !ValidTier(inst.Tier) {
			return nil, fmt.Errorf("%s: instance %q has invalid tier %q", path, name, inst.Tier)
		}
	}
	return &o, nil
}

// SplitAlias splits "org/name" into its parts; a bare name has an empty org.
func SplitAlias(alias string) (org, name string) {
	if i := strings.IndexByte(alias, '/'); i >= 0 {
		return alias[:i], alias[i+1:]
	}
	return "", alias
}
