// Package pgexec runs PostgreSQL client programs (psql, pg_dump) without
// requiring them on the host: inside the managed container for local targets,
// on the host when installed, or in a throwaway container otherwise. Running
// in a container also matches the client version to the image.
package pgexec

import (
	"context"
	"io"
	"os"

	"github.com/daviddwlee84/lazypg/internal/env"
	"github.com/daviddwlee84/lazypg/internal/instance"
	"github.com/daviddwlee84/lazypg/internal/proc"
)

// Run executes a client program (e.g. "psql", "pg_dump") against dbname on t.
// image is used for the throwaway-container fallback. tty requests an
// interactive terminal.
func Run(ctx context.Context, t *env.Target, image, dbname string, tty bool, stdin io.Reader, stdout, stderr io.Writer, program string, args ...string) error {
	if t.Managed() {
		a := append([]string{program, "-U", instance.LocalUser, "-d", dbname}, args...)
		return t.Compose.Exec(ctx, tty, stdin, stdout, stderr, a...)
	}
	if proc.Has(program) {
		return proc.Run(ctx, proc.Cmd{Name: program, Args: args, Env: t.PGEnv(dbname), Stdin: stdin, Stdout: stdout, Stderr: stderr})
	}
	host := t.Host
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		host = "host.docker.internal"
	}
	c := *t
	c.Host = host
	a := []string{"run", "--rm", "-i", "--add-host=host.docker.internal:host-gateway"}
	if tty {
		a = append(a, "-t")
	}
	for _, e := range c.PGEnv(dbname) {
		a = append(a, "-e", e)
	}
	a = append(a, image, program)
	a = append(a, args...)
	return proc.Run(ctx, proc.Cmd{Name: "docker", Args: a, Stdin: stdin, Stdout: stdout, Stderr: stderr})
}

// Interactive runs psql attached to the current terminal.
func Interactive(ctx context.Context, t *env.Target, image, dbname string, args ...string) error {
	return Run(ctx, t, image, dbname, true, os.Stdin, os.Stdout, os.Stderr, "psql", args...)
}
