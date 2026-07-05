package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// RatchetViolation 是棘轮检查发现的单个恶化。
type RatchetViolation struct {
	File    string
	Field   string
	Frozen  int
	Current int
}

// String 返回人类可读的违规描述。
func (v RatchetViolation) String() string {
	return fmt.Sprintf("棘轮违规 %s: %s 恶化 %d → %d", v.File, v.Field, v.Frozen, v.Current)
}

// CheckResult 聚合一次全仓棘轮检查的结果。
type CheckResult struct {
	// Violations 是所有棘轮违规（指标恶化）。
	Violations []RatchetViolation
	// NewFileViolations 是新文件（不在 baseline 中）超过硬阈值的违规。
	NewFileViolations []Violation
}

// OK 返回是否全部通过。
func (r CheckResult) OK() bool {
	return len(r.Violations) == 0 && len(r.NewFileViolations) == 0
}

// RatchetCheck 对比当前指标与 frozen baseline，返回违规字段的恶化。
// 注册表驱动：所有 flagRatchet 规则自动参与，无需手动同步。
func RatchetCheck(path string, cur, frozen FileMetrics) []RatchetViolation {
	var vs []RatchetViolation
	cur = normalizeBaselineMetrics(path, cur)
	frozen = normalizeBaselineMetrics(path, frozen)
	for _, r := range metricRules() {
		if !r.Flags.has(flagRatchet) {
			continue
		}
		curV := *r.Access(&cur)
		frozenV := *r.Access(&frozen)
		if curV > frozenV && isViolationByRule(r, path, curV) {
			vs = append(vs, RatchetViolation{File: path, Field: r.Field, Frozen: frozenV, Current: curV})
		}
	}
	// HasInit: bool 字段，不在 int 注册表中。false→true 是恶化。
	if cur.HasInit && !frozen.HasInit {
		vs = append(vs, RatchetViolation{File: path, Field: "has_init", Frozen: 0, Current: 1})
	}
	return vs
}

// shouldTightenRatchetField 判断单个字段是否应被收紧。
// 注册表驱动：查找注册表中对应字段的规则定义。
func shouldTightenRatchetField(path, field string, curV, frozenV int) bool {
	if curV >= frozenV {
		return false
	}
	for _, r := range metricRules() {
		if r.Field != field {
			continue
		}
		switch r.Kind {
		case limitZero:
			return true
		case limitHard:
			limit := r.HardLimit(path)
			return curV > limit || frozenV > limit
		case limitNone:
			// limitNone 字段不参与主动收缩（它们没有阈值可判断是否为「债务」）
			return false
		}
	}
	return false
}

// HasViolation 判断 metrics 是否超过任一硬阈值。
// 注册表驱动：所有 flagViolation 规则自动参与，无需手动同步。
// 用于 FreezeBaseline：只冻结有真实违规的文件，不冻结绿色代码。
func HasViolation(m FileMetrics) bool {
	return HasViolationForPath("", m)
}

// HasViolationForPath 按路径归一化指标后判断是否存在守卫债务。
func HasViolationForPath(path string, m FileMetrics) bool {
	m = normalizeBaselineMetrics(path, m)
	for _, r := range metricRules() {
		if !r.Flags.has(flagViolation) {
			continue
		}
		if isViolationByRule(r, path, *r.Access(&m)) {
			return true
		}
	}
	// HasInit: bool 字段，不在 int 注册表中。
	return m.HasInit
}

// CheckWithBaseline 执行全仓棘轮检查。
//   - 新文件（不在 baseline 中）：对 HasViolation 的指标调用现有 CheckAll 逻辑。
//   - 已冻结文件：RatchetCheck，拒绝恶化。
func CheckWithBaseline(opts CheckOptions, bl Baseline) CheckResult {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	var result CheckResult
	for path, frozen := range bl {
		absPath := filepath.Join(repoRoot, filepath.FromSlash(path))
		cur := MeasureBaselineFileMetrics(absPath)
		if cur.Lines == 0 {
			continue // 文件已删除，shrink 负责清理
		}
		vs := RatchetCheck(path, cur, frozen)
		result.Violations = append(result.Violations, vs...)
	}
	result.NewFileViolations = newFileMetricViolations(repoRoot, opts, bl)
	return result
}

// MeasureBaselineFileMetrics 补齐 baseline 棘轮使用的全部注册指标。
// 单文件守卫仍走轻量 CheckAll；baseline 路径必须覆盖新文件缺基线时的质量债务。
func MeasureBaselineFileMetrics(path string) FileMetrics {
	m := MeasureFileMetrics(path)
	if m.Lines == 0 {
		return m
	}
	node, ok := parseMetricFile(path)
	if !ok || ast.IsGenerated(node) {
		return m
	}
	m.NakedGoroutines = CountNakedGoStmts(node)
	return normalizeBaselineMetrics(path, m)
}

func parseMetricFile(path string) (*ast.File, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, path, data, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}
	return node, true
}

