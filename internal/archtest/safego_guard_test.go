package archtest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSafeGoUsageCentralized guards against regression of the 2026-04-18
// SafeGo unification. All goroutine panic-recovery call sites must go
// through runtimesafe.SafeGo(ctx, logger, label, fn) with an explicit
// ctx and named label — the legacy thin wrapper shared.SafeGo(logger,
// fn) drops ctx and forces a generic label which degrades panic
// telemetry.
//
// The wrapper itself (internal/platform/shared/safe_go.go) is kept for
// backward compatibility but marked Deprecated; no in-tree production
// code should call it. This test fails the build if any call site
// reappears. Both the bare-name import ("shared") and the prefixed
// alias ("platformshared") are forbidden so the check survives future
// import-alias drift.
func TestSafeGoUsageCentralized(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	checker := newSafeGoGuardChecker(root)
	violations := checker.violations(t, []string{"cmd", "internal", "pkg"})

	if len(violations) > 0 {
		t.Fatalf("SafeGo centralization guard violations (%d):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

type safeGoGuardChecker struct {
	root         string
	patterns     []*regexp.Regexp
	allowedFiles map[string]struct{}
	skipDirs     map[string]bool
}

func newSafeGoGuardChecker(root string) safeGoGuardChecker {
	return safeGoGuardChecker{
		root: root,
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`\bshared\.SafeGo\b`),
			regexp.MustCompile(`\bplatformshared\.SafeGo\b`),
		},
		// Only the wrapper implementation file itself is allowed to mention
		// the bare SafeGo identifier.
		allowedFiles: map[string]struct{}{
			filepath.Join("internal", "platform", "shared", "safe_go.go"): {},
		},
		skipDirs: DefaultSkipDirs(),
	}
}

func (c safeGoGuardChecker) violations(t *testing.T, scanRoots []string) []string {
	t.Helper()

	var violations []string
	for _, sr := range scanRoots {
		abs := filepath.Join(c.root, sr)
		err := filepath.Walk(abs, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			more, err := c.scanPath(path, info)
			violations = append(violations, more...)
			return err
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
	}
	return violations
}

func (c safeGoGuardChecker) scanPath(path string, info os.FileInfo) ([]string, error) {
	if info.IsDir() {
		if _, skip := c.skipDirs[info.Name()]; skip {
			return nil, filepath.SkipDir
		}
		return nil, nil
	}
	if !strings.HasSuffix(path, ".go") {
		return nil, nil
	}
	if strings.HasSuffix(path, "_test.go") {
		return nil, nil
	}
	rel, err := filepath.Rel(c.root, path)
	if err != nil {
		return nil, err
	}
	if _, ok := c.allowedFiles[rel]; ok {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return c.lineViolations(rel, string(data)), nil
}

func (c safeGoGuardChecker) lineViolations(rel, text string) []string {
	var violations []string
	for lineNo, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, re := range c.patterns {
			if re.MatchString(line) {
				violations = append(violations,
					rel+":"+itoaSG(lineNo+1)+": legacy SafeGo wrapper call — use runtimesafe.SafeGo(ctx, logger, label, fn): "+trimmed)
			}
		}
	}
	return violations
}

func itoaSG(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
