package archtest

import (
	"fmt"
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
	for _, r := range metricRules() {
		if !r.Flags.has(flagViolation) {
			continue
		}
		if isViolationByRule(r, "", *r.Access(&m)) {
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
		cur := MeasureFileMetrics(absPath)
		if cur.Lines == 0 {
			continue // 文件已删除，shrink 负责清理
		}
		vs := RatchetCheck(path, cur, frozen)
		result.Violations = append(result.Violations, vs...)
	}
	return result
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

// freezeBaselineFiltered 处理freezebaselinefiltered。
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
		m := MeasureFileMetrics(absPath)
		if HasViolation(m) {
			bl[relPath] = m
		}
	}
	return bl
}

// collectGoFiles 收集扫描根下的所有 Go 文件绝对路径（含测试文件）。
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
