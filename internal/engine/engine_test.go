package engine

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestCleanDumpDropsRestrictTokens(t *testing.T) {
	in := "--\n\\restrict abc123\nCREATE TABLE t ();\n\\unrestrict abc123\n"
	if got := string(CleanDump([]byte(in))); got != "--\nCREATE TABLE t ();" {
		t.Fatalf("%q", got)
	}
}

func TestBundleRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	b := &Bundle{Format: BundleFormat, Project: "p", Env: "prod", Plans: []*Plan{{
		Database: "main", Engine: "pgschema", Changes: true,
		Schemas: map[string]json.RawMessage{"public": json.RawMessage(`{"groups":[{}]}`)},
	}}}
	if err := WriteBundle(path, b); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBundle(path)
	if err != nil {
		t.Fatal(err)
	}
	var groups struct{ Groups []any }
	if err := json.Unmarshal(got.Plans[0].Schemas["public"], &groups); err != nil || got.Env != "prod" || len(groups.Groups) != 1 {
		t.Fatalf("%+v %v", got.Plans[0], err)
	}
}

func TestNewFileRejectsBadNames(t *testing.T) {
	if _, err := newFile(t.TempDir(), "Bad Name"); err == nil {
		t.Fatal("expected error")
	}
}
