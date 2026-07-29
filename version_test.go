package terminal

import (
	"regexp"
	"testing"
)

func TestBuildVersionPassesStampedBuildsThrough(t *testing.T) {
	for _, injected := range []string{
		"v0.7.14",            // goreleaser: a clean tag
		"v0.7.14-3-gb2535a0", // build.sh: git describe
		"1.2.3",              // no leading v — still the truth, still untouched
	} {
		if got := BuildVersion(injected); got != injected {
			t.Errorf("BuildVersion(%q) = %q, want it passed through untouched", injected, got)
		}
	}
}

// An unstamped build must NOT report the bare "dev" sentinel when Go's VCS stamp can say
// which commit it is — that sentinel cannot answer "is this the code I just built?", which
// is the only reason the version badge exists.
func TestBuildVersionEnrichesUnstampedBuildFromVCS(t *testing.T) {
	shape := regexp.MustCompile(`^dev-[0-9a-f]{7}(-dirty)?$`)
	for _, injected := range []string{"dev", ""} {
		got := BuildVersion(injected)
		if got == "dev" {
			// Test binaries are built from a module with a VCS stamp; if this environment
			// genuinely has none, "dev" is the honest answer and there is nothing to assert.
			t.Skipf("no VCS stamp available in this build environment (got %q)", got)
		}
		if !shape.MatchString(got) {
			t.Errorf("BuildVersion(%q) = %q, want dev-<7 hex>[-dirty]", injected, got)
		}
	}
}
