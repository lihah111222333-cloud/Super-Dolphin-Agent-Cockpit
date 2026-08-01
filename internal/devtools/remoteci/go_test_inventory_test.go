package remoteci

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

// TestGoTestNamesFromFilesValidatesAndExcludesTestMain 验证 TestMain 只校验签名而不进入盘点结果。
func TestGoTestNamesFromFilesValidatesAndExcludesTestMain(t *testing.T) {
	valid := parseGoTestInventoryFixture(t, `package fixture

import "testing"

func TestMain(m *testing.M) {}
func TestFast(t *testing.T) {}
`)
	names, err := goTestNamesFromFiles([]*ast.File{valid}, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"TestFast"}) {
		t.Fatalf("test inventory = %v", names)
	}

	invalid := parseGoTestInventoryFixture(t, `package fixture

import "testing"

func TestMain(t *testing.T) {}
`)
	if _, err := goTestNamesFromFiles([]*ast.File{invalid}, "linux"); err == nil ||
		!strings.Contains(err.Error(), `Go test "TestMain" has an invalid signature`) {
		t.Fatalf("invalid TestMain error = %v", err)
	}
}

// TestGoBenchmarkNamesFromFilesAcceptsOnlyRunnableTopLevelBenchmarks 验证只收集可执行的顶层基准测试。
func TestGoBenchmarkNamesFromFilesAcceptsOnlyRunnableTopLevelBenchmarks(t *testing.T) {
	file := parseGoTestInventoryFixture(t, `package fixture

import "testing"

func BenchmarkFast(b *testing.B) {}
func Benchmarklower(b *testing.B) {}
func BenchmarkMethod[T any](b *testing.B) {}
`)
	names, err := goBenchmarkNamesFromFiles([]*ast.File{file}, "linux")
	if err == nil {
		t.Fatalf("generic benchmark was accepted: %v", names)
	}

	valid := parseGoTestInventoryFixture(t, `package fixture

import "testing"

func BenchmarkFast(b *testing.B) {}
func BenchmarkOther(b *testing.B) {}
func Benchmarklower(b *testing.B) {}
`)
	names, err = goBenchmarkNamesFromFiles([]*ast.File{valid}, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"BenchmarkFast", "BenchmarkOther"}) {
		t.Fatalf("benchmark inventory = %v", names)
	}
}

// TestGoTestNamesFromFilesHonorsSourceApplicabilityDirectives 验证 helper 与平台目标不会进入错误 runner。
func TestGoTestNamesFromFilesHonorsSourceApplicabilityDirectives(t *testing.T) {
	file := parseGoTestInventoryFixture(t, `// super-dolphin-ci: platform=darwin
package fixture

import "testing"

func TestMac(t *testing.T) {}
`)
	if names, err := goTestNamesFromFiles([]*ast.File{file}, "linux"); err != nil || len(names) != 0 {
		t.Fatalf("linux inventory = %v, %v", names, err)
	}
	if names, err := goTestNamesFromFiles([]*ast.File{file}, "darwin"); err != nil || !slices.Equal(names, []string{"TestMac"}) {
		t.Fatalf("darwin inventory = %v, %v", names, err)
	}

	functions := parseGoTestInventoryFixture(t, `package fixture

import "testing"

// super-dolphin-ci: helper
func TestProcessHelper(t *testing.T) {}
func TestRegular(t *testing.T) {}
`)
	names, err := goTestNamesFromFiles([]*ast.File{functions}, "linux")
	if err != nil || !slices.Equal(names, []string{"TestRegular"}) {
		t.Fatalf("helper-filtered inventory = %v, %v", names, err)
	}
}

func TestRemoteGoPackageMatchesPlatformRejectsIgnoredOnlyDirectory(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"ignored/check.go":     []byte("//go:build ignore\n\npackage ignored\n"),
		"linux/main_linux.go":  []byte("package linux\n"),
		"other/main_darwin.go": []byte("package other\n"),
		"tests/only_test.go":   []byte("package tests\n"),
	}}
	for _, test := range []struct {
		directory string
		want      bool
	}{
		{directory: "ignored", want: false},
		{directory: "linux", want: true},
		{directory: "other", want: false},
		{directory: "tests", want: true},
	} {
		got, err := snapshot.remoteGoPackageMatchesPlatform(test.directory, "linux", "amd64", false)
		if err != nil {
			t.Fatalf("match %s: %v", test.directory, err)
		}
		if got != test.want {
			t.Errorf("match %s = %t, want %t", test.directory, got, test.want)
		}
	}
}

// parseGoTestInventoryFixture 将测试源码解析为盘点函数使用的 AST。
func parseGoTestInventoryFixture(t *testing.T, source string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(
		token.NewFileSet(), "fixture_test.go", source, parser.ParseComments|parser.SkipObjectResolution,
	)
	if err != nil {
		t.Fatal(err)
	}
	return file
}
