package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func TestCodeSizeGuard(t *testing.T) {
	root := repoRoot(t)
	opts := codeSizeGuardOptions(root)
	cache := &archtest.RepositoryGuardScanCache{}

	t.Run("size and freeze", func(t *testing.T) {
		runCodeSizeGuard(t, cache, opts, root)
	})
	t.Run("identifier", func(t *testing.T) {
		runIdentifierGuard(t, cache, opts)
	})
	t.Run("dead key", func(t *testing.T) {
		runDeadKeyGuard(t, cache, opts)
	})
}

func runCodeSizeGuard(
	t *testing.T,
	cache *archtest.RepositoryGuardScanCache,
	opts archtest.CheckOptions,
	root string,
) {
	t.Helper()
	violations := repositoryGuardViolations(t, cache, opts)
	prodViolations, testFilesWithViolations := splitGuardViolations(violations)

	// 生产文件违规：直接失败（与修改前行为一致）
	failIfGuardViolations(t, "code size guard violations", prodViolations, "")

	freezePath := filepath.Join(root, "internal/archtest/freeze_baseline.json")
	runUnifiedFreezeCheck(t, freezePath, opts, root, len(testFilesWithViolations) > 0, violations)
}

func runIdentifierGuard(
	t *testing.T,
	cache *archtest.RepositoryGuardScanCache,
	opts archtest.CheckOptions,
) {
	t.Helper()
	allViolations := filterRepositoryViolationsByKind(
		repositoryGuardViolations(t, cache, opts),
		archtest.ViolationIdentifier,
	)
	var violations []archtest.Violation
	for _, violation := range allViolations {
		if !archtest.IsTestFile(violation.File) {
			violations = append(violations, violation)
		}
	}
	failIfGuardViolations(t, "identifier guard violations", violations, "")
}

func runDeadKeyGuard(
	t *testing.T,
	cache *archtest.RepositoryGuardScanCache,
	opts archtest.CheckOptions,
) {
	t.Helper()
	violations := filterRepositoryViolationsByKind(
		repositoryGuardViolations(t, cache, opts),
		archtest.ViolationDeadKey,
	)
	failIfGuardViolations(t, "dead-key guard violations", violations, "")
}

func repositoryGuardViolations(
	t *testing.T,
	cache *archtest.RepositoryGuardScanCache,
	opts archtest.CheckOptions,
) []archtest.Violation {
	t.Helper()
	violations, err := archtest.CheckRepositoryGuardsOnce(cache, opts)
	if err != nil {
		t.Fatalf("scan repository guards: %v", err)
	}
	return violations
}

func filterRepositoryViolationsByKind(
	violations []archtest.Violation,
	kind archtest.ViolationKind,
) []archtest.Violation {
	filtered := make([]archtest.Violation, 0, len(violations))
	for _, violation := range violations {
		if violation.Kind == kind {
			filtered = append(filtered, violation)
		}
	}
	return filtered
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

func runUnifiedFreezeCheck(
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
	checkBaselineRatchetAndFreshness(t, "prod", freeze.Metrics.Production, opts, root, false)
	if checkTests {
		loadRequiredTestBaseline(t, freeze.Metrics.Tests, violations)

		// 新测试文件（不在基线中）的违规：不允许
		newTestViolations := collectNewTestViolations(violations, freeze.Metrics.Tests)
		failIfGuardViolations(t, "new test file violations not in baseline", newTestViolations,
			"\nFix the test or follow the reviewed freeze command in docs/架构/skeleton-code-guard.md")

		// 已冻结测试文件：普通测试只校验棘轮与 freshness；更新必须显式 --freeze。
		checkBaselineRatchetAndFreshness(t, "test", freeze.Metrics.Tests, opts, root, true)
	}
	checkPrioritySSABaselineFreshness(t, freeze.PrioritySSA, opts)
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
	t.Fatalf("test file violations without baseline (%d):\n%s\nSee: docs/架构/skeleton-code-guard.md",
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

// checkBaselineRatchetAndFreshness 对指定 baseline 做棘轮检查，并在 baseline 可收缩时失败。
func checkBaselineRatchetAndFreshness(
	t *testing.T,
	label string,
	baseline archtest.Baseline,
	opts archtest.CheckOptions,
	root string,
	testsOnly bool,
) {
	t.Helper()
	if len(baseline) == 0 {
		return
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
		t.Fatalf("%s baseline is stale: shrunk=%d graduated=%d removed=%d current_entries=%d\nRun: go run ./scripts/code_size_guard.go --freeze",
			label, stats.Shrunk, stats.Graduated, stats.Removed, len(newBL))
	}
}

func checkPrioritySSABaselineFreshness(t *testing.T, baseline archtest.PrioritySSABaseline, opts archtest.CheckOptions) {
	t.Helper()
	result, err := archtest.CheckPrioritySSAWithBaseline(opts, baseline)
	if err != nil {
		t.Fatalf("check priority SSA baseline failed: %v", err)
	}
	if len(result.New) > 0 {
		t.Fatalf("priority SSA new violations not in baseline (%d):\n%s\nSee: docs/架构/skeleton-code-guard.md",
			len(result.New), strings.Join(archtest.PrioritySSAViolationStrings(result.New), "\n"))
	}
	if len(result.Stale) == 0 {
		return
	}
	t.Fatalf("priority SSA baseline is stale (%d):\n%s\nRun: go run ./scripts/code_size_guard.go --freeze",
		len(result.Stale), strings.Join(archtest.PrioritySSAViolationStrings(result.Stale), "\n"))
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
