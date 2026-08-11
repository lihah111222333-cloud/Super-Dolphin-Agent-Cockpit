package cicontract

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// WorkloadEstimatorPolicyID 是 gate DurationEstimatorPolicyID 的唯一协议 owner。
// gate 只保留同名 alias 并把它作为参数传入 canonical estimation material。
const WorkloadEstimatorPolicyID = "deterministic-duration-estimator/v2"

// WorkloadEstimationPolicyMaterial 是计划估时摘要的 canonical 输入。
// 它包含 policy version、gate estimator ID 和 PlanningContext 的全部稳定字段；
// 不允许生产 caller 只传一个任意摘要逃过策略绑定。
type WorkloadEstimationPolicyMaterial struct {
	PolicyVersion                 string  `json:"policy_version"`
	EstimatorPolicyID             string  `json:"estimator_policy_id"`
	Platform                      string  `json:"platform"`
	Runner                        string  `json:"runner"`
	Toolchain                     string  `json:"toolchain"`
	Calibration                   bool    `json:"calibration"`
	CalibrationResourceClassID    string  `json:"calibration_resource_class_id"`
	CalibrationResourceCPU        float64 `json:"calibration_resource_cpu"`
	CalibrationResourceMemoryGiB  float64 `json:"calibration_resource_memory_gib"`
	TargetDurationMS              int64   `json:"target_duration_ms"`
	AcceptedSnapshotID            string  `json:"accepted_snapshot_id"`
	ShardOverheadP95MS            int64   `json:"shard_overhead_p95_ms"`
	ShardOverheadSampleCount      int     `json:"shard_overhead_sample_count"`
	ShardOverheadProvenanceDigest string  `json:"shard_overhead_provenance_digest"`
}

// WorkloadEstimationPolicyInput 是 material 的语义别名，便于调用方表达参数化 owner。
type WorkloadEstimationPolicyInput = WorkloadEstimationPolicyMaterial

// Validate 校验 canonical estimation material 的字段边界。
func (material WorkloadEstimationPolicyMaterial) Validate() error {
	if err := validateWorkloadEstimationPolicyIdentity(material); err != nil {
		return err
	}
	if err := validateWorkloadEstimationEnvironment(material); err != nil {
		return err
	}
	if err := validateWorkloadEstimationResources(material); err != nil {
		return err
	}
	return validateWorkloadEstimationOverhead(material)
}

// validateWorkloadEstimationPolicyIdentity 校验 policy 版本和 gate estimator 身份。
func validateWorkloadEstimationPolicyIdentity(material WorkloadEstimationPolicyMaterial) error {
	if material.PolicyVersion != WorkloadEstimationPolicyVersion {
		return fmt.Errorf("remote workload estimation policy version %q is not accepted %q", material.PolicyVersion, WorkloadEstimationPolicyVersion)
	}
	if strings.TrimSpace(material.EstimatorPolicyID) != material.EstimatorPolicyID || material.EstimatorPolicyID != WorkloadEstimatorPolicyID {
		return fmt.Errorf("remote workload estimator policy ID %q is not accepted", material.EstimatorPolicyID)
	}
	return nil
}

// validateWorkloadEstimationEnvironment 校验稳定环境字段与目标时长。
func validateWorkloadEstimationEnvironment(material WorkloadEstimationPolicyMaterial) error {
	for name, value := range map[string]string{
		"platform": material.Platform, "runner": material.Runner,
		"toolchain": material.Toolchain, "accepted_snapshot_id": material.AcceptedSnapshotID,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("remote workload estimation %s is required and canonical", name)
		}
	}
	if material.TargetDurationMS != int64(ShardTargetDuration/time.Millisecond) || material.ShardOverheadP95MS < 0 || material.ShardOverheadSampleCount < 0 {
		return errors.New("remote workload estimation duration and overhead values are invalid")
	}
	return nil
}

