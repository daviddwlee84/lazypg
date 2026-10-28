package tools

import "testing"

func TestChecksumFor(t *testing.T) {
	sums := []byte("aaa  pgschema-1.0.0-darwin-arm64\nbbb  pgschema-1.0.0-linux-amd64\n")
	if got, err := checksumFor(sums, "pgschema-1.0.0-linux-amd64"); err != nil || got != "bbb" {
		t.Fatal(got, err)
	}
	if _, err := checksumFor(sums, "pgschema-1.0.0-windows-amd64"); err == nil {
		t.Fatal("expected error")
	}
}
