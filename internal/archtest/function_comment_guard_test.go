package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFunctionCommentGuardRequiresChineseDocForExportedFunctions(t *testing.T) {
	t.Parallel()

	path := writeFunctionCommentFixture(t, `package sample

func BuildRunner() {}
`)

	violations := filterViolationsByKind(CheckFiles(CheckOptions{
		RepoRoot:            filepath.Dir(path),
		EnforceFuncComments: true,
	}, []string{filepath.Base(path)}), ViolationFuncComment)

	if len(violations) != 1 {
		t.Fatalf("func comment violations = %d, want 1:\n%s", len(violations), formatViolations(violations))
	}
	if !strings.Contains(violations[0].String(), "BuildRunner") {
		t.Fatalf("violation did not mention BuildRunner: %s", violations[0].String())
	}
}

func TestFunctionCommentGuardAcceptsChineseDoc(t *testing.T) {
	t.Parallel()

	path := writeFunctionCommentFixture(t, `package sample

// BuildRunner 组装运行器需要的依赖。
func BuildRunner() {}
`)

	violations := filterViolationsByKind(CheckFiles(CheckOptions{
		RepoRoot:            filepath.Dir(path),
		EnforceFuncComments: true,
	}, []string{filepath.Base(path)}), ViolationFuncComment)

	if len(violations) != 0 {
		t.Fatalf("func comment violations = %d, want 0:\n%s", len(violations), formatViolations(violations))
	}
}

func TestFunctionCommentGuardRejectsEnglishOnlyDoc(t *testing.T) {
	t.Parallel()

	path := writeFunctionCommentFixture(t, `package sample

// BuildRunner wires runner dependencies.
func BuildRunner() {}
`)

	violations := filterViolationsByKind(CheckFiles(CheckOptions{
		RepoRoot:            filepath.Dir(path),
		EnforceFuncComments: true,
	}, []string{filepath.Base(path)}), ViolationFuncComment)

	if len(violations) != 1 {
		t.Fatalf("func comment violations = %d, want 1:\n%s", len(violations), formatViolations(violations))
	}
}

func TestFunctionCommentGuardIgnoresShortPrivateFunctions(t *testing.T) {
	t.Parallel()

	path := writeFunctionCommentFixture(t, `package sample

func buildRunner() {}
`)

	violations := filterViolationsByKind(CheckFiles(CheckOptions{
		RepoRoot:            filepath.Dir(path),
		EnforceFuncComments: true,
	}, []string{filepath.Base(path)}), ViolationFuncComment)

	if len(violations) != 0 {
		t.Fatalf("func comment violations = %d, want 0:\n%s", len(violations), formatViolations(violations))
	}
}

func TestFunctionCommentGuardFlagsComplexPrivateFunctions(t *testing.T) {
	t.Parallel()

	path := writeFunctionCommentFixture(t, `package sample

func buildRunner(a, b, c bool) {
	if a {
		if b {
			if c {
				println("ready")
			}
		}
	}
}
`)

	violations := filterViolationsByKind(CheckFiles(CheckOptions{
		RepoRoot:            filepath.Dir(path),
		EnforceFuncComments: true,
	}, []string{filepath.Base(path)}), ViolationFuncComment)

	if len(violations) != 1 {
		t.Fatalf("func comment violations = %d, want 1:\n%s", len(violations), formatViolations(violations))
	}
}

func TestFunctionCommentGuardAllowsLocalIgnore(t *testing.T) {
	t.Parallel()

	path := writeFunctionCommentFixture(t, `package sample

// archguard:ignore func_comment -- 测试夹具需要覆盖局部豁免。
func BuildRunner() {}
`)

	violations := filterViolationsByKind(CheckFiles(CheckOptions{
		RepoRoot:            filepath.Dir(path),
		EnforceFuncComments: true,
	}, []string{filepath.Base(path)}), ViolationFuncComment)

	if len(violations) != 0 {
		t.Fatalf("func comment violations = %d, want 0:\n%s", len(violations), formatViolations(violations))
	}
}

func TestFunctionCommentGuardSkipsTestFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample_test.go")
	if err := os.WriteFile(path, []byte(`package sample

func TestRunner() {}
`), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	violations := filterViolationsByKind(CheckFiles(CheckOptions{
		RepoRoot:            dir,
		EnforceFuncComments: true,
	}, []string{filepath.Base(path)}), ViolationFuncComment)

	if len(violations) != 0 {
		t.Fatalf("func comment violations = %d, want 0:\n%s", len(violations), formatViolations(violations))
	}
}

func TestFunctionCommentGuardCanRunDuringCheckAll(t *testing.T) {
	t.Parallel()

	path := writeFunctionCommentFixture(t, `package sample

func BuildRunner() {}
`)

	violations := filterViolationsByKind(CheckAll(CheckOptions{
		RepoRoot:            filepath.Dir(path),
		ScanRoots:           []string{"."},
		EnforceFuncComments: true,
	}), ViolationFuncComment)

	if len(violations) != 1 {
		t.Fatalf("func comment violations = %d, want 1:\n%s", len(violations), formatViolations(violations))
	}
}

func writeFunctionCommentFixture(t *testing.T, src string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
