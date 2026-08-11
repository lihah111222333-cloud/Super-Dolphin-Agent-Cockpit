// Package gate 提供独立 CI workload 的确定性建模与分片规划。
package gate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const (
	durationLedgerVersion              = 1
	workloadExecutionPlanSchemaVersion = cicontract.WorkloadExecutionPlanSchemaVersion
	// WorkloadPlanningAlgorithmID 是 cicontract 的唯一算法 owner alias。
	WorkloadPlanningAlgorithmID      = cicontract.WorkloadPlanningAlgorithmID
	workloadPlanningSearchNodeBudget = cicontract.WorkloadPlanningSearchNodeBudget
	// FullCITargetDuration 是分片优化目标；超时只告警，不是 worker 终止时限。
	FullCITargetDuration         = cicontract.ShardTargetDuration
	FullCITargetDurationMS int64 = int64(FullCITargetDuration / time.Millisecond)
	// DurationExecutionModeNormal 标记由 normal 2C/4C/8C resource tier 采集的 sample。
	// 它与 fixed-size calibration 有意保持区分。
	DurationExecutionModeNormal = "normal"
	// DurationExecutionModeCalibration 标记由固定 4C/8GiB calibration resource 采集的
	// sample。Calibration sample 绝不能进入 normal LPT。
	DurationExecutionModeCalibration = "calibration"
)

// WorkloadKind 标识由 gate 支持的确定性执行种类。
type WorkloadKind string

const (
	WorkloadKindGoTest   WorkloadKind = "go_test"
	WorkloadKindNodeTest WorkloadKind = "node_test"
	WorkloadKindGuard    WorkloadKind = "guard"
)

// WorkloadCatalog 是版本化的、可序列化的 workload 真值源。
type WorkloadCatalog struct {
	Version       int        `json:"version"`
	Authoritative bool       `json:"authoritative"`
	Workloads     []Workload `json:"workloads"`
}

// Workload 定义一个稳定 gate 单元，并显式标记其是否可交给 worker 分片。
type Workload struct {
	ID                  string       `json:"id"`
	Kind                WorkloadKind `json:"kind"`
	CommandDigest       string       `json:"command_digest"`
	InputDigest         string       `json:"input_digest,omitempty"`
	BootstrapEstimateMS int64        `json:"bootstrap_estimate_ms"`
	Shardable           bool         `json:"shardable"`
}

// UnmarshalJSON 要求 shardable 即使为 false 也必须显式出现，避免缺字段静默降级。
func (workload *Workload) UnmarshalJSON(data []byte) error {
	type workloadDocument struct {
		ID                  string       `json:"id"`
		Kind                WorkloadKind `json:"kind"`
		CommandDigest       string       `json:"command_digest"`
		InputDigest         string       `json:"input_digest,omitempty"`
		BootstrapEstimateMS int64        `json:"bootstrap_estimate_ms"`
		Shardable           *bool        `json:"shardable"`
	}
	var document workloadDocument
	if err := decodeStrictJSON(bytes.NewReader(data), &document); err != nil {
		return err
	}
	if document.Shardable == nil {
		return errors.New("shardable is required")
	}
	*workload = Workload{
		ID: document.ID, Kind: document.Kind, CommandDigest: document.CommandDigest,
		InputDigest: document.InputDigest, BootstrapEstimateMS: document.BootstrapEstimateMS, Shardable: *document.Shardable,
	}
	return nil
}

// DurationLedger 保存按可复现执行环境分桶的观测时长样本。
type DurationLedger struct {
	Version       int                         `json:"version"`
	Calibration   *DurationCalibration        `json:"calibration,omitempty"`
	ShardOverhead *ShardOrchestrationOverhead `json:"shard_overhead,omitempty"`
	Samples       []DurationSample            `json:"samples"`
}

const DurationCalibrationSchemaVersion uint32 = 3

