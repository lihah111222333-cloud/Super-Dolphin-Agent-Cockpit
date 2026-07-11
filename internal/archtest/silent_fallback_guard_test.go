package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSilentFallbackGuardFlagsHardcodedReturnNilOnError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSilentFallbackFixture(t, root, "internal/example/config.go", `package example

func timeout(config interface{ Get(string) (int, error) }) (int, error) {
	val, err := config.Get("timeout")
	if err != nil {
		return 30, nil
	}
	return val, nil
}
`)

	violations := CheckAll(CheckOptions{RepoRoot: root, ScanRoots: []string{"internal"}})
	if !hasViolationContaining(violations, "silent fallback") {
		t.Fatalf("CheckAll() did not flag implicit fallback return; violations:\n%s", strings.Join(violationMessages(violations), "\n"))
	}
}

func TestSilentFallbackGuardFlagsNilErrorReturnsInErrorBranches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSilentFallbackFixture(t, root, "internal/example/config.go", nilErrorReturnFixture())

	violations := CheckAll(CheckOptions{RepoRoot: root, ScanRoots: []string{"internal"}})
	got := countViolationsContaining(violations, "silent fallback")
	if got != 10 {
		t.Fatalf("CheckAll() flagged %d nil-error returns, want 10; violations:\n%s", got, strings.Join(violationMessages(violations), "\n"))
	}
}

func nilErrorReturnFixture() string {
	return `package example

import (
	"errors"
	"io/fs"
	"path/filepath"
	)

type Outcome struct {
	Status string
	ErrorSummary string
}
` + basicNilErrorReturnFixture() + callbackNilErrorReturnFixture() + namedNilErrorReturnFixture()
}

func basicNilErrorReturnFixture() string {
	return `
func missingFileIsEmpty(err error) (string, error) {
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return "ok", nil
}

func defaultString(err error) (string, error) {
	if err != nil {
		return "", nil
	}
	return "ok", nil
}

func structuredFailure(err error) (Outcome, error) {
	if err != nil {
		return Outcome{Status: "failed", ErrorSummary: err.Error()}, nil
	}
	return Outcome{Status: "ok"}, nil
}

func retrySentinel(err error) (bool, error) {
	if err != nil {
		return false, nil
	}
	return true, nil
}

func closeOnly(err error) error {
	if err != nil {
		return nil
	}
	return nil
}
`
}

func callbackNilErrorReturnFixture() string {
	return `
func inCallback(run func(func() error) error) error {
	return run(func() error {
		val, err := read()
		if err != nil {
			return nil
		}
		_ = val
		return nil
	})
}

func registerCallback(run func(func() error)) {
	run(func() error {
		val, err := read()
		if err != nil {
			return nil
		}
		_ = val
		return nil
	})
}
`
}

func namedNilErrorReturnFixture() string {
	return `
func walkCallback(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		return nil
	})
}

func shortErrorName(e error) (string, error) {
	if e != nil {
		return "", nil
	}
	return "ok", nil
}

func numberedErrorName(err2 error) (string, error) {
	if err2 != nil {
		return "", nil
	}
	return "ok", nil
}

func suffixErrorName(parseError error) (string, error) {
	if parseError != nil {
		return "", nil
	}
	return "ok", nil
}

func read() (string, error) { return "", nil }
			`
}

func TestSilentFallbackGuardFailsClosedOnScanErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSilentFallbackFixture(t, root, "internal/example/broken.go", `package example

func broken(err error) (string, error) {
	if err != nil {
`)

	violations := CheckAll(CheckOptions{RepoRoot: root, ScanRoots: []string{"missing", "internal"}})
	messages := strings.Join(violationMessages(violations), "\n")
	if !strings.Contains(messages, "silent fallback guard stat error") {
		t.Fatalf("CheckAll() did not flag silent fallback guard stat error; violations:\n%s", messages)
	}
	if !strings.Contains(messages, "silent fallback guard parse error") {
		t.Fatalf("CheckAll() did not flag silent fallback guard parse error; violations:\n%s", messages)
	}
}

func writeSilentFallbackFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/lihah111222333-cloud/super-dolphin-agent\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", rel, err)
	}
}

func hasViolationContaining(violations []Violation, want string) bool {
	return countViolationsContaining(violations, want) > 0
}

func countViolationsContaining(violations []Violation, want string) int {
	count := 0
	for _, violation := range violations {
		if strings.Contains(violation.String(), want) {
			count++
		}
	}
	return count
}

func violationMessages(violations []Violation) []string {
	out := make([]string, 0, len(violations))
	for _, violation := range violations {
		out = append(out, violation.String())
	}
	return out
}
