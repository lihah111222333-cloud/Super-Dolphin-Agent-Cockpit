//go:build !windows

package installer

import (
	"go/build"
	"os"
	"testing"
)

// TestRuntimeDependencyProvisionIsWindowsScoped 用真实 MatchFile 证明 Windows 运行时目录只由源码约束选择，
// 非 Windows 构建不会依赖运行时分支或 Skip 来隐藏 Windows catalog/provision 实现。
func TestRuntimeDependencyProvisionIsWindowsScoped(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve installer package directory: %v", err)
	}
	files := []string{
		"windows_runtime_dependency_archive_windows.go",
		"windows_runtime_dependency_catalog.go",
		"windows_runtime_dependency_provision.go",
		"windows_runtime_dependency_swift_windows.go",
	}
	for _, goos := range []string{"windows", "linux", "darwin", "freebsd"} {
		context := build.Default
		context.GOOS = goos
		context.GOARCH = "arm64"
		context.CgoEnabled = false
		for _, file := range files {
			matched, matchErr := context.MatchFile(dir, file)
			if matchErr != nil {
				t.Fatalf("MatchFile(%s/%s): %v", goos, file, matchErr)
			}
			want := goos == "windows"
			if matched != want {
				t.Fatalf("MatchFile(%s/%s) = %v, want %v", goos, file, matched, want)
			}
		}
	}
}
