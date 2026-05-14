package archtest

// ShrinkStats 记录一次 baseline 自动收缩的统计。
type ShrinkStats struct {
	Shrunk    int // 数值字段被收紧的条目数
	Graduated int // 已无违规、移出 baseline 的条目数
	Removed   int // 文件已不存在被清理的条目数
}

// Changed 返回是否有任何收缩操作发生。
func (s ShrinkStats) Changed() bool {
	return s.Shrunk > 0 || s.Graduated > 0 || s.Removed > 0
}

// ShrinkBaseline 按「只缩不放宽」原则收缩 baseline。
//   - 文件不在 fileSet → Removed（已删除）
//   - 文件当前完全无违规 → Graduated（毕业）
//   - 文件仍违规但任一指标低于 frozen → Shrunk（收紧）
//   - 文件指标恶化 → 保持 frozen 原值，由 RatchetCheck 报错
//
// measure 由调用方注入（生产用 MeasureFileMetrics，测试可注入 mock）。
func ShrinkBaseline(oldBL Baseline, fileSet map[string]bool, measure func(string) FileMetrics) (Baseline, ShrinkStats) {
	newBL := make(Baseline)
	var stats ShrinkStats
	for path, frozen := range oldBL {
		if !fileSet[path] {
			stats.Removed++
			continue
		}
		cur := measure(path)
		if !HasViolation(cur) {
			stats.Graduated++
			continue
		}
		tight, changed := TightenMetricsForPath(path, cur, frozen)
		if changed {
			stats.Shrunk++
		}
		newBL[path] = tight
	}
	return newBL, stats
}

// TightenMetrics 按「只缩不放宽」原则合并 cur 与 frozen。
// 只有仍属于守卫债务的数值字段会取 min；HasInit 仅允许 true → false（删除 init() 视为收紧）。
// 任何「恶化方向」（cur > frozen）都保持 frozen 原值不变，由 RatchetCheck 报错。
func TightenMetrics(cur, frozen FileMetrics) (FileMetrics, bool) {
	return TightenMetricsForPath("", cur, frozen)
}

// TightenMetricsForPath 按单文件实际阈值收紧 baseline。
// 未越过守卫阈值的大小/复杂度字段不参与收缩，避免绿色指标制造 baseline churn。
func TightenMetricsForPath(path string, cur, frozen FileMetrics) (FileMetrics, bool) {
	out := frozen
	changed := false
	tighten := func(field string, curV int, outV *int) {
		if shouldTightenRatchetField(path, field, curV, *outV) {
			*outV = curV
			changed = true
		}
	}
	// Size
	tighten("lines", cur.Lines, &out.Lines)
	tighten("max_func_len", cur.MaxFuncLen, &out.MaxFuncLen)
	// Complexity
	tighten("max_nesting", cur.MaxNesting, &out.MaxNesting)
	tighten("max_complexity", cur.MaxComplexity, &out.MaxComplexity)
	tighten("max_params", cur.MaxParams, &out.MaxParams)
	tighten("max_returns", cur.MaxReturns, &out.MaxReturns)
	tighten("max_underscore", cur.MaxUnderscore, &out.MaxUnderscore)
	// Quality
	tighten("global_vars", cur.GlobalVars, &out.GlobalVars)
	tighten("panic_count", cur.PanicCount, &out.PanicCount)
	tighten("naked_returns", cur.NakedReturns, &out.NakedReturns)
	tighten("empty_funcs", cur.EmptyFuncs, &out.EmptyFuncs)
	tighten("todo_count", cur.TodoCount, &out.TodoCount)
	tighten("max_struct_fields", cur.MaxStructFields, &out.MaxStructFields)
	tighten("naked_goroutines", cur.NakedGoroutines, &out.NakedGoroutines)
	// HasInit: true → false 是收紧
	if !cur.HasInit && frozen.HasInit {
		out.HasInit = false
		changed = true
	}
	return out, changed
}
