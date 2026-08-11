package gate

import (
	"errors"
	"slices"
	"sort"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// dcpapHeuristicResult 保留启发式证据，调用方不得把它当成全局最优证明。
type dcpapHeuristicResult struct {
	bins             []dcpapBin
	lowerBoundShards int
	plannedShards    int
	shardGap         int
	objectiveMode    string
}

// dcpapHeuristicPack 对大规模输入执行 setup-aware BFD 和有界修复。
// 当前 dcpapItem 没有额外 setup 字段，因此稳定 workload ID 是 setup tie-breaker。
func dcpapHeuristicPack(items []dcpapItem, target int64) (dcpapHeuristicResult, error) {
	if err := validateDCPAPDurationArithmetic(items, target); err != nil {
		return dcpapHeuristicResult{}, err
	}
	lower, err := dcpapShardCountLowerBound(items, target)
	if err != nil {
		return dcpapHeuristicResult{}, err
	}
	regular := make([]dcpapItem, 0, len(items))
	atomic := make([]dcpapItem, 0)
	for _, item := range items {
		if item.durationMS >= target {
			atomic = append(atomic, item)
		} else {
			regular = append(regular, item)
		}
	}
	best := dcpapBestFitPack(regular, target)
	best, err = dcpapRepairBounded(best, target)
	if err != nil {
		return dcpapHeuristicResult{}, err
	}
	for _, item := range atomic {
		best = append(best, dcpapBin{items: []dcpapItem{item}, bodyMS: item.durationMS})
	}
	best = dcpapCanonicalBins(best)
	if err := dcpapValidateHeuristicBins(best, items, target); err != nil {
		return dcpapHeuristicResult{}, err
	}
	return dcpapHeuristicResult{
		bins:             best,
		lowerBoundShards: lower,
		plannedShards:    len(best),
		shardGap:         len(best) - lower,
		objectiveMode:    cicontract.WorkloadPlanningHeuristicSolverModeID,
	}, nil
}

// dcpapRepairBounded 依次尝试 2-move、3-cycle 和 beam，严格接受 score 改善。
func dcpapRepairBounded(initial []dcpapBin, target int64) ([]dcpapBin, error) {
	best := dcpapCanonicalBins(initial)
	if repaired, ok, err := dcpapBeamCompletion(best, target); err != nil {
		return nil, err
	} else if ok {
		return repaired, nil
	}
	for range cicontract.WorkloadPlanningHeuristicBeamDepth {
		changed, next, err := dcpapRepairTwoMove(best, target)
		if err != nil {
			return nil, err
		}
		if changed {
			best = next
			continue
		}
		changed, next, err = dcpapRepairThreeCycle(best, target)
		if err != nil {
			return nil, err
		}
		if changed {
			best = next
			continue
		}
		changed, next, err = dcpapRepairBeam(best, target)
		if err != nil {
			return nil, err
		}
		if !changed {
			break
		}
		best = next
	}
	return dcpapCanonicalBins(best), nil
}

// dcpapBeamCompletion 为极小候选提供受 transition 预算约束的正见证，不输出最优声明。
func dcpapBeamCompletion(current []dcpapBin, target int64) ([]dcpapBin, bool, error) {
	items := dcpapBeamItems(current)
	if len(items) == 0 || len(items) > cicontract.WorkloadPlanningHeuristicMaxBeamTransitions/4 {
		return current, false, nil
	}
	sortDCPAPItems(items)
	base, err := dcpapScore(current, target)
	if err != nil {
		return nil, false, err
	}
	var bins [2]dcpapBin
	transitions := 0
	if candidate, ok := dcpapBeamCompletionVisit(items, &bins, 0, target, base, &transitions); ok {
		return candidate, true, nil
	}
	return current, false, nil
}

// dcpapBeamItems 以稳定顺序收集 beam completion 的 workload。
func dcpapBeamItems(current []dcpapBin) []dcpapItem {
	items := make([]dcpapItem, 0)
	for _, bin := range current {
		items = append(items, bin.items...)
	}
	return items
}

// dcpapBeamCompletionVisit 递归枚举两个候选箱，并在叶子处比较完整 score。
func dcpapBeamCompletionVisit(items []dcpapItem, bins *[2]dcpapBin, index int, target int64, base dcpapPackingScore, transitions *int) ([]dcpapBin, bool) {
	if index == len(items) {
		if len(bins[0].items) == 0 || len(bins[1].items) == 0 {
			return nil, false
		}
		candidate := dcpapCanonicalBins(bins[:])
		score, err := dcpapScore(candidate, target)
		return candidate, err == nil && score.less(base)
	}
	item := items[index]
	for destination := range bins[:] {
		if *transitions >= cicontract.WorkloadPlanningHeuristicMaxBeamTransitions {
			return nil, false
		}
		*transitions++
		if !dcpapCanPlace(bins[destination], item, target) {
			continue
		}
		bins[destination].items = append(bins[destination].items, item)
		bins[destination].bodyMS += item.durationMS
		if candidate, ok := dcpapBeamCompletionVisit(items, bins, index+1, target, base, transitions); ok {
			return candidate, true
		}
		bins[destination].bodyMS -= item.durationMS
		bins[destination].items = bins[destination].items[:len(bins[destination].items)-1]
	}
	return nil, false
}

// dcpapRepairTwoMove 按固定顺序搜索双 item 移动与跨箱交换邻域。
func dcpapRepairTwoMove(current []dcpapBin, target int64) (bool, []dcpapBin, error) {
	base, err := dcpapScore(current, target)
	if err != nil {
		return false, nil, err
	}
	checked := 0
	for source := range current {
		if candidate, ok, err := dcpapTryPairSource(current, source, base, target, &checked); err != nil || ok {
			return ok, candidate, err
		}
		if checked >= cicontract.WorkloadPlanningHeuristicMaxTwoMoveTransitions {
			break
		}
	}
	for source := range len(current) {
		if candidate, ok, err := dcpapTrySwapSource(current, source, base, target, &checked); err != nil || ok {
			return ok, candidate, err
		}
		if checked >= cicontract.WorkloadPlanningHeuristicMaxTwoMoveTransitions {
			break
		}
	}
	return false, current, nil
}

// dcpapTryPairSource 枚举一个源箱的双 item 移动候选。
func dcpapTryPairSource(current []dcpapBin, source int, base dcpapPackingScore, target int64, checked *int) ([]dcpapBin, bool, error) {
	for first := range current[source].items {
		for second := first + 1; second < len(current[source].items); second++ {
			for destination := range current {
				if *checked >= cicontract.WorkloadPlanningHeuristicMaxTwoMoveTransitions {
					return nil, false, nil
				}
				if source == destination {
					continue
				}
				(*checked)++
				candidate, ok := dcpapMoveTwoItems(current, source, first, second, destination, target)
				if !ok {
					continue
				}
				candidate = dcpapCanonicalBins(candidate)
				score, err := dcpapScore(candidate, target)
				if err != nil {
					return nil, false, err
				}
				if score.less(base) {
					return candidate, true, nil
				}
			}
		}
	}
	return nil, false, nil
}

// dcpapTrySwapSource 枚举一个源箱与后续箱的单 item 交换候选。
func dcpapTrySwapSource(current []dcpapBin, source int, base dcpapPackingScore, target int64, checked *int) ([]dcpapBin, bool, error) {
	for destination := source + 1; destination < len(current); destination++ {
		for first := range current[source].items {
			for second := range current[destination].items {
				if *checked >= cicontract.WorkloadPlanningHeuristicMaxTwoMoveTransitions {
					return nil, false, nil
				}
				(*checked)++
				candidate, ok := dcpapSwapItems(current, source, first, destination, second, target)
				if !ok {
					continue
				}
				candidate = dcpapCanonicalBins(candidate)
				score, err := dcpapScore(candidate, target)
				if err != nil {
					return nil, false, err
				}
				if score.less(base) {
					return candidate, true, nil
				}
			}
		}
	}
	return nil, false, nil
}

// dcpapRepairThreeCycle 在固定上限内循环三个箱的首项。
func dcpapRepairThreeCycle(current []dcpapBin, target int64) (bool, []dcpapBin, error) {
	base, err := dcpapScore(current, target)
	if err != nil {
		return false, nil, err
	}
	checked := 0
	for source := range current {
		changed, candidate, stopped, splitErr := dcpapTryTripleSplitSource(current, source, base, target, &checked)
		if splitErr != nil || changed {
			return changed, candidate, splitErr
		}
		if stopped {
			break
		}
	}
	changed, candidate, err := dcpapTryRotationCycles(current, base, target, &checked)
	if err != nil || changed {
		return changed, candidate, err
	}
	return false, current, nil
}

// dcpapTryTripleSplitSource 枚举一个源箱的三项组合。
func dcpapTryTripleSplitSource(current []dcpapBin, source int, base dcpapPackingScore, target int64, checked *int) (bool, []dcpapBin, bool, error) {
	if len(current[source].items) < 3 {
		return false, nil, false, nil
	}
	for first := 0; first < len(current[source].items); first++ {
		for second := first + 1; second < len(current[source].items); second++ {
			for third := second + 1; third < len(current[source].items); third++ {
				changed, candidate, stopped, err := dcpapTryTripleSplitDestinations(current, source, first, second, third, base, target, checked)
				if err != nil || changed || stopped {
					return changed, candidate, stopped, err
				}
			}
		}
	}
	return false, nil, false, nil
}

// dcpapTryTripleSplitDestinations 枚举两个目标箱，并保留源循环顺序。
func dcpapTryTripleSplitDestinations(current []dcpapBin, source, first, second, third int, base dcpapPackingScore, target int64, checked *int) (bool, []dcpapBin, bool, error) {
	for left := range current {
		for right := range current {
			if left == source || right == source || left == right {
				continue
			}
			if *checked >= cicontract.WorkloadPlanningHeuristicMaxThreeCycleTransitions {
				return false, nil, true, nil
			}
			(*checked)++
			candidate, ok := dcpapMoveTripleSplit(current, source, first, second, third, left, right, target)
			if !ok {
				continue
			}
			candidate = dcpapCanonicalBins(candidate)
			candidateScore, err := dcpapScore(candidate, target)
			if err != nil {
				return false, nil, false, err
			}
			if candidateScore.less(base) {
				return true, candidate, false, nil
			}
		}
	}
	return false, nil, false, nil
}

// dcpapTryRotationCycles 枚举三个非空箱的循环移动。
func dcpapTryRotationCycles(current []dcpapBin, base dcpapPackingScore, target int64, checked *int) (bool, []dcpapBin, error) {
	for first := range current {
		if len(current[first].items) == 0 {
			continue
		}
		changed, candidate, stopped, err := dcpapTryRotationSecondBins(current, first, base, target, checked)
		if err != nil || changed {
			return changed, candidate, err
		}
		if stopped {
			return false, nil, nil
		}
	}
	return false, nil, nil
}

// dcpapTryRotationSecondBins 固定首箱并检查后续箱组合。
func dcpapTryRotationSecondBins(current []dcpapBin, first int, base dcpapPackingScore, target int64, checked *int) (bool, []dcpapBin, bool, error) {
	for second := first + 1; second < len(current); second++ {
		if len(current[second].items) == 0 {
			continue
		}
		changed, candidate, stopped, err := dcpapTryRotationThirdBins(current, first, second, base, target, checked)
		if err != nil || changed {
			return changed, candidate, stopped, err
		}
		if stopped {
			return false, nil, true, nil
		}
	}
	return false, nil, *checked >= cicontract.WorkloadPlanningHeuristicMaxThreeCycleTransitions, nil
}

// dcpapTryRotationThirdBins 固定前两箱并检查第三箱及其 item 组合。
func dcpapTryRotationThirdBins(current []dcpapBin, first, second int, base dcpapPackingScore, target int64, checked *int) (bool, []dcpapBin, bool, error) {
	for third := second + 1; third < len(current); third++ {
		if len(current[third].items) == 0 || *checked >= cicontract.WorkloadPlanningHeuristicMaxThreeCycleTransitions {
			break
		}
		changed, candidate, stopped, err := dcpapTryRotationItems(current, first, second, third, base, target, checked)
		if err != nil || changed {
			return changed, candidate, stopped, err
		}
		if stopped {
			return false, nil, true, nil
		}
	}
	return false, nil, *checked >= cicontract.WorkloadPlanningHeuristicMaxThreeCycleTransitions, nil
}

// dcpapTryRotationItems 检查一个箱三元组的全部 item 组合。
func dcpapTryRotationItems(current []dcpapBin, first, second, third int, base dcpapPackingScore, target int64, checked *int) (bool, []dcpapBin, bool, error) {
	for firstIndex := range current[first].items {
		for secondIndex := range current[second].items {
			for thirdIndex := range current[third].items {
				if *checked >= cicontract.WorkloadPlanningHeuristicMaxThreeCycleTransitions {
					return false, nil, true, nil
				}
				(*checked)++
				candidate, ok := dcpapRotateItems(current, first, firstIndex, second, secondIndex, third, thirdIndex, target)
				if !ok {
					continue
				}
				score, err := dcpapScore(candidate, target)
				if err != nil {
					return false, nil, false, err
				}
				candidate = dcpapCanonicalBins(candidate)
				score, err = dcpapScore(candidate, target)
				if err != nil {
					return false, nil, false, err
				}
				if score.less(base) {
					return true, candidate, false, nil
				}
			}
		}
	}
	return false, nil, false, nil
}

// dcpapMoveTripleSplit 将一个箱的三个 item 按固定顺序分配到两个目标箱。
func dcpapMoveTripleSplit(current []dcpapBin, source, first, second, third, left, right int, target int64) ([]dcpapBin, bool) {
	if !dcpapTripleSplitIndicesValid(current, source, first, second, third, left, right) {
		return nil, false
	}
	candidate := cloneDCPAPBins(current)
	items := dcpapExtractTriple(&candidate[source], first, second, third)
	if !dcpapPlaceTriple(&candidate, left, right, items, target) {
		return nil, false
	}
	return candidate, true
}

// dcpapTripleSplitIndicesValid 检查三项拆分的箱与索引，避免主循环深嵌套。
func dcpapTripleSplitIndicesValid(current []dcpapBin, source, first, second, third, left, right int) bool {
	if !dcpapTripleSplitBinsValid(len(current), source, left, right) {
		return false
	}
	if !dcpapTripleSplitItemIndicesValid(current[source].items, first, second, third) {
		return false
	}
	return true
}

// dcpapTripleSplitBinsValid 验证拆分涉及的三个箱索引互不相同且都存在。
func dcpapTripleSplitBinsValid(binCount, source, left, right int) bool {
	if !dcpapHeuristicIndexValid(source, binCount) || !dcpapHeuristicIndexValid(left, binCount) || !dcpapHeuristicIndexValid(right, binCount) {
		return false
	}
	return source != left && source != right && left != right
}

// dcpapTripleSplitItemIndicesValid 验证源箱三项索引互不相同且都存在。
func dcpapTripleSplitItemIndicesValid(items []dcpapItem, first, second, third int) bool {
	if !dcpapHeuristicIndexValid(first, len(items)) || !dcpapHeuristicIndexValid(second, len(items)) || !dcpapHeuristicIndexValid(third, len(items)) {
		return false
	}
	return first != second && first != third && second != third
}

// dcpapHeuristicIndexValid 判断一个整数是否位于半开索引区间。
func dcpapHeuristicIndexValid(index, length int) bool {
	return index >= 0 && index < length
}

// dcpapExtractTriple 稳定移除源箱三项并返回原始顺序。
func dcpapExtractTriple(source *dcpapBin, first, second, third int) []dcpapItem {
	indices := []int{first, second, third}
	items := []dcpapItem{source.items[first], source.items[second], source.items[third]}
	for index := range slices.Backward(indices) {
		itemIndex := indices[index]
		source.items = append(source.items[:itemIndex], source.items[itemIndex+1:]...)
		for prior := range indices[:index] {
			if indices[prior] > itemIndex {
				indices[prior]--
			}
		}
		source.bodyMS -= items[index].durationMS
	}
	return items
}

// dcpapPlaceTriple 按既定 right,left,left 顺序尝试三项放置。
func dcpapPlaceTriple(bins *[]dcpapBin, left, right int, items []dcpapItem, target int64) bool {
	placements := [...]struct {
		bin  int
		item dcpapItem
	}{{right, items[0]}, {left, items[1]}, {left, items[2]}}
	for _, placement := range placements {
		if !dcpapCanPlace((*bins)[placement.bin], placement.item, target) {
			return false
		}
		(*bins)[placement.bin].items = append((*bins)[placement.bin].items, placement.item)
		(*bins)[placement.bin].bodyMS += placement.item.durationMS
	}
	return true
}

type dcpapBeamCandidate struct {
	bins  []dcpapBin
	score dcpapPackingScore
}

// dcpapRepairBeam 在固定深度、宽度与 transition 上限内保留严格改善候选。
func dcpapRepairBeam(current []dcpapBin, target int64) (bool, []dcpapBin, error) {
	base, err := dcpapScore(current, target)
	if err != nil {
		return false, nil, err
	}
	initial := dcpapCanonicalBins(current)
	frontier := []dcpapBeamCandidate{{bins: initial, score: base}}
	best := dcpapBeamCandidate{bins: initial, score: base}
	transitions := 0
	for range cicontract.WorkloadPlanningHeuristicBeamDepth {
		next, stopped, expandErr := dcpapBeamExpand(frontier, &best, target, &transitions)
		if expandErr != nil {
			return false, nil, expandErr
		}
		if stopped {
			break
		}
		dcpapSortBeamCandidates(next)
		if len(next) > cicontract.WorkloadPlanningHeuristicBeamWidth {
			next = next[:cicontract.WorkloadPlanningHeuristicBeamWidth]
		}
		if len(next) == 0 {
			break
		}
		frontier = next
	}
	if best.score.less(base) {
		return true, best.bins, nil
	}
	return false, current, nil
}

func dcpapBeamExpand(frontier []dcpapBeamCandidate, best *dcpapBeamCandidate, target int64, transitions *int) ([]dcpapBeamCandidate, bool, error) {
	next := make([]dcpapBeamCandidate, 0, cicontract.WorkloadPlanningHeuristicBeamWidth)
	for _, state := range frontier {
		expanded, stopped, err := dcpapBeamExpandState(state, best, target, transitions)
		if err != nil || stopped {
			return nil, stopped, err
		}
		next = append(next, expanded...)
	}
	return next, false, nil
}

func dcpapBeamExpandState(state dcpapBeamCandidate, best *dcpapBeamCandidate, target int64, transitions *int) ([]dcpapBeamCandidate, bool, error) {
	next := make([]dcpapBeamCandidate, 0)
	for source := range state.bins {
		var stopped bool
		var err error
		next, stopped, err = dcpapBeamExpandSource(next, state, source, target, best, transitions)
		if err != nil || stopped {
			return nil, stopped, err
		}
	}
	return next, false, nil
}

// dcpapBeamExpandSource 按既定顺序展开一个源箱的全部移动类别。
func dcpapBeamExpandSource(next []dcpapBeamCandidate, state dcpapBeamCandidate, source int, target int64, best *dcpapBeamCandidate, transitions *int) ([]dcpapBeamCandidate, bool, error) {
	var stopped bool
	var err error
	next, stopped, err = dcpapBeamExpandPairMoves(next, state, source, target, best, transitions)
	if err != nil || stopped {
		return nil, stopped, err
	}
	next, stopped, err = dcpapBeamExpandItemMoves(next, state, source, target, best, transitions)
	if err != nil || stopped {
		return nil, stopped, err
	}
	next, stopped, err = dcpapBeamExpandRotations(next, state, source, target, best, transitions)
	if err != nil || stopped {
		return nil, stopped, err
	}
	return dcpapBeamExpandSwaps(next, state, source, target, best, transitions)
}

// dcpapBeamExpandPairMoves 在一个源箱内尝试两项移动。
func dcpapBeamExpandPairMoves(next []dcpapBeamCandidate, state dcpapBeamCandidate, source int, target int64, best *dcpapBeamCandidate, transitions *int) ([]dcpapBeamCandidate, bool, error) {
	for first := 0; first < len(state.bins[source].items); first++ {
		for second := first + 1; second < len(state.bins[source].items); second++ {
			for destination := range state.bins {
				if source == destination {
					continue
				}
				if !dcpapBeamTakeTransition(transitions) {
					return nil, true, nil
				}
				candidate, ok := dcpapMoveTwoItems(state.bins, source, first, second, destination, target)
				var err error
				next, err = dcpapBeamRecord(next, best, candidate, ok, target)
				if err != nil {
					return nil, false, err
				}
			}
		}
	}
	return next, false, nil
}

// dcpapBeamExpandItemMoves 在一个源箱内尝试单项移动。
func dcpapBeamExpandItemMoves(next []dcpapBeamCandidate, state dcpapBeamCandidate, source int, target int64, best *dcpapBeamCandidate, transitions *int) ([]dcpapBeamCandidate, bool, error) {
	for item := range state.bins[source].items {
		for destination := range state.bins {
			if source == destination {
				continue
			}
			if !dcpapBeamTakeTransition(transitions) {
				return nil, true, nil
			}
			candidate, ok := dcpapMoveItem(state.bins, source, item, destination, target)
			var err error
			next, err = dcpapBeamRecord(next, best, candidate, ok, target)
			if err != nil {
				return nil, false, err
			}
		}
	}
	return next, false, nil
}

// dcpapBeamExpandRotations 枚举源箱到后续两个箱的三项旋转。
func dcpapBeamExpandRotations(next []dcpapBeamCandidate, state dcpapBeamCandidate, source int, target int64, best *dcpapBeamCandidate, transitions *int) ([]dcpapBeamCandidate, bool, error) {
	for first := range state.bins[source].items {
		for second := source + 1; second < len(state.bins); second++ {
			for third := second + 1; third < len(state.bins); third++ {
				var stopped bool
				var err error
				next, stopped, err = dcpapBeamExpandRotationItems(next, state, source, first, second, third, target, best, transitions)
				if err != nil || stopped {
					return nil, stopped, err
				}
			}
		}
	}
	return next, false, nil
}

// dcpapBeamExpandRotationItems 尝试一个箱三元组的 item 旋转候选。
func dcpapBeamExpandRotationItems(next []dcpapBeamCandidate, state dcpapBeamCandidate, source, first, second, third int, target int64, best *dcpapBeamCandidate, transitions *int) ([]dcpapBeamCandidate, bool, error) {
	for secondItem := range state.bins[second].items {
		for thirdItem := range state.bins[third].items {
			if !dcpapBeamTakeTransition(transitions) {
				return nil, true, nil
			}
			candidate, ok := dcpapRotateItems(state.bins, source, first, second, secondItem, third, thirdItem, target)
			var err error
			next, err = dcpapBeamRecord(next, best, candidate, ok, target)
			if err != nil {
				return nil, false, err
			}
		}
	}
	return next, false, nil
}

// dcpapBeamExpandSwaps 枚举源箱与后续箱的单项交换。
func dcpapBeamExpandSwaps(next []dcpapBeamCandidate, state dcpapBeamCandidate, source int, target int64, best *dcpapBeamCandidate, transitions *int) ([]dcpapBeamCandidate, bool, error) {
	for item := range state.bins[source].items {
		for destination := source + 1; destination < len(state.bins); destination++ {
			for destinationItem := range state.bins[destination].items {
				if !dcpapBeamTakeTransition(transitions) {
					return nil, true, nil
				}
				candidate, ok := dcpapSwapItems(state.bins, source, item, destination, destinationItem, target)
				var err error
				next, err = dcpapBeamRecord(next, best, candidate, ok, target)
				if err != nil {
					return nil, false, err
				}
			}
		}
	}
	return next, false, nil
}

func dcpapBeamTakeTransition(transitions *int) bool {
	if *transitions >= cicontract.WorkloadPlanningHeuristicMaxBeamTransitions {
		return false
	}
	(*transitions)++
	return true
}

func dcpapBeamRecord(next []dcpapBeamCandidate, best *dcpapBeamCandidate, candidate []dcpapBin, ok bool, target int64) ([]dcpapBeamCandidate, error) {
	if !ok {
		return next, nil
	}
	candidate = dcpapCanonicalBins(candidate)
	score, err := dcpapScore(candidate, target)
	if err != nil {
		return nil, err
	}
	if score.less(best.score) {
		best.bins = candidate
		best.score = score
	}
	return append(next, dcpapBeamCandidate{bins: candidate, score: score}), nil
}

func dcpapSortBeamCandidates(next []dcpapBeamCandidate) {
	sort.SliceStable(next, func(left, right int) bool {
		if next[left].score.less(next[right].score) {
			return true
		}
		if next[right].score.less(next[left].score) {
			return false
		}
		return next[left].score.layout < next[right].score.layout
	})
}

// dcpapMoveItem 返回移动单项后的合法候选；所有修复均拒绝空箱及超容量常规箱。
func dcpapMoveItem(current []dcpapBin, source, itemIndex, destination int, target int64) ([]dcpapBin, bool) {
	if source < 0 || source >= len(current) || destination < 0 || destination >= len(current) || source == destination || itemIndex < 0 || itemIndex >= len(current[source].items) {
		return nil, false
	}
	candidate := cloneDCPAPBins(current)
	item := candidate[source].items[itemIndex]
	candidate[source].items = append(candidate[source].items[:itemIndex], candidate[source].items[itemIndex+1:]...)
	candidate[source].bodyMS -= item.durationMS
	if !dcpapCanPlace(candidate[destination], item, target) {
		return nil, false
	}
	candidate[destination].items = append(candidate[destination].items, item)
	candidate[destination].bodyMS += item.durationMS
	return candidate, true
}

// dcpapSwapItems 生成两个箱交换一个 item 的固定顺序候选。
func dcpapSwapItems(current []dcpapBin, source, sourceItem, destination, destinationItem int, target int64) ([]dcpapBin, bool) {
	if !dcpapSwapIndicesValid(current, source, sourceItem, destination, destinationItem) {
		return nil, false
	}
	candidate := cloneDCPAPBins(current)
	left, right := candidate[source].items[sourceItem], candidate[destination].items[destinationItem]
	candidate[source].items = append(candidate[source].items[:sourceItem], candidate[source].items[sourceItem+1:]...)
	candidate[destination].items = append(candidate[destination].items[:destinationItem], candidate[destination].items[destinationItem+1:]...)
	candidate[source].bodyMS -= left.durationMS
	candidate[destination].bodyMS -= right.durationMS
	if !dcpapSwapFits(candidate, source, destination, right, left, target) {
		return nil, false
	}
	dcpapAppendSwap(&candidate, source, destination, right, left)
	return candidate, true
}

// dcpapSwapIndicesValid 验证交换双方箱与 item 索引。
func dcpapSwapIndicesValid(current []dcpapBin, source, sourceItem, destination, destinationItem int) bool {
	if !dcpapHeuristicIndexValid(source, len(current)) || !dcpapHeuristicIndexValid(destination, len(current)) {
		return false
	}
	if source == destination {
		return false
	}
	return dcpapHeuristicIndexValid(sourceItem, len(current[source].items)) &&
		dcpapHeuristicIndexValid(destinationItem, len(current[destination].items))
}

// dcpapSwapFits 判断交换后的两个箱仍满足容量约束。
func dcpapSwapFits(candidate []dcpapBin, source, destination int, sourceItem, destinationItem dcpapItem, target int64) bool {
	return dcpapCanPlace(candidate[source], sourceItem, target) &&
		dcpapCanPlace(candidate[destination], destinationItem, target)
}

// dcpapAppendSwap 按 source、destination 的固定顺序完成交换写入。
func dcpapAppendSwap(candidate *[]dcpapBin, source, destination int, sourceItem, destinationItem dcpapItem) {
	(*candidate)[source].items = append((*candidate)[source].items, sourceItem)
	(*candidate)[destination].items = append((*candidate)[destination].items, destinationItem)
	(*candidate)[source].bodyMS += sourceItem.durationMS
	(*candidate)[destination].bodyMS += destinationItem.durationMS
}

// dcpapMoveTwoItems 将同一源箱的两个 item 一次移动到目标箱，可减少 BFD 分片数。
func dcpapMoveTwoItems(current []dcpapBin, source, first, second, destination int, target int64) ([]dcpapBin, bool) {
	if !dcpapMoveTwoIndicesValid(current, source, first, second, destination) {
		return nil, false
	}
	candidate := cloneDCPAPBins(current)
	items := dcpapExtractTwo(&candidate[source], first, second)
	if !dcpapPlaceItems(&candidate, destination, items, target) {
		return nil, false
	}
	return candidate, true
}

// dcpapMoveTwoIndicesValid 验证双项移动的箱和有序 item 索引。
func dcpapMoveTwoIndicesValid(current []dcpapBin, source, first, second, destination int) bool {
	if !dcpapHeuristicIndexValid(source, len(current)) || !dcpapHeuristicIndexValid(destination, len(current)) {
		return false
	}
	if source == destination || first < 0 || second <= first {
		return false
	}
	return second < len(current[source].items)
}

// dcpapExtractTwo 按原双项移动顺序从源箱移除两个 item。
func dcpapExtractTwo(source *dcpapBin, first, second int) []dcpapItem {
	items := []dcpapItem{source.items[first], source.items[second]}
	source.items = append(source.items[:first], source.items[first+1:]...)
	second--
	source.items = append(source.items[:second], source.items[second+1:]...)
	source.bodyMS -= items[0].durationMS + items[1].durationMS
	return items
}

// dcpapPlaceItems 按输入顺序尝试把多个 item 放入同一目标箱。
func dcpapPlaceItems(bins *[]dcpapBin, destination int, items []dcpapItem, target int64) bool {
	for _, item := range items {
		if !dcpapCanPlace((*bins)[destination], item, target) {
			return false
		}
		(*bins)[destination].items = append((*bins)[destination].items, item)
		(*bins)[destination].bodyMS += item.durationMS
	}
	return true
}

// dcpapRotateItems 以固定顺序将三个箱的首项循环移动。
func dcpapRotateItems(current []dcpapBin, first, firstIndex, second, secondIndex, third, thirdIndex int, target int64) ([]dcpapBin, bool) {
	candidate := cloneDCPAPBins(current)
	indices := []int{firstIndex, secondIndex, thirdIndex}
	items := []dcpapItem{candidate[first].items[firstIndex], candidate[second].items[secondIndex], candidate[third].items[thirdIndex]}
	for index, binIndex := range []int{first, second, third} {
		itemIndex := indices[index]
		candidate[binIndex].items = append(candidate[binIndex].items[:itemIndex], candidate[binIndex].items[itemIndex+1:]...)
		candidate[binIndex].bodyMS -= items[index].durationMS
	}
	for index := range []int{first, second, third} {
		destination := []int{second, third, first}[index]
		if !dcpapCanPlace(candidate[destination], items[index], target) {
			return nil, false
		}
		candidate[destination].items = append(candidate[destination].items, items[index])
		candidate[destination].bodyMS += items[index].durationMS
	}
	return candidate, true
}

// dcpapValidateHeuristicBins 校验容量、非空和输入 workload 的一一覆盖。
func dcpapValidateHeuristicBins(bins []dcpapBin, items []dcpapItem, target int64) error {
	if len(bins) == 0 {
		return errors.New("D-CPAP heuristic produced no shards")
	}
	want := dcpapHeuristicItemCounts(items)
	got := make(map[string]int, len(items))
	for _, bin := range bins {
		if err := dcpapValidateHeuristicBin(bin, target, got); err != nil {
			return err
		}
	}
	return dcpapValidateHeuristicCoverage(want, got)
}

// dcpapHeuristicItemCounts 统计 workload ID，保留重复 ID 的多重集语义。
func dcpapHeuristicItemCounts(items []dcpapItem) map[string]int {
	counts := make(map[string]int, len(items))
	for _, item := range items {
		counts[item.id]++
	}
	return counts
}

// dcpapValidateHeuristicBin 校验单个箱的非空、时长和 over-target 规则。
func dcpapValidateHeuristicBin(bin dcpapBin, target int64, got map[string]int) error {
	if len(bin.items) == 0 {
		return errors.New("D-CPAP heuristic produced an empty shard")
	}
	for _, item := range bin.items {
		got[item.id]++
	}
	sum, err := dcpapHeuristicBinDuration(bin.items)
	if err != nil {
		return err
	}
	if sum != bin.bodyMS {
		return errors.New("DCPAP heuristic shard duration mismatch")
	}
	if bin.bodyMS <= target {
		return nil
	}
	if len(bin.items) != 1 || bin.items[0].durationMS <= target {
		return errors.New("D-CPAP heuristic produced an invalid over-target shard")
	}
	return nil
}

// dcpapHeuristicBinDuration checked-sums one bin's item durations。
func dcpapHeuristicBinDuration(items []dcpapItem) (int64, error) {
	var sum int64
	for _, item := range items {
		if sum > maxDCPAPInt64-item.durationMS {
			return 0, errors.New("DCPAP heuristic shard duration overflow")
		}
		sum += item.durationMS
	}
	return sum, nil
}

// dcpapValidateHeuristicCoverage 比较输入与输出 workload ID 的完整多重集。
func dcpapValidateHeuristicCoverage(want, got map[string]int) error {
	if len(got) != len(want) {
		return errors.New("D-CPAP heuristic changed workload coverage")
	}
	for id, count := range want {
		if got[id] != count {
			return errors.New("D-CPAP heuristic duplicated or dropped workload")
		}
	}
	return nil
}