// newFileMetricViolations 对不在 baseline 中的扫描文件执行所有硬阈值检查。
// 这里与 RatchetCheck 分开，避免新文件因为没有 frozen 值而绕过零容忍指标。
func newFileMetricViolations(repoRoot string, opts CheckOptions, bl Baseline) []Violation {
	files, err := collectGoFilesFiltered(repoRoot, opts.scanRoots(), opts.skipDirs(), opts.BaselineTestsOnly)
	if err != nil {
		return []Violation{{
			Kind:    ViolationFile,
			Message: fmt.Sprintf("collect new baseline files: %v", err),
		}}
	}
	var violations []Violation
	for _, absPath := range files {
		relPath, err := filepath.Rel(repoRoot, absPath)
		if err != nil {
			violations = append(violations, Violation{
				Kind:    ViolationFile,
				File:    filepath.ToSlash(absPath),
				Message: fmt.Sprintf("baseline file relative path: %v", err),
			})
			continue
		}
		relPath = filepath.ToSlash(relPath)
		if !shouldCheckNewBaselineFile(repoRoot, relPath, bl) {
			continue
		}
		metrics := MeasureBaselineFileMetrics(absPath)
		violations = append(violations, metricViolationsForNewFile(relPath, metrics)...)
	}
	sortViolations(violations)
	return violations
}

// shouldCheckNewBaselineFile 将所有扫描范围内的 baseline 缺项纳入零容忍检查。
// baseline 只冻结既有债务；HEAD 已存在但缺 baseline 的文件同样不能绕过 panic/init/global/go 等硬规则。
func shouldCheckNewBaselineFile(repoRoot, relPath string, bl Baseline) bool {
	if _, frozen := bl[relPath]; frozen {
		return false
	}
	_ = repoRoot
	return true
}

// metricViolationsForNewFile 将新文件指标转换为零容忍违规列表。
func metricViolationsForNewFile(path string, metrics FileMetrics) []Violation {
	var violations []Violation
	metrics = normalizeBaselineMetrics(path, metrics)
	for _, r := range metricRules() {
		if !r.Flags.has(flagViolation) {
			continue
		}
		got := *r.Access(&metrics)
		if !isViolationByRule(r, path, got) {
			continue
		}
		violations = append(violations, Violation{
			Kind:    ViolationFile,
			File:    path,
			Got:     got,
			Limit:   ruleLimitForMessage(r, path),
			Message: newFileMetricViolationMessage(path, r.Field, got, ruleLimitForMessage(r, path)),
		})
	}
	if metrics.HasInit {
		violations = append(violations, Violation{
			Kind:    ViolationFile,
			File:    path,
			Got:     1,
			Limit:   0,
			Message: newFileMetricViolationMessage(path, "has_init", 1, 0),
		})
	}
	return violations
}

func ruleLimitForMessage(r metricRule, path string) int {
	switch r.Kind {
	case limitHard:
		return r.HardLimit(path)
	case limitZero:
		return 0
	default:
		return 0
	}
}

func newFileMetricViolationMessage(path, field string, got, limit int) string {
	return fmt.Sprintf("新文件 %s: %s got=%d limit=%d", path, field, got, limit)
}

// normalizeBaselineMetrics 排除测试文件不参与棘轮的文档注释债务。
func normalizeBaselineMetrics(path string, m FileMetrics) FileMetrics {
	if IsTestFile(path) {
		m.MissingDocs = 0
	}
	return m
}

// IsTestFile 判断文件路径是否为测试文件。
func IsTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}

// FreezeBaseline 全仓扫描生成生产文件 baseline。只冻结有真实违规的非测试文件。
func FreezeBaseline(opts CheckOptions) Baseline {
	return freezeBaselineFiltered(opts, false)
}

// FreezeTestBaseline 全仓扫描生成测试文件 baseline。只冻结有真实违规的测试文件。
func FreezeTestBaseline(opts CheckOptions) Baseline {
	return freezeBaselineFiltered(opts, true)
}

// freezeBaselineFiltered 扫描指定类型文件并生成只记录真实违规的 baseline。
// testsOnly 控制生产/测试基线分流，路径统一转成 slash 形式保证跨平台稳定。
func freezeBaselineFiltered(opts CheckOptions, testsOnly bool) Baseline {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	files, err := collectGoFilesFiltered(repoRoot, opts.scanRoots(), opts.skipDirs(), testsOnly)
	if err != nil {
		log.Fatalf("collect baseline files: %v", err)
	}
	bl := make(Baseline)
	for _, absPath := range files {
		relPath, err := filepath.Rel(repoRoot, absPath)
		if err != nil {
			log.Fatalf("baseline file relative path: %v", err)
		}
		relPath = filepath.ToSlash(relPath)
		m := MeasureBaselineFileMetrics(absPath)
		if HasViolationForPath(relPath, m) {
			bl[relPath] = m
		}
	}
	return bl
}

// collectGoFilesFiltered 收集go文件filtered。
func collectGoFilesFiltered(repoRoot string, scanRoots []string, skipDirs map[string]bool, testsOnly bool) ([]string, error) {
	var files []string
	for _, root := range scanRoots {
		absRoot := filepath.Join(repoRoot, root)
		if err := filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if skipDirs[info.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			isTest := strings.HasSuffix(path, "_test.go")
			if testsOnly != isTest {
				return nil
			}
			files = append(files, path)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return files, nil
}
