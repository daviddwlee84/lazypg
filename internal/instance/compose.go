// Package instance manages lazypg-owned PostgreSQL containers through
// `docker compose`: one per project checkout, or named machine-level servers
// shared by many projects.
package instance

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/daviddwlee84/lazypg/internal/config"
	"github.com/daviddwlee84/lazypg/internal/proc"
	"github.com/daviddwlee84/lazypg/internal/state"
	"github.com/daviddwlee84/lazypg/internal/xdg"
)

// Credentials of every lazypg-managed container. They only listen on loopback.
const (
	LocalUser     = "postgres"
	LocalPassword = "postgres"
	Service       = "db"
)

//go:embed compose.yaml.tmpl
var composeTmpl string

// Compose is one lazypg-managed docker compose project.
type Compose struct {
	Project string // compose project name
	File    string
	Image   string
	Port    int
	Ejected bool // the compose file lives in the repo and is not regenerated
}

// ForProject returns the compose project for a checkout in project mode.
func ForProject(p *config.Project, st *state.Project) *Compose {
	id := state.ProjectID(p)
	c := &Compose{
		Project: config.AppName + "-" + sanitize(id),
		File:    filepath.Join(xdg.DataDir(), "projects", id, "compose.yaml"),
		Image:   p.Local.Image,
		Port:    st.Port,
	}
	if ej := filepath.Join(p.Dir, config.EjectDir, "compose.yaml"); fileExists(ej) {
		c.File, c.Ejected = ej, true
	}
	return c
}

// ForServer returns the compose project of a named shared server.
func ForServer(s *state.Server) *Compose {
	return &Compose{
		Project: config.AppName + "-server-" + sanitize(s.Name),
		File:    filepath.Join(xdg.DataDir(), "servers", s.Name, "compose.yaml"),
		Image:   s.Image,
		Port:    s.Port,
	}
}

var nonCompose = regexp.MustCompile(`[^a-z0-9_-]+`)

func sanitize(s string) string { return nonCompose.ReplaceAllString(strings.ToLower(s), "-") }

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Render writes the compose file unless it was ejected into the repo.
func (c *Compose) Render() error {
	if c.Ejected {
		return nil
	}
	b, err := RenderTemplate(c.Project, c.Image)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.File), 0o755); err != nil {
		return err
	}
	return os.WriteFile(c.File, b, 0o644)
}

// RenderTemplate renders the compose template; the host port stays a
// ${LAZYPG_PORT} variable so that an ejected file remains portable.
func RenderTemplate(project, image string) ([]byte, error) {
	t, err := template.New("compose").Parse(composeTmpl)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = t.Execute(&buf, map[string]string{
		"Name":     project,
		"Image":    image,
		"DataPath": DataPath(image),
		"User":     LocalUser,
		"Password": LocalPassword,
	})
	return buf.Bytes(), err
}

var majorRe = regexp.MustCompile(`(\d+)`)

// DataPath returns the volume mount point for the image's PostgreSQL major
// version: 18+ images use /var/lib/postgresql, older ones .../data.
func DataPath(image string) string {
	tag := image
	if i := strings.LastIndex(image, ":"); i >= 0 {
		tag = image[i+1:]
	}
	if m := majorRe.FindString(tag); m != "" {
		if v, err := strconv.Atoi(m); err == nil && v < 18 {
			return "/var/lib/postgresql/data"
		}
	}
	return "/var/lib/postgresql"
}

func (c *Compose) base() []string { return []string{"compose", "-p", c.Project, "-f", c.File} }

func (c *Compose) env() []string { return []string{fmt.Sprintf("LAZYPG_PORT=%d", c.Port)} }

func (c *Compose) run(ctx context.Context, out io.Writer, args ...string) error {
	return proc.Run(ctx, proc.Cmd{Name: "docker", Args: append(c.base(), args...), Env: c.env(), Stdout: out})
}

// Up starts the container and waits for its health check.
func (c *Compose) Up(ctx context.Context) error {
	if err := c.Render(); err != nil {
		return err
	}
	return c.run(ctx, nil, "up", "-d", "--wait")
}

// Down removes the container; with volumes it also deletes all data.
func (c *Compose) Down(ctx context.Context, volumes bool) error {
	if !fileExists(c.File) {
		return nil
	}
	args := []string{"down"}
	if volumes {
		args = append(args, "--volumes")
	}
	return c.run(ctx, nil, args...)
}

// State returns the container state ("running", "exited", …) or "" when absent.
func (c *Compose) State(ctx context.Context) (string, error) {
	if !fileExists(c.File) {
		return "", nil
	}
	out, err := proc.Output(ctx, proc.Cmd{Name: "docker", Args: append(c.base(), "ps", "--all", "--format", "json"), Env: c.env()})
	if err != nil {
		return "", err
	}
	// Compose prints either a JSON array or one JSON object per line.
	for _, line := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rows []map[string]any
		if line[0] == '[' {
			if err := json.Unmarshal(line, &rows); err != nil {
				return "", err
			}
		} else {
			var row map[string]any
			if err := json.Unmarshal(line, &row); err != nil {
				return "", err
			}
			rows = append(rows, row)
		}
		for _, r := range rows {
			if r["Service"] == Service {
				s, _ := r["State"].(string)
				return s, nil
			}
		}
	}
	return "", nil
}

// Exec runs a command inside the database container.
func (c *Compose) Exec(ctx context.Context, tty bool, stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	a := append(c.base(), "exec")
	if !tty {
		a = append(a, "-T")
	}
	a = append(a, Service)
	a = append(a, args...)
	return proc.Run(ctx, proc.Cmd{Name: "docker", Args: a, Env: c.env(), Stdin: stdin, Stdout: stdout, Stderr: stderr})
}
