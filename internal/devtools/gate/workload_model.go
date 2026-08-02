// Package gate 提供独立 CI workload 的确定性建模与分片规划。
package gate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	durationLedgerVersion              = 1
	workloadExecutionPlanSchemaVersion = 3
	// FullCITargetDuration 是分片优化目标；超时只告警，不是 worker 终止时限。
	FullCITargetDuration         = 100 * time.Second
	FullCITargetDurationMS int64 = int64(FullCITargetDuration / time.Millisecond)
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
	BootstrapEstimateMS int64        `json:"bootstrap_estimate_ms"`
	Shardable           bool         `json:"shardable"`
}

// UnmarshalJSON 要求 shardable 即使为 false 也必须显式出现，避免缺字段静默降级。
func (workload *Workload) UnmarshalJSON(data []byte) error {
	type workloadDocument struct {
		ID                  string       `json:"id"`
		Kind                WorkloadKind `json:"kind"`
		CommandDigest       string       `json:"command_digest"`
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
		BootstrapEstimateMS: document.BootstrapEstimateMS, Shardable: *document.Shardable,
	}
	return nil
}

// DurationLedger 保存按可复现执行环境分桶的观测时长样本。
type DurationLedger struct {
	Version     int                  `json:"version"`
	Calibration *DurationCalibration `json:"calibration,omitempty"`
	Samples     []DurationSample     `json:"samples"`
}

const DurationCalibrationSchemaVersion uint32 = 2

// DurationCalibration 绑定首代完整 commit/push/release 校准和精确 runner 环境。
type DurationCalibration struct {
	SchemaVersion        uint32         `json:"schema_version"`
	Commit               string         `json:"commit"`
	Tree                 string         `json:"tree"`
	Platform             string         `json:"platform"`
	Runner               string         `json:"runner"`
	Toolchain            string         `json:"toolchain"`
	CommitEntrypoint     CIEntrypointID `json:"commit_entrypoint"`
	PushEntrypoint       CIEntrypointID `json:"push_entrypoint"`
	ReleaseEntrypoint    CIEntrypointID `json:"release_entrypoint"`
	CommitCatalogDigest  string         `json:"commit_catalog_digest"`
	PushCatalogDigest    string         `json:"push_catalog_digest"`
	ReleaseCatalogDigest string         `json:"release_catalog_digest"`
	WorkloadCount        int            `json:"workload_count"`
	RacePackageCount     int            `json:"race_package_count"`
	CompletedAt          time.Time      `json:"completed_at"`
}

// DurationSample 记录一个 workload 的单次执行结果和耗时。
type DurationSample struct {
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
	WorkloadID    string `json:"workload_id"`
	CommandDigest string `json:"command_digest"`
	Platform      string `json:"platform"`
	Runner        string `json:"runner"`
	Toolchain     string `json:"toolchain"`
}

// PlanningContext 是 LPT 规划的目标执行环境；它不接受瞬时 slot 状态。
type PlanningContext struct {
	Platform         string `json:"platform"`
	Runner           string `json:"runner"`
	Toolchain        string `json:"toolchain"`
	TargetDurationMS int64  `json:"target_duration_ms"`
}

type durationSampleIndexKey struct {
	workloadID    string
	commandDigest string
}

type durationSampleAggregate struct {
	successTotalMS     int64
	successCount       int64
	maxFailureDuration int64
}

// DurationSampleIndex 是单个账本 generation 和执行环境的只读时长索引。
type DurationSampleIndex struct {
	context PlanningContext
	buckets map[durationSampleIndexKey]durationSampleAggregate
}

// ShardPlan 是单个计划分片及其确定性 workload 顺序。
type ShardPlan struct {
	Index               int               `json:"index"`
	Workloads           []PlannedWorkload `json:"workloads"`
	EstimatedDurationMS int64             `json:"estimated_duration_ms"`
}

// PlannedWorkload 是带估算时长的计划 workload。
type PlannedWorkload struct {
	Workload            Workload `json:"workload"`
	EstimatedDurationMS int64    `json:"estimated_duration_ms"`
}

// WorkloadExecutionPlan 将一次确定性分片绑定到 GatePlan、目录和账本 generation。
type WorkloadExecutionPlan struct {
	SchemaVersion            uint32          `json:"schema_version"`
	GatePlanDigest           string          `json:"gate_plan_digest"`
	CatalogDigest            string          `json:"catalog_digest"`
	LedgerGeneration         uint64          `json:"ledger_generation"`
	Context                  PlanningContext `json:"context"`
	Catalog                  WorkloadCatalog `json:"catalog"`
	ReusedWorkloads          []string        `json:"reused_workloads"`
	Shards                   []ShardPlan     `json:"shards"`
	OwnerEstimatedDurationMS int64           `json:"owner_estimated_duration_ms"`
	PlanDigest               string          `json:"plan_digest"`
}

