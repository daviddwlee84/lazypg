package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/env"
	"github.com/daviddwlee84/lazypg/internal/output"
)

func TestPlanIdentityBindsDestinationAndSQL(t *testing.T) {
	dir := t.TempDir()
	p, err := config.ParseProject([]byte("project='app'\n[[databases]]\nname='app'\nengine='dbmate'\n"))
	if err != nil {
		t.Fatal(err)
	}
	p.Dir = dir
	a := &App{Project: p}
	target := &env.Target{Host: "localhost", Port: 5432, User: "app", Password: "secret", Env: "dev"}
	refs, err := a.Owned(target, nil)
	if err != nil {
		t.Fatal(err)
	}
	migration := filepath.Join(dir, "db/migrations/20260101000000_test.sql")
	os.MkdirAll(filepath.Dir(migration), 0o755)
	os.WriteFile(migration, []byte("-- migrate:up\nSELECT 1;"), 0o644)
	dest, source, err := a.planIdentity(target, refs)
	if err != nil {
		t.Fatal(err)
	}
	target.Password = "rotated"
	p.Dir = dir + "-other"
	os.MkdirAll(filepath.Join(p.Dir, "db/migrations"), 0o755)
	os.WriteFile(filepath.Join(p.Dir, "db/migrations/20260101000000_test.sql"), []byte("-- migrate:up\nSELECT 1;"), 0o644)
	d2, s2, err := a.planIdentity(target, refs)
	if err != nil {
		t.Fatal(err)
	}
	if dest != d2 || source != s2 {
		t.Fatal("credential rotation or checkout relocation invalidated plan")
	}
	target.Host = "elsewhere"
	d2, _, _ = a.planIdentity(target, refs)
	if dest == d2 {
		t.Fatal("destination not bound")
	}
	os.WriteFile(filepath.Join(p.Dir, "db/migrations/20260101000000_test.sql"), []byte("-- migrate:up\nDROP TABLE test;"), 0o644)
	_, s2, _ = a.planIdentity(target, refs)
	if source == s2 {
		t.Fatal("SQL contents not bound")
	}
}
func TestOwnedRejectsTruncationCollisionAndReservedNames(t *testing.T) {
	for _, names := range [][]string{{"same", "same"}, {strings.Repeat("x", 64)}, {"postgres"}, {"app__branch"}} {
		p := &config.Project{Project: "app"}
		for i, n := range names {
			p.Databases = append(p.Databases, config.Database{Name: string(rune('a' + i)), DBName: n})
		}
		if _, err := (&App{Project: p}).Owned(&env.Target{}, nil); err == nil {
			t.Fatalf("accepted %v", names)
		}
	}
}
func TestBranchSwitchDeniedBeforeDatabaseAccess(t *testing.T) {
	a := &App{Out: &output.Printer{Out: io.Discard, Err: io.Discard}}
	for _, tier := range []string{config.TierStage, config.TierProd} {
		if err := a.BranchUse(context.Background(), &env.Target{Env: tier, Tier: tier}, "exp"); err == nil {
			t.Fatal("branch switch allowed", tier)
		}
	}
}
