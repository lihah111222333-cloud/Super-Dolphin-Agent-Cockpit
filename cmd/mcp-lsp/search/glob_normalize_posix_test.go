//go:build !windows

package search

import "testing"

func TestPlatformNormalizeSearchGlobPreservesPOSIXEscapes(t *testing.T) {
	const glob = `**/*\{demo\}.jsx`
	if got := platformNormalizeSearchGlob(glob); got != glob {
		t.Fatalf("platformNormalizeSearchGlob(%q) = %q, want unchanged", glob, got)
	}
}
