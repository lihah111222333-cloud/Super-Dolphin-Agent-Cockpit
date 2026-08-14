package gate

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

type compilePackingBin struct {
	shard        ShardPlan
	affinities   map[string]struct{}
	artifactKeys map[string]struct{}
}

type compilePackingUndo struct {
	workloadCount int
	groupCount    int
	durationMS    int64
	affinityKey   string
	artifactKey   string
}

type compilePackingSearchResult struct {
	best            []ShardPlan
	bestScore       compilePackingScore
	found           bool
	objectiveTarget int64
}

type compilePackingScore struct {
	excessMS     int64
	shardCount   int
	makespanMS   int64
	setupProxyMS int64
	layout       string
}

const maxCompilePackingDuration = int64(1<<63 - 1)

var (
	errCompilePackingDurationOverflow  = errors.New("compile packing duration overflow")
	errCompilePackingInvalidShardCount = errors.New("compile packing shard count must be non-negative")
)

func (score compilePackingScore) less(other compilePackingScore) bool {
	if score.excessMS != other.excessMS {
		return score.excessMS < other.excessMS
	}
	if score.shardCount != other.shardCount {
		return score.shardCount < other.shardCount
	}
	if score.makespanMS != other.makespanMS {
		return score.makespanMS < other.makespanMS
	}
	if score.setupProxyMS != other.setupProxyMS {
		return score.setupProxyMS < other.setupProxyMS
	}
	return score.layout < other.layout
}

