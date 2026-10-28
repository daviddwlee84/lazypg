package env

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/state"
)

func TestPolicyMatrix(t *testing.T) {
	cases := []struct {
		tier string
		op   Op
		want Decision
	}{
		{config.TierLocal, OpReset, Allow},
		{config.TierDev, OpReset, Confirm},
		{config.TierStage, OpReset, Deny},
		{config.TierStage, OpMigrate, Confirm},
		{config.TierProd, OpMigrate, ConfirmTyped},
		{config.TierProd, OpSeed, Deny},
		{config.TierProd, OpRollback, Deny},
		{config.TierProd, OpBranch, Deny},
		{config.TierProd, OpUse, Deny},
		{config.TierProd, OpConnect, Confirm},
		{config.TierProd, OpRead, Allow},
		{"unknown", OpRead, Deny},
	}
	for _, c := range cases {
		if got := Policy(c.tier, c.op); got != c.want {
			t.Errorf("Policy(%s, %s) = %d, want %d", c.tier, c.op, got, c.want)
		}
	}
}

func TestCheckNonInteractive(t *testing.T) {
	no := false
	prod := &Target{Env: "prod", Tier: config.TierProd, Instance: "acme/prod"}
	dev := &Target{Env: "dev", Tier: config.TierDev, Instance: "acme/dev"}
	p := func(yes bool, confirm string) Prompt {
		return Prompt{Yes: yes, Confirm: confirm, Interactive: &no, In: strings.NewReader(""), Out: &strings.Builder{}}
	}
	if err := Check(prod, OpMigrate, "x", p(true, "")); !errors.Is(err, ErrDenied) {
		t.Fatalf("--yes must not satisfy prod: %v", err)
	}
	if err := Check(prod, OpMigrate, "x", p(false, "stage")); !errors.Is(err, ErrDenied) {
		t.Fatalf("--confirm for another env must not satisfy prod: %v", err)
	}
	if err := Check(prod, OpMigrate, "x", p(false, "prod")); err != nil {
		t.Fatalf("--confirm prod should pass: %v", err)
	}
	if err := Check(prod, OpReset, "x", p(true, "prod")); !errors.Is(err, ErrDenied) {
		t.Fatalf("reset on prod must be denied: %v", err)
	}
	if err := Check(dev, OpMigrate, "x", p(false, "")); !errors.Is(err, ErrDenied) {
		t.Fatalf("dev migrate needs --yes when non-interactive: %v", err)
	}
	if err := Check(dev, OpMigrate, "x", p(true, "")); err != nil {
		t.Fatal(err)
	}
}

func TestCheckInteractive(t *testing.T) {
	yes := true
	prod := &Target{Env: "prod", Tier: config.TierProd, Instance: "acme/prod"}
	for answer, ok := range map[string]bool{"prod\n": true, "yes\n": false, "y\n": false, "\n": false} {
		err := Check(prod, OpMigrate, "x", Prompt{Interactive: &yes, In: strings.NewReader(answer), Out: &strings.Builder{}})
		if (err == nil) != ok {
			t.Errorf("answer %q: err=%v", answer, err)
		}
	}
	dev := &Target{Env: "dev", Tier: config.TierDev}
	if err := Check(dev, OpReset, "x", Prompt{Interactive: &yes, In: strings.NewReader("y\n"), Out: &strings.Builder{}}); err != nil {
		t.Fatal(err)
	}
}

func TestDBNames(t *testing.T) {
	p := &config.Project{Project: "journal"}
	db := &config.Database{Name: "main"}
	dedicated := &Target{}
	shared := &Target{Shared: true, Branch: "try"}
	override := &Target{Shared: true, DBNames: map[string]string{"main": "legacy_db"}}
	if got := dedicated.DBName(p, db); got != "main" {
		t.Error(got)
	}
	if got := shared.DBName(p, db); got != "journal_main__try" {
		t.Error(got)
	}
	if got := override.DBName(p, db); got != "legacy_db" {
		t.Error(got)
	}
	if got := shared.DepDBName("events", &config.Database{Name: "ev"}); got != "events_ev" {
		t.Errorf("deps must ignore branches: %s", got)
	}
}

func TestURLEscapesPassword(t *testing.T) {
	tg := &Target{Host: "db.example.com", Port: 5432, User: "app", Password: "p@ss/w:rd?", SSLMode: "require"}
	u := tg.URL("main")
	if !strings.Contains(u, "p%40ss%2Fw%3Ard%3F@db.example.com:5432/main?sslmode=require") {
		t.Fatal(u)
	}
	if strings.Contains(tg.RedactedURL("main"), "p%40ss") {
		t.Fatal(tg.RedactedURL("main"))
	}
}

