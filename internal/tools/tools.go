// Package tools locates pinned external tools, downloading verified release
// binaries into the XDG data directory when they are not already installed.
package tools

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/daviddwlee84/lazypg/internal/xdg"
)

// PgschemaEnv overrides the pgschema binary path (offline or custom builds).
const PgschemaEnv = "LAZYPG_PGSCHEMA_BIN"

// Logf receives progress messages (download notices).
type Logf func(format string, a ...any)

// Pgschema returns the path of a pgschema binary at exactly version, in order:
// $LAZYPG_PGSCHEMA_BIN, a matching `pgschema` on PATH, the XDG tool cache,
// then a checksum-verified download from the GitHub release.
func Pgschema(ctx context.Context, version string, logf Logf) (string, error) {
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
		return "", fmt.Errorf("invalid pgschema version")
	}
	if p := os.Getenv(PgschemaEnv); p != "" {
		return p, nil
	}
	if p, err := exec.LookPath("pgschema"); err == nil {
		if out, err := exec.CommandContext(ctx, p, "--version").Output(); err == nil && strings.TrimSpace(string(out)) == version {
			return p, nil
		}
	}
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("pgschema does not support Windows; use WSL")
	}
	dir := filepath.Join(xdg.DataDir(), "tools", "pgschema", version)
	bin := filepath.Join(dir, "pgschema")
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	if logf != nil {
		logf("downloading pgschema %s …", version)
	}
	if err := downloadPgschema(ctx, version, dir, bin); err != nil {
		return "", fmt.Errorf("install pgschema %s: %w\n(install it manually and set %s, or put a matching `pgschema` on PATH)", version, err, PgschemaEnv)
	}
	return bin, nil
}

func downloadPgschema(ctx context.Context, version, dir, bin string) error {
	asset := fmt.Sprintf("pgschema-%s-%s-%s", version, runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("https://github.com/pgplex/pgschema/releases/download/v%s/", version)
	sums, err := fetch(ctx, base+fmt.Sprintf("pgschema-%s-checksums.txt", version))
	if err != nil {
		return err
	}
	want, err := checksumFor(sums, asset)
	if err != nil {
		return err
	}
	data, err := fetch(ctx, base+asset)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s want %s", asset, got, want)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Chmod(0o755); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, bin)
}

func checksumFor(sums []byte, asset string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[1] == asset {
			return f[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s (unsupported platform?)", asset)
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
