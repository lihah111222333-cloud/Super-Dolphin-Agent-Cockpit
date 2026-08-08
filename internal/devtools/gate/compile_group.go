package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// CompileGroupSchemaVersion 是 package-affinity 编译组 strict JSON 的版本 owner。
const CompileGroupSchemaVersion uint32 = 2

// CompileGroupBatchTargetMS 是批次关键路径的规划目标；超出时只记录告警，
// 不转换为执行超时或校验失败。
const CompileGroupBatchTargetMS int64 = 100_000

const (
	// CompileGroupSemanticGoTestNormal 是普通精确 Go test selector 的唯一语义键。
	CompileGroupSemanticGoTestNormal = "go-test-selector/v1/race=false"
	// CompileGroupSemanticGoTestRace 是 race 精确 Go test selector 的唯一语义键。
	CompileGroupSemanticGoTestRace = "go-test-selector/v1/race=true"
	// CompileGroupSemanticGoBenchmark 是普通精确 Go benchmark selector 的唯一语义键。
	CompileGroupSemanticGoBenchmark = "go-benchmark-selector/v1/race=false"
)

// CompileGroup 描述同一 shard 内一次 Go test 二进制编译及其 selector 集合。
//
// CompileArtifactKey 只由编译语义和共享输入组成，不包含 selector 成员或时长估算；
// GroupID 再绑定有序 WorkloadIDs，以证明计划覆盖和保持 worker 的 selector 顺序。
type CompileGroup struct {
	GroupID             string                    `json:"group_id"`
	PackageTarget       string                    `json:"package_target"`
	SemanticKey         string                    `json:"semantic_key"`
	SharedInputDigest   string                    `json:"shared_input_digest"`
	ProfileDigest       string                    `json:"profile_digest"`
	ResourceClassID     string                    `json:"resource_class_id"`
	WorkloadIDs         []GateID                  `json:"workload_ids"`
	SelectorEstimates   []CompileSelectorEstimate `json:"selector_estimates"`
	BatchPlan           []CompileGroupBatch       `json:"batch_plan"`
	BatchPlanDigest     string                    `json:"batch_plan_digest"`
	BatchPlanWarning    string                    `json:"batch_plan_warning,omitempty"`
	CompileEstimateMS   int64                     `json:"compile_estimate_ms"`
	BodyEstimateMS      int64                     `json:"body_estimate_ms"`
	EstimatedDurationMS int64                     `json:"estimated_duration_ms"`
}

// CompileSelectorEstimate 是 planner 冻结的单 selector 正文估时。
// CompileEstimateMS 仍是 group 级字段，不在 selector wire 对象中重复。
type CompileSelectorEstimate struct {
	SelectorID     GateID `json:"selector_id"`
	BodyEstimateMS int64  `json:"body_estimate_ms"`
}

// CompileGroupBatch 是独立启动的一个 test2json 进程；同 wave 批次并行，
// exclusive 批次各占唯一串行 wave，禁止与其他批次重叠。
type CompileGroupBatch struct {
	BatchID         string   `json:"batch_id"`
	Wave            int      `json:"wave"`
	SelectorIDs     []GateID `json:"selector_ids"`
	EstimatedBodyMS int64    `json:"estimated_body_ms"`
	Exclusive       bool     `json:"exclusive"`
}

// CompileGroupInput 是 Prepare 指纹阶段为一个 selector 冻结的共享编译输入。
// 它独立于通用 Workload/PASS catalog，避免把 selector-independent 编译优化
// 身份混入 correctness PASS identity。
type CompileGroupInput struct {
	PackageTarget     string `json:"package_target"`
	SemanticKey       string `json:"semantic_key"`
	SharedInputDigest string `json:"shared_input_digest"`
	ProfileDigest     string `json:"profile_digest"`
}

// Validate 校验 Prepare 输出的完整共享编译输入；缺失字段必须 fail-fast。
func (input CompileGroupInput) Validate() error {
	if !isCanonicalCompileGroupPackageTarget(input.PackageTarget) {
		return errors.New("compile group input package target is invalid")
	}
	if err := ValidateCompileGroupSemanticKey(input.SemanticKey); err != nil {
		return err
	}
	if !isPrefixedSHA256Digest(input.SharedInputDigest) || !isPrefixedSHA256Digest(input.ProfileDigest) {
		return errors.New("compile group input digests must be sha256 digests")
	}
	return nil
}

