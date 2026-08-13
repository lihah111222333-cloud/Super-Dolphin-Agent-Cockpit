package gate

import (
	"errors"
	"maps"
	"sort"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// heuristicCompileUnitPacking 对 >12 个 packable unit 按 domain 使用受约束 BFD 与修复。
func heuristicCompileUnitPacking(units []compilePlanningUnit, target int64) ([]ShardPlan, error) {
	ordinary, serial, isolated := partitionCompilePlanningUnits(units)
	if len(ordinary)+len(serial) <= cicontract.WorkloadPlanningExactPackableUnitThreshold {
		return nil, errors.New("compile heuristic requires more than twelve packable units")
	}
	lower, err := compilePackingShardLowerBound(units, target)
	if err != nil {
		return nil, err
	}
	ordinaryShards, err := heuristicCompilePackingDomain(ordinary, target)
	if err != nil {
		return nil, err
	}
	serialShards, err := heuristicCompilePackingDomain(serial, target)
	if err != nil {
		return nil, err
	}
	best := append(ordinaryShards, serialShards...)
	best = append(best, isolatedCompileShards(isolated)...)
	best = canonicalCompilePacking(best)
	if !compileShardsMeetTarget(best, target, units) {
		return nil, errors.New("compile heuristic produced an invalid target violation")
	}
	if len(best) < lower {
		return nil, errors.New("compile heuristic shard count is below lower bound")
	}
	return best, nil
}

// heuristicCompilePackingDomain 在一个可共箱 domain 内执行 BFD 与有限修复。
func heuristicCompilePackingDomain(units []compilePlanningUnit, target int64) ([]ShardPlan, error) {
	if len(units) == 0 {
		return nil, nil
	}
	upper, err := compileVariableBinBFD(units, target)
	if err != nil {
		return nil, err
	}
	return repairCompilePackingBounded(units, upper, target)
}

// partitionCompilePlanningUnits 将 ordinary、可串行 compile group 与硬隔离 unit 分域。
// ordinary workload 永远不能与 compile-group shard 共享 BFD、精确搜索或 repair 输入。
func partitionCompilePlanningUnits(units []compilePlanningUnit) ([]compilePlanningUnit, []compilePlanningUnit, []compilePlanningUnit) {
	ordinary := make([]compilePlanningUnit, 0, len(units))
	serial := make([]compilePlanningUnit, 0, len(units))
	isolated := make([]compilePlanningUnit, 0, len(units))
	for _, unit := range units {
		if unit.group == nil {
			ordinary = append(ordinary, unit)
		} else if CompileGroupSerialPackingEligible(*unit.group) {
			serial = append(serial, unit)
		} else {
			isolated = append(isolated, unit)
		}
	}
	return ordinary, serial, isolated
}

// compileVariableBinBFD 逐 unit 在所有合法现有箱中选择最佳 projected score，否则开新箱。
// unit.costMS 已由 CompileGroupCriticalDurationMS 固化 compile-once 加各 wave 最大
// body 成本；unit 不可拆且每组仅出现一次，因此合法布局不再产生额外 setup proxy。
func compileVariableBinBFD(units []compilePlanningUnit, target int64) ([]ShardPlan, error) {
	ordered := append([]compilePlanningUnit(nil), units...)
	sortCompilePlanningUnits(ordered)
	bins := make([]compilePackingBin, 0, len(ordered))
	for _, unit := range ordered {
		bestIndex, err := compileSelectBFDIndex(bins, unit, target)
		if err != nil {
			return nil, err
		}
		if bestIndex < 0 {
			bins = append(bins, compilePackingBin{shard: ShardPlan{Index: len(bins)}, affinities: make(map[string]struct{}), artifactKeys: make(map[string]struct{})})
			bestIndex = len(bins) - 1
		}
		if _, placed, err := applyCompilePackingUnit(&bins[bestIndex], unit, target); err != nil {
			return nil, err
		} else if !placed {
			return nil, errors.New("compile BFD could not place unit")
		}
	}
	shards, ok, err := completedCompilePacking(bins)
	if err != nil || !ok {
		return nil, errors.New("compile BFD produced an empty shard")
	}
	return canonicalCompilePacking(shards), nil
}

// compileSelectBFDIndex 选择 projected excess、剩余容量和 canonical key 最优的箱。
func compileSelectBFDIndex(bins []compilePackingBin, unit compilePlanningUnit, target int64) (int, error) {
	bestIndex := -1
	bestExcess, bestResidual, bestKey := maxCompilePackingDuration, maxCompilePackingDuration, ""
	for index := range bins {
		excess, residual, key, ok, err := compileBFDPlacementCandidate(bins[index], unit, target)
		if err != nil {
			return -1, err
		}
		if !ok {
			continue
		}
		if bestIndex < 0 || excess < bestExcess || (excess == bestExcess && (residual < bestResidual || (residual == bestResidual && key < bestKey))) {
			bestIndex, bestExcess, bestResidual, bestKey = index, excess, residual, key
		}
	}
	return bestIndex, nil
}

// compileBFDPlacementCandidate 复制箱状态并校验一次合法 unit 放置。
func compileBFDPlacementCandidate(bin compilePackingBin, unit compilePlanningUnit, target int64) (int64, int64, string, bool, error) {
	if !compileUnitFitsPackingCapacity(bin.shard, unit, target) || !compileUnitCanShareShard(bin.shard, bin.affinities, bin.artifactKeys, bin.serialEligible, bin.resourceClassID, unit) {
		return 0, 0, "", false, nil
	}
	candidateBin := cloneCompilePackingBin(bin)
	if _, placed, err := applyCompilePackingUnit(&candidateBin, unit, target); err != nil {
		return 0, 0, "", false, err
	} else if !placed {
		return 0, 0, "", false, nil
	}
	excess, residual := compileUnitPlacementScore(bin.shard.EstimatedDurationMS, unit.costMS, target)
	return excess, residual, compilePackingShardKey(candidateBin.shard), true, nil
}

func isolatedCompileShards(units []compilePlanningUnit) []ShardPlan {
	shards := make([]ShardPlan, 0, len(units))
	for _, unit := range units {
		shard := ShardPlan{Workloads: append([]PlannedWorkload(nil), unit.workloads...), EstimatedDurationMS: unit.costMS}
		if unit.group != nil {
			shard.CompileGroupIDs = []string{unit.group.GroupID}
		}
		shards = append(shards, shard)
	}
	return shards
}

// repairCompilePackingBounded 按固定顺序执行 move、swap、cycle 和 beam。
func repairCompilePackingBounded(units []compilePlanningUnit, initial []ShardPlan, target int64) ([]ShardPlan, error) {
	assignments, ok := compilePackingAssignments(units, initial)
	if !ok {
		return nil, errors.New("compile heuristic cannot map initial unit assignment")
	}
	best := canonicalCompilePacking(initial)
	for range cicontract.WorkloadPlanningHeuristicBeamDepth {
		changed, next, err := compileRepairMove(units, assignments, best, target)
		if err != nil {
			return nil, err
		}
		if changed {
			best, assignments = next.shards, next.assignments
			continue
		}
		changed, next, err = compileRepairCycle(units, assignments, best, target)
		if err != nil {
			return nil, err
		}
		if changed {
			best, assignments = next.shards, next.assignments
			continue
		}
		changed, next, err = compileRepairBeam(units, assignments, best, target)
		if err != nil {
			return nil, err
		}
		if !changed {
			break
		}
		best, assignments = next.shards, next.assignments
	}
	return best, nil
}

type compileRepairCandidate struct {
	shards      []ShardPlan
	assignments map[string]int
}

// compileRepairBeamState 保存有限 beam 的候选布局及其评分。
type compileRepairBeamState struct {
	candidate compileRepairCandidate
	score     compilePackingScore
}

// compileRepairMove 在固定 transition 上限内尝试移动或交换 unit。
func compileRepairMove(units []compilePlanningUnit, baseAssignments map[string]int, current []ShardPlan, target int64) (bool, compileRepairCandidate, error) {
	base, err := compilePackingScoreForShards(current, target)
	if err != nil {
		return false, compileRepairCandidate{}, err
	}
	keys := compileUnitKeys(units)
	checked := 0
	if changed, candidate, err := compileRepairPairMoveTransitions(units, baseAssignments, current, target, keys, base, &checked); err != nil || changed {
		return changed, candidate, err
	}
	if changed, candidate, err := compileRepairMoveTransitions(units, baseAssignments, current, target, keys, base, &checked); err != nil || changed {
		return changed, candidate, err
	}
	return compileRepairSwapTransitions(units, baseAssignments, current, target, keys, base, &checked)
}

// compileRepairPairMoveTransitions 尝试将同一源箱的两个 unit 一起迁移到目标箱。
// 这是 2-move 邻域的严格有限部分；候选仍须经过完整 affinity、artifact、资源档
// 和容量重放，任何不合法布局都不会进入评分。
func compileRepairPairMoveTransitions(units []compilePlanningUnit, baseAssignments map[string]int, current []ShardPlan, target int64, keys []string, base compilePackingScore, checked *int) (bool, compileRepairCandidate, error) {
	for left := range len(keys) {
		leftSource := baseAssignments[keys[left]]
		for right := left + 1; right < len(keys); right++ {
			if baseAssignments[keys[right]] != leftSource {
				continue
			}
			for destination := range current {
				if leftSource == destination {
					continue
				}
				if *checked >= cicontract.WorkloadPlanningHeuristicMaxTwoMoveTransitions {
					return false, compileRepairCandidate{}, nil
				}
				(*checked)++
				candidateAssignments := cloneCompileAssignments(baseAssignments)
				candidateAssignments[keys[left]] = destination
				candidateAssignments[keys[right]] = destination
				candidate, improved, err := compileRepairCandidateIfImproved(units, candidateAssignments, len(current), target, base)
				if err != nil {
					return false, compileRepairCandidate{}, err
				}
				if improved {
					return true, candidate, nil
				}
			}
		}
	}
	return false, compileRepairCandidate{}, nil
}

// compileRepairMoveTransitions 尝试单 unit 的有界迁移。
func compileRepairMoveTransitions(units []compilePlanningUnit, baseAssignments map[string]int, current []ShardPlan, target int64, keys []string, base compilePackingScore, checked *int) (bool, compileRepairCandidate, error) {
	for _, key := range keys {
		source := baseAssignments[key]
		for destination := range current {
			if source == destination || *checked >= cicontract.WorkloadPlanningHeuristicMaxTwoMoveTransitions {
				break
			}
			(*checked)++
			candidateAssignments := cloneCompileAssignments(baseAssignments)
			candidateAssignments[key] = destination
			candidate, improved, err := compileRepairCandidateIfImproved(units, candidateAssignments, len(current), target, base)
			if err != nil {
				return false, compileRepairCandidate{}, err
			}
			if improved {
				return true, candidate, nil
			}
		}
	}
	return false, compileRepairCandidate{}, nil
}

// compileRepairSwapTransitions 尝试两个 unit 的有界交换。
func compileRepairSwapTransitions(units []compilePlanningUnit, baseAssignments map[string]int, current []ShardPlan, target int64, keys []string, base compilePackingScore, checked *int) (bool, compileRepairCandidate, error) {
	for left := range len(keys) {
		for right := left + 1; right < len(keys) && *checked < cicontract.WorkloadPlanningHeuristicMaxTwoMoveTransitions; right++ {
			leftKey, rightKey := keys[left], keys[right]
			if baseAssignments[leftKey] == baseAssignments[rightKey] {
				continue
			}
			(*checked)++
			candidateAssignments := cloneCompileAssignments(baseAssignments)
			candidateAssignments[leftKey], candidateAssignments[rightKey] = candidateAssignments[rightKey], candidateAssignments[leftKey]
			candidate, improved, err := compileRepairCandidateIfImproved(units, candidateAssignments, len(current), target, base)
			if err != nil {
				return false, compileRepairCandidate{}, err
			}
			if improved {
				return true, candidate, nil
			}
		}
	}
	return false, compileRepairCandidate{}, nil
}

// compileRepairCandidateIfImproved 重建候选布局并报告其是否优于基准。
func compileRepairCandidateIfImproved(units []compilePlanningUnit, assignments map[string]int, count int, target int64, base compilePackingScore) (compileRepairCandidate, bool, error) {
	candidate, ok, err := buildCompileRepairCandidate(units, assignments, count, target)
	if err != nil || !ok {
		return compileRepairCandidate{}, false, err
	}
	score, err := compilePackingScoreForShards(candidate, target)
	if err != nil {
		return compileRepairCandidate{}, false, err
	}
	return compileRepairCandidate{shards: candidate, assignments: assignments}, score.less(base), nil
}

// compileRepairCycle 在固定 transition 上限内尝试三个 unit 的循环换箱。
func compileRepairCycle(units []compilePlanningUnit, baseAssignments map[string]int, current []ShardPlan, target int64) (bool, compileRepairCandidate, error) {
	base, err := compilePackingScoreForShards(current, target)
	if err != nil {
		return false, compileRepairCandidate{}, err
	}
	keys := compileUnitKeys(units)
	checked := 0
	return compileRepairCycleTransitions(units, baseAssignments, current, target, keys, base, &checked)
}

// compileRepairCycleTransitions 尝试三个 unit 的有界循环换箱。
func compileRepairCycleTransitions(units []compilePlanningUnit, baseAssignments map[string]int, current []ShardPlan, target int64, keys []string, base compilePackingScore, checked *int) (bool, compileRepairCandidate, error) {
	for first := range len(keys) {
		for second := first + 1; second < len(keys); second++ {
			for third := second + 1; third < len(keys) && *checked < cicontract.WorkloadPlanningHeuristicMaxThreeCycleTransitions; third++ {
				firstKey, secondKey, thirdKey := keys[first], keys[second], keys[third]
				candidateAssignments, ok := compileRepairCycleAssignments(baseAssignments, firstKey, secondKey, thirdKey)
				if !ok {
					continue
				}
				(*checked)++
				candidate, improved, candidateErr := compileRepairCandidateIfImproved(units, candidateAssignments, len(current), target, base)
				if candidateErr != nil {
					return false, compileRepairCandidate{}, candidateErr
				}
				if improved {
					return true, candidate, nil
				}
			}
		}
	}
	return false, compileRepairCandidate{}, nil
}

// compileRepairCycleAssignments 构造三个不同箱位的确定性循环映射。
func compileRepairCycleAssignments(baseAssignments map[string]int, firstKey, secondKey, thirdKey string) (map[string]int, bool) {
	if baseAssignments[firstKey] == baseAssignments[secondKey] || baseAssignments[secondKey] == baseAssignments[thirdKey] || baseAssignments[firstKey] == baseAssignments[thirdKey] {
		return nil, false
	}
	assignments := cloneCompileAssignments(baseAssignments)
	assignments[firstKey], assignments[secondKey], assignments[thirdKey] = baseAssignments[thirdKey], baseAssignments[firstKey], baseAssignments[secondKey]
	return assignments, true
}

// compileRepairBeam 在有限宽度、深度和 transition 预算中保留合法中间状态。
func compileRepairBeam(units []compilePlanningUnit, baseAssignments map[string]int, current []ShardPlan, target int64) (bool, compileRepairCandidate, error) {
	base, err := compilePackingScoreForShards(current, target)
	if err != nil {
		return false, compileRepairCandidate{}, err
	}
	frontier := []compileRepairBeamState{{candidate: compileRepairCandidate{shards: current, assignments: baseAssignments}, score: base}}
	best := frontier[0]
	keys := compileUnitKeys(units)
	transitions := 0
	for range cicontract.WorkloadPlanningHeuristicBeamDepth {
		next, nextBest, err := compileRepairBeamTransitions(units, frontier, best, keys, len(current), target, &transitions)
		if err != nil {
			return false, compileRepairCandidate{}, err
		}
		best = nextBest
		sort.SliceStable(next, func(left, right int) bool { return next[left].score.less(next[right].score) })
		if len(next) > cicontract.WorkloadPlanningHeuristicBeamWidth {
			next = next[:cicontract.WorkloadPlanningHeuristicBeamWidth]
		}
		if len(next) == 0 {
			break
		}
		frontier = next
	}
	if best.score.less(base) {
		return true, best.candidate, nil
	}
	return false, compileRepairCandidate{}, nil
}

// compileRepairBeamTransitions 生成一层有限宽度的合法 beam 状态。
func compileRepairBeamTransitions(units []compilePlanningUnit, frontier []compileRepairBeamState, best compileRepairBeamState, keys []string, count int, target int64, transitions *int) ([]compileRepairBeamState, compileRepairBeamState, error) {
	next := make([]compileRepairBeamState, 0, cicontract.WorkloadPlanningHeuristicBeamWidth)
	for _, item := range frontier {
		for _, key := range keys {
			for destination := range item.candidate.shards {
				if *transitions >= cicontract.WorkloadPlanningHeuristicMaxBeamTransitions {
					break
				}
				if item.candidate.assignments[key] == destination {
					continue
				}
				(*transitions)++
				candidateAssignments := cloneCompileAssignments(item.candidate.assignments)
				candidateAssignments[key] = destination
				candidate, ok, err := buildCompileRepairCandidate(units, candidateAssignments, count, target)
				if err != nil {
					return nil, compileRepairBeamState{}, err
				}
				if !ok {
					continue
				}
				score, err := compilePackingScoreForShards(candidate, target)
				if err != nil {
					return nil, compileRepairBeamState{}, err
				}
				state := compileRepairBeamState{candidate: compileRepairCandidate{shards: candidate, assignments: candidateAssignments}, score: score}
				if score.less(best.score) {
					best = state
				}
				next = append(next, state)
			}
		}
	}
	return next, best, nil
}

// buildCompileRepairCandidate 按 assignment 重建 compile bin，过滤空箱并规范化索引。
func buildCompileRepairCandidate(units []compilePlanningUnit, assignments map[string]int, count int, target int64) ([]ShardPlan, bool, error) {
	ordered := append([]compilePlanningUnit(nil), units...)
	sortCompilePlanningUnits(ordered)
	bins := make([]compilePackingBin, count)
	for index := range bins {
		bins[index] = compilePackingBin{shard: ShardPlan{Index: index}, affinities: make(map[string]struct{}), artifactKeys: make(map[string]struct{})}
	}
	if ok, err := applyCompileRepairAssignments(bins, ordered, assignments, target); err != nil || !ok {
		return nil, false, err
	}
	shards := make([]ShardPlan, 0, len(bins))
	for _, bin := range bins {
		if len(bin.shard.Workloads) == 0 {
			continue
		}
		shards = append(shards, bin.shard)
	}
	ok := len(shards) > 0
	if !ok {
		return nil, false, nil
	}
	normalized := canonicalCompilePacking(shards)
	updated, ok := compilePackingAssignments(units, normalized)
	if !ok {
		return nil, false, nil
	}
	for key := range assignments {
		delete(assignments, key)
	}
	maps.Copy(assignments, updated)
	return normalized, true, nil
}

// applyCompileRepairAssignments 按指定箱索引重放所有 unit，拒绝非法或不可放置状态。
func applyCompileRepairAssignments(bins []compilePackingBin, units []compilePlanningUnit, assignments map[string]int, target int64) (bool, error) {
	for _, unit := range units {
		binIndex, found := assignments[compileUnitKey(unit)]
		if !found || binIndex < 0 || binIndex >= len(bins) {
			return false, nil
		}
		_, placed, err := applyCompilePackingUnit(&bins[binIndex], unit, target)
		if err != nil {
			return false, err
		}
		if !placed {
			return false, nil
		}
	}
	return true, nil
}

func cloneCompilePackingBin(source compilePackingBin) compilePackingBin {
	return compilePackingBin{
		shard:           cloneCompilePacking([]ShardPlan{source.shard})[0],
		affinities:      cloneStringSet(source.affinities),
		artifactKeys:    cloneStringSet(source.artifactKeys),
		serialEligible:  source.serialEligible,
		resourceClassID: source.resourceClassID,
	}
}

func cloneStringSet(source map[string]struct{}) map[string]struct{} {
	clone := make(map[string]struct{}, len(source))
	for key := range source {
		clone[key] = struct{}{}
	}
	return clone
}

// compilePackingAssignments 从 canonical shard 反推每个 unit 的稳定箱索引。
func compilePackingAssignments(units []compilePlanningUnit, shards []ShardPlan) (map[string]int, bool) {
	workloadShard := make(map[string]int)
	for index, shard := range shards {
		for _, workload := range shard.Workloads {
			workloadShard[workload.Workload.ID] = index
		}
	}
	assignments := make(map[string]int, len(units))
	for _, unit := range units {
		if len(unit.workloads) == 0 {
			return nil, false
		}
		shard, found := workloadShard[unit.workloads[0].Workload.ID]
		if !found {
			return nil, false
		}
		for _, workload := range unit.workloads[1:] {
			mapped, memberFound := workloadShard[workload.Workload.ID]
			if !memberFound || mapped != shard {
				return nil, false
			}
		}
		key := compileUnitKey(unit)
		if _, duplicate := assignments[key]; duplicate {
			return nil, false
		}
		assignments[key] = shard
	}
	return assignments, true
}

func compileUnitKeys(units []compilePlanningUnit) []string {
	keys := make([]string, 0, len(units))
	seen := make(map[string]struct{}, len(units))
	for _, unit := range units {
		key := compileUnitKey(unit)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func compileUnitKey(unit compilePlanningUnit) string {
	if unit.sortID != "" {
		return unit.sortID
	}
	if len(unit.workloads) == 0 {
		return ""
	}
	return unit.workloads[0].Workload.ID
}

func cloneCompileAssignments(assignments map[string]int) map[string]int {
	clone := make(map[string]int, len(assignments))
	maps.Copy(clone, assignments)
	return clone
}
