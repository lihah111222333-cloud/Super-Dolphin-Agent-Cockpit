package archtest

import (
	"fmt"
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

// RatchetCheck 对比当前指标与 frozen baseline，返回所有恶化方向的违规。
// 只报告 cur > frozen 的字段（恶化）。cur <= frozen 视为改善或持平，不报违规。
func RatchetCheck(path string, cur, frozen FileMetrics) []RatchetViolation {
	var vs []RatchetViolation
	check := func(field string, curV, frozenV int) {
		if curV > frozenV {
			vs = append(vs, RatchetViolation{File: path, Field: field, Frozen: frozenV, Current: curV})
		}
	}
	// Size
	check("lines", cur.Lines, frozen.Lines)
	check("max_func_len", cur.MaxFuncLen, frozen.MaxFuncLen)
	// Complexity
	check("max_nesting", cur.MaxNesting, frozen.MaxNesting)
	check("max_complexity", cur.MaxComplexity, frozen.MaxComplexity)
	check("max_params", cur.MaxParams, frozen.MaxParams)
	check("max_returns", cur.MaxReturns, frozen.MaxReturns)
	check("max_underscore", cur.MaxUnderscore, frozen.MaxUnderscore)
	// Quality
	check("global_vars", cur.GlobalVars, frozen.GlobalVars)
	check("panic_count", cur.PanicCount, frozen.PanicCount)
	check("naked_returns", cur.NakedReturns, frozen.NakedReturns)
	check("empty_funcs", cur.EmptyFuncs, frozen.EmptyFuncs)
	check("todo_count", cur.TodoCount, frozen.TodoCount)
	check("max_struct_fields", cur.MaxStructFields, frozen.MaxStructFields)
	check("naked_goroutines", cur.NakedGoroutines, frozen.NakedGoroutines)
	// HasInit: false→true 是恶化
	if cur.HasInit && !frozen.HasInit {
		vs = append(vs, RatchetViolation{File: path, Field: "has_init", Frozen: 0, Current: 1})
	}
	return vs
}

// HasViolation 判断 metrics 是否超过任一硬阈值。
// 用于 FreezeBaseline：只冻结有真实违规的文件，不冻结绿色代码。
func HasViolation(m FileMetrics) bool {
	return hasSizeViolation(m) || hasQualityViolation(m)
}

// hasSizeViolation 检查 size 和 complexity 类阈值。
func hasSizeViolation(m FileMetrics) bool {
	return m.Lines > MaxFileLines ||
		m.MaxFuncLen > MaxFuncLines ||
		m.MaxNesting > MaxNestingDepth ||
		m.MaxComplexity > MaxCCComplexity ||
		m.MaxUnderscore > MaxUnderscores
}

// hasQualityViolation 检查质量类指标（非零即违规）。
func hasQualityViolation(m FileMetrics) bool {
	return m.GlobalVars > 0 ||
		m.HasInit ||
		m.PanicCount > 0 ||
		m.NakedReturns > 0 ||
		m.EmptyFuncs > 0 ||
		m.TodoCount > 0 ||
		m.NakedGoroutines > 0
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

// FreezeBaseline 全仓扫描生成 baseline。只冻结有真实违规的文件。
func FreezeBaseline(opts CheckOptions) Baseline {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	files := collectGoFiles(repoRoot, opts.scanRoots(), opts.skipDirs())
	bl := make(Baseline)
	for _, absPath := range files {
		relPath, err := filepath.Rel(repoRoot, absPath)
		if err != nil {
			continue
		}
		relPath = filepath.ToSlash(relPath)
		m := MeasureFileMetrics(absPath)
		if HasViolation(m) {
			bl[relPath] = m
		}
	}
	return bl
}

// collectGoFiles 收集扫描根下的所有非测试 Go 文件绝对路径。
func collectGoFiles(repoRoot string, scanRoots []string, skipDirs map[string]bool) []string {
	var files []string
	for _, root := range scanRoots {
		absRoot := filepath.Join(repoRoot, root)
		_ = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if skipDirs[info.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			files = append(files, path)
			return nil
		})
	}
	return files
}
