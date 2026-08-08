package archtest

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSafeGoUsageCentralized is the single parent snapshot for the
// production-source guards that scan the cmd/internal/pkg tree. Each child
// keeps one original rule assertion while sharing the immutable source view.
func TestSafeGoUsageCentralized(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)
	snapshot := loadProductionSourceSnapshot(t, root)
	checks := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "safe-go", run: func(t *testing.T) {
			checker := newSafeGoGuardChecker(snapshot.root)
			violations := checker.violationsFromSnapshot(t, snapshot, []string{"cmd", "internal", "pkg"})
			if len(violations) > 0 {
				t.Fatalf("SafeGo centralization guard violations (%d):\n  %s",
					len(violations), strings.Join(violations, "\n  "))
			}
		}},
		{name: "error-string-match", run: func(t *testing.T) {
			violations := collectErrorStringMatchViolationsFromSnapshot(snapshot, []string{"internal", "cmd"})
			if len(violations) > 0 {
				t.Fatalf("Error string match guard violations (%d):\n  %s",
					len(violations), strings.Join(violations, "\n  "))
			}
		}},
		{name: "scattered-decimal", run: func(t *testing.T) {
			violations := collectScatteredDecimalViolationsFromSnapshot(snapshot, []string{"cmd", "internal", "pkg"})
			if len(violations) > 0 {
				t.Fatalf("Scattered Decimal violations (%d):\n  %s",
					len(violations), strings.Join(violations, "\n  "))
			}
		}},
		{name: "structured-log", run: func(t *testing.T) {
			violations := collectStructuredLogViolationsFromSnapshot(t, snapshot, []string{"internal", "cmd"})
			if len(violations) > 0 {
				t.Fatalf("Structured log guard violations (%d):\n  %s",
					len(violations), strings.Join(violations, "\n  "))
			}
		}},
		{name: "naked-goroutine", run: func(t *testing.T) {
			violations := findNakedGoroutineViolationsFromSnapshot(t, snapshot, []string{"internal"}, nakedGoroutineAllowedFiles())
			if len(violations) > 0 {
				t.Fatalf("Naked goroutine guard violations (%d):\n  %s",
					len(violations), strings.Join(violations, "\n  "))
			}
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			t.Parallel()
			check.run(t)
		})
	}
}

type safeGoGuardChecker struct {
	root         string
	patterns     []*regexp.Regexp
	allowedFiles map[string]struct{}
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
	}
}

func (c safeGoGuardChecker) violationsFromSnapshot(t *testing.T, snapshot *productionSourceSnapshot, scanRoots []string) []string {
	t.Helper()

	var violations []string
	for _, file := range snapshot.files {
		if !productionSourcePathInRoots(file.relPath, scanRoots) {
			continue
		}
		rel := filepath.FromSlash(file.relPath)
		if _, ok := c.allowedFiles[rel]; ok {
			continue
		}
		violations = append(violations, c.lineViolations(file.relPath, string(file.data))...)
	}
	return violations
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