// validateWorkloadEstimationResources 校验 normal/calibration 资源身份的互斥边界。
func validateWorkloadEstimationResources(material WorkloadEstimationPolicyMaterial) error {
	if err := validateWorkloadEstimationNumericResources(material); err != nil {
		return err
	}
	if material.Calibration {
		return validateCalibrationWorkloadEstimationResource(material)
	}
	return validateNormalWorkloadEstimationResource(material)
}

// validateWorkloadEstimationNumericResources 拒绝 NaN、Inf 和负资源数值。
func validateWorkloadEstimationNumericResources(material WorkloadEstimationPolicyMaterial) error {
	for name, value := range map[string]float64{
		"calibration_resource_cpu":        material.CalibrationResourceCPU,
		"calibration_resource_memory_gib": material.CalibrationResourceMemoryGiB,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("remote workload estimation %s is invalid", name)
		}
	}
	return nil
}

// validateCalibrationWorkloadEstimationResource 校验固定校准资源的完整身份。
func validateCalibrationWorkloadEstimationResource(material WorkloadEstimationPolicyMaterial) error {
	if err := ValidateCalibrationResources(material.CalibrationResourceClassID, material.CalibrationResourceCPU, material.CalibrationResourceMemoryGiB); err != nil {
		return fmt.Errorf("calibration workload estimation resource identity: %w", err)
	}
	return nil
}

// validateNormalWorkloadEstimationResource 确认 normal material 不携带校准身份。
func validateNormalWorkloadEstimationResource(material WorkloadEstimationPolicyMaterial) error {
	if material.CalibrationResourceClassID != "" || material.CalibrationResourceCPU != 0 || material.CalibrationResourceMemoryGiB != 0 {
		return errors.New("normal workload estimation must not carry calibration resource identity")
	}
	return nil
}

// validateWorkloadEstimationOverhead 校验 overhead 样本计数和来源摘要的配对。
func validateWorkloadEstimationOverhead(material WorkloadEstimationPolicyMaterial) error {
	overheadEmpty := material.ShardOverheadP95MS == 0 && material.ShardOverheadSampleCount == 0 && material.ShardOverheadProvenanceDigest == ""
	if !overheadEmpty && (material.ShardOverheadSampleCount <= 0 || !isCanonicalSHA256(material.ShardOverheadProvenanceDigest)) {
		return errors.New("workload estimation overhead provenance is incomplete")
	}
	if material.ShardOverheadP95MS >= material.TargetDurationMS {
		return errors.New("workload estimation overhead must leave a positive planning budget")
	}
	return nil
}

// WorkloadEstimationPolicyDigest 返回完整 material 的 canonical 摘要。
func WorkloadEstimationPolicyDigest(material WorkloadEstimationPolicyMaterial) (string, error) {
	if err := material.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal workload estimation policy material: %w", err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(encoded)), nil
}

// ValidateWorkloadEstimationPolicyDigest 拒绝不匹配 policy、estimator 和 PlanningContext 的摘要。
func ValidateWorkloadEstimationPolicyDigest(material WorkloadEstimationPolicyMaterial, digest string) error {
	expected, err := WorkloadEstimationPolicyDigest(material)
	if err != nil {
		return err
	}
	if digest != expected {
		return errors.New("remote workload estimation policy digest does not match canonical material")
	}
	return nil
}

// WorkloadPlanningPolicyMaterial 是 D-CPAP planner 的 canonical policy 输入。
// 阈值、修复边界和 solver mode 进入计划摘要，禁止 planner 私自漂移。
type WorkloadPlanningPolicyMaterial struct {
	ExactPackableUnitThreshold        int    `json:"exact_packable_unit_threshold"`
	ExactSearchNodeBudget             int    `json:"exact_search_node_budget"`
	ExactSolverModeID                 string `json:"exact_solver_mode_id"`
	HeuristicSolverModeID             string `json:"heuristic_solver_mode_id"`
	IsolatedSolverModeID              string `json:"isolated_solver_mode_id"`
	HeuristicMaxTwoMoveTransitions    int    `json:"heuristic_max_two_move_transitions"`
	HeuristicMaxThreeCycleTransitions int    `json:"heuristic_max_three_cycle_transitions"`
	HeuristicBeamWidth                int    `json:"heuristic_beam_width"`
	HeuristicBeamDepth                int    `json:"heuristic_beam_depth"`
	HeuristicMaxBeamTransitions       int    `json:"heuristic_max_beam_transitions"`
}

