//go:build ignore

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

type cliConfig struct {
	mode       string
	goFiles    []string
	acceptance archtest.GuardFreezeAcceptance
}

type cliParseState struct {
	metadataSet    bool
	modeSeen       string
	positionalOnly bool
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
		RepoRoot:            repoRoot,
		ScanRoots:           []string{"internal", "cmd", "pkg", "scripts"},
		SkipDirs:            archtest.DefaultSkipDirs(),
		EnforceFuncComments: true,
	}
	freezePath := filepath.Join(repoRoot, "internal/archtest/freeze_baseline.json")

	if len(cfg.goFiles) > 0 {
		runSingleFileCheck(opts, cfg.goFiles)
		return
	}

	switch cfg.mode {
	case "freeze":
		runFreeze(opts, freezePath, cfg.acceptance)
	case "strict":
		runStrict(opts)
	default:
		runCheck(opts, freezePath)
	}
}

// parseArgs 将 CLI 参数解析为运行模式和单文件检查列表。
// freeze/strict 不能和文件路径混用，避免 baseline 操作被误当成局部检查。
func parseArgs(args []string) (cliConfig, error) {
	cfg := cliConfig{mode: "check"}
	state := cliParseState{}
	for index := 0; index < len(args); index++ {
		if err := consumeCLIArgument(&cfg, &state, args, &index); err != nil {
			return cliConfig{}, err
		}
	}
	if err := validateCLIConfig(cfg, state.metadataSet); err != nil {
		return cliConfig{}, err
	}
	return cfg, nil
}

// consumeCLIArgument 消费一个模式、审批或文件参数，并维护参数终止状态。
func consumeCLIArgument(cfg *cliConfig, state *cliParseState, args []string, index *int) error {
	arg := args[*index]
	if state.positionalOnly {
		return appendGuardFile(cfg, arg, true)
	}
	if arg == "--" {
		state.positionalOnly = true
		return nil
	}
	if err := consumeGuardMode(cfg, state, arg); err != nil {
		return err
	}
	handled, approvalFlag := parseGuardFlag(cfg, args, index)
	state.metadataSet = state.metadataSet || approvalFlag
	if handled {
		return nil
	}
	return appendGuardFile(cfg, arg, false)
}

// consumeGuardMode 校验模式参数唯一且互斥，并记录已选择模式。
func consumeGuardMode(cfg *cliConfig, state *cliParseState, arg string) error {
	if arg != "--freeze" && arg != "--strict" {
		return nil
	}
	if state.modeSeen == arg {
		return fmt.Errorf("duplicate mode flag %s", arg)
	}
	if state.modeSeen != "" {
		return fmt.Errorf("conflicting mode flags %s and %s", state.modeSeen, arg)
	}
	state.modeSeen = arg
	if arg == "--freeze" {
		cfg.mode = "freeze"
	} else {
		cfg.mode = "strict"
	}
	return nil
}

// validateCLIConfig 校验运行模式、文件参数与冻结审批参数的组合是否合法。
func validateCLIConfig(cfg cliConfig, metadataSet bool) error {
	if cfg.mode != "check" && len(cfg.goFiles) > 0 {
		return fmt.Errorf("%s cannot be combined with Go file paths", modeFlag(cfg.mode))
	}
	if cfg.mode != "freeze" && metadataSet {
		return fmt.Errorf("freeze approval flags require --freeze")
	}
	if cfg.mode != "freeze" {
		return nil
	}
	if err := archtest.ValidateGuardFreezeApproval(cfg.acceptance); err != nil {
		return fmt.Errorf("invalid freeze approval: %w", err)
	}
	return nil
}

// parseGuardFlag 解析守卫模式和冻结审批参数，并报告是否消费了审批字段。
func parseGuardFlag(cfg *cliConfig, args []string, index *int) (bool, bool) {
	flag := args[*index]
	switch flag {
	case "--freeze":
	case "--strict":
	case "--freeze-owner":
		cfg.acceptance.Owner = requiredGuardArg(args, index)
	case "--freeze-reason":
		cfg.acceptance.Reason = requiredGuardArg(args, index)
	case "--freeze-reviewed-at":
		cfg.acceptance.ReviewedAt = requiredGuardArg(args, index)
	case "--freeze-review-by":
		cfg.acceptance.ReviewBy = requiredGuardArg(args, index)
	case "--freeze-fail-first":
		cfg.acceptance.FailFirstEvidence = requiredGuardArg(args, index)
	default:
		return false, false
	}
	return true, strings.HasPrefix(flag, "--freeze-")
}

