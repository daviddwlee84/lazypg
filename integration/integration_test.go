// Package integration exercises the lazypg binary against real Docker
// containers. Run with: LAZYPG_IT=1 go test ./integration -v -timeout 15m
package integration

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var bin string

func TestMain(m *testing.M) {
	if os.Getenv("LAZYPG_IT") == "" {
		fmt.Println("skipping integration tests (set LAZYPG_IT=1)")
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "lazypg-it-bin-")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "lazypg")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/lazypg").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type env struct {
	t    *testing.T
	root string
	vars []string
}

func newEnv(t *testing.T) *env {
	root := t.TempDir()
	e := &env{t: t, root: root}
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		e.vars = append(e.vars, v+"="+filepath.Join(root, "xdg", v))
	}
	if p := os.Getenv("LAZYPG_PGSCHEMA_BIN"); p != "" {
		e.vars = append(e.vars, "LAZYPG_PGSCHEMA_BIN="+p)
	}
	return e
}

// run executes lazypg in dir and returns combined output and exit code.
func (e *env) run(dir string, args ...string) (string, int) {
	e.t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), e.vars...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatal(err)
	}
	return string(out), code
}

func (e *env) ok(dir string, args ...string) string {
	e.t.Helper()
	out, code := e.run(dir, args...)
	if code != 0 {
		e.t.Fatalf("lazypg %s: exit %d\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

func (e *env) fails(dir string, wantCode int, wantMsg string, args ...string) {
	e.t.Helper()
	out, code := e.run(dir, args...)
	if code != wantCode || !strings.Contains(out, wantMsg) {
		e.t.Fatalf("lazypg %s: want exit %d with %q, got %d\n%s", strings.Join(args, " "), wantCode, wantMsg, code, out)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalWorkflow(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.root, "journal")
	os.MkdirAll(dir, 0o755)
	e.ok(dir, "init")
	t.Cleanup(func() { e.run(dir, "stop", "--purge", "--yes") })

	write(t, filepath.Join(dir, "db/schema/public/main.sql"), "CREATE TABLE entries (id bigserial PRIMARY KEY, title text NOT NULL);\n")
	write(t, filepath.Join(dir, "db/seeds/seed.sql"), "INSERT INTO entries (title) VALUES ('a'), ('b');\n")
	e.ok(dir, "start")
	if out := e.ok(dir, "plan"); !strings.Contains(out, "No changes") {
		t.Fatalf("expected no changes:\n%s", out)
	}

	// A rename through a pre script must keep data.
	script := strings.TrimSpace(e.ok(dir, "script", "new", "rename_title", "--phase", "pre"))
	write(t, filepath.Join(dir, script), "-- migrate:up\nALTER TABLE entries RENAME COLUMN title TO headline;\n-- migrate:down\n")
	write(t, filepath.Join(dir, "db/schema/public/main.sql"), "CREATE TABLE entries (id bigserial PRIMARY KEY, headline text NOT NULL);\n")
	write(t, filepath.Join(dir, "db/seeds/seed.sql"), "INSERT INTO entries (headline) VALUES ('a'), ('b');\n")
	e.ok(dir, "migrate")
	if out := e.ok(dir, "psql", "--", "-tAc", "select string_agg(headline, '+' order by id) from entries"); !strings.Contains(out, "a+b") {
		t.Fatalf("data lost by rename:\n%s", out)
	}

	// Branches are isolated copies.
	e.ok(dir, "branch", "create", "exp")
	e.ok(dir, "psql", "--", "-c", "delete from entries")
	e.ok(dir, "branch", "use", "main")
	if out := e.ok(dir, "psql", "--", "-tAc", "select count(*) from entries"); !strings.Contains(out, "2") {
		t.Fatalf("main affected by branch:\n%s", out)
	}
	e.ok(dir, "branch", "delete", "exp")

	// Reset rebuilds from desired state; pre scripts are baselined, not rerun.
	if out := e.ok(dir, "reset"); !strings.Contains(out, "marked 1 pre script(s) as applied") {
		t.Fatalf("expected baseline on reset:\n%s", out)
	}
	if out := e.ok(dir, "exec", "--", "sh", "-c", "echo $LAZYPG_TIER $PGDATABASE"); !strings.Contains(out, "local journal") {
		t.Fatalf("exec env:\n%s", out)
	}
}

func TestRemoteGuards(t *testing.T) {
	e := newEnv(t)
	name := fmt.Sprintf("lazypg-it-prod-%d", time.Now().UnixNano())
	port := ""
	if out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-e", "POSTGRES_PASSWORD=pw", "-p", "127.0.0.1::5432", "postgres:18").CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })
	binding, err := exec.Command("docker", "port", name, "5432/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	_, port, err = net.SplitHostPort(strings.TrimSpace(string(binding)))
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(e.root, "svc")
	os.MkdirAll(dir, 0o755)
	e.ok(dir, "init")
	t.Cleanup(func() { e.run(dir, "stop", "--purge", "--yes") })
	write(t, filepath.Join(dir, "db/schema/public/main.sql"), "CREATE TABLE t (id int PRIMARY KEY);\n")
	f, _ := os.OpenFile(filepath.Join(dir, "lazypg.toml"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("\n[envs.prod]\ntier = \"prod\"\ninstance = \"fake\"\n")
	f.Close()
	extra, err := os.OpenFile(filepath.Join(dir, "lazypg.toml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	extra.WriteString("\n[envs.dev]\ntier='dev'\ninstance='devfake'\n[envs.stage]\ntier='stage'\ninstance='stagefake'\n")
	extra.Close()
	cfg := filepath.Join(e.root, "xdg", "XDG_CONFIG_HOME", "lazypg")
	write(t, filepath.Join(cfg, "config.toml"), "[instances.fake]\nhost = \"127.0.0.1\"\nport = "+port+"\nsslmode = \"disable\"\ntier = \"prod\"\n")
	write(t, filepath.Join(cfg, "credentials.toml"), "[fake]\npassword = \"pw\"\n")
	userCfg, err := os.OpenFile(filepath.Join(cfg, "config.toml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, tier := range []string{"dev", "stage"} {
		fmt.Fprintf(userCfg, "\n[instances.%sfake]\nhost='127.0.0.1'\nport=%s\nsslmode='disable'\ntier='%s'\n", tier, port, tier)
	}
	userCfg.Close()
	write(t, filepath.Join(cfg, "credentials.toml"), "[fake]\npassword='pw'\n[devfake]\npassword='pw'\n[stagefake]\npassword='pw'\n")
	e.ok(dir, "start") // the local instance doubles as pgschema's plan host

	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, code := e.run(dir, "status", "--env", "prod", "--json"); code == 0 {
			if out, _ := e.run(dir, "doctor"); strings.Contains(out, "env prod") && !strings.Contains(out, "✗ env prod") {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("fake prod never became reachable")
		}
		time.Sleep(time.Second)
	}

	for _, tier := range []string{"dev", "stage"} {
		e.fails(dir, 3, "needs confirmation", "migrate", "--env", tier)
	}
	e.fails(dir, 3, "reset is not allowed", "reset", "--env", "stage", "--yes")
	e.fails(dir, 3, "branch is not allowed", "branch", "use", "main", "--env", "stage", "--yes")
	e.ok(dir, "env", "use", "dev")
	e.fails(dir, 3, "use is not allowed on prod", "env", "use", "prod")
	e.fails(dir, 3, "requires --confirm prod", "migrate", "--env", "prod")
	e.fails(dir, 3, "requires --confirm prod", "migrate", "--env", "prod", "--yes")
	e.ok(dir, "migrate", "--env", "prod", "--confirm", "prod")
	e.fails(dir, 3, "reset is not allowed on prod", "reset", "--env", "prod", "--confirm", "prod")
	e.fails(dir, 3, "seed is not allowed on prod", "seed", "--env", "prod", "--confirm", "prod")
	e.fails(dir, 3, "branch is not allowed on prod", "branch", "create", "x", "--env", "prod")
	e.fails(dir, 3, "branch is not allowed on prod", "branch", "use", "main", "--env", "prod")
	e.ok(dir, "url", "--env", "prod", "--yes")

	// A saved plan goes stale when the database changes underneath it.
	write(t, filepath.Join(dir, "db/schema/public/main.sql"), "CREATE TABLE t (id int PRIMARY KEY, v text);\n")
	plan := filepath.Join(e.root, "plan.json")
	e.ok(dir, "plan", "--env", "prod", "--out", plan)
	if out, err := exec.Command("docker", "exec", name, "psql", "-U", "postgres", "-d", "svc", "-c", "ALTER TABLE t ADD COLUMN drift int").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	e.fails(dir, 1, "fingerprint mismatch", "migrate", "--env", "prod", "--plan", plan, "--confirm", "prod")
}

func TestDbmateWorkflow(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.root, "app with spaces")
	os.MkdirAll(dir, 0o755)
	e.ok(dir, "init", "--name", "svc")
	t.Cleanup(func() { e.run(dir, "stop", "--purge", "--yes") })
	cfg := filepath.Join(dir, "lazypg.toml")
	data, _ := os.ReadFile(cfg)
	data = []byte(strings.ReplaceAll(string(data), `engine = "pgschema"`, `engine = "dbmate"`))
	os.WriteFile(cfg, data, 0o644)
	migration := filepath.Join(dir, "db/migrations/20260101000000_create.sql")
	write(t, migration, "-- migrate:up\nCREATE TABLE entries(id int PRIMARY KEY);\n-- migrate:down\nDROP TABLE entries;\n")
	e.ok(dir, "start")
	write(t, filepath.Join(dir, "db/migrations/20260101000001_add.sql"), "-- migrate:up\nALTER TABLE entries ADD COLUMN title text;\n-- migrate:down\nALTER TABLE entries DROP COLUMN title;\n")
	plan := filepath.Join(dir, "plan.json")
	e.ok(dir, "plan", "--out", plan)
	write(t, filepath.Join(dir, "db/migrations/20260101000001_add.sql"), "-- migrate:up\nALTER TABLE entries ADD COLUMN other text;\n-- migrate:down\nALTER TABLE entries DROP COLUMN other;\n")
	e.fails(dir, 1, "inputs changed", "migrate", "--plan", plan)
	e.ok(dir, "plan", "--out", plan)
	e.ok(dir, "migrate", "--plan", plan)
	e.ok(dir, "rollback")
	e.ok(dir, "branch", "create", "try")
	e.ok(dir, "branch", "use", "main")
	e.ok(dir, "branch", "delete", "try")
	e.fails(dir, 1, "branch name", "branch", "create", "main")
}

func TestCheckoutAndSharedServerIsolation(t *testing.T) {
	e := newEnv(t)
	first, second := filepath.Join(e.root, "first"), filepath.Join(e.root, "second")
	for _, dir := range []string{first, second} {
		write(t, filepath.Join(dir, "lazypg.toml"), "project='same'\n[[databases]]\nname='main'\nengine='dbmate'\n")
		e.ok(dir, "start")
		d := dir
		t.Cleanup(func() { e.run(d, "stop", "--purge", "--yes") })
	}
	one := e.ok(first, "url")
	two := e.ok(second, "url")
	if one == two {
		t.Fatal("separate checkouts shared a port")
	}
	server := fmt.Sprintf("it%d", time.Now().UnixNano())
	owner, consumer := filepath.Join(e.root, "owner"), filepath.Join(e.root, "consumer")
	write(t, filepath.Join(owner, "lazypg.toml"), "project='owner'\n[local]\nmode='server'\nserver='"+server+"'\n[[databases]]\nname='events'\nengine='dbmate'\n")
	write(t, filepath.Join(owner, "db/migrations/20260101000000_create.sql"), "-- migrate:up\nCREATE TABLE events(id int);\n-- migrate:down\nDROP TABLE events;\n")
	e.ok(owner, "start")
	t.Cleanup(func() { e.run(owner, "server", "stop", server, "--purge", "--yes") })
	e.ok(owner, "psql", "--", "-c", "INSERT INTO events VALUES (1)")
	write(t, filepath.Join(consumer, "lazypg.toml"), "project='consumer'\n[local]\nmode='server'\nserver='"+server+"'\n[[databases]]\nname='main'\nengine='dbmate'\n[[deps]]\nname='events'\npath='../owner'\n")
	e.ok(consumer, "start")
	if out := e.ok(consumer, "psql", "--db", "events", "--", "-tAc", "SELECT count(*) FROM events"); !strings.Contains(out, "1") {
		t.Fatal(out)
	}
	e.ok(consumer, "reset")
	if out := e.ok(owner, "psql", "--", "-tAc", "SELECT count(*) FROM events"); !strings.Contains(out, "1") {
		t.Fatal("consumer reset changed owner", out)
	}
}