// CanonicalWorkloadPlanningPolicyMaterial 返回唯一 D-CPAP policy material。
func CanonicalWorkloadPlanningPolicyMaterial() WorkloadPlanningPolicyMaterial {
	return WorkloadPlanningPolicyMaterial{
		ExactPackableUnitThreshold:        WorkloadPlanningExactPackableUnitThreshold,
		ExactSearchNodeBudget:             WorkloadPlanningSearchNodeBudget,
		ExactSolverModeID:                 WorkloadPlanningExactSolverModeID,
		HeuristicSolverModeID:             WorkloadPlanningHeuristicSolverModeID,
		IsolatedSolverModeID:              WorkloadPlanningIsolatedSolverModeID,
		HeuristicMaxTwoMoveTransitions:    WorkloadPlanningHeuristicMaxTwoMoveTransitions,
		HeuristicMaxThreeCycleTransitions: WorkloadPlanningHeuristicMaxThreeCycleTransitions,
		HeuristicBeamWidth:                WorkloadPlanningHeuristicBeamWidth,
		HeuristicBeamDepth:                WorkloadPlanningHeuristicBeamDepth,
		HeuristicMaxBeamTransitions:       WorkloadPlanningHeuristicMaxBeamTransitions,
	}
}

// Validate 校验 D-CPAP 规划策略字段与冻结的 canonical policy 一致。
func (material WorkloadPlanningPolicyMaterial) Validate() error {
	if err := validateWorkloadPlanningThresholds(material); err != nil {
		return err
	}
	if err := validateWorkloadPlanningSolverModes(material); err != nil {
		return err
	}
	return validateWorkloadPlanningRepairBounds(material)
}

// validateWorkloadPlanningThresholds 校验 exact 分支阈值和搜索节点预算。
func validateWorkloadPlanningThresholds(material WorkloadPlanningPolicyMaterial) error {
	if material.ExactPackableUnitThreshold != WorkloadPlanningExactPackableUnitThreshold || material.ExactPackableUnitThreshold <= 0 {
		return fmt.Errorf("remote workload planning exact threshold %d is not accepted", material.ExactPackableUnitThreshold)
	}
	if material.ExactSearchNodeBudget != WorkloadPlanningSearchNodeBudget || material.ExactSearchNodeBudget <= 0 {
		return fmt.Errorf("remote workload planning exact search node budget %d is not accepted", material.ExactSearchNodeBudget)
	}
	return nil
}

// validateWorkloadPlanningSolverModes 校验 solver mode 标识非空、canonical 且与 owner identity 一致。
func validateWorkloadPlanningSolverModes(material WorkloadPlanningPolicyMaterial) error {
	for name, value := range map[string]string{"exact_solver_mode_id": material.ExactSolverModeID, "heuristic_solver_mode_id": material.HeuristicSolverModeID, "isolated_solver_mode_id": material.IsolatedSolverModeID} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("remote workload planning %s is required and canonical", name)
		}
	}
	if material.ExactSolverModeID != WorkloadPlanningExactSolverModeID || material.HeuristicSolverModeID != WorkloadPlanningHeuristicSolverModeID || material.IsolatedSolverModeID != WorkloadPlanningIsolatedSolverModeID {
		return errors.New("remote workload planning solver mode identity is not accepted")
	}
	return nil
}