// DurationCalibration 绑定首代完整 commit/push/release 校准和精确 runner 环境。
type DurationCalibration struct {
	SchemaVersion                uint32         `json:"schema_version"`
	Commit                       string         `json:"commit"`
	Tree                         string         `json:"tree"`
	Platform                     string         `json:"platform"`
	Runner                       string         `json:"runner"`
	Toolchain                    string         `json:"toolchain"`
	CommitEntrypoint             CIEntrypointID `json:"commit_entrypoint"`
	PushEntrypoint               CIEntrypointID `json:"push_entrypoint"`
	ReleaseEntrypoint            CIEntrypointID `json:"release_entrypoint"`
	CommitCatalogDigest          string         `json:"commit_catalog_digest"`
	PushCatalogDigest            string         `json:"push_catalog_digest"`
	ReleaseCatalogDigest         string         `json:"release_catalog_digest"`
	CalibrationResourceClassID   string         `json:"calibration_resource_class_id"`
	CalibrationResourceCPU       float64        `json:"calibration_resource_cpu"`
	CalibrationResourceMemoryGiB float64        `json:"calibration_resource_memory_gib"`
	WorkloadCount                int            `json:"workload_count"`
	// RacePackageCount 是校准目录中过滤后仍可执行的去重 race 包数；normal-only 静态目标不计入。
	RacePackageCount   int       `json:"race_package_count"`
	AcceptedSnapshotID string    `json:"accepted_snapshot_id"`
	CompletedAt        time.Time `json:"completed_at"`
}

// DurationSample 记录一个 workload 的单次执行结果和耗时。
type DurationSample struct {
	// AcceptedGeneration 是读取侧保留的 SQLite accepted generation；写入侧由同一事务提供。
	AcceptedGeneration  uint64         `json:"accepted_generation,omitempty"`
	Bucket              DurationBucket `json:"bucket"`
	Succeeded           bool           `json:"succeeded"`
	DurationMS          int64          `json:"duration_ms"`
	TargetKind          WorkloadKind   `json:"target_kind,omitempty"`
	ParentWorkloadID    string         `json:"parent_workload_id,omitempty"`
	ParentCommandDigest string         `json:"parent_command_digest,omitempty"`
	TargetName          string         `json:"target_name,omitempty"`
	TargetStatus        GoTestStatus   `json:"target_status,omitempty"`
}

// DurationBucket 将观测绑定到 workload、命令和执行环境，避免不可比样本混用。
type DurationBucket struct {
	WorkloadID        string  `json:"workload_id"`
	CommandDigest     string  `json:"command_digest"`
	InputDigest       string  `json:"input_digest"`
	Platform          string  `json:"platform"`
	Runner            string  `json:"runner"`
	Toolchain         string  `json:"toolchain"`
	ExecutionMode     string  `json:"execution_mode"`
	ResourceClassID   string  `json:"resource_class_id"`
	ResourceCPU       float64 `json:"resource_cpu"`
	ResourceMemoryGiB float64 `json:"resource_memory_gib"`
}

// PlanningContext 是 LPT 规划的目标执行环境；它不接受瞬时 slot 状态。Calibration=true 时所有时长档位共享固定校准资源。
type PlanningContext struct {
	Platform                      string  `json:"platform"`
	Runner                        string  `json:"runner"`
	Toolchain                     string  `json:"toolchain"`
	Calibration                   bool    `json:"calibration,omitempty"`
	CalibrationResourceClassID    string  `json:"calibration_resource_class_id,omitempty"`
	CalibrationResourceCPU        float64 `json:"calibration_resource_cpu,omitempty"`
	CalibrationResourceMemoryGiB  float64 `json:"calibration_resource_memory_gib,omitempty"`
	TargetDurationMS              int64   `json:"target_duration_ms"`
	AcceptedSnapshotID            string  `json:"accepted_snapshot_id"`
	ShardOverheadP95MS            int64   `json:"shard_overhead_p95_ms"`
	ShardOverheadSampleCount      int     `json:"shard_overhead_sample_count"`
	ShardOverheadProvenanceDigest string  `json:"shard_overhead_provenance_digest"`
}

