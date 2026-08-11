package gate

import (
	"errors"
	"fmt"
	"sort"
)

var errDeterministicPackingSearchBudget = errors.New("deterministic packing proof exhausted its node budget")
var errDCPAPDurationOverflow = errors.New("D-CPAP duration arithmetic overflow")

const maxDCPAPInt64 = int64(^uint64(0) >> 1)

type deterministicPackingSearchBudget struct {
	remaining int
}

func newDeterministicPackingSearchBudget() deterministicPackingSearchBudget {
	return deterministicPackingSearchBudget{remaining: workloadPlanningSearchNodeBudget}
}

func (budget *deterministicPackingSearchBudget) consume() error {
	if budget.remaining <= 0 {
		return errDeterministicPackingSearchBudget
	}
	budget.remaining--
	return nil
}

// validateDCPAPDurationArithmetic 校验 D-CPAP 输入身份、正时长和总时长加法不溢出。
func validateDCPAPDurationArithmetic(items []dcpapItem, target int64) error {
	if target <= 0 {
		return errors.New("D-CPAP planner target must be positive")
	}
	total := int64(0)
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.id == "" {
			return errors.New("D-CPAP workload ID must not be empty")
		}
		if _, duplicate := seen[item.id]; duplicate {
			return fmt.Errorf("D-CPAP workload ID %q is duplicated", item.id)
		}
		seen[item.id] = struct{}{}
		if item.durationMS <= 0 {
			return errors.New("D-CPAP workload duration must be positive")
		}
		if item.durationMS > maxDCPAPInt64-total {
			return errDCPAPDurationOverflow
		}
		total += item.durationMS
	}
	return nil
}

// sortDCPAPItems 固定时长降序与 ID 平局顺序，避免 map 或输入排列影响计划。
func sortDCPAPItems(items []dcpapItem) {
	sort.Slice(items, func(left, right int) bool {
		if items[left].durationMS != items[right].durationMS {
			return items[left].durationMS > items[right].durationMS
		}
		return items[left].id < items[right].id
	})
}

// dcpapShardCountLowerBound 以超容量 workload 独占和其余总时长容量下界计算箱数。
func dcpapShardCountLowerBound(items []dcpapItem, target int64) (int, error) {
	if target <= 0 {
		return 0, errors.New("D-CPAP planner target must be positive")
	}
	oversize, regularTotal := 0, int64(0)
	for _, item := range items {
		if item.durationMS <= 0 {
			return 0, errors.New("D-CPAP workload duration must be positive")
		}
		if item.durationMS > target {
			oversize++
			continue
		}
		if item.durationMS > maxDCPAPInt64-regularTotal {
			return 0, errDCPAPDurationOverflow
		}
		regularTotal += item.durationMS
	}
	regularShards := regularTotal / target
	if regularTotal%target != 0 {
		regularShards++
	}
	return oversize + int(regularShards), nil
}
