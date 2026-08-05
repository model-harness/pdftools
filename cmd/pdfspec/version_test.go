package main

import (
	"strings"
	"testing"
)

// The version string is user-visible twice — the version verb and every OKF bundle's
// generated.by — and it went four phases reporting 0.0.0-dev because nothing checked it.
// These are the two properties a test can hold without a linker: the -X hook is honoured,
// and the reported version is never the empty string.
//
// What a unit test cannot cover is which value debug.ReadBuildInfo returns per build mode,
// since that is decided by the toolchain and differs between go run, go build, and
// go install. Those four are measured by hand and recorded in buildVersion's comment.
func TestBuildVersionPrefersTheLinkerOverride(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })

	// The override is taken verbatim, v-prefix and all. A release script passing -X owns
	// the string it passes; stripping anything from it would be this code second-guessing
	// a caller that was explicit.
	version = "9.9.9"
	if got := buildVersion(); got != "9.9.9" {
		t.Errorf("buildVersion() = %q with the override set, want 9.9.9", got)
	}

	version = ""
	got := buildVersion()
	if got == "" {
		t.Fatal("buildVersion() is empty with no override: generated.by would read pdfspec/")
	}
	if strings.HasPrefix(got, "v") {
		t.Errorf("buildVersion() = %q, want no v prefix: generated.by is pdfspec/0.1.0, not pdfspec/v0.1.0", got)
	}
}
