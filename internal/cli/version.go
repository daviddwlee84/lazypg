package cli

import "runtime/debug"

func resolveVersion(injected, module string) string {
	if injected != "" && injected != "dev" {
		return injected
	}
	if module != "" && module != "(devel)" {
		return module
	}
	return "dev"
}
func buildVersion() string {
	module := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		module = info.Main.Version
	}
	return resolveVersion(Version, module)
}