// provenCompileUnitPacking 对 <=12 unit 做 exact；大输入走有界 compile-aware heuristic。
func provenCompileUnitPacking(units []compilePlanningUnit, target int64) ([]ShardPlan, error) {
	ordinary, serial, isolated := partitionCompilePlanningUnits(units)
	if len(ordinary)+len(serial) > cicontract.WorkloadPlanningExactPackableUnitThreshold {
		result, err := heuristicCompileUnitPacking(units, target)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	ordinaryShards, err := provenCompilePackableExact(ordinary, target)
	if err != nil {
		return nil, err
	}
	// serial 资格只参与全局 exact/heuristic 阈值；不同 canonical group 不能
	// 进入同一次精确搜索，避免 shard workload 与 group 覆盖漂移。
	best := append(ordinaryShards, isolatedCompileShards(serial)...)
	best = append(best, isolatedCompileShards(isolated)...)
	best = canonicalCompilePacking(best)
	if !compileShardsMeetTarget(best, target, units) {
		return nil, errors.New("compile packing produced an invalid target violation")
	}
	return best, nil
}

// provenCompilePackableExact 在 packable 单元上执行确定性的精确分片搜索，并返回最小 makespan 布局。
func provenCompilePackableExact(units []compilePlanningUnit, target int64) ([]ShardPlan, error) {
	if len(units) == 0 {
		return nil, nil
	}
	upper, err := greedyCompilePackingUpperBound(units, target)
	if err != nil {
		return nil, err
	}
	budget := newDeterministicPackingSearchBudget()
	lower, err := compilePackingShardLowerBound(units, target)
	if err != nil {
		return nil, err
	}
	best := upper
	for count := lower; count < len(upper); count++ {
		candidate, found, searchErr := searchCompilePackingFixedCount(units, count, target, target, &budget)
		if searchErr != nil {
			return nil, searchErr
		}
		if found {
			best = candidate
			break
		}
	}
	return minimizeCompilePackingMakespan(units, best, target, &budget)
}

// greedyCompilePackingUpperBound 计算满足目标时长的确定性 greedy 分片上界。
func greedyCompilePackingUpperBound(units []compilePlanningUnit, target int64) ([]ShardPlan, error) {
	lower, err := compilePackingShardLowerBound(units, target)
	if err != nil {
		return nil, err
	}
	shards, placed := distributeCompileUnits(units, lower, target)
	if placed && compileShardsMeetTarget(shards, target, units) {
		return shards, nil
	}
	if lower < len(units) {
		shards, placed = distributeCompileUnits(units, len(units), target)
		if placed && compileShardsMeetTarget(shards, target, units) {
			return shards, nil
		}
	}
	return nil, errors.New("compile shard count did not converge")
}

// minimizeCompilePackingMakespan 在已证明的最少分片数内二分并枚举最小 makespan。
func minimizeCompilePackingMakespan(units []compilePlanningUnit, initial []ShardPlan, target int64, budget *deterministicPackingSearchBudget) ([]ShardPlan, error) {
	low, err := compilePackingMakespanLowerBound(units, len(initial))
	if err != nil {
		return nil, err
	}
	high := compilePackingMakespan(initial)
	best := canonicalCompilePacking(initial)
	for low < high {
		mid := low + (high-low)/2
		candidate, found, err := searchCompilePackingFixedCount(units, len(initial), mid, target, budget)
		if err != nil {
			return nil, err
		}
		if found {
			high = mid
			best = candidate
			continue
		}
		low = mid + 1
	}
	if low != high {
		return nil, errors.New("compile packing makespan search did not converge")
	}
	if compilePackingMakespan(best) > high {
		return nil, errors.New("compile packing optimal capacity has no feasible layout")
	}
	return best, nil
}

// compilePackingShardLowerBound 按 ordinary 容量和每个 compile-group domain 计算下界。
func compilePackingShardLowerBound(units []compilePlanningUnit, target int64) (int, error) {
	if len(units) == 0 {
		return 0, nil
	}
	if target <= 0 {
		return 0, errors.New("compile packing target must be positive")
	}
	classes, err := classifyCompilePackingShardClasses(units, target)
	if err != nil {
		return 0, err
	}
	ordinaryLower, err := compilePackingCapacityLowerBound(classes.ordinaryTotal, classes.ordinaryOversize, target)
	if err != nil {
		return 0, err
	}
	lower, err := addCompilePackingShardCounts(classes.compileGroups, ordinaryLower)
	if err != nil {
		return 0, err
	}
	return addCompilePackingShardCounts(lower, classes.exclusive)
}

type compilePackingShardClasses struct {
	ordinaryTotal    int64
	ordinaryOversize int
	compileGroups    int
	exclusive        int
}

// classifyCompilePackingShardClasses 汇总 ordinary、独立 compile-group 与 hard-isolation unit。
func classifyCompilePackingShardClasses(units []compilePlanningUnit, target int64) (compilePackingShardClasses, error) {
	var classes compilePackingShardClasses
	for _, unit := range units {
		if unit.costMS < 0 {
			return compilePackingShardClasses{}, errors.New("compile packing unit cost must be non-negative")
		}
		if unit.group == nil {
			if unit.costMS > target {
				classes.ordinaryOversize++
				continue
			}
			var err error
			classes.ordinaryTotal, err = addCompilePackingDuration(classes.ordinaryTotal, unit.costMS)
			if err != nil {
				return compilePackingShardClasses{}, err
			}
			continue
		}
		if CompileGroupSerialPackingEligible(*unit.group) {
			classes.compileGroups++
		} else {
			classes.exclusive++
		}
	}
	return classes, nil
}

func addCompilePackingDuration(total, cost int64) (int64, error) {
	if cost < 0 || total > maxCompilePackingDuration-cost {
		return 0, errCompilePackingDurationOverflow
	}
	return total + cost, nil
}

func addCompilePackingShardCounts(left, right int) (int, error) {
	if left < 0 || right < 0 {
		return 0, errCompilePackingDurationOverflow
	}
	maxInt := uint64(^uint(0) >> 1)
	if uint64(left) > maxInt-uint64(right) {
		return 0, errCompilePackingDurationOverflow
	}
	return left + right, nil
}

// compilePackingCapacityLowerBound 以总时长和超大 unit 计算类别容量下界。
func compilePackingCapacityLowerBound(regularTotal int64, oversize int, target int64) (int, error) {
	if regularTotal < 0 || oversize < 0 || target <= 0 {
		return 0, errors.New("compile packing capacity lower bound inputs are invalid")
	}
	if regularTotal == 0 {
		return oversize, nil
	}
	regularLower := regularTotal / target
	if regularTotal%target != 0 {
		regularLower++
	}
	maxInt := uint64(^uint(0) >> 1)
	if uint64(regularLower) > maxInt-uint64(oversize) {
		return 0, errCompilePackingDurationOverflow
	}
	return oversize + int(regularLower), nil
}

// compilePackingMakespanLowerBound 对固定 shard 数给出总时长与最大 unit 下界。
func compilePackingMakespanLowerBound(units []compilePlanningUnit, count int) (int64, error) {
	if count < 0 {
		return 0, errCompilePackingInvalidShardCount
	}
	if len(units) > 0 && count == 0 {
		return 0, errors.New("compile packing shard count must be positive for non-empty units")
	}
	var total, largest int64
	for _, unit := range units {
		if unit.costMS < 0 {
			return 0, errors.New("compile packing unit cost must be non-negative")
		}
		if total > maxCompilePackingDuration-unit.costMS {
			return 0, errCompilePackingDurationOverflow
		}
		total += unit.costMS
		largest = max(largest, unit.costMS)
	}
	if count == 0 {
		return 0, nil
	}
	count64 := int64(count)
	totalLower := total / count64
	if total%count64 != 0 {
		totalLower++
	}
	return max(largest, totalLower), nil
}

func compilePackingMakespan(shards []ShardPlan) int64 {
	var result int64
	for _, shard := range shards {
		result = max(result, shard.EstimatedDurationMS)
	}
	return result
}

func searchCompilePackingFixedCount(units []compilePlanningUnit, count int, capacity, objectiveTarget int64, budget *deterministicPackingSearchBudget) ([]ShardPlan, bool, error) {
	if count < 0 {
		return nil, false, errCompilePackingInvalidShardCount
	}
	ordered := append([]compilePlanningUnit(nil), units...)
	sortCompilePlanningUnits(ordered)
	bins := make([]compilePackingBin, count)
	for index := range bins {
		bins[index] = compilePackingBin{shard: ShardPlan{Index: index}, affinities: make(map[string]struct{}), artifactKeys: make(map[string]struct{})}
	}
	search := compilePackingSearchResult{
		bestScore:       compilePackingScore{excessMS: int64(^uint64(0) >> 1), makespanMS: int64(^uint64(0) >> 1)},
		objectiveTarget: objectiveTarget,
	}
	if err := searchCompilePackingPlacement(ordered, bins, 0, capacity, budget, &search); err != nil {
		return nil, false, err
	}
	return search.best, search.found, nil
}

// searchCompilePackingPlacement 在固定箱数和容量内证明 compile affinity 可行性。
func searchCompilePackingPlacement(units []compilePlanningUnit, bins []compilePackingBin, index int, capacity int64, budget *deterministicPackingSearchBudget, search *compilePackingSearchResult) error {
	if err := budget.consume(); err != nil {
		return err
	}
	if index == len(units) {
		candidate, complete, err := completedCompilePacking(bins)
		if err != nil || !complete {
			return err
		}
		canonical := canonicalCompilePacking(candidate)
		if compilePackingMakespan(canonical) > capacity && capacity != search.objectiveTarget {
			return nil
		}
		if !compileShardsMeetTarget(canonical, search.objectiveTarget, units) {
			return nil
		}
		return search.consider(canonical)
	}
	if len(units)-index < emptyCompilePackingBins(bins) {
		return nil
	}
	return searchCompilePackingCandidates(units, bins, index, capacity, budget, search)
}

// searchCompilePackingCandidates 原地尝试候选箱，避免节点数乘箱数的复制和分配。
func searchCompilePackingCandidates(units []compilePlanningUnit, bins []compilePackingBin, index int, capacity int64, budget *deterministicPackingSearchBudget, search *compilePackingSearchResult) error {
	emptyVisited := false
	for binIndex := range bins {
		empty := len(bins[binIndex].shard.Workloads) == 0
		if empty && emptyVisited {
			continue
		}
		emptyVisited = emptyVisited || empty
		undo, ok, err := applyCompilePackingUnit(&bins[binIndex], units[index], capacity)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		searchErr := searchCompilePackingPlacement(units, bins, index+1, capacity, budget, search)
		undoCompilePackingUnit(&bins[binIndex], undo)
		if searchErr != nil {
			return searchErr
		}
	}
	return nil
}

func (search *compilePackingSearchResult) consider(candidate []ShardPlan) error {
	score, err := compilePackingScoreForShards(candidate, search.objectiveTarget)
	if err != nil {
		return err
	}
	if !search.found || score.less(search.bestScore) {
		search.bestScore = score
		search.best = cloneCompilePacking(candidate)
		search.found = true
	}
	return nil
}

// compilePackingScoreForShards 计算超额、分片数、makespan 与 canonical 布局目标元组。
func compilePackingScoreForShards(shards []ShardPlan, target int64) (compilePackingScore, error) {
	if target <= 0 {
		return compilePackingScore{}, errors.New("compile packing target must be positive")
	}
	// compile unit 已固化 compile-once 与 wave critical-path；合法布局的 setup
	// 总量恒定，setup proxy 规范化为零并显式保留在比较器中。
	score := compilePackingScore{shardCount: len(shards), makespanMS: compilePackingMakespan(shards), setupProxyMS: 0}
	var layout strings.Builder
	for _, shard := range shards {
		if shard.EstimatedDurationMS > target {
			excess := shard.EstimatedDurationMS - target
			if score.excessMS > maxCompilePackingDuration-excess {
				return compilePackingScore{}, errCompilePackingDurationOverflow
			}
			score.excessMS += excess
		}
		layout.WriteString(compilePackingShardKey(shard))
		layout.WriteByte('|')
	}
	score.layout = layout.String()
	return score, nil
}

func canonicalCompilePacking(shards []ShardPlan) []ShardPlan {
	result := cloneCompilePacking(shards)
	for index := range result {
		sort.SliceStable(result[index].Workloads, func(left, right int) bool {
			return result[index].Workloads[left].Workload.ID < result[index].Workloads[right].Workload.ID
		})
		slices.Sort(result[index].CompileGroupIDs)
	}
	sort.SliceStable(result, func(left, right int) bool {
		return compilePackingShardKey(result[left]) < compilePackingShardKey(result[right])
	})
	for index := range result {
		result[index].Index = index
	}
	return result
}

func cloneCompilePacking(shards []ShardPlan) []ShardPlan {
	result := make([]ShardPlan, len(shards))
	for index, shard := range shards {
		result[index] = shard
		result[index].Workloads = append([]PlannedWorkload(nil), shard.Workloads...)
		result[index].CompileGroupIDs = append([]string(nil), shard.CompileGroupIDs...)
	}
	return result
}

func compilePackingShardKey(shard ShardPlan) string {
	ids := make([]string, len(shard.Workloads))
	for index, workload := range shard.Workloads {
		ids[index] = workload.Workload.ID
	}
	slices.Sort(ids)
	return strings.Join(ids, ",")
}

// applyCompilePackingUnit 将一个 unit 原地加入箱，并返回精确撤销信息。
func applyCompilePackingUnit(bin *compilePackingBin, unit compilePlanningUnit, capacity int64) (compilePackingUndo, bool, error) {
	if !compileUnitFitsPackingCapacity(bin.shard, unit, capacity) ||
		!compileUnitCanShareShard(bin.shard, bin.affinities, bin.artifactKeys, unit) {
		return compilePackingUndo{}, false, nil
	}
	undo := compilePackingUndo{
		workloadCount: len(bin.shard.Workloads), groupCount: len(bin.shard.CompileGroupIDs),
		durationMS:  bin.shard.EstimatedDurationMS,
		affinityKey: unit.affinityKey,
	}
	bin.shard.Workloads = append(bin.shard.Workloads, unit.workloads...)
	bin.shard.EstimatedDurationMS += unit.costMS
	bin.affinities[unit.affinityKey] = struct{}{}
	if unit.group == nil {
		return undo, true, nil
	}
	artifactKey, err := CompileArtifactKey(*unit.group)
	if err != nil {
		undoCompilePackingUnit(bin, undo)
		return compilePackingUndo{}, false, fmt.Errorf("compile packing artifact: %w", err)
	}
	undo.artifactKey = artifactKey
	bin.shard.CompileGroupIDs = append(bin.shard.CompileGroupIDs, unit.group.GroupID)
	bin.artifactKeys[artifactKey] = struct{}{}
	return undo, true, nil
}

// undoCompilePackingUnit 撤销最近一次 unit 放置并恢复集合状态。
func undoCompilePackingUnit(bin *compilePackingBin, undo compilePackingUndo) {
	bin.shard.Workloads = bin.shard.Workloads[:undo.workloadCount]
	bin.shard.CompileGroupIDs = bin.shard.CompileGroupIDs[:undo.groupCount]
	bin.shard.EstimatedDurationMS = undo.durationMS
	delete(bin.affinities, undo.affinityKey)
	if undo.artifactKey != "" {
		delete(bin.artifactKeys, undo.artifactKey)
	}
}

func compileUnitFitsPackingCapacity(shard ShardPlan, unit compilePlanningUnit, capacity int64) bool {
	if len(shard.Workloads) == 0 {
		return true
	}
	return shard.EstimatedDurationMS <= capacity && unit.costMS <= capacity-shard.EstimatedDurationMS
}

func completedCompilePacking(bins []compilePackingBin) ([]ShardPlan, bool, error) {
	shards := make([]ShardPlan, len(bins))
	for index, bin := range bins {
		if len(bin.shard.Workloads) == 0 {
			return nil, false, nil
		}
		shards[index] = bin.shard
		slices.Sort(shards[index].CompileGroupIDs)
	}
	return shards, true, nil
}

func emptyCompilePackingBins(bins []compilePackingBin) int {
	empty := 0
	for _, bin := range bins {
		if len(bin.shard.Workloads) == 0 {
			empty++
		}
	}
	return empty
}
