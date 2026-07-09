package archtest

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// PrioritySSARule 标识触发冻结的 priority SSA 规则，便于假阳性反查规则来源。
type PrioritySSARule string

const (
	PrioritySSAWidePortRule      PrioritySSARule = "priority_ssa_wide_port"
	PrioritySSAIgnoredReturnRule PrioritySSARule = "priority_ssa_ignored_return"
	PrioritySSAContextCancelRule PrioritySSARule = "priority_ssa_context_cancel"
	PrioritySSARawSQLRule        PrioritySSARule = "priority_ssa_raw_sql"
	PrioritySSAErrorStringRule   PrioritySSARule = "priority_ssa_error_string"
	PrioritySSAFXInvokeRule      PrioritySSARule = "priority_ssa_fx_invoke_side_effect"
	PrioritySSAOnStartRule       PrioritySSARule = "priority_ssa_onstart_side_effect"
	prioritySSABaselineFileMode                  = 0o644
)

// PrioritySSAViolation 记录一条可冻结的 priority SSA 违规及其规则归因。
type PrioritySSAViolation struct {
	Rule   PrioritySSARule `json:"rule"`
	File   string          `json:"file"`
	Line   int             `json:"line"`
	Detail string          `json:"detail"`
}

// PrioritySSABaseline 以稳定 key 保存当前接受的 priority SSA 违规集合。
type PrioritySSABaseline map[string]PrioritySSAViolation

// PrioritySSABaselineInfo 封装 priority SSA baseline 数据和文件元信息。
type PrioritySSABaselineInfo struct {
	Data    PrioritySSABaseline
	ModTime time.Time
}

// PrioritySSABaselineResult 描述 priority SSA 当前扫描相对冻结文件的变化。
type PrioritySSABaselineResult struct {
	New     []PrioritySSAViolation
	Stale   []PrioritySSAViolation
	Current []PrioritySSAViolation
}

// OK 判断 priority SSA baseline 是否没有新增违规。
func (r PrioritySSABaselineResult) OK() bool {
	return len(r.New) == 0
}

// LoadPrioritySSABaseline 读取 priority SSA 冻结文件，并校验每条冻结都保留规则元数据。
func LoadPrioritySSABaseline(path string) (PrioritySSABaselineInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return PrioritySSABaselineInfo{}, fmt.Errorf("stat priority SSA baseline: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return PrioritySSABaselineInfo{}, fmt.Errorf("read priority SSA baseline: %w", err)
	}
	var baseline PrioritySSABaseline
	if err := json.Unmarshal(data, &baseline); err != nil {
		return PrioritySSABaselineInfo{}, fmt.Errorf("parse priority SSA baseline: %w", err)
	}
	if baseline == nil {
		return PrioritySSABaselineInfo{}, fmt.Errorf("priority SSA baseline %s is null", path)
	}
	if err := validatePrioritySSABaseline(path, baseline); err != nil {
		return PrioritySSABaselineInfo{}, err
	}
	return PrioritySSABaselineInfo{Data: baseline, ModTime: info.ModTime()}, nil
}

// SavePrioritySSABaseline 将当前 priority SSA 违规转换为可反查规则的冻结文件。
func SavePrioritySSABaseline(path string, violations []PrioritySSAViolation) error {
	baseline := prioritySSABaselineFromViolations(violations)
	data, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal priority SSA baseline: %w", err)
	}
	if err := os.WriteFile(path, data, prioritySSABaselineFileMode); err != nil {
		return fmt.Errorf("write priority SSA baseline: %w", err)
	}
	return nil
}

// CheckPrioritySSABaseline 扫描当前 priority SSA 违规，并与冻结文件做新增和失效对比。
func CheckPrioritySSABaseline(opts CheckOptions, path string) (PrioritySSABaselineResult, error) {
	info, err := LoadPrioritySSABaseline(path)
	if err != nil {
		return PrioritySSABaselineResult{}, err
	}
	return CheckPrioritySSAWithBaseline(opts, info.Data)
}

// ShrinkPrioritySSABaseline 在已冻结违规消失时只删除失效项，避免放宽 baseline。
func ShrinkPrioritySSABaseline(path string, result PrioritySSABaselineResult) error {
	if len(result.Stale) == 0 {
		return nil
	}
	return SavePrioritySSABaseline(path, result.Current)
}

func prioritySSABaselineFromViolations(violations []PrioritySSAViolation) PrioritySSABaseline {
	out := PrioritySSABaseline{}
	for _, violation := range violations {
		out[violation.Key()] = violation
	}
	return out
}

// validatePrioritySSABaseline 确保冻结项包含 rule/file/detail 且 JSON key 可由内容重建。
func validatePrioritySSABaseline(path string, baseline PrioritySSABaseline) error {
	for key, violation := range baseline {
		if violation.Rule == "" {
			return fmt.Errorf("priority SSA baseline %s entry %q missing rule", path, key)
		}
		if violation.File == "" {
			return fmt.Errorf("priority SSA baseline %s entry %q missing file", path, key)
		}
		if violation.Detail == "" {
			return fmt.Errorf("priority SSA baseline %s entry %q missing detail", path, key)
		}
		if got := violation.Key(); got != key {
			return fmt.Errorf("priority SSA baseline %s entry %q key mismatch, want %q", path, key, got)
		}
	}
	return nil
}

func comparePrioritySSABaseline(
	baseline PrioritySSABaseline,
	current []PrioritySSAViolation,
) PrioritySSABaselineResult {
	currentByKey := prioritySSABaselineFromViolations(current)
	result := PrioritySSABaselineResult{Current: current}
	for _, violation := range current {
		if _, ok := baseline[violation.Key()]; !ok {
			result.New = append(result.New, violation)
		}
	}
	for key, frozen := range baseline {
		if _, ok := currentByKey[key]; !ok {
			result.Stale = append(result.Stale, frozen)
		}
	}
	sortPrioritySSAViolations(result.New)
	sortPrioritySSAViolations(result.Stale)
	return result
}

// Key 返回冻结项的稳定身份，包含路径、行号、规则和细节。
func (v PrioritySSAViolation) Key() string {
	return fmt.Sprintf("%s:%d:%s:%s", v.File, v.Line, v.Rule, v.Detail)
}

// String 输出带 rule 的违规文本，供 guard 失败时直接定位规则来源。
func (v PrioritySSAViolation) String() string {
	return fmt.Sprintf("%s:%d %s %s", v.File, v.Line, v.Rule, v.Detail)
}

func sortPrioritySSAViolations(violations []PrioritySSAViolation) {
	sort.Slice(violations, func(i, j int) bool {
		return strings.Compare(violations[i].Key(), violations[j].Key()) < 0
	})
}

// PrioritySSAViolationStrings 将 priority SSA 违规列表转换为稳定文本行。
func PrioritySSAViolationStrings(violations []PrioritySSAViolation) []string {
	lines := make([]string, 0, len(violations))
	for _, violation := range violations {
		lines = append(lines, violation.String())
	}
	return lines
}
