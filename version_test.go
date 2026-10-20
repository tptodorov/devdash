package main

import (
	"runtime/debug"
	"testing"
)

func TestVersionString(t *testing.T) {
	built := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/tptodorov/devdash", Version: v}}
	}
	tests := []struct {
		name    string
		stamped string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{"a release build stamps its tag", "v0.2.0", built("(devel)"), true, "v0.2.0"},
		{"go install @version records it", "", built("v0.1.0"), true, "v0.1.0"},
		{"a local build has no version", "", built("(devel)"), true, "dev"},
		{"no build info at all", "", nil, false, "dev"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionString(tc.stamped, tc.info, tc.ok); got != tc.want {
				t.Errorf("versionString() = %q, want %q", got, tc.want)
			}
		})
	}
}