// ShardPlan 是单个计划分片及其确定性 workload 顺序。
type ShardPlan struct {
	Index               int               `json:"index"`
	Workloads           []PlannedWorkload `json:"workloads"`
	CompileGroupIDs     []string          `json:"compile_group_ids,omitempty"`
	EstimatedDurationMS int64             `json:"estimated_duration_ms"`
}

// PlannedWorkload 是带估算时长的计划 workload。
type PlannedWorkload struct {
	Workload            Workload `json:"workload"`
	EstimatedDurationMS int64    `json:"estimated_duration_ms"`
	ResourceCPU         float64  `json:"resource_cpu"`
	ResourceMemoryGiB   float64  `json:"resource_memory_gib"`
}

// WorkloadPackingEvidence 是每个 stable resource tier 的可审计 D-CPAP 见证。
// lower/planned/gap 均以该 tier 的总 shard 数计，禁止只报告 packable 子集。
type WorkloadPackingEvidence struct {
	ResourceTier       cicontract.WorkloadResourceTier `json:"resource_tier"`
	SolverMode         string                          `json:"solver_mode"`
	PackableUnitCount  int                             `json:"packable_unit_count"`
	IsolatedUnitCount  int                             `json:"isolated_unit_count"`
	LowerBoundShards   int                             `json:"lower_bound_shards"`
	PlannedShards      int                             `json:"planned_shards"`
	HeuristicGapShards int                             `json:"heuristic_gap_shards"`
}

// WorkloadExecutionPlan 将一次确定性分片绑定到 GatePlan、目录和账本 generation。
type WorkloadExecutionPlan struct {
	SchemaVersion            uint32                    `json:"schema_version"`
	AlgorithmID              string                    `json:"algorithm_id"`
	ObjectiveDigest          string                    `json:"objective_digest"`
	PlanningPolicyDigest     string                    `json:"planning_policy_digest"`
	EstimationPolicyDigest   string                    `json:"estimation_policy_digest"`
	GatePlanDigest           string                    `json:"gate_plan_digest"`
	CatalogDigest            string                    `json:"catalog_digest"`
	LedgerGeneration         uint64                    `json:"ledger_generation"`
	Context                  PlanningContext           `json:"context"`
	Catalog                  WorkloadCatalog           `json:"catalog"`
	ExecutionWorkloadIDs     []GateID                  `json:"execution_workload_ids"`
	ExecutionWorkloadDigest  string                    `json:"execution_workload_digest"`
	CompileGroups            []CompileGroup            `json:"compile_groups"`
	Shards                   []ShardPlan               `json:"shards"`
	PackingEvidence          []WorkloadPackingEvidence `json:"packing_evidence"`
	OwnerEstimatedDurationMS int64                     `json:"owner_estimated_duration_ms"`
	PlanDigest               string                    `json:"plan_digest"`
}

// workloadPlanningObjectiveDigest 返回 cicontract 固定目标顺序的内容摘要。
func workloadPlanningObjectiveDigest() string {
	return cicontract.WorkloadPlanningObjectiveDigest()
}

// workloadEstimationPolicyMaterial 将 gate PlanningContext 的稳定字段交给
// cicontract canonical owner；不在 gate 中复制序列化或摘要算法。
func workloadEstimationPolicyMaterial(context PlanningContext) cicontract.WorkloadEstimationPolicyMaterial {
	return cicontract.WorkloadEstimationPolicyMaterial{
		PolicyVersion:                 cicontract.WorkloadEstimationPolicyVersion,
		EstimatorPolicyID:             DurationEstimatorPolicyID,
		Platform:                      context.Platform,
		Runner:                        context.Runner,
		Toolchain:                     context.Toolchain,
		Calibration:                   context.Calibration,
		CalibrationResourceClassID:    context.CalibrationResourceClassID,
		CalibrationResourceCPU:        context.CalibrationResourceCPU,
		CalibrationResourceMemoryGiB:  context.CalibrationResourceMemoryGiB,
		TargetDurationMS:              context.TargetDurationMS,
		AcceptedSnapshotID:            context.AcceptedSnapshotID,
		ShardOverheadP95MS:            context.ShardOverheadP95MS,
		ShardOverheadSampleCount:      context.ShardOverheadSampleCount,
		ShardOverheadProvenanceDigest: context.ShardOverheadProvenanceDigest,
	}
}

