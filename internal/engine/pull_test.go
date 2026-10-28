package engine

import (
	"context"
	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/env"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedPullPreservesDesiredState(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "pgschema")
	os.WriteFile(bin, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	t.Setenv("LAZYPG_PGSCHEMA_BIN", bin)
	p := &config.Project{Dir: dir}
	db := &config.Database{SchemaDir: "schema", Schemas: []string{"public"}}
	file := DesiredFile(p, db, "public")
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.WriteFile(file, []byte("valuable desired state"), 0o644)
	err := (Pgschema{}).Pull(context.Background(), &Context{Project: p, DB: db, Target: &env.Target{}, Log: io.Discard})
	if err == nil {
		t.Fatal("expected failure")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "valuable desired state" {
		t.Fatal("old state was lost", err)
	}
}
