package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

func TestCodeSizeGuard(t *testing.T) {
	root := repoRoot(t)
	opts := codeSizeGuardOptions(root)
	// 守卫运行时自动收缩 / 删除已回落到默认预算的 freeze 条目（同步回写 freeze_registry.go）。
	fixes, err := archtest.AutoRepairFreezeRegistry(opts)
	if err != nil {
		t.Fatalf("freeze registry autofix failed: %v", err)
	}
	logFreezeRegistryRepairs(t, fixes)

	violations := archtest.CheckAll(opts)
	prodViolations, testFilesWithViolations := splitGuardViolations(violations)

	// 生产文件违规：直接失败（与修改前行为一致）
	failIfGuardViolations(t, "code size guard violations", prodViolations, "")

	// ── 生产文件：棘轮检查 + 自动收缩 ──
	prodBaselinePath := filepath.Join(root, "internal/archtest/baseline.json")
	runBaselineRatchetAndShrink(t, "prod", prodBaselinePath, opts, root, false)

	if len(testFilesWithViolations) == 0 {
		return
	}

	// ── 测试文件违规：通过 baseline_test.json 棘轮管理 ──
	testBaselinePath := filepath.Join(root, "internal/archtest/baseline_test.json")
	testBLInfo := loadRequiredTestBaseline(t, testBaselinePath, violations)

	// 新测试文件（不在基线中）的违规：不允许
	newTestViolations := collectNewTestViolations(violations, testBLInfo.Data)
	failIfGuardViolations(t, "new test file violations not in baseline", newTestViolations,
		"\nFix the test or run: go run scripts/code_size_guard.go --freeze")

	// 已冻结测试文件：棘轮检查 + 自动收缩
	runBaselineRatchetAndShrink(t, "test", testBaselinePath, opts, root, true)
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
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func logFreezeRegistryRepairs(t *testing.T, fixes []archtest.FreezeRegistryAutoFix) {
	t.Helper()
	for _, f := range fixes {
		t.Logf("freeze registry auto-repaired: %s", f.String())
	}
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

func loadRequiredTestBaseline(t *testing.T, path string, violations []archtest.Violation) archtest.BaselineInfo {
	t.Helper()
	testBLInfo, loadErr := archtest.LoadBaseline(path)
	if loadErr != nil {
		t.Fatalf("load test baseline failed: %v", loadErr)
	}
	if len(testBLInfo.Data) != 0 {
		return testBLInfo
	}
	lines := violationStrings(collectTestViolations(violations))
	t.Fatalf("test file violations without baseline (%d):\n%s\nRun: go run scripts/code_size_guard.go --freeze",
		len(lines), strings.Join(lines, "\n"))
	return testBLInfo
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
func runBaselineRatchetAndShrink(t *testing.T, label, blPath string, opts archtest.CheckOptions, root string, testsOnly bool) {
	t.Helper()
	blInfo, loadErr := archtest.LoadBaseline(blPath)
	if loadErr != nil {
		t.Fatalf("load %s baseline failed: %v", label, loadErr)
	}
	if len(blInfo.Data) == 0 {
		return
	}
	phaseOpts := opts
	phaseOpts.BaselineTestsOnly = testsOnly
	result := archtest.CheckWithBaseline(phaseOpts, blInfo.Data)
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
	newBL, stats := archtest.ShrinkBaseline(blInfo.Data, fileSet, func(relPath string) archtest.FileMetrics {
		return archtest.MeasureBaselineFileMetrics(filepath.Join(root, filepath.FromSlash(relPath)))
	})
	if stats.Changed() {
		if saveErr := archtest.SaveBaseline(blPath, newBL); saveErr != nil {
			t.Fatalf("save shrunk %s baseline failed: %v", label, saveErr)
		} else {
			t.Logf("🧹 %s baseline auto-shrunk: shrunk=%d graduated=%d removed=%d", label, stats.Shrunk, stats.Graduated, stats.Removed)
		}
	}
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