// ValidateCompileGroupSemanticKey 校验 compile-group 语义键必须来自唯一 owner。
// 不接受绑定 parent、资源档位或任意自定义文本，避免 worker/planner 混合语义。
func ValidateCompileGroupSemanticKey(value string) error {
	if strings.TrimSpace(value) != value {
		return errors.New("compile group semantic key is not canonical")
	}
	switch value {
	case CompileGroupSemanticGoTestNormal, CompileGroupSemanticGoTestRace, CompileGroupSemanticGoBenchmark:
		return nil
	default:
		return fmt.Errorf("compile group semantic key %q is unsupported", value)
	}
}

// CompileGroupSemanticKey 根据 selector 类型和 race 执行语义返回 canonical 键。
func CompileGroupSemanticKey(kind WorkloadTargetKind, race bool) (string, error) {
	switch kind {
	case WorkloadTargetGoTest:
		if race {
			return CompileGroupSemanticGoTestRace, nil
		}
		return CompileGroupSemanticGoTestNormal, nil
	case WorkloadTargetGoBenchmark:
		if race {
			return "", errors.New("race Go benchmark cannot form a compile group")
		}
		return CompileGroupSemanticGoBenchmark, nil
	default:
		return "", fmt.Errorf("workload target kind %q cannot form a compile group semantic", kind)
	}
}

// CompileGroupSemanticKeyForWorkloadID 从 canonical selector ID 推导其唯一语义键。
// 该函数是 planner、manifest validator 和 worker 共用的语义 owner。
func CompileGroupSemanticKeyForWorkloadID(id GateID) (string, error) {
	parent, kind, _, targeted, err := ParseWorkloadID(string(id))
	if err != nil {
		return "", err
	}
	if !targeted {
		return "", fmt.Errorf("workload %q is not an exact selector", id)
	}
	return CompileGroupSemanticKey(kind, parent == GateIDBackendTestGuardWithRace)
}

// LoadCompileGroupInput 严格解析单一 Prepare 编译输入。
func LoadCompileGroupInput(reader io.Reader) (CompileGroupInput, error) {
	var input CompileGroupInput
	if err := decodeStrictJSON(reader, &input); err != nil {
		return CompileGroupInput{}, fmt.Errorf("decode compile group input: %w", err)
	}
	if err := input.Validate(); err != nil {
		return CompileGroupInput{}, err
	}
	return input, nil
}

// compileGroupIdentityMaterial 是 GroupID 的 selector-independent canonical material。
type compileGroupIdentityMaterial struct {
	SchemaVersion     uint32 `json:"schema_version"`
	ExecutionPath     string `json:"execution_path"`
	PackageTarget     string `json:"package_target"`
	SemanticKey       string `json:"semantic_key"`
	SharedInputDigest string `json:"shared_input_digest"`
	ProfileDigest     string `json:"profile_digest"`
}

// CompileArtifactKey 计算 selector-independent 的可复用编译输入身份。
// 同一 key 和资源档在一个计划内只能属于一个 compile group；不同 normal
// 资源档仍各自执行，binary artifact 只属于本次 shard，不得跨 shard CAS 复用。
func CompileArtifactKey(group CompileGroup) (string, error) {
	input := CompileGroupInput{
		PackageTarget:     group.PackageTarget,
		SemanticKey:       group.SemanticKey,
		SharedInputDigest: group.SharedInputDigest,
		ProfileDigest:     group.ProfileDigest,
	}
	if err := input.Validate(); err != nil {
		return "", err
	}
	return CompileArtifactKeyForInput(input)
}