// validateWorkloadPlanningRepairBounds 校验 bounded repair 的 transition、beam width 和 depth 上限。
func validateWorkloadPlanningRepairBounds(material WorkloadPlanningPolicyMaterial) error {
	if material.HeuristicMaxTwoMoveTransitions != WorkloadPlanningHeuristicMaxTwoMoveTransitions || material.HeuristicMaxThreeCycleTransitions != WorkloadPlanningHeuristicMaxThreeCycleTransitions || material.HeuristicBeamWidth != WorkloadPlanningHeuristicBeamWidth || material.HeuristicBeamDepth != WorkloadPlanningHeuristicBeamDepth || material.HeuristicMaxBeamTransitions != WorkloadPlanningHeuristicMaxBeamTransitions {
		return errors.New("remote workload planning repair bounds are not accepted")
	}
	return nil
}

// WorkloadPlanningPolicyDigest 返回完整 D-CPAP policy material 的 canonical 摘要。
func WorkloadPlanningPolicyDigest() (string, error) {
	material := CanonicalWorkloadPlanningPolicyMaterial()
	if err := material.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal workload planning policy material: %w", err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(encoded)), nil
}

// ValidateWorkloadPlanningPolicyDigest 校验持久化计划绑定 canonical D-CPAP policy。
func ValidateWorkloadPlanningPolicyDigest(digest string) error {
	expected, err := WorkloadPlanningPolicyDigest()
	if err != nil {
		return err
	}
	if digest != expected {
		return errors.New("remote workload planning policy digest does not match canonical material")
	}
	return nil
}

// ValidateWorkloadPlanContract 校验计划 schema、算法、目标、D-CPAP policy 和 canonical 估时 material。
func ValidateWorkloadPlanContract(schemaVersion uint32, algorithmID, objectiveDigest, planningPolicyDigest, estimationPolicyDigest string, material WorkloadEstimationPolicyMaterial) error {
	if schemaVersion != WorkloadExecutionPlanSchemaVersion {
		return fmt.Errorf("remote workload execution plan schema %d is not accepted schema %d", schemaVersion, WorkloadExecutionPlanSchemaVersion)
	}
	if algorithmID != WorkloadPlanningAlgorithmID {
		return fmt.Errorf("remote workload execution plan algorithm %q is not accepted", algorithmID)
	}
	if objectiveDigest != WorkloadPlanningObjectiveDigest() {
		return errors.New("remote workload execution plan objective digest does not match canonical objective")
	}
	if err := ValidateWorkloadPlanningPolicyDigest(planningPolicyDigest); err != nil {
		return fmt.Errorf("remote workload execution plan planning policy: %w", err)
	}
	if err := ValidateWorkloadEstimationPolicyDigest(material, estimationPolicyDigest); err != nil {
		return fmt.Errorf("remote workload execution plan estimation policy: %w", err)
	}
	return nil
}

// WorkloadPlanningObjectiveDigest 返回 canonical objective 的内容摘要，供 gate
// producer 与架构守卫复用，避免复制目标字符串后产生漂移。
func WorkloadPlanningObjectiveDigest() string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(WorkloadPlanningObjective)))
}

// ValidateRemoteCIProtocolVersions 锁定 accepted bootstrap、临时 manifest、current
// request 与 worker manifest 的唯一滚动矩阵；任何旧版本、协商或 fallback 都必须在 decoder 边界拒绝。
func ValidateRemoteCIProtocolVersions(bootstrapSchema, bootstrapCompileGroupSchema, bootstrapManifestSchema, shardRequestSchema, manifestSchema uint32) error {
	if bootstrapSchema != AcceptedBootstrapRequestSchemaVersion || bootstrapCompileGroupSchema != AcceptedCompileGroupSchemaVersion || bootstrapManifestSchema != AcceptedBootstrapManifestSchemaVersion {
		return fmt.Errorf("remote CI accepted bootstrap schema matrix must be %d/nested %d/manifest %d", AcceptedBootstrapRequestSchemaVersion, AcceptedCompileGroupSchemaVersion, AcceptedBootstrapManifestSchemaVersion)
	}
	if shardRequestSchema != ShardRequestSchemaVersion || manifestSchema != ShardExecutionManifestSchemaVersion {
		return fmt.Errorf("remote CI current request/manifest schema matrix must be %d/%d", ShardRequestSchemaVersion, ShardExecutionManifestSchemaVersion)
	}
	return nil
}

