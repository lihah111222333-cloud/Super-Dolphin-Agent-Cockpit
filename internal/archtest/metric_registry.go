package archtest

// ┌───────────────────────────────────────────────────────────────────────────┐
// │ metric_registry.go — 注册表驱动的指标规则中心                             │
// │                                                                           │
// │ 职责: 集中定义 FileMetrics 所有 int 字段的棘轮/违规/收缩行为。            │
// │ 消除 shotgun surgery: check/shrink/freeze 全部改为 for range 注册表。     │
// │                                                                           │
// │ SYNC-CHECKLIST: 新增 FileMetrics int 字段后需同步以下 2 处:               │
// │   1. baseline.go       — 新增字段（SizeMetrics/ComplexityMetrics/...）     │
// │   2. metric_registry.go — 追加一行 metricRule（你在这里）                  │
// │                                                                           │
// │ 以下消费点由注册表自动驱动，无需手动同步:                                 │
// │   ✅ ratchet.go          RatchetCheck()                                   │
// │   ✅ ratchet.go          HasViolation()                                   │
// │   ✅ baseline_shrink.go  TightenMetricsForPath()                          │
// │                                                                           │
// │ TestRegistryCoversAllIntFields (metric_registry_test.go) 用反射保证       │
// │ 任何遗漏都会在测试中立即暴露。                                            │
// └───────────────────────────────────────────────────────────────────────────┘

// metricRuleFlag 控制一条规则参与哪些消费循环。
type metricRuleFlag int

const (
	// flagRatchet 参与棘轮检查: cur > frozen 且超阈值时报违规。
	flagRatchet metricRuleFlag = 1 << iota
	// flagViolation 参与违规判定: HasViolation() 中用来决定文件是否应冻结到 baseline。
	flagViolation
	// flagTighten 参与收缩合并: TightenMetrics() 中 cur < frozen 时收紧。
	flagTighten
	// flagAll 是最常用的组合: 参与全部三个消费循环。
	flagAll = flagRatchet | flagViolation | flagTighten
)

func (f metricRuleFlag) has(flag metricRuleFlag) bool { return f&flag != 0 }

// limitKind 区分阈值类型。
type limitKind int

const (
	// limitHard 有硬上限（如 MaxFileLines=800）,超过阈值才算违规。
	limitHard limitKind = iota
	// limitZero 零容忍（如 panic_count）,任何非零值都是违规。
	limitZero
	// limitNone 无独立阈值。字段参与棘轮(防恶化)但不独立触发违规。
	// 典型场景: max_params/max_returns 在 CheckAll 中由 functionViolations 硬编码检查，
	// 棘轮系统只负责防已冻结文件恶化。
	limitNone
)

// metricRule 描述 FileMetrics 中一个 int 字段的完整行为。
type metricRule struct {
	// Field 是 JSON 名称（用于错误信息和调试）。
	Field string
	// Access 返回指向 FileMetrics 中该字段的指针。
	Access func(m *FileMetrics) *int
	// Kind 决定阈值判定方式。
	Kind limitKind
	// HardLimit 返回硬阈值（仅 limitHard 使用）。path 参数支持路径相关阈值。
	HardLimit func(path string) int
	// Flags 控制该规则参与哪些消费循环。
	Flags metricRuleFlag
}

// metricRules 返回 FileMetrics 所有 int 字段的注册表。
// ⚠️ 新增字段后在此追加一行。TestRegistryCoversAllIntFields 会验证完整性。
func metricRules() []metricRule {
	return append(append(sizeRules(), complexityRules()...), qualityRules()...)
}

// ── Size 维度 ──

func sizeRules() []metricRule {
	return []metricRule{
		{Field: "lines", Access: func(m *FileMetrics) *int { return &m.Lines }, Kind: limitHard, HardLimit: func(path string) int { return fileLineLimit(path, isFactoryFile(path)) }, Flags: flagAll},
		{Field: "max_func_len", Access: func(m *FileMetrics) *int { return &m.MaxFuncLen }, Kind: limitHard, HardLimit: func(_ string) int { return MaxFuncLines }, Flags: flagAll},
	}
}

// ── Complexity 维度 ──

func complexityRules() []metricRule {
	return []metricRule{
		{Field: "max_nesting", Access: func(m *FileMetrics) *int { return &m.MaxNesting }, Kind: limitHard, HardLimit: func(_ string) int { return MaxNestingDepth }, Flags: flagAll},
		{Field: "max_complexity", Access: func(m *FileMetrics) *int { return &m.MaxComplexity }, Kind: limitHard, HardLimit: func(_ string) int { return MaxCCComplexity }, Flags: flagAll},
		{Field: "max_params", Access: func(m *FileMetrics) *int { return &m.MaxParams }, Kind: limitNone, Flags: flagAll},
		{Field: "max_returns", Access: func(m *FileMetrics) *int { return &m.MaxReturns }, Kind: limitNone, Flags: flagAll},
		{Field: "max_underscore", Access: func(m *FileMetrics) *int { return &m.MaxUnderscore }, Kind: limitHard, HardLimit: func(_ string) int { return MaxUnderscores }, Flags: flagAll},
	}
}

// ── Quality 维度 ──

func qualityRules() []metricRule {
	return []metricRule{
		{Field: "global_vars", Access: func(m *FileMetrics) *int { return &m.GlobalVars }, Kind: limitZero, Flags: flagAll},
		// HasInit 是 bool 字段,不在 int 注册表中,由 RatchetCheck/HasViolation/TightenMetrics 单独处理。
		{Field: "panic_count", Access: func(m *FileMetrics) *int { return &m.PanicCount }, Kind: limitZero, Flags: flagAll},
		{Field: "naked_returns", Access: func(m *FileMetrics) *int { return &m.NakedReturns }, Kind: limitZero, Flags: flagAll},
		{Field: "empty_funcs", Access: func(m *FileMetrics) *int { return &m.EmptyFuncs }, Kind: limitZero, Flags: flagAll},
		{Field: "todo_count", Access: func(m *FileMetrics) *int { return &m.TodoCount }, Kind: limitZero, Flags: flagAll},
		{Field: "max_struct_fields", Access: func(m *FileMetrics) *int { return &m.MaxStructFields }, Kind: limitNone, Flags: flagAll},
		{Field: "naked_goroutines", Access: func(m *FileMetrics) *int { return &m.NakedGoroutines }, Kind: limitZero, Flags: flagAll},
		{Field: "raw_goroutines", Access: func(m *FileMetrics) *int { return &m.RawGoroutines }, Kind: limitZero, Flags: flagAll},
		{Field: "missing_docs", Access: func(m *FileMetrics) *int { return &m.MissingDocs }, Kind: limitZero, Flags: flagAll},
		{Field: "max_struct_methods", Access: func(m *FileMetrics) *int { return &m.MaxStructMethods }, Kind: limitHard, HardLimit: func(_ string) int { return 10 }, Flags: flagAll},
	}
}

// isViolationByRule 根据注册表规则判断单个字段值是否构成违规。
func isViolationByRule(r metricRule, path string, value int) bool {
	switch r.Kind {
	case limitHard:
		return value > r.HardLimit(path)
	case limitZero:
		return value > 0
	case limitNone:
		// limitNone 字段不独立触发违规,但仍参与棘轮防恶化。
		return false
	}
	return false
}