// CompileArtifactKeyForInput 计算 selector-independent 的二进制内容身份。
// ResourceClassID 不进入该身份：CPU/内存只影响 group 执行与可比 timing，
// 不应让同一包和语义因资源档位而重复编译。
func CompileArtifactKeyForInput(input CompileGroupInput) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	material := compileGroupIdentityMaterial{
		SchemaVersion:     CompileGroupSchemaVersion,
		ExecutionPath:     cicontract.CompileGroupExecutionPathID,
		PackageTarget:     input.PackageTarget,
		SemanticKey:       input.SemanticKey,
		SharedInputDigest: input.SharedInputDigest,
		ProfileDigest:     input.ProfileDigest,
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal compile group identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// CompileGroupID 计算绑定有序 selector 成员的确定性计划组身份。
// GroupID 与 CompileArtifactKey 分层：前者证明计划覆盖/分组，后者证明编译语义。
func CompileGroupID(group CompileGroup) (string, error) {
	artifactKey, err := CompileArtifactKey(group)
	if err != nil {
		return "", err
	}
	if len(group.WorkloadIDs) == 0 {
		return "", errors.New("compile group workload IDs must not be empty")
	}
	planDigest, err := compileGroupBatchPlanDigest(group)
	if err != nil {
		return "", err
	}
	material := struct {
		SchemaVersion   uint32   `json:"schema_version"`
		ArtifactKey     string   `json:"artifact_key"`
		ResourceClass   string   `json:"resource_class_id"`
		WorkloadIDs     []GateID `json:"workload_ids"`
		BatchPlanDigest string   `json:"batch_plan_digest"`
	}{CompileGroupSchemaVersion, artifactKey, group.ResourceClassID, append([]GateID(nil), group.WorkloadIDs...), planDigest}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal compile group identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// Validate 校验编译组字段、覆盖集合和严格的成本闭包。
func (group CompileGroup) Validate() error {
	if err := group.validateIdentityFields(); err != nil {
		return err
	}
	if err := group.validateWorkloadIDs(); err != nil {
		return err
	}
	if err := group.validateEstimates(); err != nil {
		return err
	}
	expectedID, err := CompileGroupID(group)
	if err != nil {
		return err
	}
	if group.GroupID != expectedID {
		return errors.New("compile group ID does not match canonical identity")
	}
	return nil
}

// validateWorkloadIDs 校验 group 成员非空且不重复。
func (group CompileGroup) validateWorkloadIDs() error {
	if len(group.WorkloadIDs) == 0 {
		return errors.New("compile group workload IDs must not be empty")
	}
	seen := make(map[GateID]struct{}, len(group.WorkloadIDs))
	for index, workloadID := range group.WorkloadIDs {
		if workloadID == "" {
			return fmt.Errorf("compile group workload ID %d is empty", index)
		}
		if _, duplicate := seen[workloadID]; duplicate {
			return fmt.Errorf("compile group workload ID %q is duplicated", workloadID)
		}
		seen[workloadID] = struct{}{}
	}
	return nil
}

// validateEstimates 校验编译、body 和总耗时的严格加法闭包。
func (group CompileGroup) validateEstimates() error {
	if group.CompileEstimateMS <= 0 || group.BodyEstimateMS <= 0 || group.EstimatedDurationMS <= 0 {
		return errors.New("compile group estimates must be positive")
	}
	if group.CompileEstimateMS > int64(^uint64(0)>>1)-group.BodyEstimateMS {
		return errors.New("compile group estimate overflows")
	}
	if group.EstimatedDurationMS != group.CompileEstimateMS+group.BodyEstimateMS {
		return errors.New("compile group estimated duration must equal compile plus body estimate")
	}
	return group.validateSelectorEstimatesAndBatchPlan()
}

// compileGroupBatchPlanMaterial 是同时绑定 BatchPlanDigest 与 GroupID 的
// canonical selector/body 计划材料。
type compileGroupBatchPlanMaterial struct {
	PackageTarget     string                    `json:"package_target"`
	SemanticKey       string                    `json:"semantic_key"`
	SharedInputDigest string                    `json:"shared_input_digest"`
	ProfileDigest     string                    `json:"profile_digest"`
	ResourceClassID   string                    `json:"resource_class_id"`
	WorkloadIDs       []GateID                  `json:"workload_ids"`
	SelectorEstimates []CompileSelectorEstimate `json:"selector_estimates"`
	BatchPlan         []CompileGroupBatch       `json:"batch_plan"`
	BatchPlanWarning  string                    `json:"batch_plan_warning"`
}

// compileGroupBatchPlanDigest 计算计划摘要，不信任 wire 自带摘要；它独立于
// GroupID，避免循环身份依赖。
func compileGroupBatchPlanDigest(group CompileGroup) (string, error) {
	material := compileGroupBatchPlanMaterial{
		PackageTarget: group.PackageTarget, SemanticKey: group.SemanticKey,
		SharedInputDigest: group.SharedInputDigest, ProfileDigest: group.ProfileDigest,
		ResourceClassID:   group.ResourceClassID,
		WorkloadIDs:       append([]GateID(nil), group.WorkloadIDs...),
		SelectorEstimates: append([]CompileSelectorEstimate(nil), group.SelectorEstimates...),
		BatchPlan:         append([]CompileGroupBatch(nil), group.BatchPlan...),
		BatchPlanWarning:  group.BatchPlanWarning,
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal compile group batch plan: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// CompileGroupBatchPlanDigest 返回 planner 必须持久化到 compile-group wire
// 对象的 canonical 摘要。
func CompileGroupBatchPlanDigest(group CompileGroup) (string, error) {
	return compileGroupBatchPlanDigest(group)
}

// validateSelectorEstimatesAndBatchPlan 校验 selector 估时、批次覆盖与共享编译语义。
func (group CompileGroup) validateSelectorEstimatesAndBatchPlan() error {
	if len(group.WorkloadIDs) == 0 {
		return errors.New("compile group workload IDs must not be empty")
	}
	strictExactSelectors := compileGroupHasExactGoSelectors(group)
	if _, err := validateCompileGroupSelectorIdentitySafety(group); err != nil {
		return err
	}
	if err := validateArchtestCompileGroupShape(group, strictExactSelectors); err != nil {
		return err
	}
	if err := validateAtomicAgentTerminalBatchPlan(group, strictExactSelectors); err != nil {
		return err
	}
	estimates, err := validateCompileGroupSelectorEstimates(group, strictExactSelectors)
	if err != nil {
		return err
	}
	if len(group.BatchPlan) == 0 {
		return validateEmptyCompileGroupBatchPlan(group, strictExactSelectors)
	}
	return validateCompileGroupBatchPlan(group, estimates)
}

// validateArchtestCompileGroupShape 是 archtest 有界 compile group 的语义 owner。
// 每个 group 最多持有唯一 owner 规定数量的 exact selector，并在自己的 ECI
// shard 内只启动一个 test-binary batch；更大的 selector 集合必须在 planner
// bucket 层拆成多个 group，不能把全量 MISS 塞回同一个 4 GiB 进程。
func validateArchtestCompileGroupShape(group CompileGroup, strictExactSelectors bool) error {
	if group.PackageTarget != AtomicArchtestPackageTarget || !strictExactSelectors {
		return nil
	}
	if len(group.WorkloadIDs) > cicontract.ArchtestMaxSelectorsPerCompileGroup {
		return fmt.Errorf("archtest compile group exceeds selector bound %d", cicontract.ArchtestMaxSelectorsPerCompileGroup)
	}
	if len(group.BatchPlan) != 1 {
		return errors.New("archtest compile group must contain exactly one batch")
	}
	batch := group.BatchPlan[0]
	if batch.Wave != 0 || batch.Exclusive {
		return errors.New("archtest compile group batch must be a non-exclusive wave-0 batch")
	}
	return nil
}

// validateAtomicAgentTerminalBatchPlan 强制 agent-terminal exact Go selector 共用唯一 batch。
// TestMain 在该包中准备一次 rollback helper；拆成多个 batch 会重复启动 TestMain
// 及 helper 准备，抵消同一 package 测试二进制只编译一次的收益。
func validateAtomicAgentTerminalBatchPlan(group CompileGroup, strictExactSelectors bool) error {
	if group.PackageTarget != AtomicAgentTerminalPackageTarget || !strictExactSelectors {
		return nil
	}
	if len(group.BatchPlan) != 1 {
		return errors.New("atomic agent-terminal compile group must contain exactly one batch")
	}
	return nil
}

// validateCompileGroupSelectorEstimates 校验 selector body 估时集合及总和闭包。
func validateCompileGroupSelectorEstimates(group CompileGroup, strictExact bool) (map[GateID]int64, error) {
	estimates, err := collectCompileGroupSelectorEstimates(group.SelectorEstimates)
	if err != nil {
		return nil, err
	}
	if strictExact && len(group.WorkloadIDs) > 1 && len(estimates) != len(group.WorkloadIDs) {
		return nil, errors.New("compile group selector estimates must cover every selector")
	}
	if len(estimates) == 0 {
		return estimates, nil
	}
	if err := validateCompileGroupSelectorEstimateCoverage(group.WorkloadIDs, group.BodyEstimateMS, estimates); err != nil {
		return nil, err
	}
	return estimates, nil
}

// collectCompileGroupSelectorEstimates 收集并校验 selector 估时的 canonical 顺序。
func collectCompileGroupSelectorEstimates(items []CompileSelectorEstimate) (map[GateID]int64, error) {
	estimates := make(map[GateID]int64, len(items))
	for index, estimate := range items {
		if estimate.SelectorID == "" || estimate.BodyEstimateMS <= 0 {
			return nil, fmt.Errorf("compile group selector estimate %d is invalid", index)
		}
		if index > 0 && items[index-1].SelectorID >= estimate.SelectorID {
			return nil, errors.New("compile group selector estimates must be canonical sorted")
		}
		if _, duplicate := estimates[estimate.SelectorID]; duplicate {
			return nil, fmt.Errorf("compile group selector estimate %q is duplicated", estimate.SelectorID)
		}
		estimates[estimate.SelectorID] = estimate.BodyEstimateMS
	}
	return estimates, nil
}

// validateCompileGroupSelectorEstimateCoverage 校验估时覆盖所有 workload 并闭合 group 正文总和。
func validateCompileGroupSelectorEstimateCoverage(ids []GateID, expected int64, estimates map[GateID]int64) error {
	var sum int64
	for _, id := range ids {
		body, ok := estimates[id]
		if !ok {
			return fmt.Errorf("compile group selector estimate for %q is missing", id)
		}
		if body > int64(^uint64(0)>>1)-sum {
			return errors.New("compile group selector estimates overflow")
		}
		sum += body
	}
	if sum != expected {
		return errors.New("compile group body estimate does not equal selector estimates")
	}
	return nil
}

// validateEmptyCompileGroupBatchPlan 校验无需拆批的 legacy/benchmark group。
func validateEmptyCompileGroupBatchPlan(group CompileGroup, strictExact bool) error {
	if strictExact && len(group.WorkloadIDs) > 1 && (group.SemanticKey == CompileGroupSemanticGoTestNormal || group.SemanticKey == CompileGroupSemanticGoTestRace) {
		return errors.New("compile group batch plan is required for multiple Go test selectors")
	}
	if group.BatchPlanDigest != "" {
		return errors.New("compile group batch plan digest is present without a batch plan")
	}
	if group.BatchPlanWarning != "" {
		return errors.New("compile group batch plan warning is present without a batch plan")
	}
	return nil
}

// validateCompileGroupBatchPlan 校验批次覆盖、估时、wave 和 canonical 摘要。
func validateCompileGroupBatchPlan(group CompileGroup, estimates map[GateID]int64) error {
	if len(estimates) == 0 {
		return errors.New("compile group batch plan requires selector estimates")
	}
	seen := make(map[GateID]struct{}, len(group.WorkloadIDs))
	seenBatch := make(map[string]struct{}, len(group.BatchPlan))
	seenWave := make(map[int]struct{}, len(group.BatchPlan))
	lastWave := -1
	for index, batch := range group.BatchPlan {
		if err := validateCompileGroupBatch(batch, index, estimates, seen, seenBatch, seenWave, &lastWave); err != nil {
			return err
		}
	}
	if err := validateCompileGroupBatchWaves(group.BatchPlan); err != nil {
		return err
	}
	if err := validateCompileGroupBatchCoverage(group.WorkloadIDs, seen); err != nil {
		return err
	}
	if err := validateCompileGroupSelectorSafety(group); err != nil {
		return err
	}
	if err := validateCompileGroupBatchPlanWarning(group); err != nil {
		return err
	}
	return validateCompileGroupBatchDigest(group)
}

// validateCompileGroupBatchPlanWarning 校验批次告警只能作为安全单行文本出现。
func validateCompileGroupBatchPlanWarning(group CompileGroup) error {
	if group.BatchPlanWarning == "" {
		return nil
	}
	if strings.TrimSpace(group.BatchPlanWarning) != group.BatchPlanWarning || strings.ContainsAny(group.BatchPlanWarning, "\x00\r\n") {
		return errors.New("compile group batch plan warning is not canonical")
	}
	if len(group.BatchPlanWarning) > 512 {
		return errors.New("compile group batch plan warning is too long")
	}
	return nil
}

// validateCompileGroupBatchCoverage 校验 batch plan 覆盖每个 group selector。
func validateCompileGroupBatchCoverage(ids []GateID, seen map[GateID]struct{}) error {
	if len(seen) != len(ids) {
		return errors.New("compile group batch plan does not cover every selector")
	}
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("compile group selector %q is missing from batch plan", id)
		}
	}
	return nil
}

// validateCompileGroupBatchDigest 校验 batch plan 的 canonical wire 摘要。
func validateCompileGroupBatchDigest(group CompileGroup) error {
	if group.BatchPlanDigest == "" {
		return errors.New("compile group batch plan digest is required")
	}
	expectedDigest, err := compileGroupBatchPlanDigest(group)
	if err != nil {
		return err
	}
	if group.BatchPlanDigest != expectedDigest {
		return errors.New("compile group batch plan digest does not match canonical plan")
	}
	return nil
}

// validateCompileGroupBatch 校验单个批次的成员、估时和 canonical 顺序。
func validateCompileGroupBatch(batch CompileGroupBatch, index int, estimates map[GateID]int64, seen map[GateID]struct{}, seenBatch map[string]struct{}, seenWave map[int]struct{}, lastWave *int) error {
	if err := validateCompileGroupBatchIdentity(batch, index, seenBatch, seenWave, lastWave); err != nil {
		return err
	}
	if err := validateCompileGroupBatchSelectors(batch, estimates, seen); err != nil {
		return err
	}
	return nil
}

// validateCompileGroupBatchIdentity 校验批次标识和 wave 顺序。
func validateCompileGroupBatchIdentity(batch CompileGroupBatch, index int, seenBatch map[string]struct{}, seenWave map[int]struct{}, lastWave *int) error {
	if err := validateCompileGroupBatchID(batch, index, seenBatch); err != nil {
		return err
	}
	if err := validateCompileGroupBatchWave(batch, seenWave, lastWave); err != nil {
		return err
	}
	return validateCompileGroupBatchShape(batch)
}

// validateCompileGroupBatchID 校验批次 ID 非空且不重复。
func validateCompileGroupBatchID(batch CompileGroupBatch, index int, seenBatch map[string]struct{}) error {
	if batch.BatchID == "" || strings.TrimSpace(batch.BatchID) != batch.BatchID {
		return fmt.Errorf("compile group batch %d has invalid ID", index)
	}
	if _, duplicate := seenBatch[batch.BatchID]; duplicate {
		return fmt.Errorf("compile group batch %q is duplicated", batch.BatchID)
	}
	seenBatch[batch.BatchID] = struct{}{}
	return nil
}

// validateCompileGroupBatchWave 校验批次 wave 的非负和 canonical 顺序。
func validateCompileGroupBatchWave(batch CompileGroupBatch, seenWave map[int]struct{}, lastWave *int) error {
	if batch.Wave < 0 || (*lastWave == -1 && batch.Wave != 0) || (*lastWave >= 0 && batch.Wave > *lastWave+1) || batch.Wave < *lastWave {
		return fmt.Errorf("compile group batch %q has non-canonical wave", batch.BatchID)
	}
	if batch.Wave != *lastWave {
		*lastWave = batch.Wave
		if _, duplicate := seenWave[batch.Wave]; duplicate {
			return fmt.Errorf("compile group wave %d is not canonical", batch.Wave)
		}
		seenWave[batch.Wave] = struct{}{}
	}
	return nil
}

// validateCompileGroupBatchShape 校验批次非空和 exclusive singleton 约束。
func validateCompileGroupBatchShape(batch CompileGroupBatch) error {
	if len(batch.SelectorIDs) == 0 || batch.EstimatedBodyMS <= 0 {
		return fmt.Errorf("compile group batch %q is empty or has no estimate", batch.BatchID)
	}
	if batch.Exclusive && len(batch.SelectorIDs) != 1 {
		return fmt.Errorf("exclusive compile group batch %q must contain one selector", batch.BatchID)
	}
	return nil
}

// validateCompileGroupBatchSelectors 校验批次 selector 覆盖和正文估时闭包。
func validateCompileGroupBatchSelectors(batch CompileGroupBatch, estimates map[GateID]int64, seen map[GateID]struct{}) error {
	var sum int64
	for selectorIndex, id := range batch.SelectorIDs {
		if id == "" || (selectorIndex > 0 && batch.SelectorIDs[selectorIndex-1] >= id) {
			return fmt.Errorf("compile group batch %q selector IDs are not canonical", batch.BatchID)
		}
		body, ok := estimates[id]
		if !ok {
			return fmt.Errorf("compile group batch %q references unknown selector %q", batch.BatchID, id)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("compile group selector %q occurs in multiple batches", id)
		}
		seen[id] = struct{}{}
		if body > int64(^uint64(0)>>1)-sum {
			return errors.New("compile group batch estimate overflows")
		}
		sum += body
	}
	if sum != batch.EstimatedBodyMS {
		return fmt.Errorf("compile group batch %q estimate does not match selectors", batch.BatchID)
	}
	return nil
}

// validateCompileGroupBatchWaves 保证 exclusive 批次独占自己的串行 wave。
func validateCompileGroupBatchWaves(batches []CompileGroupBatch) error {
	for index := 0; index < len(batches); {
		wave := batches[index].Wave
		count := 0
		hasExclusive := false
		for index < len(batches) && batches[index].Wave == wave {
			count++
			hasExclusive = hasExclusive || batches[index].Exclusive
			index++
		}
		if hasExclusive && count != 1 {
			return fmt.Errorf("exclusive compile group wave %d must contain one batch", wave)
		}
	}
	return nil
}

// compileGroupHasExactGoSelectors 判断 group 是否由可安全拆批的精确 Go test selector 构成。
func compileGroupHasExactGoSelectors(group CompileGroup) bool {
	if group.SemanticKey != CompileGroupSemanticGoTestNormal && group.SemanticKey != CompileGroupSemanticGoTestRace {
		return false
	}
	for _, id := range group.WorkloadIDs {
		_, kind, _, targeted, err := ParseWorkloadID(string(id))
		if err != nil || !targeted || kind != WorkloadTargetGoTest {
			return false
		}
	}
	return true
}

// validateCompileGroupSelectorSafety 拒绝 helper/manual 测试身份，并校验
// codexapp exclusive 集合只能由串行 wave 中的 exclusive singleton 表示。
func validateCompileGroupSelectorSafety(group CompileGroup) error {
	exclusive, err := validateCompileGroupSelectorIdentitySafety(group)
	if err != nil {
		return err
	}
	for _, batch := range group.BatchPlan {
		if err := validateCompileGroupSafetyBatch(batch, exclusive); err != nil {
			return err
		}
	}
	return nil
}

// validateCompileGroupSelectorIdentitySafety 在 batch plan 为空时仍拒绝 helper/manual selector。
func validateCompileGroupSelectorIdentitySafety(group CompileGroup) (map[GateID]struct{}, error) {
	exclusive := make(map[GateID]struct{})
	for _, id := range group.WorkloadIDs {
		_, kind, _, targeted, err := ParseWorkloadID(string(id))
		if err != nil {
			return nil, err
		}
		if !targeted || kind != WorkloadTargetGoTest {
			continue
		}
		isExclusive, err := compileGroupSelectorSafetyExpectation(id)
		if err != nil {
			return nil, err
		}
		if isExclusive {
			exclusive[id] = struct{}{}
		}
	}
	return exclusive, nil
}

// compileGroupSelectorSafetyExpectation 解析 selector 并返回是否必须独占。
func compileGroupSelectorSafetyExpectation(id GateID) (bool, error) {
	parent, kind, payload, targeted, err := ParseWorkloadID(string(id))
	if err != nil || !targeted || kind != WorkloadTargetGoTest {
		return false, fmt.Errorf("compile group selector %q is not an exact Go test", id)
	}
	target, err := ParseGoTestTarget(payload)
	if err != nil {
		return false, fmt.Errorf("compile group selector %q target: %w", id, err)
	}
	if isCompileGroupManualPackage(target.Package) {
		return false, fmt.Errorf("compile group selector %q belongs to manual/codex_smoketest and is not in the default catalog", id)
	}
	if target.Name == "TestCodexHelperProcess" {
		return false, fmt.Errorf("compile group selector %q is the codex helper process and cannot run ordinarily", id)
	}
	isExclusive := compileGroupSelectorIsCodexExclusive(parent, target)
	return isExclusive, nil
}

// compileGroupSelectorIsCodexExclusive 是 planner 与 validator 共用的 codexapp 独占判定。
// 独占语义绑定正常 backend gate；其他 parent 的同名 selector 必须保持普通批次。
func compileGroupSelectorIsCodexExclusive(parent GateID, target GoTestTarget) bool {
	return parent == GateIDBackendTestWithGuard && target.Package == AtomicCodexAppPackageTarget && isCodexExclusiveTestName(target.Name)
}

// isCompileGroupManualPackage 判断 selector 是否属于手工或 smoketest 包。
func isCompileGroupManualPackage(packageTarget string) bool {
	return strings.Contains(packageTarget, "codex_smoketest") || strings.Contains(packageTarget, "/manual/") || strings.HasPrefix(packageTarget, "manual/")
}

// validateCompileGroupSafetyBatch 校验独占 selector 的 singleton wire 形态。
func validateCompileGroupSafetyBatch(batch CompileGroupBatch, exclusive map[GateID]struct{}) error {
	if len(batch.SelectorIDs) == 0 {
		return fmt.Errorf("compile group batch %q has no selector", batch.BatchID)
	}
	var exclusiveSelector GateID
	for _, selectorID := range batch.SelectorIDs {
		if _, expectedExclusive := exclusive[selectorID]; !expectedExclusive {
			continue
		}
		if exclusiveSelector != "" {
			return fmt.Errorf("compile group batch %q contains multiple codexapp exclusive selectors", batch.BatchID)
		}
		exclusiveSelector = selectorID
	}
	if exclusiveSelector != "" {
		if !batch.Exclusive || len(batch.SelectorIDs) != 1 {
			return fmt.Errorf("codexapp selector %q must be an exclusive singleton batch", exclusiveSelector)
		}
		return nil
	}
	if batch.Exclusive {
		return fmt.Errorf("non-exclusive selector batch %q cannot be marked exclusive", batch.BatchID)
	}
	return nil
}

func isCodexExclusiveTestName(name string) bool {
	switch name {
	case "TestDiscoverProcessesReturnsBothMaps",
		"TestCleanOrphanedMCPProcessesSkipsSelf",
		"TestCleanOrphanedMCPProcessesNilSkip",
		"TestServerManagerStartupDoesNotEnsureCodexCLIAvailable",
		"TestRunPoolSpawnAbortsAndCleansChildWhenPidregistryPersistFails",
		"TestServerManagerStartLockedAbortsWhenPidregistryPersistFails":
		return true
	default:
		return false
	}
}

func (group CompileGroup) validateIdentityFields() error {
	if err := (CompileGroupInput{
		PackageTarget:     group.PackageTarget,
		SemanticKey:       group.SemanticKey,
		SharedInputDigest: group.SharedInputDigest,
		ProfileDigest:     group.ProfileDigest,
	}).Validate(); err != nil {
		return err
	}
	if err := validateCompileGroupResourceClass(group.ResourceClassID); err != nil {
		return err
	}
	return nil
}

// validateCompileGroupResourceClass 校验资源档位只作为 group execution 身份。
func validateCompileGroupResourceClass(value string) error {
	if strings.TrimSpace(value) != value {
		return errors.New("compile group resource class is not canonical")
	}
	switch value {
	case "small", "medium", "maximum", "calibration":
		return nil
	default:
		return fmt.Errorf("compile group resource class %q is unsupported", value)
	}
}

// isCanonicalCompileGroupPackageTarget 校验 package target 的 canonical 相对路径。
func isCanonicalCompileGroupPackageTarget(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "/") ||
		!strings.HasPrefix(value, "./") || strings.Contains(value, "...") ||
		strings.ContainsAny(value, "\\\x00\r\n,") {
		return false
	}
	withoutPrefix := strings.TrimPrefix(value, "./")
	return withoutPrefix != "" && path.Clean(withoutPrefix) == withoutPrefix
}

// isPrefixedSHA256Digest 供严格 gate 身份校验复用。
func isPrefixedSHA256Digest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	for _, char := range value[len(prefix):] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

// LoadCompileGroup 严格解析单一 compile group JSON。
func LoadCompileGroup(reader io.Reader) (CompileGroup, error) {
	var group CompileGroup
	if err := decodeStrictJSON(reader, &group); err != nil {
		return CompileGroup{}, fmt.Errorf("decode compile group: %w", err)
	}
	if err := group.Validate(); err != nil {
		return CompileGroup{}, err
	}
	return group, nil
}

// LoadCompileGroups 严格解析有序 compile group JSON 数组，并拒绝重复身份。
func LoadCompileGroups(reader io.Reader) ([]CompileGroup, error) {
	var groups []CompileGroup
	if err := decodeStrictJSON(reader, &groups); err != nil {
		return nil, fmt.Errorf("decode compile groups: %w", err)
	}
	if len(groups) == 0 {
		return nil, errors.New("compile groups must not be empty")
	}
	seen := make(map[string]struct{}, len(groups))
	for index, group := range groups {
		if err := group.Validate(); err != nil {
			return nil, fmt.Errorf("compile_groups[%d]: %w", index, err)
		}
		if _, duplicate := seen[group.GroupID]; duplicate {
			return nil, fmt.Errorf("compile_groups[%d]: duplicate group ID %q", index, group.GroupID)
		}
		seen[group.GroupID] = struct{}{}
	}
	return groups, nil
}

// CompileGroupWorkloadIDs 返回 group 内 workload IDs 的稳定副本，避免调用方修改身份输入。
func CompileGroupWorkloadIDs(group CompileGroup) []GateID {
	ids := append([]GateID(nil), group.WorkloadIDs...)
	return ids
}

// SortCompileGroupsByID 返回按 group ID 排序的副本，仅用于 canonical digest/materialization。
func SortCompileGroupsByID(groups []CompileGroup) []CompileGroup {
	ordered := append([]CompileGroup(nil), groups...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].GroupID < ordered[right].GroupID })
	return ordered
}