// ValidateWorkloadPassEnvironmentSchema 严格绑定 PASS correctness environment v10，
// 防止旧摘要在 SQLite identity 查询中被当成等价证据。
func ValidateWorkloadPassEnvironmentSchema(schema string) error {
	if schema != WorkloadPassEnvironmentSchemaVersion {
		return fmt.Errorf("remote workload PASS environment schema %q is retired; want %q", schema, WorkloadPassEnvironmentSchemaVersion)
	}
	return nil
}

// ValidateRetentionGenerations 拒绝偏离统一的三代 SQLite 历史窗口。
func ValidateRetentionGenerations() error {
	if RetentionGenerations != 3 {
		return errors.New("remote CI SQLite retention generation count drifted from the accepted contract")
	}
	if AcceptedGenerationColumn != "accepted_generation" {
		return errors.New("remote CI SQLite retention generation column drifted from the accepted contract")
	}
	retentionBindings := retentionRootBindingList()
	if len(retentionBindings) != 7 {
		return fmt.Errorf("remote CI SQLite retention must own exactly seven historical roots, got %d", len(retentionBindings))
	}
	authorityBindings := sqlAuthorityBindingList()
	authorityTables := make(map[string]struct{}, len(authorityBindings))
	for _, binding := range authorityBindings {
		authorityTables[binding.Table] = struct{}{}
	}
	seenRoots := make(map[string]struct{}, len(retentionBindings))
	for _, binding := range retentionBindings {
		if strings.TrimSpace(binding.Table) == "" || binding.GenerationColumn != AcceptedGenerationColumn {
			return fmt.Errorf("remote CI SQLite retention root %+v is invalid", binding)
		}
		if _, exists := authorityTables[binding.Table]; !exists {
			return fmt.Errorf("remote CI SQLite retention root %q is not a SQL authority table", binding.Table)
		}
		if _, exists := seenRoots[binding.Table]; exists {
			return fmt.Errorf("remote CI SQLite retention root %q is duplicated", binding.Table)
		}
		seenRoots[binding.Table] = struct{}{}
	}
	return nil
}

// ValidateWorkloadPassEvidenceGeneration 校验 WorkloadPassEvidenceFreshnessPolicy 的代际部分。
// freshness 不使用 wall-clock TTL；调用方还必须核验权威状态、完整 identity 和 canonical receipt proof。
// future、零值和超过当前 accepted generation 前两代窗口的 evidence 必须视为 miss，而不能降级复用。
func ValidateWorkloadPassEvidenceGeneration(acceptedGeneration, evidenceGeneration uint64) error {
	if acceptedGeneration == 0 || evidenceGeneration == 0 {
		return errors.New("remote CI workload PASS evidence requires accepted and evidence generations")
	}
	if evidenceGeneration > acceptedGeneration {
		return errors.New("remote CI workload PASS evidence generation is in the future")
	}
	if acceptedGeneration-evidenceGeneration >= WorkloadPassEvidenceGenerationWindow {
		return fmt.Errorf("remote CI workload PASS evidence generation %d is outside accepted generation %d reuse window", evidenceGeneration, acceptedGeneration)
	}
	return nil
}

// ValidateNormalResources 拒绝 generation-one 内容检查使用 calibration 或未登记的资源规格。
func ValidateNormalResources(cpu, memoryGiB float64) error {
	if cpu <= 0 || memoryGiB <= 0 {
		return errors.New("remote CI normal resource CPU and memory are required")
	}
	if !((cpu == 2 && memoryGiB == 4) || (cpu == 4 && memoryGiB == 8) || (cpu == 8 && memoryGiB == 16)) {
		return errors.New("remote CI normal resources must use exactly 2 vCPU/4 GiB, 4 vCPU/8 GiB, or 8 vCPU/16 GiB")
	}
	return nil
}

