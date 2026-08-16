//go:build windows

package search

import "testing"

func TestPlatformNormalizeSearchGlobPreservesBraceEscapes(t *testing.T) {
	const glob = `src\**\*\{demo\}.jsx`
	const want = `src/**/*\{demo\}.jsx`
	if got := platformNormalizeSearchGlob(glob); got != want {
		t.Fatalf("platformNormalizeSearchGlob(%q) = %q, want %q", glob, got, want)
	}
}
