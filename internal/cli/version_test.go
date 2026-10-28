package cli

import "testing"

func TestResolveVersion(t *testing.T) {
	for _, tc := range []struct{ injected, module, want string }{{"v1.0.0", "v0.9.0", "v1.0.0"}, {"dev", "v0.1.0", "v0.1.0"}, {"", "v0.0.0-20261028000000-abcdefabcdef", "v0.0.0-20261028000000-abcdefabcdef"}, {"dev", "(devel)", "dev"}, {"", "", "dev"}} {
		if got := resolveVersion(tc.injected, tc.module); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
}