// workloadEstimationPolicyDigest 返回 cicontract canonical 估时摘要。
func workloadEstimationPolicyDigest(context PlanningContext) (string, error) {
	return cicontract.WorkloadEstimationPolicyDigest(workloadEstimationPolicyMaterial(context))
}

func validateWorkloadPlanningIdentity(plan WorkloadExecutionPlan) error {
	return cicontract.ValidateWorkloadPlanContract(
		plan.SchemaVersion, plan.AlgorithmID, plan.ObjectiveDigest, plan.PlanningPolicyDigest, plan.EstimationPolicyDigest,
		workloadEstimationPolicyMaterial(plan.Context),
	)
}

type dcpapItem struct {
	id         string
	durationMS int64
}

type dcpapBin struct {
	items  []dcpapItem
	bodyMS int64
}

// distributeDCPAP 按 exact 阈值分流：小输入穷举完整目标元组，大输入使用有界
// heuristic 并保留容量下界与 gap 证据。exact 预算耗尽必须 fail-fast。
func distributeDCPAP(planned []PlannedWorkload, targetDurationMS int64) ([]ShardPlan, error) {
	if len(planned) == 0 {
		return nil, errors.New("D-CPAP planner requires at least one workload")
	}
	if targetDurationMS <= 0 {
		return nil, errors.New("D-CPAP planner target must be positive")
	}
	items := make([]dcpapItem, len(planned))
	for index, workload := range planned {
		if workload.EstimatedDurationMS <= 0 {
			return nil, fmt.Errorf("workload %q estimate must be positive", workload.Workload.ID)
		}
		items[index] = dcpapItem{id: workload.Workload.ID, durationMS: workload.EstimatedDurationMS}
	}
	bins, err := dcpapProvenPack(items, targetDurationMS)
	if err != nil {
		return nil, fmt.Errorf("D-CPAP packing proof: %w", err)
	}
	return dcpapBinsToShardPlans(bins, planned), nil
}

func dcpapBinsToShardPlans(bins []dcpapBin, planned []PlannedWorkload) []ShardPlan {
	byID := make(map[string]PlannedWorkload, len(planned))
	for _, item := range planned {
		byID[item.Workload.ID] = item
	}
	shards := make([]ShardPlan, len(bins))
	for index, bin := range bins {
		shards[index].Index = index
		shards[index].EstimatedDurationMS = bin.bodyMS
		for _, item := range bin.items {
			shards[index].Workloads = append(shards[index].Workloads, byID[item.id])
		}
		sort.Slice(shards[index].Workloads, func(left, right int) bool {
			if shards[index].Workloads[left].EstimatedDurationMS != shards[index].Workloads[right].EstimatedDurationMS {
				return shards[index].Workloads[left].EstimatedDurationMS > shards[index].Workloads[right].EstimatedDurationMS
			}
			return shards[index].Workloads[left].Workload.ID < shards[index].Workloads[right].Workload.ID
		})
	}
	return shards
}