// ValidateCalibrationResources 拒绝缺失或不可被回执精确绑定的固定规格。
func ValidateCalibrationResources(classID string, cpu, memoryGiB float64) error {
	if strings.TrimSpace(classID) == "" || classID != strings.TrimSpace(classID) || cpu <= 0 || memoryGiB <= 0 {
		return errors.New("remote CI calibration class, CPU, and memory are required")
	}
	if classID == "medium" {
		return errors.New("remote CI calibration resource class ID must remain independent from the normal medium class")
	}
	if cpu != CalibrationResourceCPU || memoryGiB != CalibrationResourceMemoryGiB {
		return errors.New("remote CI calibration resources must be exactly 4 vCPU and 8 GiB")
	}
	return nil
}

// ValidateECIMultiZoneScheduling 校验 ECI 请求只能使用两到十个不同 vSwitch 的随机多区调度。
func ValidateECIMultiZoneScheduling(strategy string, vSwitches []ECIVSwitch) error {
	if strategy != ECIMultiZoneScheduleStrategy {
		return errors.New("remote CI ECI schedule strategy must be VSwitchRandom")
	}
	if len(vSwitches) < ECIMinVSwitchCount || len(vSwitches) > ECIMaxVSwitchCount {
		return errors.New("remote CI requires two to ten vSwitch IDs for multi-zone scheduling")
	}
	zoneCount, err := validateECIVSwitchBindings(vSwitches)
	if err != nil {
		return err
	}
	if zoneCount < 2 {
		return errors.New("remote CI vSwitches must cover at least two zones")
	}
	return nil
}

// validateECIVSwitchBindings 校验 ECI vSwitch 集合的标识、可用区和唯一性。
func validateECIVSwitchBindings(vSwitches []ECIVSwitch) (int, error) {
	seenIDs := make(map[string]struct{}, len(vSwitches))
	seenZones := make(map[string]struct{}, len(vSwitches))
	for _, vSwitch := range vSwitches {
		if strings.TrimSpace(vSwitch.ID) != vSwitch.ID || !strings.HasPrefix(vSwitch.ID, "vsw-") || len(vSwitch.ID) <= len("vsw-") {
			return 0, errors.New("remote CI vSwitch ID is invalid")
		}
		if strings.TrimSpace(vSwitch.ZoneID) != vSwitch.ZoneID || !strings.HasPrefix(vSwitch.ZoneID, "cn-") {
			return 0, errors.New("remote CI vSwitch zone ID is invalid")
		}
		if _, duplicate := seenIDs[vSwitch.ID]; duplicate {
			return 0, errors.New("remote CI vSwitch IDs must be unique")
		}
		seenIDs[vSwitch.ID] = struct{}{}
		seenZones[vSwitch.ZoneID] = struct{}{}
	}
	return len(seenZones), nil
}

// CanonicalMarkdown 返回必须逐字嵌入 Accepted 文档的代码契约映射块。
func CanonicalMarkdown() string {
	var builder strings.Builder
	builder.WriteString("<!-- cicontract:begin -->\n| ID | 章节 | 代码约束 | 执行层 |\n")
	builder.WriteString("| --- | --- | --- | --- |\n")
	for _, requirement := range requirements {
		fmt.Fprintf(&builder, "| `%s` | §%d | %s | `%s` |\n", requirement.ID, requirement.Section, requirement.Summary, requirement.Enforcement)
	}
	builder.WriteString("<!-- cicontract:end -->")
	return builder.String()
}

// CanonicalSQLiteSchemaMarkdown 返回 Accepted 文档头部必须逐字嵌入的物理 schema 身份。
func CanonicalSQLiteSchemaMarkdown() string {
	return fmt.Sprintf("<!-- cicontract:sqlite-schema:begin -->%cduration-ledger SQLite physical schema：`%d`%c<!-- cicontract:sqlite-schema:end -->", '\n', DurationLedgerSQLiteSchemaVersion, '\n')
}

