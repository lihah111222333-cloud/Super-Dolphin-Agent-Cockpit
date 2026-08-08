package archtest

// SizeMetrics 聚合文件级大小指标。
type SizeMetrics struct {
	Lines      int `json:"lines"`
	MaxFuncLen int `json:"max_func_len"`
}

// ComplexityMetrics 聚合文件级复杂度指标。
type ComplexityMetrics struct {
	MaxNesting    int `json:"max_nesting"`
	MaxComplexity int `json:"max_complexity"`
	MaxParams     int `json:"max_params"`
	MaxReturns    int `json:"max_returns"`
	MaxUnderscore int `json:"max_underscore"`
}

// QualityMetrics 聚合文件级质量指标。
// 新字段必须使用 omitempty，避免旧 baseline 反序列化后产生兼容性漂移。
//
// ┌───────────────────────────────────────────────────────────────────────┐
// │ SYNC-CHECKLIST: 新增 int 指标字段后需同步以下 2 处：                 │
// │                                                                       │
// │  1. baseline.go          — 新增字段（你在这里）                       │
// │  2. metric_registry.go   — 追加一行 metricRule                        │
// │                                                                       │
// │  以下消费点由 metric_registry.go 自动驱动，无需手动同步：             │
// │    ✅ ratchet.go          RatchetCheck()                              │
// │    ✅ ratchet.go          HasViolation()                              │
// │    ✅ baseline_shrink.go  TightenMetricsForPath()                     │
// │                                                                       │
// │  TestRegistryCoversAllIntFields (metric_registry_test.go) 用反射      │
// │  保证任何遗漏都会在测试中立即暴露。                                   │
// └───────────────────────────────────────────────────────────────────────┘
type QualityMetrics struct {
	GlobalVars       int  `json:"global_vars"`
	HasInit          bool `json:"has_init,omitempty"`
	PanicCount       int  `json:"panic_count"`
	NakedReturns     int  `json:"naked_returns"`
	EmptyFuncs       int  `json:"empty_funcs"`
	TodoCount        int  `json:"todo_count"`
	MaxStructFields  int  `json:"max_struct_fields,omitempty"`
	NakedGoroutines  int  `json:"naked_goroutines,omitempty"`
	RawGoroutines    int  `json:"raw_goroutines,omitempty"`
	MissingDocs      int  `json:"missing_docs,omitempty"`
	MaxStructMethods int  `json:"max_struct_methods,omitempty"`
}

// FileMetrics 聚合单文件的全部可棘轮指标。
type FileMetrics struct {
	SizeMetrics
	ComplexityMetrics
	QualityMetrics
}

// Baseline 是 per-file metrics 的 JSON 映射表。key 是相对于仓库根的文件路径。
type Baseline map[string]FileMetrics
