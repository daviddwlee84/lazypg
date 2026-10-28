package config

import (
	"strings"
	"testing"
)

func TestRejectUnsafeComponents(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", ".", "a/b", "", "a\nname", strings.Repeat("a", 64)} {
		if ValidateComponent("server", name) == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, name := range []string{"default", "my-server", "my_server"} {
		if err := ValidateComponent("server", name); err != nil {
			t.Fatal(err)
		}
	}
}
