package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

func TestCodeSizeGuard(t *testing.T) {
	root := repoRoot(t)
	opts := codeSizeGuardOptions(root)

	violations := archtest.CheckAll(opts)
	prodViolations, testFilesWithViolations := splitGuardViolations(violations)

	// 生产文件违规：直接失败（与修改前行为一致）
	failIfGuardViolations(t, "code size guard violations", prodViolations, "")

	freezePath := filepath.Join(root, "internal/archtest/freeze_baseline.json")
	runUnifiedFreezeRatchetAndShrink(t, freezePath, opts, root, len(testFilesWithViolations) > 0, violations)
}

func TestModularityConventionMatchesCodeSizeGuard(t *testing.T) {
	for _, limit := range []struct {
		name string
		got  int
	}{
		{name: "default", got: archtest.MaxFileLines},
		{name: "core", got: archtest.MaxCorePackageFileLines},
		{name: "factory", got: archtest.MaxFactoryFileLines},
	} {
		if limit.got != 800 {
			t.Errorf("%s production file effective-line limit = %d, want 800", limit.name, limit.got)
		}
	}

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs/契约/modularity-convention.md"))
	if err != nil {
		t.Fatalf("read modularity convention: %v", err)
	}
	doc := string(raw)
	for _, want := range []string{
		fmt.Sprintf("默认守卫**：单文件有效行数 `<=%d`", archtest.MaxFileLines),
		fmt.Sprintf("包有效行数 `<=10000`、单文件有效行数 `<=%d`", archtest.MaxCorePackageFileLines),
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("modularity convention must contain %q", want)
		}
	}
}

func runUnifiedFreezeRatchetAndShrink(
	t *testing.T,
	path string,
	opts archtest.CheckOptions,
	root string,
	checkTests bool,
	violations []archtest.Violation,
) {
	t.Helper()
	info, err := archtest.LoadGuardFreeze(path)
	if err != nil {
		t.Fatalf("load unified freeze failed: %v", err)
	}
	freeze := info.Data
	changed := false
	freeze.Metrics.Production, changed = runBaselineRatchetAndShrink(t, "prod", freeze.Metrics.Production, opts, root, false, changed)
	if checkTests {
		loadRequiredTestBaseline(t, freeze.Metrics.Tests, violations)

		// 新测试文件（不在基线中）的违规：不允许
		newTestViolations := collectNewTestViolations(violations, freeze.Metrics.Tests)
		failIfGuardViolations(t, "new test file violations not in baseline", newTestViolations,
			"\nFix the test or run: go run scripts/code_size_guard.go --freeze")

		// 已冻结测试文件：棘轮检查 + 自动收缩
		freeze.Metrics.Tests, changed = runBaselineRatchetAndShrink(t, "test", freeze.Metrics.Tests, opts, root, true, changed)
	}
	priorityChanged := runPrioritySSABaselineRatchetAndShrink(t, &freeze, opts)
	if !changed && !priorityChanged {
		return
	}
	if err := archtest.SaveGuardFreeze(path, freeze); err != nil {
		t.Fatalf("save shrunk unified freeze failed: %v", err)
	}
}

func codeSizeGuardOptions(root string) archtest.CheckOptions {
	return archtest.CheckOptions{
		RepoRoot:  root,
		ScanRoots: []string{"internal", "cmd", "pkg", "scripts"},
		SkipDirs:  archtest.DefaultSkipDirs(),
	}
}

func TestCodeSizeGuardScansPkgRoot(t *testing.T) {
	defaultRoots := archtest.DefaultScanRoots()
	if !containsScanRoot(defaultRoots, "pkg") {
		t.Fatalf("DefaultScanRoots() = %v, want pkg included", defaultRoots)
	}
	opts := codeSizeGuardOptions(repoRoot(t))
	if !containsScanRoot(opts.ScanRoots, "pkg") {
		t.Fatalf("codeSizeGuardOptions().ScanRoots = %v, want pkg included", opts.ScanRoots)
	}
}

func containsScanRoot(values []string, want string) bool {
	return slices.Contains(values, want)
}

func splitGuardViolations(violations []archtest.Violation) ([]archtest.Violation, map[string]bool) {
	var prodViolations []archtest.Violation
	testFilesWithViolations := make(map[string]bool)
	for _, v := range violations {
		if archtest.IsTestFile(v.File) {
			testFilesWithViolations[v.File] = true
			continue
		}
		prodViolations = append(prodViolations, v)
	}
	return prodViolations, testFilesWithViolations
}

