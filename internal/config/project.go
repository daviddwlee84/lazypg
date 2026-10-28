package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/pelletier/go-toml/v2"
)

// Engine names.
const (
	EnginePgschema = "pgschema"
	EngineDbmate   = "dbmate"
)

// Local instance modes.
const (
	ModeProject = "project" // one container per project checkout
	ModeServer  = "server"  // a machine-level shared instance with one database per project
)

// DefaultImage is the PostgreSQL image used for local instances.
const DefaultImage = "postgres:18"

// DefaultPgschemaVersion is used when a project does not pin [tools].pgschema.
const DefaultPgschemaVersion = "1.13.4"

// Project is the committed lazypg.toml.
type Project struct {
	Version   int                `toml:"version"`
	Project   string             `toml:"project"`
	Org       string             `toml:"org,omitempty"`
	Tools     Tools              `toml:"tools"`
	Local     Local              `toml:"local"`
	Databases []Database         `toml:"databases"`
	Deps      []Dep              `toml:"deps,omitempty"`
	Envs      map[string]EnvSpec `toml:"envs,omitempty"`

	// Dir is the repository root containing lazypg.toml.
	Dir string `toml:"-"`
}

// Tools pins external tool versions so every teammate and CI run the same ones.
type Tools struct {
	Pgschema string `toml:"pgschema,omitempty"`
}

// Local configures the implicit `local` environment.
type Local struct {
	Mode   string `toml:"mode,omitempty"`
	Image  string `toml:"image,omitempty"`
	Server string `toml:"server,omitempty"` // named machine server when Mode == "server"
}

// Database is a database this repository owns (and may migrate).
type Database struct {
	Name       string   `toml:"name"`
	Engine     string   `toml:"engine,omitempty"`
	DBName     string   `toml:"db_name,omitempty"`
	Schemas    []string `toml:"schemas,omitempty"`
	Extensions []string `toml:"extensions,omitempty"`
	SchemaDir  string   `toml:"schema_dir,omitempty"` // pgschema desired state root: <schema_dir>/<schema>/main.sql
	Migrations string   `toml:"migrations,omitempty"` // dbmate migrations directory
	SchemaFile string   `toml:"schema_file,omitempty"`
	Scripts    string   `toml:"scripts,omitempty"` // <scripts>/pre and <scripts>/post
	Seeds      []string `toml:"seeds,omitempty"`
	Bootstrap  string   `toml:"bootstrap,omitempty"`
}

// Dep is a read-only dependency on databases owned by another repository.
type Dep struct {
	Name      string   `toml:"name"`
	Git       string   `toml:"git,omitempty"`
	Ref       string   `toml:"ref,omitempty"`
	Path      string   `toml:"path,omitempty"`
	Vendor    string   `toml:"vendor,omitempty"`
	Databases []string `toml:"databases,omitempty"` // subset of the owner's databases; empty = all
}

// EnvSpec binds an environment name to a tier and an instance alias.
type EnvSpec struct {
	Tier     string            `toml:"tier"`
	Instance string            `toml:"instance"`
	DBNames  map[string]string `toml:"db_names,omitempty"` // logical name -> physical name override
}

var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ErrNoProject is returned when no lazypg.toml is found.
var ErrNoProject = errors.New("no " + ProjectFile + " found (run `" + AppName + " init`)")

// FindProject walks up from dir looking for lazypg.toml.
func FindProject(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(abs, ProjectFile)
		if _, err := os.Stat(p); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", ErrNoProject
		}
		abs = parent
	}
}

// LoadProject reads and validates lazypg.toml from the project root dir.
func LoadProject(dir string) (*Project, error) {
	data, err := os.ReadFile(filepath.Join(dir, ProjectFile))
	if err != nil {
		return nil, err
	}
	p, err := ParseProject(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, ProjectFile), err)
	}
	p.Dir = dir
	return p, nil
}