func setupXDG(t *testing.T) string {
	dir := t.TempDir()
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(v, filepath.Join(dir, v))
	}
	return dir
}

func TestResolveOrgInstanceAndCredentials(t *testing.T) {
	dir := setupXDG(t)
	orgDir := filepath.Join(dir, "infra")
	os.MkdirAll(orgDir, 0o755)
	os.WriteFile(filepath.Join(orgDir, config.OrgFile), []byte(`
org = "acme"
[instances.prod]
host = "pg.acme.internal"
tier = "prod"
[instances.dev]
host = "pg-dev.acme.internal"
tier = "dev"
shared = true
`), 0o644)
	if err := config.SaveTOML(UserConfigPath(), &config.User{Orgs: map[string]config.OrgRef{"acme": {Path: orgDir}}}, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACME_PW", "from-env")
	creds := config.Credentials{
		"acme/prod": {User: "migrator", PasswordCmd: "printf from-cmd"},
		"acme/dev":  {PasswordEnv: "ACME_PW"},
	}
	if err := config.SaveTOML(CredentialsPath(), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	p := &config.Project{Project: "journal", Envs: map[string]config.EnvSpec{
		"prod":  {Tier: config.TierProd, Instance: "acme/prod"},
		"dev":   {Tier: config.TierDev, Instance: "acme/dev"},
		"wrong": {Tier: config.TierDev, Instance: "acme/prod"},
	}}
	st := &state.Project{Branches: map[string]string{"dev": "exp"}}
	r, err := LoadResolver(p, st)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prod, err := r.Resolve(ctx, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if prod.Host != "pg.acme.internal" || prod.Port != 5432 || prod.User != "migrator" || prod.Password != "from-cmd" || prod.Branch != "" {
		t.Fatalf("prod: %+v", prod)
	}
	dev, err := r.Resolve(ctx, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if dev.Password != "from-env" || !dev.Shared || dev.Branch != "exp" {
		t.Fatalf("dev: %+v", dev)
	}
	if _, err := r.Resolve(ctx, "wrong"); err == nil || !strings.Contains(err.Error(), "refusing to guess") {
		t.Fatalf("tier mismatch must fail: %v", err)
	}
	t.Setenv("LAZYPG_URL_PROD", "postgres://ci:secret@10.0.0.5:6543/ignored?sslmode=verify-full")
	ci, err := r.Resolve(ctx, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if ci.Host != "10.0.0.5" || ci.Port != 6543 || ci.User != "ci" || ci.Password != "secret" || ci.SSLMode != "verify-full" || ci.Tier != config.TierProd {
		t.Fatalf("url override: %+v", ci)
	}
}

func TestMalformedURLDoesNotExposeCredential(t *testing.T) {
	target := &Target{}
	for _, raw := range []string{"postgres://user:private%zz@localhost/db", "postgres://localhost/db"} {
		err := target.applyURL(raw)
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe URL diagnostic: %v", err)
		}
	}
}
func TestPGEnvClearsInheritedSecrets(t *testing.T) {
	target := &Target{Host: "localhost", Port: 5432, User: "app"}
	vars := strings.Join(target.PGEnv("app"), "\n")
	if !strings.Contains(vars, "PGPASSWORD=\n") {
		t.Fatal("empty credential override is missing")
	}
}

func TestStickyEnvironmentCannotBecomeProduction(t *testing.T) {
	project := &config.Project{Envs: map[string]config.EnvSpec{"selected": {Tier: config.TierDev}}}
	resolver := &Resolver{Project: project, State: &state.Project{Env: "selected"}}
	if got := resolver.Current(); got != "selected" {
		t.Fatal(got)
	}
	project.Envs["selected"] = config.EnvSpec{Tier: config.TierProd}
	if got := resolver.Current(); got != config.EnvLocal {
		t.Fatalf("stale production selection must fall back to local, got %s", got)
	}
	project.Envs["selected"] = config.EnvSpec{Tier: config.TierStage}
	if got := resolver.Current(); got != "selected" {
		t.Fatal(got)
	}
	delete(project.Envs, "selected")
	if got := resolver.Current(); got != config.EnvLocal {
		t.Fatal(got)
	}
}
