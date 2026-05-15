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
	opts := archtest.CheckOptions{
		RepoRoot:  root,
		ScanRoots: []string{"internal", "cmd", "scripts"},
		SkipDirs:  archtest.DefaultSkipDirs(),
	}
	// 守卫运行时自动收缩 / 删除已回落到默认预算的 freeze 条目（同步回写 freeze_registry.go）。
	fixes, err := archtest.AutoRepairFreezeRegistry(opts)
	if err != nil {
		t.Fatalf("freeze registry autofix failed: %v", err)
	}
	for _, f := range fixes {
		t.Logf("freeze registry auto-repaired: %s", f.String())
	}

	violations := archtest.CheckAll(opts)

	// ── 分离生产文件和测试文件的违规 ──
	var prodViolations []archtest.Violation
	testFilesWithViolations := make(map[string]bool)
	for _, v := range violations {
		if archtest.IsTestFile(v.File) {
			testFilesWithViolations[v.File] = true
		} else {
			prodViolations = append(prodViolations, v)
		}
	}

	// 生产文件违规：直接失败（与修改前行为一致）
	if len(prodViolations) > 0 {
		lines := make([]string, 0, len(prodViolations))
		for _, v := range prodViolations {
			lines = append(lines, v.String())
		}
		t.Fatalf("code size guard violations (%d):\n%s", len(prodViolations), strings.Join(lines, "\n"))
	}

	// ── 生产文件：棘轮检查 + 自动收缩 ──
	prodBaselinePath := filepath.Join(root, "internal/archtest/baseline.json")
	runBaselineRatchetAndShrink(t, "prod", prodBaselinePath, opts, root, false)

	if len(testFilesWithViolations) == 0 {
		return
	}

	// ── 测试文件违规：通过 baseline_test.json 棘轮管理 ──
	testBaselinePath := filepath.Join(root, "internal/archtest/baseline_test.json")
	testBLInfo, loadErr := archtest.LoadBaseline(testBaselinePath)
	if loadErr != nil {
		t.Fatalf("load test baseline failed: %v", loadErr)
	}
	if len(testBLInfo.Data) == 0 {
		var lines []string
		for _, v := range violations {
			if archtest.IsTestFile(v.File) {
				lines = append(lines, v.String())
			}
		}
		t.Fatalf("test file violations without baseline (%d):\n%s\nRun: go run scripts/code_size_guard.go --freeze",
			len(lines), strings.Join(lines, "\n"))
	}

	// 新测试文件（不在基线中）的违规：不允许
	var newTestViolations []archtest.Violation
	for _, v := range violations {
		if !archtest.IsTestFile(v.File) {
			continue
		}
		if _, inBaseline := testBLInfo.Data[v.File]; !inBaseline {
			newTestViolations = append(newTestViolations, v)
		}
	}
	if len(newTestViolations) > 0 {
		lines := make([]string, 0, len(newTestViolations))
		for _, v := range newTestViolations {
			lines = append(lines, v.String())
		}
		t.Fatalf("new test file violations not in baseline (%d):\n%s\nFix the test or run: go run scripts/code_size_guard.go --freeze",
			len(newTestViolations), strings.Join(lines, "\n"))
	}

	// 已冻结测试文件：棘轮检查 + 自动收缩
	runBaselineRatchetAndShrink(t, "test", testBaselinePath, opts, root, true)
}

// runBaselineRatchetAndShrink 对指定 baseline 做棘轮检查 + 自动收缩。
func runBaselineRatchetAndShrink(t *testing.T, label, blPath string, opts archtest.CheckOptions, root string, testsOnly bool) {
	t.Helper()
	blInfo, loadErr := archtest.LoadBaseline(blPath)
	if loadErr != nil {
		t.Logf("⚠️ load %s baseline failed: %v", label, loadErr)
		return
	}
	if len(blInfo.Data) == 0 {
		return
	}
	result := archtest.CheckWithBaseline(opts, blInfo.Data)
	if !result.OK() {
		lines := make([]string, 0, len(result.Violations))
		for _, v := range result.Violations {
			lines = append(lines, v.String())
		}
		t.Fatalf("%s baseline ratchet regressions (%d):\n%s", label, len(result.Violations), strings.Join(lines, "\n"))
	}
	fileSet := buildFileSetFiltered(opts, root, testsOnly)
	newBL, stats := archtest.ShrinkBaseline(blInfo.Data, fileSet, func(relPath string) archtest.FileMetrics {
		return archtest.MeasureFileMetrics(filepath.Join(root, filepath.FromSlash(relPath)))
	})
	if stats.Changed() {
		if saveErr := archtest.SaveBaseline(blPath, newBL); saveErr != nil {
			t.Logf("⚠️ save shrunk %s baseline failed: %v", label, saveErr)
		} else {
			t.Logf("🧹 %s baseline auto-shrunk: shrunk=%d graduated=%d removed=%d", label, stats.Shrunk, stats.Graduated, stats.Removed)
		}
	}
}

func buildFileSetFiltered(opts archtest.CheckOptions, root string, testsOnly bool) map[string]bool {
	scanRoots := opts.ScanRoots
	if len(scanRoots) == 0 {
		scanRoots = archtest.DefaultScanRoots()
	}
	fileSet := make(map[string]bool)
	for _, sr := range scanRoots {
		absRoot := filepath.Join(root, sr)
		_ = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
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
				return nil
			}
			fileSet[filepath.ToSlash(rel)] = true
			return nil
		})
	}
	return fileSet
}
