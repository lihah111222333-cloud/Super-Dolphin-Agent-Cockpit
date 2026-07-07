package archtest

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

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

// BaselineInfo 封装基线数据及其元信息。
type BaselineInfo struct {
	Data    Baseline
	ModTime time.Time
}

const baselineFileMode = 0o644

// LoadBaseline 从 path 读取 baseline JSON。文件不存在时直接报错，避免守卫基线缺失被静默忽略。
func LoadBaseline(path string) (BaselineInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return BaselineInfo{}, fmt.Errorf("stat baseline: %w", err)
		}
		return BaselineInfo{}, fmt.Errorf("stat baseline: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return BaselineInfo{}, fmt.Errorf("read baseline: %w", err)
	}
	var bl Baseline
	if err := json.Unmarshal(data, &bl); err != nil {
		return BaselineInfo{}, fmt.Errorf("parse baseline: %w", err)
	}
	if bl == nil {
		return BaselineInfo{}, fmt.Errorf("baseline %s is null", path)
	}
	return BaselineInfo{Data: bl, ModTime: info.ModTime()}, nil
}

// SaveBaseline 将 baseline 写入 path（覆盖式）。
func SaveBaseline(path string, bl Baseline) error {
	data, err := json.MarshalIndent(bl, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal baseline: %w", err)
	}
	if err := os.WriteFile(path, data, baselineFileMode); err != nil {
		return fmt.Errorf("write baseline: %w", err)
	}
	return nil
}