// dcpapBestFitPack 构造确定性 BFD 初解；小输入交给 exact，大输入仅做有界修复并报告 gap。
func dcpapBestFitPack(items []dcpapItem, target int64) []dcpapBin {
	ordered := append([]dcpapItem(nil), items...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].durationMS != ordered[right].durationMS {
			return ordered[left].durationMS > ordered[right].durationMS
		}
		return ordered[left].id < ordered[right].id
	})
	bins := make([]dcpapBin, 0, len(items))
	for _, item := range ordered {
		best := -1
		bestResidual := int64(^uint64(0) >> 1)
		for index := range bins {
			if !dcpapCanPlace(bins[index], item, target) {
				continue
			}
			residual := dcpapResidual(bins[index], item, target)
			if best < 0 || residual < bestResidual || (residual == bestResidual && dcpapBinKey(bins[index]) < dcpapBinKey(bins[best])) {
				best, bestResidual = index, residual
			}
		}
		if best < 0 {
			bins = append(bins, dcpapBin{items: []dcpapItem{item}, bodyMS: item.durationMS})
			continue
		}
		bins[best].items = append(bins[best].items, item)
		bins[best].bodyMS += item.durationMS
	}
	return dcpapCanonicalBins(bins)
}

// dcpapExactPack 穷举小输入的完整目标元组，并返回 canonical 最小见证。
func dcpapExactPack(items []dcpapItem, target int64, budget *deterministicPackingSearchBudget) ([]dcpapBin, error) {
	if err := validateDCPAPDurationArithmetic(items, target); err != nil {
		return nil, err
	}
	ordered := append([]dcpapItem(nil), items...)
	sortDCPAPItems(ordered)
	search := dcpapExactSearch{
		ordered: ordered,
		target:  target,
		budget:  budget,
		best:    dcpapPackingScore{excessMS: int64(^uint64(0) >> 1), shardCount: len(items) + 1},
	}
	minimumCount, err := dcpapShardCountLowerBound(items, target)
	if err != nil {
		return nil, err
	}
	for count := minimumCount; count <= len(items); count++ {
		search.bins = make([]dcpapBin, count)
		if err := search.place(0); err != nil {
			return nil, err
		}
		if search.best.excessMS == 0 && search.best.shardCount == count {
			return search.bestBins, nil
		}
	}
	return search.bestBins, nil
}

type dcpapPackingScore struct {
	excessMS     int64
	shardCount   int
	makespanMS   int64
	setupProxyMS int64
	layout       string
}

