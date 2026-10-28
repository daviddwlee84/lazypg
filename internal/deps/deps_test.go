package deps

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/daviddwlee84/lazypg/internal/config"
)

func writeProject(t *testing.T, dir, body string) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, config.ProjectFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePathAndOverride(t *testing.T) {
	root := t.TempDir()
	writeProject(t, filepath.Join(root, "events"), "project = \"events\"\n[[databases]]\nname = \"ev\"\n[[databases]]\nname = \"other\"\n")
	writeProject(t, filepath.Join(root, "events-dev"), "project = \"events\"\n[[databases]]\nname = \"ev\"\n")
	writeProject(t, filepath.Join(root, "app"), "project = \"app\"\n[[deps]]\nname = \"events\"\npath = \"../events\"\ndatabases = [\"ev\"]\n")
	p, err := config.LoadProject(filepath.Join(root, "app"))
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := Resolve(context.Background(), p, &config.User{}, &Lock{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || len(res[0].Databases) != 1 || res[0].Databases[0].Name != "ev" || res[0].OwnerName() != "events" {
		t.Fatalf("%+v", res[0])
	}
	u := &config.User{Overrides: map[string]config.Override{"app": {DepPaths: map[string]string{"events": filepath.Join(root, "events-dev")}}}}
	res, _, err = Resolve(context.Background(), p, u, &Lock{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Owner.Dir != filepath.Join(root, "events-dev") {
		t.Fatalf("override ignored: %s", res[0].Owner.Dir)
	}
}

func TestResolveUnknownDatabase(t *testing.T) {
	root := t.TempDir()
	writeProject(t, filepath.Join(root, "events"), "project = \"events\"\n[[databases]]\nname = \"ev\"\n")
	writeProject(t, filepath.Join(root, "app"), "project = \"app\"\n[[deps]]\nname = \"events\"\npath = \"../events\"\ndatabases = [\"nope\"]\n")
	p, _ := config.LoadProject(filepath.Join(root, "app"))
	if _, _, err := Resolve(context.Background(), p, &config.User{}, &Lock{}, false); err == nil {
		t.Fatal("expected error")
	}
}
