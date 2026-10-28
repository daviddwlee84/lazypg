package config

import (
	"fmt"
	"regexp"
	"strings"
)

var componentRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// ValidateComponent protects names used as filesystem path components.
func ValidateComponent(kind, name string) error {
	if !componentRE.MatchString(name) || len(name) > 63 {
		return fmt.Errorf("invalid %s name %q (use 1-63 lowercase letters, digits, underscores or hyphens; start with a letter)", kind, name)
	}
	return nil
}

// ValidateDBName prevents PostgreSQL truncation and collisions with bookkeeping and branches.
func ValidateDBName(name string) error {
	if name == "" || len(name) > 63 || strings.ContainsRune(name, 0) || strings.Contains(name, "__") {
		return fmt.Errorf("invalid database name %q: use 1-63 bytes without NUL or __", name)
	}
	switch name {
	case "postgres", "template0", "template1", PlanDB:
		return fmt.Errorf("database name %q is reserved", name)
	}
	return nil
}
