package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestFormatVersionInfoIncludesBuildIdentity(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.time", Value: "2026-09-04T00:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
			{Key: "-tags", Value: "fts5"},
		},
	}
	got := formatVersionInfo(info, true)
	for _, want := range []string{"shoka v1.2.3", "commit: 0123456789ab", "source_time: 2026-09-04T00:00:00Z", "modified: true", "features: fts5"} {
		if !strings.Contains(got, want) {
			t.Errorf("version output missing %q:\n%s", want, got)
		}
	}
}

func TestFormatVersionInfoHandlesUnavailableBuildInfo(t *testing.T) {
	got := formatVersionInfo(nil, false)
	if !strings.Contains(got, "shoka (devel)") || !strings.Contains(got, "commit: unknown") {
		t.Fatalf("fallback version output = %s", got)
	}
}
