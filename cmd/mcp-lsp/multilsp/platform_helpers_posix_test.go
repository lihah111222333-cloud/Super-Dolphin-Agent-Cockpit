//go:build !windows

package multilsp

import "testing"

func TestPlatformFileURIPathPreservesPOSIXPath(t *testing.T) {
	const path = "/tmp/project/main.ts"
	if got := platformFileURIPath(path); got != path {
		t.Fatalf("platformFileURIPath(%q) = %q, want %q", path, got, path)
	}
}

func TestPlatformTypeScriptNavigationModuleSearchPathsPreservesPOSIXLookup(t *testing.T) {
	paths, err := platformTypeScriptNavigationModuleSearchPaths()
	if err != nil {
		t.Fatalf("platformTypeScriptNavigationModuleSearchPaths() error = %v", err)
	}
	if paths != nil {
		t.Fatalf("platformTypeScriptNavigationModuleSearchPaths() = %v, want nil", paths)
	}
}