func appendGuardFile(cfg *cliConfig, arg string, positionalOnly bool) error {
	if !positionalOnly && strings.HasPrefix(arg, "-") {
		return fmt.Errorf("unknown flag %s", arg)
	}
	if filepath.Ext(arg) != ".go" {
		return fmt.Errorf("expected Go file path, got %s", arg)
	}
	cfg.goFiles = append(cfg.goFiles, arg)
	return nil
}

func requiredGuardArg(args []string, index *int) string {
	(*index)++
	if *index >= len(args) {
		return ""
	}
	value := args[*index]
	if strings.HasPrefix(value, "-") {
		return ""
	}
	return value
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
func runFreeze(opts archtest.CheckOptions, freezePath string, acceptance archtest.GuardFreezeAcceptance) {
	fmt.Println("🔒  代码守卫: freeze 模式 — 建立 baseline")
	sourceHead, err := currentSourceHead(opts.RepoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  读取冻结源码 HEAD 失败: %v\n", err)
		os.Exit(1)
	}
	freeze, err := archtest.FreezeGuardState(opts, acceptance)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  生成统一冻结失败: %v\n", err)
		os.Exit(1)
	}
	freeze.Acceptance, err = archtest.BindGuardFreezeAcceptance(opts.RepoRoot, sourceHead, freeze)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  绑定冻结证据失败: %v\n", err)
		os.Exit(1)
	}
	if err := archtest.SaveGuardFreeze(freezePath, freeze); err != nil {
		fmt.Fprintf(os.Stderr, "❌  保存统一冻结失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅  统一冻结 — 生产 %d 个文件，测试 %d 个文件，priority SSA %d 条违规已冻结\n",
		len(freeze.Metrics.Production), len(freeze.Metrics.Tests), len(freeze.PrioritySSA))
}

// currentSourceHead 返回本次冻结所针对的仓库 HEAD。
func currentSourceHead(repoRoot string) (string, error) {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	head := strings.TrimSpace(string(out))
	if len(head) != 40 {
		return "", fmt.Errorf("git rev-parse HEAD returned invalid SHA %q", head)
	}
	return head, nil
}

