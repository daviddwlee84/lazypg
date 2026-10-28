package pgexec

import (
	"context"
	"github.com/daviddwlee84/lazypg/internal/env"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerFallbackDoesNotExposePasswordInArguments(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	secret := filepath.Join(dir, "env")
	t.Setenv("PATH", dir)
	t.Setenv("CAPTURE", args)
	t.Setenv("CAPTURE_ENV", secret)
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE\"\nprintf '%s' \"$PGPASSWORD\" > \"$CAPTURE_ENV\"\n"
	os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755)
	target := &env.Target{Host: "localhost", Port: 5432, User: "app", Password: "private-test-password", SSLMode: "require"}
	if err := Run(context.Background(), target, "postgres:18", "app", false, nil, nil, nil, "psql", "-c", "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), target.Password) || !strings.Contains(string(data), "PGPASSWORD\n") {
		t.Fatal("unsafe docker arguments")
	}
	data, err = os.ReadFile(secret)
	if err != nil || string(data) != target.Password {
		t.Fatal("password environment not forwarded", err)
	}
}
