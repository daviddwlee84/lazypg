// Package config loads and validates lazypg's project, org, and user configuration.
package config

// AppName is the single source of truth for the tool's name. The binary name,
// config file names, XDG directory names, and container prefixes derive from it
// so that a future rename touches one line.
const AppName = "lazypg"

const (
	ProjectFile = AppName + ".toml"
	OrgFile     = AppName + "-org.toml"
	LockFile    = AppName + ".lock"
	// EjectDir is the in-repo directory used when a project ejects its compose file.
	EjectDir = "." + AppName
	// MetaSchema holds lazypg's own bookkeeping tables (script history) so that
	// declarative engines never see them in user schemas.
	MetaSchema = AppName
	// PlanDB is the scratch database pgschema uses to validate desired state.
	PlanDB = AppName + "_plan"
)
