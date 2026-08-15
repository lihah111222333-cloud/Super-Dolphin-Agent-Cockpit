package search

import (
	"runtime"
	"testing"
)

func TestForeignPathFamilyRejectsOppositePlatformAbsolutePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		for _, path := range []string{"/mnt/c/project/main.go", "/mnt/Z/project/main.go"} {
			if !foreignPathFamily(path) {
				t.Fatalf("foreignPathFamily(%q) = false on Windows", path)
			}
		}
		return
	}
	for _, path := range []string{`C:\project\main.go`, "d:/project/main.go", `\\server\share\main.go`} {
		if !foreignPathFamily(path) {
			t.Fatalf("foreignPathFamily(%q) = false on %s", path, runtime.GOOS)
		}
	}
}

func TestForeignPathFamilyAllowsNativeAndRelativePaths(t *testing.T) {
	paths := []string{"main.go", "nested/main.go"}
	if runtime.GOOS == "windows" {
		paths = append(paths, `C:\project\main.go`, `\\server\share\main.go`)
	} else {
		paths = append(paths, "/workspace/main.go", "/mnt/c/project/main.go")
	}
	for _, path := range paths {
		if foreignPathFamily(path) {
			t.Fatalf("foreignPathFamily(%q) = true on %s", path, runtime.GOOS)
		}
	}
}