// LoadWorkloadCatalog 严格解析并校验 workload catalog JSON。
func LoadWorkloadCatalog(reader io.Reader) (WorkloadCatalog, error) {
	var catalog WorkloadCatalog
	if err := decodeStrictJSON(reader, &catalog); err != nil {
		return WorkloadCatalog{}, fmt.Errorf("decode workload catalog: %w", err)
	}
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return WorkloadCatalog{}, err
	}
	return catalog, nil
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

// LoadDurationLedger 严格解析并校验 JSON duration ledger。
func LoadDurationLedger(reader io.Reader) (DurationLedger, error) {
	var ledger DurationLedger
	if err := decodeStrictJSON(reader, &ledger); err != nil {
		return DurationLedger{}, fmt.Errorf("decode duration ledger: %w", err)
	}
	if err := ValidateDurationLedger(ledger); err != nil {
		return DurationLedger{}, err
	}
	return ledger, nil
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
	if err := validateDurationBucket(DurationBucket{
		WorkloadID: "calibration", CommandDigest: strings.Repeat("0", 64),
		Platform: calibration.Platform, Runner: calibration.Runner, Toolchain: calibration.Toolchain,
	}); err != nil {
		return err
	}
	if err := validateCalibrationCatalogs(calibration); err != nil {
		return err
	}
	if err := validateCalibrationEntrypoints(calibration); err != nil {
		return err
	}
	if calibration.WorkloadCount <= 0 || calibration.RacePackageCount <= 0 {
		return errors.New("duration calibration workload counts must be positive")
	}
	if calibration.CompletedAt.IsZero() || calibration.CompletedAt.Location() != time.UTC {
		return errors.New("duration calibration completion time must be UTC")
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

// PlanLPT 使用成功样本估算并按稳定 LPT 规则将所有 workload 分配到固定数量的分片。
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
	if strings.TrimSpace(bucket.WorkloadID) == "" {
		return errors.New("workload_id must not be empty")
	}
	if !isSHA256Digest(bucket.CommandDigest) {
		return errors.New("command_digest must be a lowercase SHA-256 hex digest")
	}
	for field, value := range map[string]string{
		"platform":  bucket.Platform,
		"runner":    bucket.Runner,
		"toolchain": bucket.Toolchain,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not be empty", field)
		}
	}
	return nil
}

