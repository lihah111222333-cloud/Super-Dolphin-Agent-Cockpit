package gate

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"

type dcpapExactSearch struct {
	ordered  []dcpapItem
	bins     []dcpapBin
	target   int64
	budget   *deterministicPackingSearchBudget
	best     dcpapPackingScore
	bestBins []dcpapBin
}

// place 穷举小输入布局；仅空箱按对称性剪枝。
func (search *dcpapExactSearch) place(index int) error {
	if err := search.budget.consume(); err != nil {
		return err
	}
	if index == len(search.ordered) {
		return search.consider()
	}
	item := search.ordered[index]
	emptyVisited := false
	for binIndex := range search.bins {
		empty := len(search.bins[binIndex].items) == 0
		if empty && emptyVisited {
			continue
		}
		emptyVisited = emptyVisited || empty
		if !dcpapCanPlace(search.bins[binIndex], item, search.target) {
			continue
		}
		search.apply(binIndex, item)
		err := search.place(index + 1)
		search.undo(binIndex, item)
		if err != nil {
			return err
		}
	}
	return nil
}

// consider 比较完整目标元组并保留 canonical 最小布局。
func (search *dcpapExactSearch) consider() error {
	candidate := dcpapCanonicalBins(search.bins)
	score, err := dcpapScore(candidate, search.target)
	if err != nil {
		return err
	}
	if score.less(search.best) {
		search.best, search.bestBins = score, cloneDCPAPBins(candidate)
	}
	return nil
}

// apply 将 workload 原地加入候选箱。
func (search *dcpapExactSearch) apply(index int, item dcpapItem) {
	search.bins[index].items = append(search.bins[index].items, item)
	search.bins[index].bodyMS += item.durationMS
}

// undo 撤销最近一次 workload 放置。
func (search *dcpapExactSearch) undo(index int, item dcpapItem) {
	search.bins[index].bodyMS -= item.durationMS
	search.bins[index].items = search.bins[index].items[:len(search.bins[index].items)-1]
}

// dcpapProvenPack 对小输入执行 exact；大输入返回有界启发式并保留 gap 证据。
func dcpapProvenPack(items []dcpapItem, target int64) ([]dcpapBin, error) {
	if err := validateDCPAPDurationArithmetic(items, target); err != nil {
		return nil, err
	}
	budget := newDeterministicPackingSearchBudget()
	if len(items) <= cicontract.WorkloadPlanningExactPackableUnitThreshold {
		return dcpapExactPack(items, target, &budget)
	}
	result, err := dcpapHeuristicPack(items, target)
	if err != nil {
		return nil, err
	}
	return result.bins, nil
}
