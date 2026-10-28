// Package proc runs external programs (docker, git, pgschema, psql) with
// consistent error reporting.
package proc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Cmd describes one external invocation.
type Cmd struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string // appended to os.Environ()
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Run executes c. When Stdout/Stderr are nil, output is captured and stderr is
// included in the returned error.
func Run(ctx context.Context, c Cmd) error {
	_, err := run(ctx, c)
	return err
}

// Output executes c and returns its stdout.
func Output(ctx context.Context, c Cmd) ([]byte, error) {
	var out bytes.Buffer
	c.Stdout = &out
	_, err := run(ctx, c)
	return out.Bytes(), err
}

func run(ctx context.Context, c Cmd) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin = c.Stdin
	var errBuf bytes.Buffer
	cmd.Stdout = c.Stdout
	if c.Stderr != nil {
		cmd.Stderr = c.Stderr
	} else {
		cmd.Stderr = &errBuf
	}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg != "" {
			return cmd, fmt.Errorf("%s %s: %w\n%s", c.Name, firstArg(c.Args), err, msg)
		}
		return cmd, fmt.Errorf("%s %s: %w", c.Name, firstArg(c.Args), err)
	}
	return cmd, nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// Has reports whether a program is on PATH.
func Has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