// ParseProject decodes, defaults, and validates a project file.
func ParseProject(data []byte) (*Project, error) {
	var p Project
	dec := toml.NewDecoder(bytesReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		var sm *toml.StrictMissingError
		if errors.As(err, &sm) {
			return nil, fmt.Errorf("unknown field(s):\n%s", sm.String())
		}
		return nil, err
	}
	p.applyDefaults()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

func (p *Project) applyDefaults() {
	if p.Version == 0 {
		p.Version = 1
	}
	if p.Tools.Pgschema == "" {
		p.Tools.Pgschema = DefaultPgschemaVersion
	}
	if p.Local.Mode == "" {
		p.Local.Mode = ModeProject
	}
	if p.Local.Image == "" {
		p.Local.Image = DefaultImage
	}
	if p.Local.Server == "" {
		p.Local.Server = "default"
	}
	for i := range p.Databases {
		d := &p.Databases[i]
		if d.Engine == "" {
			d.Engine = EnginePgschema
		}
		if len(d.Schemas) == 0 {
			d.Schemas = []string{"public"}
		}
		if d.SchemaDir == "" {
			d.SchemaDir = "db/schema"
		}
		if d.Migrations == "" {
			d.Migrations = "db/migrations"
		}
		if d.SchemaFile == "" {
			d.SchemaFile = "db/schema.sql"
		}
		if d.Scripts == "" {
			d.Scripts = "db/scripts"
		}
		if d.Seeds == nil {
			d.Seeds = []string{"db/seeds"}
		}
	}
}

// Validate checks names, tiers, and engines.
func (p *Project) Validate() error {
	var errs []error
	if p.Version != 1 {
		errs = append(errs, fmt.Errorf("unsupported version %d", p.Version))
	}
	if !identRe.MatchString(p.Project) {
		errs = append(errs, fmt.Errorf("project %q must match %s", p.Project, identRe))
	}
	switch p.Local.Mode {
	case ModeProject, ModeServer:
	default:
		errs = append(errs, fmt.Errorf("local.mode %q must be %q or %q", p.Local.Mode, ModeProject, ModeServer))
	}
	if len(p.Databases) == 0 && len(p.Deps) == 0 {
		errs = append(errs, errors.New("declare at least one [[databases]] or [[deps]]"))
	}
	seen := map[string]bool{}
	for _, d := range p.Databases {
		if !identRe.MatchString(d.Name) {
			errs = append(errs, fmt.Errorf("database name %q must match %s", d.Name, identRe))
		}
		if seen[d.Name] {
			errs = append(errs, fmt.Errorf("duplicate database %q", d.Name))
		}
		seen[d.Name] = true
		if d.Engine != EnginePgschema && d.Engine != EngineDbmate {
			errs = append(errs, fmt.Errorf("database %q: engine %q must be %q or %q", d.Name, d.Engine, EnginePgschema, EngineDbmate))
		}
		for _, s := range d.Schemas {
			if !identRe.MatchString(s) {
				errs = append(errs, fmt.Errorf("database %q: schema %q must match %s", d.Name, s, identRe))
			}
			if s == MetaSchema {
				errs = append(errs, fmt.Errorf("database %q: schema %q is reserved", d.Name, s))
			}
		}
	}
	depNames := map[string]bool{}
	for _, d := range p.Deps {
		if !identRe.MatchString(d.Name) {
			errs = append(errs, fmt.Errorf("dep name %q must match %s", d.Name, identRe))
		}
		if depNames[d.Name] {
			errs = append(errs, fmt.Errorf("duplicate dep %q", d.Name))
		}
		depNames[d.Name] = true
		n := 0
		for _, s := range []string{d.Git, d.Path, d.Vendor} {
			if s != "" {
				n++
			}
		}
		if n != 1 {
			errs = append(errs, fmt.Errorf("dep %q: set exactly one of git, path, vendor", d.Name))
		}
		if d.Vendor != "" && len(d.Databases) != 1 {
			errs = append(errs, fmt.Errorf("dep %q: vendor deps must list exactly one database", d.Name))
		}
	}
	for name, e := range p.Envs {
		if name == EnvLocal {
			errs = append(errs, fmt.Errorf("env %q is implicit and cannot be redefined", EnvLocal))
		}
		if !identRe.MatchString(name) {
			errs = append(errs, fmt.Errorf("env name %q must match %s", name, identRe))
		}
		if !ValidTier(e.Tier) || e.Tier == TierLocal {
			errs = append(errs, fmt.Errorf("env %q: tier %q must be one of dev, stage, prod", name, e.Tier))
		}
		if e.Instance == "" {
			errs = append(errs, fmt.Errorf("env %q: instance is required", name))
		}
	}
	return errors.Join(errs...)
}

// DB returns the owned database with the given logical name.
func (p *Project) DB(name string) (*Database, bool) {
	for i := range p.Databases {
		if p.Databases[i].Name == name {
			return &p.Databases[i], true
		}
	}
	return nil, false
}

// EnvNames returns all environment names, local first, then sorted by tier and name.
func (p *Project) EnvNames() []string {
	names := make([]string, 0, len(p.Envs)+1)
	for n := range p.Envs {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		ti, tj := TierRank(p.Envs[names[i]].Tier), TierRank(p.Envs[names[j]].Tier)
		if ti != tj {
			return ti < tj
		}
		return names[i] < names[j]
	})
	return append([]string{EnvLocal}, names...)
}

// Abs resolves a project-relative path.
func (p *Project) Abs(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(p.Dir, rel)
}
