package config

import (
	"slices"
	"strings"
	"testing"
)

func TestParseProjectDefaults(t *testing.T) {
	p, err := ParseProject([]byte(`
project = "app"
[[databases]]
name = "main"
`))
	if err != nil {
		t.Fatal(err)
	}
	d := p.Databases[0]
	if d.Engine != EnginePgschema || !slices.Equal(d.Schemas, []string{"public"}) || d.SchemaDir != "db/schema" {
		t.Fatalf("unexpected defaults: %+v", d)
	}
	if p.Local.Mode != ModeProject || p.Local.Image != DefaultImage || p.Tools.Pgschema != DefaultPgschemaVersion {
		t.Fatalf("unexpected local defaults: %+v %+v", p.Local, p.Tools)
	}
}

func TestParseProjectErrors(t *testing.T) {
	cases := map[string]struct{ toml, want string }{
		"unknown field": {`project = "a"
typo = 1
[[databases]]
name = "x"`, "unknown field"},
		"bad project name": {`project = "My-App"
[[databases]]
name = "x"`, "project"},
		"local env redefined": {`project = "a"
[[databases]]
name = "x"
[envs.local]
tier = "dev"
instance = "i"`, "implicit"},
		"bad tier": {`project = "a"
[[databases]]
name = "x"
[envs.qa]
tier = "qa"
instance = "i"`, "tier"},
		"duplicate db": {`project = "a"
[[databases]]
name = "x"
[[databases]]
name = "x"`, "duplicate"},
		"bad engine": {`project = "a"
[[databases]]
name = "x"
engine = "flyway"`, "engine"},
		"reserved schema": {`project = "a"
[[databases]]
name = "x"
schemas = ["lazypg"]`, "reserved"},
		"dep with two sources": {`project = "a"
[[deps]]
name = "d"
git = "g"
path = "p"`, "exactly one"},
		"vendor dep without db": {`project = "a"
[[deps]]
name = "d"
vendor = "x.sql"`, "exactly one database"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseProject([]byte(c.toml))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestEnvNamesOrderedByTier(t *testing.T) {
	p := &Project{Envs: map[string]EnvSpec{
		"prod": {Tier: TierProd}, "qa": {Tier: TierStage}, "dev": {Tier: TierDev}, "dev2": {Tier: TierDev},
	}}
	got := p.EnvNames()
	want := []string{"local", "dev", "dev2", "qa", "prod"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSplitAlias(t *testing.T) {
	if o, n := SplitAlias("acme/prod"); o != "acme" || n != "prod" {
		t.Fatal(o, n)
	}
	if o, n := SplitAlias("mine"); o != "" || n != "mine" {
		t.Fatal(o, n)
	}
}
