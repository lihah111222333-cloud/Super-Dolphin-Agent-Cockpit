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

	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\bshared\.SafeGo\b`),
		regexp.MustCompile(`\bplatformshared\.SafeGo\b`),
	}

	// Only the wrapper implementation file itself is allowed to mention
	// the bare SafeGo identifier.
	allowedFiles := map[string]struct{}{
		filepath.Join("internal", "platform", "shared", "safe_go.go"): {},
	}

	scanRoots := []string{"cmd", "internal", "pkg"}
	skipDirs := DefaultSkipDirs()

	var violations []string
	for _, sr := range scanRoots {
		abs := filepath.Join(root, sr)
		err := filepath.Walk(abs, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				name := info.Name()
				if _, skip := skipDirs[name]; skip {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if _, ok := allowedFiles[rel]; ok {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for lineNo, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				for _, re := range patterns {
					if re.MatchString(line) {
						violations = append(violations,
							rel+":"+itoaSG(lineNo+1)+": legacy SafeGo wrapper call — use runtimesafe.SafeGo(ctx, logger, label, fn): "+trimmed)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("SafeGo centralization guard violations (%d):\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
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