// runStrict 不使用 baseline 进行全量检查，适合验证新规则当前是否全仓通过。
func runStrict(opts archtest.CheckOptions) {
	fmt.Println("🔍  代码守卫: strict 模式")
	violations := archtest.CheckAll(opts)
	printThresholds()
	if len(violations) > 0 {
		reportAndExit("strict", violations)
	}
	priorityViolations, err := archtest.CollectPrioritySSAViolations(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  扫描 priority SSA 违规失败: %v\n", err)
		os.Exit(1)
	}
	if len(priorityViolations) > 0 {
		reportPrioritySSAViolationsAndExit("priority SSA strict 违规", priorityViolations)
	}
	failIfGuardGeneratedFilesDrifted(opts)
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
func runCheck(opts archtest.CheckOptions, freezePath string) {
	violations := archtest.CheckAll(opts)
	printThresholds()

	prodViolations := filterProdViolations(violations)
	if len(prodViolations) > 0 {
		reportAndExit("生产文件", prodViolations)
	}

	runUnifiedFreezePhase(freezePath, opts)
	failIfGuardGeneratedFilesDrifted(opts)
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

// reportPrioritySSAViolationsAndExit 输出 SSA 规则违规并提示显式刷新冻结。
func reportPrioritySSAViolationsAndExit(label string, vs []archtest.PrioritySSAViolation) {
	fmt.Fprintf(os.Stderr, "\n❌  %s (%d):\n\n", label, len(vs))
	for _, v := range vs {
		fmt.Fprintln(os.Stderr, "  •", v.String())
	}
	fmt.Fprintln(os.Stderr, "\n确认真阳性后请修复；接受当前债务必须使用 docs/架构/skeleton-code-guard.md 中的审批式 freeze 命令")
	fmt.Fprintln(os.Stderr)
	os.Exit(1)
}

// runUnifiedFreezePhase 对统一冻结文件执行新增拦截和自动毕业。
func runUnifiedFreezePhase(freezePath string, opts archtest.CheckOptions) {
	info, err := archtest.LoadGuardFreeze(freezePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  加载统一冻结失败: %v\n", err)
		os.Exit(1)
	}
	root := resolveRoot(opts)
	freeze := info.Data
	changed := false
	freeze.Metrics.Production, changed = runMetricFreezePhase("生产", freeze.Metrics.Production, opts, root, false, changed)
	freeze.Metrics.Tests, changed = runMetricFreezePhase("测试", freeze.Metrics.Tests, opts, root, true, changed)
	priorityChanged := runPrioritySSAFreezePhase(&freeze, opts)
	if changed || priorityChanged {
		if err := archtest.SaveGuardFreeze(freezePath, freeze); err != nil {
			fmt.Fprintf(os.Stderr, "❌  保存统一冻结收缩失败: %v\n", err)
			os.Exit(1)
		}
	}
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

// runMetricFreezePhase 对统一冻结里的一个 metrics 分区做棘轮校验和自动收缩。
func runMetricFreezePhase(
	label string,
	bl archtest.Baseline,
	opts archtest.CheckOptions,
	root string,
	testsOnly bool,
	changed bool,
) (archtest.Baseline, bool) {
	phaseOpts := opts
	phaseOpts.BaselineTestsOnly = testsOnly
	checkRatchetResult(label, phaseOpts, bl)
	fileSet, err := buildFileSet(root, opts, testsOnly)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  收集 %s 冻结文件集合失败: %v\n", label, err)
		os.Exit(1)
	}
	measure := func(rel string) archtest.FileMetrics {
		return archtest.MeasureBaselineFileMetrics(filepath.Join(root, filepath.FromSlash(rel)))
	}
	newBL, stats := archtest.ShrinkBaseline(bl, fileSet, measure)
	if stats.Changed() {
		changed = true
		fmt.Printf("🧹  %s 冻结收缩 — 收紧 %d, 毕业 %d, 清理 %d\n",
			label, stats.Shrunk, stats.Graduated, stats.Removed)
	}
	fmt.Printf("📊  %s 冻结棘轮通过 — %d 个文件冻结中\n", label, len(newBL))
	return newBL, changed
}

// runPrioritySSAFreezePhase 对 priority SSA 分区做新增拦截和自动毕业。
func runPrioritySSAFreezePhase(freeze *archtest.GuardFreeze, opts archtest.CheckOptions) bool {
	result, err := archtest.CheckPrioritySSAWithBaseline(opts, freeze.PrioritySSA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  扫描 priority SSA 违规失败: %v\n", err)
		os.Exit(1)
	}
	if len(result.New) > 0 {
		reportPrioritySSAViolationsAndExit("priority SSA 新增违规", result.New)
	}
	if len(result.Stale) == 0 {
		fmt.Printf("📊  priority SSA 冻结检查通过 — %d 条违规冻结中\n", len(result.Current))
		return false
	}
	freeze.PrioritySSA = archtest.PrioritySSABaselineFromCurrent(result)
	fmt.Printf("🧹  priority SSA 冻结收缩 — 清理 %d\n", len(result.Stale))
	fmt.Printf("📊  priority SSA 冻结检查通过 — %d 条违规冻结中\n", len(result.Current))
	return true
}

// failIfGuardGeneratedFilesDrifted 让 hook/CI 守卫在自动修复 baseline 或 freeze 后失败。
// 开发者需要显式运行 freeze/shrink 修复命令并人工审查这些生成文件，而不是让 hook 静默改写。
func failIfGuardGeneratedFilesDrifted(opts archtest.CheckOptions) {
	if os.Getenv("SUPER_DOLPHIN_GUARD_FAIL_ON_DRIFT") != "1" {
		return
	}
	repoRoot := resolveRoot(opts)
	paths := []string{
		"internal/archtest/freeze_baseline.json",
	}
	args := append([]string{"-C", repoRoot, "diff", "--exit-code", "--"}, paths...)
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "❌  guard generated-file drift detected; run explicit baseline/freeze repair and review the diff.")
	for _, path := range paths {
		fmt.Fprintf(os.Stderr, "  • %s\n", path)
	}
	if len(out) > 0 {
		fmt.Fprintln(os.Stderr, string(out))
	}
	os.Exit(1)
}

// printThresholds 输出当前代码守卫阈值，方便定位哪类限制触发。
func printThresholds() {
	fmt.Printf("📏  文件≤%d 函数≤%d 嵌套≤%d CC≤%d 下划线≤%d 包文件≤%d\n",
		archtest.MaxFileLines, archtest.MaxFuncLines, archtest.MaxNestingDepth,
		archtest.MaxCCComplexity, archtest.MaxUnderscores, archtest.MaxPackageFiles)
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
