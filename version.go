package main

import "runtime/debug"

// version is stamped into release builds with
// -ldflags "-X main.version=v1.2.3"; other builds leave it empty.
var version string

// versionString reports which devdash this is: the stamped release version,
// else the module version Go records for "go install ...@v1.2.3", else dev
// for a local build.
func versionString(stamped string, info *debug.BuildInfo, ok bool) string {
	if stamped != "" {
		return stamped
	}
	if ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
