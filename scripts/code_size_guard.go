//go:build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

func main() {
	mode := parseMode()
	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  代码守卫: %v\n", err)
		os.Exit(1)
	}
	opts := archtest.CheckOptions{
		RepoRoot:  repoRoot,
		ScanRoots: []string{"internal", "cmd", "scripts"},
		SkipDirs:  archtest.DefaultSkipDirs(),
	}
	baselinePath := filepath.Join(repoRoot, "internal/archtest/baseline.json")
	testBaselinePath := filepath.Join(repoRoot, "internal/archtest/baseline_test.json")

	switch mode {
	case "freeze":
		runFreeze(opts, baselinePath, testBaselinePath)
	case "strict":
		runStrict(opts)
	default:
		runCheck(opts, baselinePath, testBaselinePath)
	}
}

func parseMode() string {
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--freeze":
			return "freeze"
		case "--strict":
			return "strict"
		}
	}
	return "check"
}

// runFreeze 全仓扫描建立/重建 baseline（生产 + 测试分文件）。
func runFreeze(opts archtest.CheckOptions, baselinePath, testBaselinePath string) {
	fmt.Println("🔒  代码守卫: freeze 模式 — 建立 baseline")
	bl := archtest.FreezeBaseline(opts)
	if err := archtest.SaveBaseline(baselinePath, bl); err != nil {
		fmt.Fprintf(os.Stderr, "❌  保存 baseline 失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅  生产 baseline — %d 个文件已冻结\n", len(bl))

	testBL := archtest.FreezeTestBaseline(opts)
	if err := archtest.SaveBaseline(testBaselinePath, testBL); err != nil {
		fmt.Fprintf(os.Stderr, "❌  保存 test baseline 失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅  测试 baseline — %d 个文件已冻结\n", len(testBL))
}

// runStrict 无 baseline 全量检查。
func runStrict(opts archtest.CheckOptions) {
	fmt.Println("🔍  代码守卫: strict 模式")
	runFreezeRegistryAutoRepair(opts)
	violations := archtest.CheckAll(opts)
	printThresholds()
	if len(violations) > 0 {
		reportAndExit("strict", violations)
	}
	fmt.Println("✅  strict 模式全量通过")
}

// runCheck 默认棘轮模式。
func runCheck(opts archtest.CheckOptions, blPath, testBLPath string) {
	runFreezeRegistryAutoRepair(opts)
	violations := archtest.CheckAll(opts)
	printThresholds()

	prodViolations := filterProdViolations(violations)
	if len(prodViolations) > 0 {
		reportAndExit("生产文件", prodViolations)
	}

	root := resolveRoot(opts)
	runRatchetPhase("生产", blPath, opts, root, false)
	runRatchetPhase("测试", testBLPath, opts, root, true)
	printPassSummary()
}

func filterProdViolations(all []archtest.Violation) []archtest.Violation {
	var out []archtest.Violation
	for _, v := range all {
		if !archtest.IsTestFile(v.File) {
			out = append(out, v)
		}
	}
	return out
}

func resolveRoot(opts archtest.CheckOptions) string {
	if opts.RepoRoot != "" {
		return opts.RepoRoot
	}
	return "."
}

func reportAndExit(label string, vs []archtest.Violation) {
	fmt.Fprintf(os.Stderr, "\n❌  %s违规 (%d):\n\n", label, len(vs))
	for _, v := range vs {
		fmt.Fprintln(os.Stderr, "  •", v.String())
	}
	fmt.Fprintln(os.Stderr)
	os.Exit(1)
}

func runRatchetPhase(label, blPath string, opts archtest.CheckOptions, root string, testsOnly bool) {
	blInfo, err := archtest.LoadBaseline(blPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  加载 %s baseline 失败: %v\n", label, err)
		os.Exit(1)
	}
	checkRatchetResult(label, opts, blInfo.Data)
	shrinkAndSave(label, blPath, blInfo.Data, opts, root, testsOnly)
}

func checkRatchetResult(label string, opts archtest.CheckOptions, bl archtest.Baseline) {
	result := archtest.CheckWithBaseline(opts, bl)
	if result.OK() {
		return
	}
	fmt.Fprintf(os.Stderr, "\n❌  %s棘轮恶化 (%d):\n\n", label, len(result.Violations))
	for _, v := range result.Violations {
		fmt.Fprintln(os.Stderr, "  •", v.String())
	}
	fmt.Fprintln(os.Stderr)
	os.Exit(1)
}

func shrinkAndSave(label, blPath string, bl archtest.Baseline, opts archtest.CheckOptions, root string, testsOnly bool) {
	fileSet, err := buildFileSet(root, opts, testsOnly)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  收集 %s baseline 文件集合失败: %v\n", label, err)
		os.Exit(1)
	}
	measure := func(rel string) archtest.FileMetrics {
		return archtest.MeasureFileMetrics(filepath.Join(root, filepath.FromSlash(rel)))
	}
	newBL, stats := archtest.ShrinkBaseline(bl, fileSet, measure)
	if stats.Changed() {
		if err := archtest.SaveBaseline(blPath, newBL); err != nil {
			fmt.Fprintf(os.Stderr, "❌  保存收缩 %s baseline 失败: %v\n", label, err)
			os.Exit(1)
		} else {
			fmt.Printf("🧹  %s baseline 收缩 — 收紧 %d, 毕业 %d, 清理 %d\n",
				label, stats.Shrunk, stats.Graduated, stats.Removed)
		}
	}
	fmt.Printf("📊  %s baseline 棘轮通过 — %d 个文件冻结中\n", label, len(newBL))
}

func runFreezeRegistryAutoRepair(opts archtest.CheckOptions) {
	fixes, err := archtest.AutoRepairFreezeRegistry(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  auto-fix freeze registry 失败: %v\n", err)
		os.Exit(1)
	}
	for _, fix := range fixes {
		fmt.Printf("🧹  %s\n", fix.String())
	}
}

func printThresholds() {
	fmt.Printf("📏  文件≤%d 函数≤%d 嵌套≤%d CC≤%d 下划线≤%d 包文件≤%d 包行≤%d\n",
		archtest.MaxFileLines, archtest.MaxFuncLines, archtest.MaxNestingDepth,
		archtest.MaxCCComplexity, archtest.MaxUnderscores, archtest.MaxPackageFiles, archtest.MaxPackageLines)
}

func printPassSummary() {
	fmt.Println("✅  代码守卫: 全部通过")
}

func buildFileSet(root string, opts archtest.CheckOptions, testsOnly bool) (map[string]bool, error) {
	scanRoots := opts.ScanRoots
	if len(scanRoots) == 0 {
		scanRoots = archtest.DefaultScanRoots()
	}
	skipDirs := opts.SkipDirs
	if len(skipDirs) == 0 {
		skipDirs = archtest.DefaultSkipDirs()
	}
	out := make(map[string]bool)
	for _, sr := range scanRoots {
		if err := walkCollect(filepath.Join(root, sr), root, skipDirs, testsOnly, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func walkCollect(absRoot, repoRoot string, skip map[string]bool, testsOnly bool, out map[string]bool) error {
	return filepath.Walk(absRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && skip[info.Name()] {
			return filepath.SkipDir
		}
		if info.IsDir() || filepath.Ext(p) != ".go" {
			return nil
		}
		if testsOnly != strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = true
		return nil
	})
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd, nil
		}
		next := filepath.Dir(wd)
		if next == wd {
			return "", fmt.Errorf("go.mod not found from %s", wd)
		}
		wd = next
	}
}
