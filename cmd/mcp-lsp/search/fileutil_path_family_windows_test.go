//go:build windows

package search

import "testing"

func TestForeignPathFamilyRejectsOppositePlatformAbsolutePaths(t *testing.T) {
	for _, path := range []string{"/mnt/c/project/main.go", "/mnt/Z/project/main.go"} {
		if !foreignPathFamily(path) {
			t.Fatalf("foreignPathFamily(%q) = false on Windows", path)
		}
	}
}

func TestForeignPathFamilyAllowsNativeAndRelativePaths(t *testing.T) {
	for _, path := range []string{"main.go", "nested/main.go", `C:\project\main.go`, `\\server\share\main.go`} {
		if foreignPathFamily(path) {
			t.Fatalf("foreignPathFamily(%q) = true on Windows", path)
		}
	}
}
