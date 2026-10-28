package env

import (
	"fmt"
	"net"
	"net/url"
	"strconv"

	"github.com/daviddwlee84/lazypg/internal/config"
)

// BranchSep separates a database name from its branch suffix.
const BranchSep = "__"

// BaseDBName is the physical name of an owned database, ignoring branches.
// Precedence: env db_names override, database db_name, shared-instance
// prefix, logical name.
func (t *Target) BaseDBName(p *config.Project, db *config.Database) string {
	if n, ok := t.DBNames[db.Name]; ok && n != "" {
		return n
	}
	if db.DBName != "" {
		return db.DBName
	}
	if t.Shared {
		return p.Project + "_" + db.Name
	}
	return db.Name
}

// DBName is the physical name of an owned database on this target, including
// the active branch.
func (t *Target) DBName(p *config.Project, db *config.Database) string {
	base := t.BaseDBName(p, db)
	if t.Branch != "" {
		return base + BranchSep + t.Branch
	}
	return base
}

// DepDBName is the physical name of a database owned by another project.
// Branches never apply to dependencies.
func (t *Target) DepDBName(owner string, db *config.Database) string {
	if n, ok := t.DBNames[db.Name]; ok && n != "" {
		return n
	}
	if db.DBName != "" {
		return db.DBName
	}
	if t.Shared {
		return owner + "_" + db.Name
	}
	return db.Name
}

// URL returns a postgres:// connection URL for the given database.
func (t *Target) URL(dbname string) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(t.User, t.Password),
		Host:   net.JoinHostPort(t.Host, strconv.Itoa(t.Port)),
		Path:   "/" + dbname,
	}
	if t.Password == "" {
		u.User = url.User(t.User)
	}
	if t.SSLMode != "" {
		u.RawQuery = "sslmode=" + url.QueryEscape(t.SSLMode)
	}
	return u.String()
}

// RedactedURL is URL with the password masked, for display.
func (t *Target) RedactedURL(dbname string) string {
	c := *t
	if c.Password != "" {
		c.Password = "xxxxx"
	}
	return c.URL(dbname)
}

// PGEnv returns libpq environment variables for the given database.
func (t *Target) PGEnv(dbname string) []string {
	env := []string{
		"PGHOST=" + t.Host,
		"PGPORT=" + strconv.Itoa(t.Port),
		"PGUSER=" + t.User,
		"PGDATABASE=" + dbname,
	}
	if t.Password != "" {
		env = append(env, "PGPASSWORD="+t.Password)
	}
	if t.SSLMode != "" {
		env = append(env, "PGSSLMODE="+t.SSLMode)
	}
	return env
}

// Label is the short description used in banners and errors.
func (t *Target) Label() string {
	return fmt.Sprintf("%s · %s", t.Env, t.Instance)
}