// CanonicalRetentionMarkdown 返回 accepted 文档必须逐字嵌入的有界增长策略。
func CanonicalRetentionMarkdown() string {
	return fmt.Sprintf(`<!-- cicontract:retention:begin -->
%[1]s 是唯一 retention 常量 owner。duration samples、shard overhead aggregates、逐分片 overhead samples、catalog observations、runs、strict workload PASS evidence 与 calibration checkpoints 七个历史根都必须绑定已验证的 accepted baseline generation；每个根写事务必须先读取同一 SQLite authority 的 accepted singleton，拒绝零值、无 authority、无效 authority 与晚于当前 accepted generation 的伪造未来代。已启动的旧 accepted generation 运行仍可在完成时写入。七个根的 distinct generation 并集按数值确定全库唯一保留集合，任何根都不得保留该集合之外的数据。每一代可包含任意数量的 workload、sample、shard、timing、receipt 或 scenario，禁止用固定行数限制代码和测试增长。SQLite 只保留最新 %[2]d 个有数据的 generation；第四个有数据代首次成功写入时，必须在同一事务内淘汰最老一代全部历史根及其 cascade 子数据。

100 秒结构化 timing warning 只能沿同一 SQLite authority 的互斥生命周期流转：ci_live_timing_warnings 只暂存仍在运行的 provider StartTime 事实，run finalizer 必须在同一事务精确吸收到 ci_run_timing_warnings 并删除对应 live 行；不得预写或伪造 ci_runs 失败终态，也不得让 live 与 final 行同时存在。live 表不是第八个历史根或第二真相源，不参与七根 generation 并集；唯一 compactor 必须按已校验 accepted singleton 的 current/current-2 数值窗口保留 active 行并清理崩溃残留。

唯一 compactDurationLedgerAuthority 只能在既有成功写事务的 commit 前同步调用，禁止 timer、goroutine、后台 GC 或第二入口。generation 按数值排序，不能用行数、时间戳或插入顺序冒充；无法证明 generation 的旧行必须 fail-fast，不能默认绑定当前代。删除旧 run 依靠 FK cascade 同步删除 requester、shard/workload、execution、timing、warning 与 receipt；删除旧 checkpoint 同步删除任意数量 scenario；catalog 内容只有在不再被保留代 observation/run 引用时才能删除。SQLite authority 必须使用 FULL auto-vacuum，让每次成功淘汰在同一提交边界自动归还空页；无生产读取者且重复保存完整 run payload 的 raw observation event 表、索引、触发器和旧 schema migration 入口均已退役，禁止恢复。accepted baseline 是当前状态 singleton，duration meta/calibration 与 query meta 不是历史代，不参与淘汰。
<!-- cicontract:retention:end -->`, "`cicontract`", RetentionGenerations)
}

// CanonicalSchedulingMarkdown 返回 accepted 文档必须逐字嵌入的多可用区调度语义。
func CanonicalSchedulingMarkdown() string {
	return `<!-- cicontract:scheduling:begin -->
所有 normal、校准与首代内容检查 ECI container group 必须使用配置中 2 到 10 个不同 vSwitch，并显式绑定每个 vSwitch 的 zone_id；集合必须覆盖至少两个可用区。CreateContainerGroup 必须把全部 ID 以阿里云原生逗号列表传入 VSwitchId，并固定 ScheduleStrategy=VSwitchRandom。禁止单 vSwitch、多个同区 vSwitch、单区失败重试、串行 fallback 或用并发上限掩盖 NoStock；调度库存等待必须作为 provider eci_wait 记录。
<!-- cicontract:scheduling:end -->`
}

