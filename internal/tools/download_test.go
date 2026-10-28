package tools

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDownloadFailureNeverInstallsBinary(t *testing.T) {
	for _, scenario := range []string{"checksum", "http"} {
		t.Run(scenario, func(t *testing.T) {
			old := http.DefaultTransport
			defer func() { http.DefaultTransport = old }()
			http.DefaultTransport = testTransport(func(r *http.Request) (*http.Response, error) {
				body := "bad binary"
				status := 200
				if strings.HasSuffix(r.URL.Path, "checksums.txt") {
					body = fmt.Sprintf("%x  pgschema-1.0.0-%s-%s\n", sha256.Sum256([]byte("expected binary")), runtime.GOOS, runtime.GOARCH)
				}
				if scenario == "http" {
					status = 503
				}
				return &http.Response{StatusCode: status, Status: "test response", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			dir := t.TempDir()
			bin := filepath.Join(dir, "pgschema")
			if err := downloadPgschema(context.Background(), "1.0.0", dir, bin); err == nil {
				t.Fatal("expected download failure")
			}
			if _, err := os.Stat(bin); !os.IsNotExist(err) {
				t.Fatal("failed download installed binary")
			}
		})
	}
}