func validatePlanningContext(context PlanningContext) error {
	if context.TargetDurationMS != FullCITargetDurationMS {
		return fmt.Errorf("target_duration_ms must equal %d", FullCITargetDurationMS)
	}
	return validateDurationBucket(DurationBucket{
		WorkloadID:    "planning-context",
		CommandDigest: strings.Repeat("0", 64),
		Platform:      context.Platform,
		Runner:        context.Runner,
		Toolchain:     context.Toolchain,
	})
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

// BuildDurationSampleIndex 对当前 generation 只扫描一次账本，并隔离不同比较环境。
func BuildDurationSampleIndex(ledger DurationLedger, context PlanningContext) (DurationSampleIndex, error) {
	if context.TargetDurationMS <= 0 {
		return DurationSampleIndex{}, errors.New("duration sample index target must be positive")
	}
	if err := validateDurationBucket(DurationBucket{
		WorkloadID:    "duration-sample-index",
		CommandDigest: strings.Repeat("0", 64),
		Platform:      context.Platform,
		Runner:        context.Runner,
		Toolchain:     context.Toolchain,
	}); err != nil {
		return DurationSampleIndex{}, err
	}
	index := DurationSampleIndex{
		context: context,
		buckets: make(map[durationSampleIndexKey]durationSampleAggregate),
	}
	const maximumInt64 = int64(^uint64(0) >> 1)
	for _, sample := range ledger.Samples {
		if err := index.addSample(sample, maximumInt64); err != nil {
			return DurationSampleIndex{}, err
		}
	}
	return index, nil
}

// addSample 按当前比较环境把一条样本合并到确定性的 workload 聚合中。
func (index DurationSampleIndex) addSample(sample DurationSample, maximumInt64 int64) error {
	if sample.Bucket.Platform != index.context.Platform || sample.Bucket.Runner != index.context.Runner || sample.Bucket.Toolchain != index.context.Toolchain {
		return nil
	}
	if sample.DurationMS <= 0 {
		return fmt.Errorf("duration sample for workload %q must be positive", sample.Bucket.WorkloadID)
	}
	key := durationSampleIndexKey{workloadID: sample.Bucket.WorkloadID, commandDigest: sample.Bucket.CommandDigest}
	aggregate := index.buckets[key]
	if !sample.Succeeded {
		if sample.DurationMS > aggregate.maxFailureDuration {
			aggregate.maxFailureDuration = sample.DurationMS
		}
		index.buckets[key] = aggregate
		return nil
	}
	if sample.DurationMS > maximumInt64-aggregate.successTotalMS {
		return fmt.Errorf("duration estimate overflows for workload %q", sample.Bucket.WorkloadID)
	}
	aggregate.successTotalMS += sample.DurationMS
	aggregate.successCount++
	index.buckets[key] = aggregate
	return nil
}

// DurationSampleIndexFromSnapshot 优先复用 SQLite 聚合索引，并拒绝环境不一致的快照。
func DurationSampleIndexFromSnapshot(
	snapshot DurationLedgerSnapshot,
	context PlanningContext,
) (DurationSampleIndex, error) {
	if err := validatePlanningContext(context); err != nil {
		return DurationSampleIndex{}, err
	}
	if snapshot.SampleIndex == nil {
		return BuildDurationSampleIndex(snapshot.Ledger, context)
	}
	index := *snapshot.SampleIndex
	if index.context != context {
		return DurationSampleIndex{}, errors.New("duration sample index planning context does not match")
	}
	if index.buckets == nil {
		return DurationSampleIndex{}, errors.New("duration sample index buckets are missing")
	}
	return index, nil
}

// HasComparableSuccessfulDurationSample 判断索引中是否已有同命令成功样本。
func (index DurationSampleIndex) HasComparableSuccessfulDurationSample(workload Workload) bool {
	aggregate := index.buckets[durationSampleIndexKey{
		workloadID: workload.ID, commandDigest: workload.CommandDigest,
	}]
	return aggregate.successCount > 0
}

// HasFailureExceedingDuration 判断索引中是否有超过阈值的同命令失败样本。
func (index DurationSampleIndex) HasFailureExceedingDuration(workload Workload, durationMS int64) bool {
	aggregate := index.buckets[durationSampleIndexKey{
		workloadID: workload.ID, commandDigest: workload.CommandDigest,
	}]
	return aggregate.maxFailureDuration > durationMS
}

// EstimateWorkloadDurationMS 使用预聚合成功样本估算单个 workload。
func (index DurationSampleIndex) EstimateWorkloadDurationMS(workload Workload) (int64, error) {
	aggregate := index.buckets[durationSampleIndexKey{
		workloadID: workload.ID, commandDigest: workload.CommandDigest,
	}]
	if aggregate.successCount == 0 {
		return workload.BootstrapEstimateMS, nil
	}
	estimate := aggregate.successTotalMS / aggregate.successCount
	if estimate > index.context.TargetDurationMS &&
		exactGoTestBootstrapFitsBudget(workload, index.context.TargetDurationMS) {
		return workload.BootstrapEstimateMS, nil
	}
	return estimate, nil
}

// GoTestDurationMS 返回同父 workload、同顶层测试和同环境的成功耗时均值。
func (index DurationSampleIndex) GoTestDurationMS(
	parent Workload,
	testName string,
) (int64, bool) {
	aggregate := index.buckets[durationSampleIndexKey{
		workloadID:    GoTestDurationWorkloadID(parent.ID, testName),
		commandDigest: GoTestDurationCommandDigest(parent.CommandDigest, testName),
	}]
	if aggregate.successCount == 0 {
		return 0, false
	}
	return aggregate.successTotalMS / aggregate.successCount, true
}

// EstimateWorkloadDurationMS 只聚合完全匹配环境桶的成功样本；失败样本绝不会改变成功估算。
func EstimateWorkloadDurationMS(workload Workload, ledger DurationLedger, context PlanningContext) (int64, error) {
	var successTotal int64
	var successCount int64
	for _, sample := range ledger.Samples {
		if !sample.Succeeded || !matchesPlanningBucket(sample.Bucket, workload, context) {
			continue
		}
		if sample.DurationMS > (int64(^uint64(0)>>1) - successTotal) {
			return 0, fmt.Errorf("duration estimate overflows for workload %q", workload.ID)
		}
		successTotal += sample.DurationMS
		successCount++
	}
	if successCount == 0 {
		return workload.BootstrapEstimateMS, nil
	}
	estimate := successTotal / successCount
	if estimate > context.TargetDurationMS && exactGoTestBootstrapFitsBudget(workload, context.TargetDurationMS) {
		return workload.BootstrapEstimateMS, nil
	}
	return estimate, nil
}

// HasComparableSuccessfulDurationSample 判断账本是否包含同命令与执行环境的成功样本。
func HasComparableSuccessfulDurationSample(workload Workload, ledger DurationLedger, context PlanningContext) bool {
	for _, sample := range ledger.Samples {
		if sample.Succeeded && matchesPlanningBucket(sample.Bucket, workload, context) {
			return true
		}
	}
	return false
}

func matchesPlanningBucket(bucket DurationBucket, workload Workload, context PlanningContext) bool {
	return bucket.WorkloadID == workload.ID &&
		bucket.CommandDigest == workload.CommandDigest &&
		bucket.Platform == context.Platform &&
		bucket.Runner == context.Runner &&
		bucket.Toolchain == context.Toolchain
}

func leastLoadedShard(shards []ShardPlan) int {
	selected := 0
	for index := 1; index < len(shards); index++ {
		if shards[index].EstimatedDurationMS < shards[selected].EstimatedDurationMS {
			selected = index
		}
	}
	return selected
}