// CanonicalTimingMarkdown 返回 accepted 文档必须逐字嵌入的精确耗时语义。
func CanonicalTimingMarkdown() string {
	return `<!-- cicontract:timing:begin -->
每条 measured observation 必须在同一 SQLite authority 保存真实 started_at、completed_at 与 duration_ms；统一账本分辨率是 1ms，实际为正但不足 1ms 的 worker startup/test_body 阶段必须在唯一计时生产者处量化为 1ms，禁止向下截断成表示缺失的 0ms；仅当量化后的两个串行阶段超出真实 workload total 时，生产者才可将 workload completed_at 向上规范化到恰好覆盖二者，禁止改写 started_at 或扩大到额外整数毫秒。并发 selector 的 test_body 必须以 top-level run→pause→cont→terminal 事件中的 cont 时间为起点，run→pause 排队等待不得计入 workload test_body；测试进程仍保持并发，shard/run wall time 继续保留真实起止与关键路径。raw 和 critical_path 的 duration_ms 必须严格等于按该分辨率规范化后的区间长度。interval_union 的 duration_ms 必须是全部原始 workload 子区间的精确并集：重叠只计一次、空隙不计入，禁止用最早开始到最晚结束的 envelope 冒充活跃耗时。

workload 的 startup、test_body 与 total 是 raw；shard 的 startup 与 test_body 是 workload raw 区间的 interval_union，shard/run total 是 critical_path。每个 compile group 另以 test_binary_compile raw observation 记录一次，scope=compile_group，包含 group/artifact identity、真实起止、Go cache hit/miss/put、artifact digest/size/status；该时间不得写入 workload startup/test_body/total，也不得与 candidate Gate CLI 的 candidate_compile 合并或重复计数。每个 calibration-resource shard 的 orchestration overhead 必须按 v2 accounted-interval-union 计算：从 shard total interval 中扣除 workload total、shard eci_wait/source_materialize/candidate_compile 以及 compile-group test_binary_compile 的全部 measured 区间精确并集，重叠只扣一次、间隙保留为真实编排开销；禁止用最早 workload 到最晚 workload 的 envelope 把上述已单独计量阶段重新算作 overhead。aggregate 使用 nearest-rank P95，并把 accounted duration/count、workload envelope、完整样本事实、accepted generation、snapshot 与 4C/8GiB 资源身份写入同一 SQLite authority；缺少任一必需 shard 阶段、区间越过 shard total、重复 workload/compile-group 身份或旧 v1 policy 必须 fail-fast。eci_wait 只能使用 ECI provider 返回的 CreationTime 到 materializer CurrentState.StartTime；shard total 终点必须取同一终态响应中 container-group SucceededTime/FailedTime 与唯一 worker CurrentState.FinishTime 的较晚者，两者都属于 provider lifecycle evidence。阿里云 ECI 已返回终态但 CreationTime、materializer CurrentState.StartTime、SucceededTime/FailedTime 或唯一 worker CurrentState.FinishTime 尚未同步时，只允许沿同一 Describe 路径按 PollInterval 对该分片有界重读最多 3 次；重读期间不得伪造时间、消费报告、移出 pending、取消兄弟分片或跳过清理，窗口耗尽后缺失任一项必须 fail-fast。禁止用 worker 日志、report 端点、本地请求或轮询时间替换 provider 终态；任一真实子阶段仍越过该 provider envelope 时保持 provisional NOT_VERIFIED。本地请求、轮询或日志时间不得写成权威耗时。所有 cache evidence 与阶段观测绑定，人类账本只能读取同一事务已提交的 SQLite observations。compile timing history 只能按 PackageTarget、SemanticKey、Platform、RunnerIdentityDigest、ToolchainDigest、ExecutionMode 与 ResourceClassID/CPU/Memory 完整 identity 查询；只允许最近三个 accepted generation 中 authoritative、passed、cleanup-complete、measured/raw 的真实 compile-group observation，source tree、shared input 与 artifact digest 不得跨 identity 混用。normal 无历史固定 2C/4GiB，owner fixed-point 发生在 PlannedWorkload 创建前，shared compile cost 每组只计一次且不写入 selector body；calibration 固定 4C/8GiB。
<!-- cicontract:timing:end -->`
}
