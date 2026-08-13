package format

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

func TestAbsolutePathFromURIDecodesUnicodeSpaceAndLiteralPercent(t *testing.T) {
	root := t.TempDir()
	want, err := platformshared.NormalizeAbsolutePath(filepath.Join(root, "中转 space", "100% ready", "main.go"))
	if err != nil {
		t.Fatalf("normalize expected path: %v", err)
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(want)}).String()

	got, err := AbsolutePathFromURI(uri)
	if err != nil {
		t.Fatalf("AbsolutePathFromURI(%q): %v", uri, err)
	}
	if got != want {
		t.Fatalf("AbsolutePathFromURI(%q) = %q, want %q", uri, got, want)
	}
}

func TestAbsolutePathFromURIDecodesExactlyOnce(t *testing.T) {
	root := t.TempDir()
	want, err := platformshared.NormalizeAbsolutePath(filepath.Join(root, "%2e%2e", "main.go"))
	if err != nil {
		t.Fatalf("normalize expected path: %v", err)
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(want)}).String()

	got, err := AbsolutePathFromURI(uri)
	if err != nil {
		t.Fatalf("AbsolutePathFromURI(%q): %v", uri, err)
	}
	if got != want {
		t.Fatalf("AbsolutePathFromURI(%q) = %q, want exactly-once decoded %q", uri, got, want)
	}
}

func TestAbsolutePathFromURIRejectsMalformedEscape(t *testing.T) {
	_, err := AbsolutePathFromURI("file:///tmp/%zz/main.go")
	if err == nil || !strings.Contains(err.Error(), "parse file URI") {
		t.Fatalf("AbsolutePathFromURI malformed escape error = %v, want parse failure", err)
	}
}
