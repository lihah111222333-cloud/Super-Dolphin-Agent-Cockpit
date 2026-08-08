package archtest

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// GuardFreezeViolationError 标识冻结棘轮或 priority SSA 新增违规。
type GuardFreezeViolationError struct {
	Err error
}

// Error 返回守卫违规的具体说明。
func (e *GuardFreezeViolationError) Error() string {
	if e == nil || e.Err == nil {
		return "guard freeze violation"
	}
	return e.Err.Error()
}

// Unwrap 返回底层违规说明，供 gate worker 选择稳定退出码。
func (e *GuardFreezeViolationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// GuardFreezeDriftError 记录自动收缩后生成冻结文件相对 Git 的漂移。
type GuardFreezeDriftError struct {
	Paths  []string
	Output string
}

// Error 返回要求显式审查生成文件漂移的稳定说明。
func (e *GuardFreezeDriftError) Error() string {
	var builder strings.Builder
	builder.WriteString("❌  guard generated-file drift detected; run explicit baseline/freeze repair and review the diff.")
	for _, path := range e.Paths {
		builder.WriteString("\n  • ")
		builder.WriteString(path)
	}
	if output := strings.TrimSpace(e.Output); output != "" {
		builder.WriteString("\n")
		builder.WriteString(output)
	}
	return builder.String()
}

// GuardFreezeCheckResult 描述一次统一冻结棘轮检查及其自动收缩结果。
type GuardFreezeCheckResult struct {
	Freeze          GuardFreeze
	ProductionStats ShrinkStats
	TestsStats      ShrinkStats
	Priority        PrioritySSABaselineResult
	Changed         bool
}

// CheckAndShrinkGuardFreeze 对统一冻结文件执行生产、测试和 priority SSA 棘轮检查。
// 检查沿用同一文件快照和指标缓存；只有收紧、毕业或清理发生时才写回冻结文件。
func CheckAndShrinkGuardFreeze(opts CheckOptions, freezePath string) (GuardFreezeCheckResult, error) {
	info, err := LoadGuardFreeze(freezePath)
	if err != nil {
		return GuardFreezeCheckResult{}, fmt.Errorf("load guard freeze: %w", err)
	}
	snapshot, err := NewBaselineFileSnapshot(opts)
	if err != nil {
		return GuardFreezeCheckResult{}, fmt.Errorf("collect guard freeze files: %w", err)
	}
	metrics := NewBaselineMetricCache()
	priorityPackages, err := loadPrioritySSAPackagesAndSeedMetrics(opts, snapshot.Files(false), metrics)
	if err != nil {
		return GuardFreezeCheckResult{}, err
	}
	freeze := info.Data
	production, productionStats, err := checkAndShrinkGuardFreezeMetrics(
		"生产", freeze.Metrics.Production, opts, false, snapshot, metrics,
	)
	if err != nil {
		return GuardFreezeCheckResult{}, err
	}
	freeze.Metrics.Production = production
	tests, testsStats, err := checkAndShrinkGuardFreezeMetrics(
		"测试", freeze.Metrics.Tests, opts, true, snapshot, metrics,
	)
	if err != nil {
		return GuardFreezeCheckResult{}, err
	}
	freeze.Metrics.Tests = tests
	priority, err := checkPrioritySSAWithBaselinePackages(priorityPackages, freeze.PrioritySSA)
	if err != nil {
		return GuardFreezeCheckResult{}, fmt.Errorf("scan priority SSA violations: %w", err)
	}
	if len(priority.New) > 0 {
		return GuardFreezeCheckResult{}, &GuardFreezeViolationError{Err: guardFreezePriorityError("priority SSA 新增违规", priority.New)}
	}
	changed := applyGuardFreezeChanges(&freeze, productionStats, testsStats, priority)
	if err := persistGuardFreezeIfChanged(freezePath, freeze, changed); err != nil {
		return GuardFreezeCheckResult{}, err
	}
	return GuardFreezeCheckResult{
		Freeze:          freeze,
		ProductionStats: productionStats,
		TestsStats:      testsStats,
		Priority:        priority,
		Changed:         changed,
	}, nil
}

func loadPrioritySSAPackagesAndSeedMetrics(opts CheckOptions, files map[string]string, metrics *BaselineMetricCache) ([]*prioritySSAPackage, error) {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	priorityPackages, err := loadPrioritySSAPackages(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("load priority SSA packages: %w", err)
	}
	if err := seedPrioritySSAMetricCache(metrics, priorityPackages, files); err != nil {
		return nil, err
	}
	return priorityPackages, nil
}

func applyGuardFreezeChanges(
	freeze *GuardFreeze,
	productionStats ShrinkStats,
	testsStats ShrinkStats,
	priority PrioritySSABaselineResult,
) bool {
	changed := productionStats.Changed()
	if testsStats.Changed() {
		changed = true
	}
	if len(priority.Stale) > 0 {
		freeze.PrioritySSA = PrioritySSABaselineFromCurrent(priority)
		changed = true
	}
	return changed
}

func persistGuardFreezeIfChanged(path string, freeze GuardFreeze, changed bool) error {
	if !changed {
		return nil
	}
	if err := SaveGuardFreeze(path, freeze); err != nil {
		return fmt.Errorf("save guard freeze shrink: %w", err)
	}
	return nil
}

// WriteGuardFreezeCheckSummary 输出统一冻结检查的稳定摘要。
func WriteGuardFreezeCheckSummary(output io.Writer, result GuardFreezeCheckResult) error {
	if result.ProductionStats.Changed() {
		if _, err := fmt.Fprintf(output, "🧹  生产 冻结收缩 — 收紧 %d, 毕业 %d, 清理 %d\n", result.ProductionStats.Shrunk, result.ProductionStats.Graduated, result.ProductionStats.Removed); err != nil {
			return fmt.Errorf("write production guard freeze summary: %w", err)
		}
	}
	if _, err := fmt.Fprintf(output, "📊  生产 冻结棘轮通过 — %d 个文件冻结中\n", len(result.Freeze.Metrics.Production)); err != nil {
		return fmt.Errorf("write production guard freeze summary: %w", err)
	}
	if result.TestsStats.Changed() {
		if _, err := fmt.Fprintf(output, "🧹  测试 冻结收缩 — 收紧 %d, 毕业 %d, 清理 %d\n", result.TestsStats.Shrunk, result.TestsStats.Graduated, result.TestsStats.Removed); err != nil {
			return fmt.Errorf("write test guard freeze summary: %w", err)
		}
	}
	if _, err := fmt.Fprintf(output, "📊  测试 冻结棘轮通过 — %d 个文件冻结中\n", len(result.Freeze.Metrics.Tests)); err != nil {
		return fmt.Errorf("write test guard freeze summary: %w", err)
	}
	if len(result.Priority.Stale) > 0 {
		if _, err := fmt.Fprintf(output, "🧹  priority SSA 冻结收缩 — 清理 %d\n", len(result.Priority.Stale)); err != nil {
			return fmt.Errorf("write priority SSA guard freeze summary: %w", err)
		}
	}
	if _, err := fmt.Fprintf(output, "📊  priority SSA 冻结检查通过 — %d 条违规冻结中\n", len(result.Priority.Current)); err != nil {
		return fmt.Errorf("write priority SSA guard freeze summary: %w", err)
	}
	return nil
}

// CheckGuardGeneratedFilesDrift 在显式 drift 守卫开启时拒绝未审查的冻结文件改写。
func CheckGuardGeneratedFilesDrift(opts CheckOptions) error {
	if os.Getenv("SUPER_DOLPHIN_GUARD_FAIL_ON_DRIFT") != "1" {
		return nil
	}
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	paths := []string{"internal/archtest/freeze_baseline.json"}
	args := append([]string{"-C", repoRoot, "diff", "--exit-code", "--"}, paths...)
	output, err := exec.Command("git", args...).CombinedOutput()
	if err == nil {
		return nil
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); !ok || exitErr.ExitCode() != 1 {
		return fmt.Errorf("check guard generated-file drift: %w", err)
	}
	return &GuardFreezeViolationError{Err: &GuardFreezeDriftError{Paths: paths, Output: string(output)}}
}

func checkAndShrinkGuardFreezeMetrics(
	label string,
	bl Baseline,
	opts CheckOptions,
	testsOnly bool,
	snapshot *BaselineFileSnapshot,
	metrics *BaselineMetricCache,
) (Baseline, ShrinkStats, error) {
	phaseOpts := opts
	phaseOpts.BaselineTestsOnly = testsOnly
	result, err := CheckWithBaselineCachedFiles(phaseOpts, bl, metrics, snapshot.Files(testsOnly))
	if err != nil {
		return nil, ShrinkStats{}, fmt.Errorf("%s guard ratchet check: %w", label, err)
	}
	if !result.OK() {
		return nil, ShrinkStats{}, &GuardFreezeViolationError{Err: guardFreezeRatchetError(label, result)}
	}
	newBaseline, stats, err := snapshot.Shrink(bl, testsOnly, metrics)
	if err != nil {
		return nil, ShrinkStats{}, fmt.Errorf("%s guard freeze shrink: %w", label, err)
	}
	return newBaseline, stats, nil
}

func guardFreezeRatchetError(label string, result CheckResult) error {
	total := len(result.Violations) + len(result.NewFileViolations)
	lines := make([]string, 0, total)
	for _, violation := range result.Violations {
		lines = append(lines, violation.String())
	}
	for _, violation := range result.NewFileViolations {
		lines = append(lines, violation.String())
	}
	return fmt.Errorf("%s棘轮恶化 (%d): %s", label, total, strings.Join(lines, "; "))
}

func guardFreezePriorityError(label string, violations []PrioritySSAViolation) error {
	lines := PrioritySSAViolationStrings(violations)
	return fmt.Errorf("%s (%d): %s", label, len(lines), strings.Join(lines, "; "))
}
