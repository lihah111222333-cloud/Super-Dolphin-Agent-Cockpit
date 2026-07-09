package archtest

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

const (
	// GuardFreezeVersion 标识统一冻结文件结构版本。
	GuardFreezeVersion = 1
	guardFreezeMode    = 0o644
)

// GuardMetricFreeze 保存普通 metrics 棘轮的生产和测试冻结区。
type GuardMetricFreeze struct {
	Production Baseline `json:"production"`
	Tests      Baseline `json:"tests"`
}

// GuardFreeze 是所有守卫共用的统一冻结文件结构。
type GuardFreeze struct {
	Version     int                 `json:"version"`
	Metrics     GuardMetricFreeze   `json:"metrics"`
	PrioritySSA PrioritySSABaseline `json:"priority_ssa"`
}

// GuardFreezeInfo 封装统一冻结文件数据和文件元信息。
type GuardFreezeInfo struct {
	Data    GuardFreeze
	ModTime time.Time
}

// NewEmptyGuardFreeze 返回带完整空分区的统一冻结结构。
func NewEmptyGuardFreeze() GuardFreeze {
	return GuardFreeze{
		Version: GuardFreezeVersion,
		Metrics: GuardMetricFreeze{
			Production: Baseline{},
			Tests:      Baseline{},
		},
		PrioritySSA: PrioritySSABaseline{},
	}
}

// LoadGuardFreeze 读取统一冻结文件并校验所有分区存在。
func LoadGuardFreeze(path string) (GuardFreezeInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return GuardFreezeInfo{}, fmt.Errorf("stat guard freeze: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return GuardFreezeInfo{}, fmt.Errorf("read guard freeze: %w", err)
	}
	var freeze GuardFreeze
	if err := json.Unmarshal(data, &freeze); err != nil {
		return GuardFreezeInfo{}, fmt.Errorf("parse guard freeze: %w", err)
	}
	if err := validateGuardFreeze(path, freeze); err != nil {
		return GuardFreezeInfo{}, err
	}
	return GuardFreezeInfo{Data: freeze, ModTime: info.ModTime()}, nil
}

// SaveGuardFreeze 覆盖写入统一冻结文件。
func SaveGuardFreeze(path string, freeze GuardFreeze) error {
	if err := validateGuardFreeze(path, freeze); err != nil {
		return err
	}
	data, err := json.MarshalIndent(freeze, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal guard freeze: %w", err)
	}
	if err := os.WriteFile(path, data, guardFreezeMode); err != nil {
		return fmt.Errorf("write guard freeze: %w", err)
	}
	return nil
}

// FreezeGuardState 扫描当前仓库并生成统一冻结快照。
func FreezeGuardState(opts CheckOptions) (GuardFreeze, error) {
	freeze := NewEmptyGuardFreeze()
	freeze.Metrics.Production = FreezeBaseline(opts)
	freeze.Metrics.Tests = FreezeTestBaseline(opts)
	priorityViolations, err := CollectPrioritySSAViolations(opts)
	if err != nil {
		return GuardFreeze{}, err
	}
	freeze.PrioritySSA = prioritySSABaselineFromViolations(priorityViolations)
	return freeze, nil
}

// CheckPrioritySSAWithBaseline 使用内存中的 priority SSA baseline 检查新增和失效违规。
func CheckPrioritySSAWithBaseline(opts CheckOptions, baseline PrioritySSABaseline) (PrioritySSABaselineResult, error) {
	current, err := CollectPrioritySSAViolations(opts)
	if err != nil {
		return PrioritySSABaselineResult{}, err
	}
	return comparePrioritySSABaseline(baseline, current), nil
}

// PrioritySSABaselineFromCurrent 返回结果中的当前违规集合，供统一冻结收缩写回。
func PrioritySSABaselineFromCurrent(result PrioritySSABaselineResult) PrioritySSABaseline {
	return prioritySSABaselineFromViolations(result.Current)
}

func validateGuardFreeze(path string, freeze GuardFreeze) error {
	if freeze.Version != GuardFreezeVersion {
		return fmt.Errorf("guard freeze %s version=%d, want %d", path, freeze.Version, GuardFreezeVersion)
	}
	if freeze.Metrics.Production == nil {
		return fmt.Errorf("guard freeze %s missing metrics.production", path)
	}
	if freeze.Metrics.Tests == nil {
		return fmt.Errorf("guard freeze %s missing metrics.tests", path)
	}
	if freeze.PrioritySSA == nil {
		return fmt.Errorf("guard freeze %s missing priority_ssa", path)
	}
	return validatePrioritySSABaseline(path, freeze.PrioritySSA)
}
