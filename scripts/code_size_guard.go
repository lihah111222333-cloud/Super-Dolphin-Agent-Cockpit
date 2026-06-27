//go:build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

type cliConfig struct {
	mode    string
	goFiles []string
}

// main 解析代码守卫参数，并按单文件、strict、freeze 或默认棘轮模式执行。
func main() {
	cfg, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  代码守卫: %v\n", err)
		os.Exit(1)
	}
	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  代码守卫: %v\n", err)
		os.Exit(1)
	}
	opts := archtest.CheckOptions{
		RepoRoot:  repoRoot,
		ScanRoots: []string{"internal", "cmd", "pkg", "scripts"},
		SkipDirs:  archtest.DefaultSkipDirs(),
	}
	baselinePath := filepath.Join(repoRoot, "internal/archtest/baseline.json")
	testBaselinePath := filepath.Join(repoRoot, "internal/archtest/baseline_test.json")

	if len(cfg.goFiles) > 0 {
		runSingleFileCheck(opts, cfg.goFiles)
		return
	}

	switch cfg.mode {
	case "freeze":
		runFreeze(opts, baselinePath, testBaselinePath)
	case "strict":
		runStrict(opts)
	default:
		runCheck(opts, baselinePath, testBaselinePath)
	}
}

// parseArgs 将 CLI 参数解析为运行模式和单文件检查列表。
// freeze/strict 不能和文件路径混用，避免 baseline 操作被误当成局部检查。
func parseArgs(args []string) (cliConfig, error) {
	cfg := cliConfig{mode: "check"}
	for _, arg := range args {
		if arg == "--" {
			continue
		}
		switch arg {
		case "--freeze":
			cfg.mode = "freeze"
		case "--strict":
			cfg.mode = "strict"
		default:
			if strings.HasPrefix(arg, "-") {
				return cliConfig{}, fmt.Errorf("unknown flag %s", arg)
			}
			if filepath.Ext(arg) != ".go" {
				return cliConfig{}, fmt.Errorf("expected Go file path, got %s", arg)
			}
			cfg.goFiles = append(cfg.goFiles, arg)
		}
	}
	if cfg.mode != "check" && len(cfg.goFiles) > 0 {
		return cliConfig{}, fmt.Errorf("%s cannot be combined with Go file paths", modeFlag(cfg.mode))
	}
	return cfg, nil
}

// modeFlag 将内部模式名映射回 CLI flag，用于错误消息。
func modeFlag(mode string) string {
	switch mode {
	case "freeze":
		return "--freeze"
	case "strict":
		return "--strict"
	default:
		return mode
	}
}

// runFreeze 全仓扫描并重建生产/测试 baseline。
// 该模式会写 baseline 文件，只应在明确更新守卫基线时使用。
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

// runStrict 不使用 baseline 进行全量检查，适合验证新规则当前是否全仓通过。
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

// runSingleFileCheck 对指定 Go 文件启用函数注释守卫并只输出违规项。
// 该模式供 pre-commit 和分区 worker 快速检查单文件使用。
func runSingleFileCheck(opts archtest.CheckOptions, goFiles []string) {
	opts.EnforceFuncComments = true
	violations := archtest.CheckFiles(opts, goFiles)
	if len(violations) == 0 {
		return
	}
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, v.String())
	}
	os.Exit(1)
}

// runCheck 执行默认棘轮模式：先检查生产违规，再分别校验和收缩生产/测试 baseline。
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

// filterProdViolations 从全量违规中剔除测试文件，只保留生产代码违规。
func filterProdViolations(all []archtest.Violation) []archtest.Violation {
	var out []archtest.Violation
	for _, v := range all {
		if !archtest.IsTestFile(v.File) {
			out = append(out, v)
		}
	}
	return out
}

// resolveRoot 返回检查使用的仓库根目录，空值时回退当前目录。
func resolveRoot(opts archtest.CheckOptions) string {
	if opts.RepoRoot != "" {
		return opts.RepoRoot
	}
	return "."
}

// reportAndExit 输出违规列表并以 1 退出，保持守卫 fail-fast。
func reportAndExit(label string, vs []archtest.Violation) {
	fmt.Fprintf(os.Stderr, "\n❌  %s违规 (%d):\n\n", label, len(vs))
	for _, v := range vs {
		fmt.Fprintln(os.Stderr, "  •", v.String())
	}
	fmt.Fprintln(os.Stderr)
	os.Exit(1)
}

// runRatchetPhase 运行单个 baseline 文件的棘轮校验和自动收缩。
func runRatchetPhase(label, blPath string, opts archtest.CheckOptions, root string, testsOnly bool) {
	blInfo, err := archtest.LoadBaseline(blPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  加载 %s baseline 失败: %v\n", label, err)
		os.Exit(1)
	}
	phaseOpts := opts
	phaseOpts.BaselineTestsOnly = testsOnly
	checkRatchetResult(label, phaseOpts, blInfo.Data)
	shrinkAndSave(label, blPath, blInfo.Data, phaseOpts, root, testsOnly)
}

// checkRatchetResult 比对当前度量与 baseline，发现恶化立即退出。
func checkRatchetResult(label string, opts archtest.CheckOptions, bl archtest.Baseline) {
	result := archtest.CheckWithBaseline(opts, bl)
	if result.OK() {
		return
	}
	total := len(result.Violations) + len(result.NewFileViolations)
	fmt.Fprintf(os.Stderr, "\n❌  %s棘轮恶化 (%d):\n\n", label, total)
	for _, v := range result.Violations {
		fmt.Fprintln(os.Stderr, "  •", v.String())
	}
	for _, v := range result.NewFileViolations {
		fmt.Fprintln(os.Stderr, "  •", v.String())
	}
	fmt.Fprintln(os.Stderr)
	os.Exit(1)
}

// shrinkAndSave 删除或收紧已改善的 baseline 项，并在有变化时写回文件。
func shrinkAndSave(label, blPath string, bl archtest.Baseline, opts archtest.CheckOptions, root string, testsOnly bool) {
	fileSet, err := buildFileSet(root, opts, testsOnly)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  收集 %s baseline 文件集合失败: %v\n", label, err)
		os.Exit(1)
	}
	measure := func(rel string) archtest.FileMetrics {
		return archtest.MeasureBaselineFileMetrics(filepath.Join(root, filepath.FromSlash(rel)))
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

// runFreezeRegistryAutoRepair 执行 freeze registry 自动修复，失败时终止守卫。
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

// printThresholds 输出当前代码守卫阈值，方便定位哪类限制触发。
func printThresholds() {
	fmt.Printf("📏  文件≤%d 函数≤%d 嵌套≤%d CC≤%d 下划线≤%d 包文件≤%d 包行≤%d\n",
		archtest.MaxFileLines, archtest.MaxFuncLines, archtest.MaxNestingDepth,
		archtest.MaxCCComplexity, archtest.MaxUnderscores, archtest.MaxPackageFiles, archtest.MaxPackageLines)
}

// printPassSummary 输出默认检查全部通过的摘要。
func printPassSummary() {
	fmt.Println("✅  代码守卫: 全部通过")
}

// buildFileSet 收集生产或测试 baseline 需要覆盖的 Go 文件集合。
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

// walkCollect 递归收集 baseline 棘轮需要比对的生产或测试文件。
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

// findRepoRoot 从当前目录向上查找包含 go.mod 的仓库根目录。
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
