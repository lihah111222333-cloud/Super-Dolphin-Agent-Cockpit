//go:build windows

package format

import (
	"path/filepath"
	"testing"
)

func TestAbsolutePathFromURIWindowsDriveUnicode(t *testing.T) {
	want := filepath.Clean(`G:\develop\中转\new-api-main\main.go`)
	got, err := AbsolutePathFromURI("file:///G:/develop/%E4%B8%AD%E8%BD%AC/new-api-main/main.go")
	if err != nil {
		t.Fatalf("AbsolutePathFromURI Windows drive: %v", err)
	}
	if got != want {
		t.Fatalf("Windows drive URI path = %q, want %q", got, want)
	}
}

func TestAbsolutePathFromURIWindowsUNCUnicode(t *testing.T) {
	want := filepath.Clean(`\\server\share\中转 space\100% ready\main.go`)
	got, err := AbsolutePathFromURI("file://server/share/%E4%B8%AD%E8%BD%AC%20space/100%25%20ready/main.go")
	if err != nil {
		t.Fatalf("AbsolutePathFromURI Windows UNC: %v", err)
	}
	if got != want {
		t.Fatalf("Windows UNC URI path = %q, want %q", got, want)
	}
}