func failIfGuardViolations(t *testing.T, title string, violations []archtest.Violation, suffix string) {
	t.Helper()
	if len(violations) == 0 {
		return
	}
	lines := violationStrings(violations)
	t.Fatalf("%s (%d):\n%s%s", title, len(violations), strings.Join(lines, "\n"), suffix)
}

func violationStrings(violations []archtest.Violation) []string {
	lines := make([]string, 0, len(violations))
	for _, v := range violations {
		lines = append(lines, v.String())
	}
	return lines
}

func loadRequiredTestBaseline(t *testing.T, baseline archtest.Baseline, violations []archtest.Violation) {
	t.Helper()
	if len(baseline) != 0 {
		return
	}
	lines := violationStrings(collectTestViolations(violations))
	t.Fatalf("test file violations without baseline (%d):\n%s\nRun: go run scripts/code_size_guard.go --freeze",
		len(lines), strings.Join(lines, "\n"))
}

func collectTestViolations(violations []archtest.Violation) []archtest.Violation {
	var out []archtest.Violation
	for _, v := range violations {
		if archtest.IsTestFile(v.File) {
			out = append(out, v)
		}
	}
	return out
}

func collectNewTestViolations(violations []archtest.Violation, baseline map[string]archtest.FileMetrics) []archtest.Violation {
	var out []archtest.Violation
	for _, v := range violations {
		if !archtest.IsTestFile(v.File) {
			continue
		}
		if _, inBaseline := baseline[v.File]; !inBaseline {
			out = append(out, v)
		}
	}
	return out
}

// runBaselineRatchetAndShrink 对指定 baseline 做棘轮检查 + 自动收缩。
func runBaselineRatchetAndShrink(
	t *testing.T,
	label string,
	baseline archtest.Baseline,
	opts archtest.CheckOptions,
	root string,
	testsOnly bool,
	changed bool,
) (archtest.Baseline, bool) {
	t.Helper()
	if len(baseline) == 0 {
		return baseline, changed
	}
	phaseOpts := opts
	phaseOpts.BaselineTestsOnly = testsOnly
	result := archtest.CheckWithBaseline(phaseOpts, baseline)
	if !result.OK() {
		lines := make([]string, 0, len(result.Violations)+len(result.NewFileViolations))
		for _, v := range result.Violations {
			lines = append(lines, v.String())
		}
		for _, v := range result.NewFileViolations {
			lines = append(lines, v.String())
		}
		t.Fatalf("%s baseline ratchet regressions (%d):\n%s", label, len(lines), strings.Join(lines, "\n"))
	}
	fileSet := buildFileSetFiltered(t, phaseOpts, root, testsOnly)
	newBL, stats := archtest.ShrinkBaseline(baseline, fileSet, func(relPath string) archtest.FileMetrics {
		return archtest.MeasureBaselineFileMetrics(filepath.Join(root, filepath.FromSlash(relPath)))
	})
	if stats.Changed() {
		changed = true
		t.Logf("🧹 %s baseline auto-shrunk: shrunk=%d graduated=%d removed=%d", label, stats.Shrunk, stats.Graduated, stats.Removed)
	}
	return newBL, changed
}

func runPrioritySSABaselineRatchetAndShrink(t *testing.T, freeze *archtest.GuardFreeze, opts archtest.CheckOptions) bool {
	t.Helper()
	result, err := archtest.CheckPrioritySSAWithBaseline(opts, freeze.PrioritySSA)
	if err != nil {
		t.Fatalf("check priority SSA baseline failed: %v", err)
	}
	if len(result.New) > 0 {
		t.Fatalf("priority SSA new violations not in baseline (%d):\n%s\nRun: go run scripts/code_size_guard.go --freeze",
			len(result.New), strings.Join(archtest.PrioritySSAViolationStrings(result.New), "\n"))
	}
	if len(result.Stale) == 0 {
		return false
	}
	freeze.PrioritySSA = archtest.PrioritySSABaselineFromCurrent(result)
	t.Logf("🧹 priority SSA baseline auto-shrunk: stale=%d", len(result.Stale))
	return true
}

func buildFileSetFiltered(t *testing.T, opts archtest.CheckOptions, root string, testsOnly bool) map[string]bool {
	t.Helper()
	scanRoots := opts.ScanRoots
	if len(scanRoots) == 0 {
		scanRoots = archtest.DefaultScanRoots()
	}
	fileSet := make(map[string]bool)
	for _, sr := range scanRoots {
		absRoot := filepath.Join(root, sr)
		if err := filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if testsOnly != strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			fileSet[filepath.ToSlash(rel)] = true
			return nil
		}); err != nil {
			t.Fatalf("build file set failed for %s: %v", absRoot, err)
		}
	}
	return fileSet
}
