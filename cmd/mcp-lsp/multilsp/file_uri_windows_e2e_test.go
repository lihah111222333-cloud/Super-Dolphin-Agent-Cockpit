//go:build windows && e2e

package multilsp

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// TestWindowsFileURIFromPathRoundTripsDriveLetter verifies the complete
// native drive path -> RFC 8089 file URI -> native drive path boundary used
// by all LSP clients.
func TestWindowsFileURIFromPathRoundTripsDriveLetter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace with space", "main.ts")
	uri := fileURIFromPath(path)
	volume := filepath.VolumeName(path)
	wantPrefix := "file:///" + strings.ToLower(volume[:1]) + "%3A/"
	if !strings.HasPrefix(uri, wantPrefix) {
		t.Fatalf("file URI %q prefix mismatch, want VS Code-compatible %q", uri, wantPrefix)
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("parse file URI %q: %v", uri, err)
	}
	if parsed.Host != "" {
		t.Fatalf("file URI %q host = %q, want empty host", uri, parsed.Host)
	}
	if !strings.HasPrefix(parsed.EscapedPath(), "/") {
		t.Fatalf("file URI %q escaped path = %q, want absolute URI path", uri, parsed.EscapedPath())
	}
	roundTrip, err := absolutePathFromURI(uri)
	if err != nil {
		t.Fatalf("absolutePathFromURI(%q): %v", uri, err)
	}
	if !strings.EqualFold(filepath.Clean(roundTrip), filepath.Clean(path)) {
		t.Fatalf("file URI %q round trip = %q, want %q", uri, roundTrip, path)
	}
}
