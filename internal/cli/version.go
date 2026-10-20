package cli

import "runtime/debug"

// readBuildInfo reads the binary's build information; tests replace it.
var readBuildInfo = debug.ReadBuildInfo

// buildVersion is the version this binary reports (ONB-07): the -ldflags value when set,
// else the main module version recorded by the Go toolchain (`go install ...@v1.2.3`),
// else "dev" (a local `go build` without version control reports "(devel)").
func buildVersion() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	if info, ok := readBuildInfo(); ok && info != nil {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}