func (score dcpapPackingScore) less(other dcpapPackingScore) bool {
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

// dcpapScore 计算 D-CPAP 的超额、分片数、关键路径和 canonical layout 评分。
func dcpapScore(bins []dcpapBin, target int64) (dcpapPackingScore, error) {
	// dcpapItem 已是不可拆的 critical-path 原子；setup proxy 对所有合法布局恒为零，
	// 仍显式保留在目标比较器中。
	score := dcpapPackingScore{shardCount: len(bins), setupProxyMS: 0}
	for _, bin := range bins {
		cost := bin.bodyMS
		if cost > target {
			excess := cost - target
			if excess > maxDCPAPInt64-score.excessMS {
				return dcpapPackingScore{}, errDCPAPDurationOverflow
			}
			score.excessMS += excess
		}
		if cost > score.makespanMS {
			score.makespanMS = cost
		}
	}
	var layout strings.Builder
	for _, bin := range bins {
		layout.WriteString(dcpapBinKey(bin))
		layout.WriteByte('|')
	}
	score.layout = layout.String()
	return score, nil
}

func dcpapCanPlace(bin dcpapBin, item dcpapItem, target int64) bool {
	cost := bin.bodyMS
	if len(bin.items) == 0 {
		return true
	}
	return cost <= target && item.durationMS+cost <= target
}

func dcpapResidual(bin dcpapBin, item dcpapItem, target int64) int64 {
	return target - (bin.bodyMS + item.durationMS)
}

func dcpapBinKey(bin dcpapBin) string {
	ids := make([]string, len(bin.items))
	for index, item := range bin.items {
		ids[index] = item.id
	}
	slices.Sort(ids)
	return strings.Join(ids, ",")
}

func dcpapCanonicalBins(bins []dcpapBin) []dcpapBin {
	result := make([]dcpapBin, 0, len(bins))
	for _, bin := range bins {
		if len(bin.items) != 0 {
			result = append(result, bin)
		}
	}
	result = cloneDCPAPBins(result)
	for index := range result {
		sort.Slice(result[index].items, func(left, right int) bool { return result[index].items[left].id < result[index].items[right].id })
	}
	sort.Slice(result, func(left, right int) bool { return dcpapBinKey(result[left]) < dcpapBinKey(result[right]) })
	return result
}

func cloneDCPAPBins(bins []dcpapBin) []dcpapBin {
	result := make([]dcpapBin, len(bins))
	for index, bin := range bins {
		result[index] = bin
		result[index].items = append([]dcpapItem(nil), bin.items...)
	}
	return result
}

func leastLoadedDCPAPBin(bins []dcpapBin) int {
	selected := 0
	for index := 1; index < len(bins); index++ {
		if bins[index].bodyMS < bins[selected].bodyMS || (bins[index].bodyMS == bins[selected].bodyMS && dcpapBinKey(bins[index]) < dcpapBinKey(bins[selected])) {
			selected = index
		}
	}
	return selected
}

// ValidateWorkloadCatalog 拒绝缺失、重复和不受支持的 workload 定义。
func ValidateWorkloadCatalog(catalog WorkloadCatalog) error {
	if catalog.Version != durationLedgerVersion {
		return fmt.Errorf("workload catalog version must equal %d", durationLedgerVersion)
	}
	if len(catalog.Workloads) == 0 {
		return errors.New("workload catalog must contain at least one workload")
	}
	seen := make(map[string]struct{}, len(catalog.Workloads))
	for index, workload := range catalog.Workloads {
		if err := validateWorkload(workload); err != nil {
			return fmt.Errorf("workloads[%d]: %w", index, err)
		}
		if _, exists := seen[workload.ID]; exists {
			return fmt.Errorf("workloads[%d].id: duplicate workload ID %q", index, workload.ID)
		}
		seen[workload.ID] = struct{}{}
	}
	return nil
}

// ValidateDurationLedger 拒绝不可分桶或无效时长的样本。
func ValidateDurationLedger(ledger DurationLedger) error {
	if ledger.Version != durationLedgerVersion {
		return fmt.Errorf("duration ledger version must equal %d", durationLedgerVersion)
	}
	if ledger.Calibration != nil {
		if err := ValidateDurationCalibration(*ledger.Calibration); err != nil {
			return fmt.Errorf("duration ledger calibration: %w", err)
		}
	}
	if ledger.ShardOverhead != nil {
		if err := ValidateShardOrchestrationOverhead(*ledger.ShardOverhead); err != nil {
			return fmt.Errorf("duration ledger shard overhead: %w", err)
		}
	}
	for index, sample := range ledger.Samples {
		if err := validateDurationBucket(sample.Bucket); err != nil {
			return fmt.Errorf("samples[%d].bucket: %w", index, err)
		}
		if sample.DurationMS <= 0 {
			return fmt.Errorf("samples[%d].duration_ms must be positive", index)
		}
		if err := validateDurationSampleTarget(sample); err != nil {
			return fmt.Errorf("samples[%d]: %w", index, err)
		}
	}
	return nil
}

// NewDurationLedger 返回首代校准可原子创建的空账本。
func NewDurationLedger() DurationLedger {
	return DurationLedger{Version: durationLedgerVersion, Samples: []DurationSample{}}
}

// ValidateDurationCalibration 拒绝不完整、不可比或非 UTC 的校准身份。
func ValidateDurationCalibration(calibration DurationCalibration) error {
	if err := validateCalibrationIdentity(calibration); err != nil {
		return err
	}
	if err := cicontract.ValidateCalibrationResources(calibration.CalibrationResourceClassID, calibration.CalibrationResourceCPU, calibration.CalibrationResourceMemoryGiB); err != nil {
		return fmt.Errorf("duration calibration resource: %w", err)
	}
	if err := validateDurationBucket(DurationBucket{
		WorkloadID: "calibration", CommandDigest: strings.Repeat("0", 64), InputDigest: "sha256:" + strings.Repeat("0", 64),
		Platform: calibration.Platform, Runner: calibration.Runner, Toolchain: calibration.Toolchain,
		ExecutionMode: DurationExecutionModeCalibration, ResourceClassID: calibration.CalibrationResourceClassID, ResourceCPU: calibration.CalibrationResourceCPU, ResourceMemoryGiB: calibration.CalibrationResourceMemoryGiB,
	}); err != nil {
		return err
	}
	if err := validateCalibrationCatalogs(calibration); err != nil {
		return err
	}
	if err := validateCalibrationEntrypoints(calibration); err != nil {
		return err
	}
	if err := validateCalibrationWorkloadCounts(calibration); err != nil {
		return err
	}
	if strings.TrimSpace(calibration.AcceptedSnapshotID) == "" {
		return errors.New("duration calibration accepted snapshot identity is required")
	}
	if calibration.CompletedAt.IsZero() || calibration.CompletedAt.Location() != time.UTC {
		return errors.New("duration calibration completion time must be UTC")
	}
	return nil
}

func validateCalibrationWorkloadCounts(calibration DurationCalibration) error {
	if calibration.WorkloadCount <= 0 || calibration.RacePackageCount <= 0 {
		return errors.New("duration calibration workload counts must be positive")
	}
	return nil
}

// validateCalibrationIdentity 校验 schema 与 Git commit/tree 身份。
func validateCalibrationIdentity(calibration DurationCalibration) error {
	if calibration.SchemaVersion != DurationCalibrationSchemaVersion {
		return errors.New("duration calibration schema is invalid")
	}
	if !validCalibrationOID(calibration.Commit) || !validCalibrationOID(calibration.Tree) {
		return errors.New("duration calibration Git identity is invalid")
	}
	return nil
}

// validateCalibrationCatalogs 校验 commit、push 和 release 目录摘要。
func validateCalibrationCatalogs(calibration DurationCalibration) error {
	if !isPrefixedSHA256Digest(calibration.CommitCatalogDigest) || !isPrefixedSHA256Digest(calibration.PushCatalogDigest) || !isPrefixedSHA256Digest(calibration.ReleaseCatalogDigest) {
		return errors.New("duration calibration catalog digest is invalid")
	}
	return nil
}

// validateCalibrationEntrypoints 保证首代校准绑定三条权威入口。
func validateCalibrationEntrypoints(calibration DurationCalibration) error {
	if calibration.CommitEntrypoint != CIEntrypointGitPreCommit || calibration.PushEntrypoint != CIEntrypointGitPrePush || calibration.ReleaseEntrypoint != CIEntrypointRelease {
		return errors.New("duration calibration entrypoints are not authoritative")
	}
	return nil
}

func validCalibrationOID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	return strings.Trim(value, "0123456789abcdef") == ""
}

func decodeStrictJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON payload")
	}
	return nil
}

// validateWorkload 校验单个 workload 的稳定身份和 bootstrap 时长。
func validateWorkload(workload Workload) error {
	if strings.TrimSpace(workload.ID) == "" {
		return errors.New("id must not be empty")
	}
	if !isKnownWorkloadKind(workload.Kind) {
		return fmt.Errorf("kind %q is not supported", workload.Kind)
	}
	if !isSHA256Digest(workload.CommandDigest) {
		return errors.New("command_digest must be a lowercase SHA-256 hex digest")
	}
	if workload.InputDigest != "" && !isPrefixedSHA256Digest(workload.InputDigest) {
		return errors.New("input_digest must be a prefixed SHA-256 digest when present")
	}
	if workload.BootstrapEstimateMS <= 0 {
		return errors.New("bootstrap_estimate_ms must be positive")
	}
	return nil
}

// isSHA256Digest 只接受固定长度的小写 SHA-256 十六进制摘要，避免账本混入不稳定命令标识。
func isSHA256Digest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validateDurationBucket(bucket DurationBucket) error {
	if err := validateDurationBucketIdentity(bucket); err != nil {
		return err
	}
	if err := validateDurationBucketResourceNumbers(bucket); err != nil {
		return err
	}
	return validateDurationBucketResourceClass(bucket)
}

