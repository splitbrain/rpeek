// Package version exposes the rpeek build version to the rest of the program. Release
// builds stamp the value into the binary with the linker's -X flag; other builds report
// "dev" or, when installed from a tagged module, the module version.
package version

import "runtime/debug"

// Version is the rpeek build version. Release builds set it at link time with
// -ldflags "-X rpeek/internal/version.Version=<tag>"; unstamped builds leave it "dev".
var Version = "dev"

// init backfills Version from the embedded module build info when it was not stamped at
// link time, so a binary produced by "go install rpeek/cmd/rpeek@vX.Y.Z" still reports
// its version instead of "dev".
func init() {
	if Version != "dev" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		Version = v
	}
}
