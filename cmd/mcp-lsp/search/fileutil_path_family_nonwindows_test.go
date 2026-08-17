//go:build !windows

package search

import "testing"

func TestForeignPathFamilyRejectsOppositePlatformAbsolutePaths(t *testing.T) {
	for _, path := range []string{`C:\project\main.go`, "d:/project/main.go", `\\server\share\main.go`} {
		if !foreignPathFamily(path) {
			t.Fatalf("foreignPathFamily(%q) = false on non-Windows", path)
		}
	}
}

func TestForeignPathFamilyAllowsNativeAndRelativePaths(t *testing.T) {
	for _, path := range []string{"main.go", "nested/main.go", "/workspace/main.go", "/mnt/c/project/main.go"} {
		if foreignPathFamily(path) {
			t.Fatalf("foreignPathFamily(%q) = true on non-Windows", path)
		}
	}
}