// validateDurationBucketIdentity 校验 workload、命令、输入和环境身份。
func validateDurationBucketIdentity(bucket DurationBucket) error {
	if strings.TrimSpace(bucket.WorkloadID) == "" {
		return errors.New("workload_id must not be empty")
	}
	if !isSHA256Digest(bucket.CommandDigest) {
		return errors.New("command_digest must be a lowercase SHA-256 hex digest")
	}
	if !isPrefixedSHA256Digest(bucket.InputDigest) {
		return errors.New("input_digest must be a prefixed SHA-256 digest")
	}
	if err := validateDurationEnvironment(bucket.Platform, bucket.Runner, bucket.Toolchain); err != nil {
		return err
	}
	if bucket.ExecutionMode != DurationExecutionModeNormal && bucket.ExecutionMode != DurationExecutionModeCalibration {
		return fmt.Errorf("execution_mode %q is unsupported", bucket.ExecutionMode)
	}
	if strings.TrimSpace(bucket.ResourceClassID) == "" || bucket.ResourceClassID != strings.TrimSpace(bucket.ResourceClassID) {
		return errors.New("resource_class_id must not be empty or padded")
	}
	return nil
}

func validateDurationBucketResourceNumbers(bucket DurationBucket) error {
	if math.IsNaN(bucket.ResourceCPU) || math.IsInf(bucket.ResourceCPU, 0) || math.IsNaN(bucket.ResourceMemoryGiB) || math.IsInf(bucket.ResourceMemoryGiB, 0) {
		return errors.New("resource CPU and memory must be finite")
	}
	return nil
}

func validateDurationBucketResourceClass(bucket DurationBucket) error {
	if bucket.ExecutionMode == DurationExecutionModeCalibration {
		if err := cicontract.ValidateCalibrationResources(bucket.ResourceClassID, bucket.ResourceCPU, bucket.ResourceMemoryGiB); err != nil {
			return fmt.Errorf("calibration resource: %w", err)
		}
		return nil
	}
	if !isNormalResourceIdentity(bucket.ResourceCPU, bucket.ResourceMemoryGiB) {
		return errors.New("normal resource must be exactly 2C/4GiB, 4C/8GiB, or 8C/16GiB")
	}
	return nil
}

func validateDurationEnvironment(platform, runner, toolchain string) error {
	fields := []struct {
		name  string
		value string
	}{
		{name: "platform", value: platform},
		{name: "runner", value: runner},
		{name: "toolchain", value: toolchain},
	}
	for _, field := range fields {
		trimmed := strings.TrimSpace(field.value)
		if trimmed == "" {
			return fmt.Errorf("%s must not be empty", field.name)
		}
		if trimmed != field.value {
			return fmt.Errorf("%s must not contain leading or trailing whitespace", field.name)
		}
	}
	return nil
}

// ValidateDurationBucket 向远程 producer 暴露严格的 duration identity 守卫。
func ValidateDurationBucket(bucket DurationBucket) error {
	return validateDurationBucket(bucket)
}

// isNormalResourceIdentity 判断资源是否属于 normal 的三个固定规格。
func isNormalResourceIdentity(cpu, memoryGiB float64) bool {
	return (cpu == 2 && memoryGiB == 4) ||
		(cpu == 4 && memoryGiB == 8) ||
		(cpu == 8 && memoryGiB == 16)
}

func estimateOwnerWorkloadDurationMS(catalog WorkloadCatalog, index DurationSampleIndex) (int64, error) {
	var total int64
	for _, workload := range catalog.Workloads {
		if workload.Shardable {
			continue
		}
		estimate, err := index.EstimateWorkloadDurationMS(workload)
		if err != nil {
			return 0, err
		}
		if estimate > int64(^uint64(0)>>1)-total {
			return 0, errors.New("owner workload duration estimate overflows")
		}
		total += estimate
	}
	return total, nil
}
