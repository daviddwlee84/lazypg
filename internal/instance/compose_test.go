package instance

import (
	"strings"
	"testing"
)

func TestDataPath(t *testing.T) {
	cases := map[string]string{
		"postgres:18":                "/var/lib/postgresql",
		"postgres:17-alpine":         "/var/lib/postgresql/data",
		"pgvector/pgvector:pg16":     "/var/lib/postgresql/data",
		"pgvector/pgvector:pg18":     "/var/lib/postgresql",
		"registry:5000/postgres":     "/var/lib/postgresql",
		"postgres:latest":            "/var/lib/postgresql",
		"localhost:5000/postgres:15": "/var/lib/postgresql/data",
	}
	for image, want := range cases {
		if got := DataPath(image); got != want {
			t.Errorf("DataPath(%q) = %q, want %q", image, got, want)
		}
	}
}

func TestRenderTemplateKeepsPortVariable(t *testing.T) {
	b, err := RenderTemplate("lazypg-x", "postgres:17")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"name: lazypg-x", "image: postgres:17", "127.0.0.1:${LAZYPG_PORT", "pgdata:/var/lib/postgresql/data"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}
