package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const (
	// ExecutorShardExecutionManifestPath 是 worker 唯一接受的 gate-owned manifest 路径。
	ExecutorShardExecutionManifestPath         = ExecutorWorkRoot + "/shard-execution-manifest.json"
	ShardExecutionManifestSchemaVersion uint32 = 1
	compileGroupErrorTextBytes                 = 4 << 10
)

// ShardExecutionManifest 是 coordinator 投影给 worker 的严格 shard 输入。
// ManifestDigest 是 ManifestDigest 置空后的 canonical JSON sha256。
type ShardExecutionManifest struct {
	SchemaVersion  uint32         `json:"schema_version"`
	Profile        Profile        `json:"profile"`
	PlanDigest     string         `json:"plan_digest"`
	ShardIdentity  string         `json:"shard_identity"`
	SourceTreeSHA  string         `json:"source_tree_sha"`
	GateIDs        []GateID       `json:"gate_ids"`
	CompileGroups  []CompileGroup `json:"compile_groups"`
	ManifestDigest string         `json:"manifest_digest"`
}

// CompileGroupExecution 是 shard-local test binary compile 的可审计观察。
type CompileGroupExecution struct {
	Scope                cicontract.TimingScope `json:"scope"`
	Phase                cicontract.TimingPhase `json:"phase"`
	GroupID              string                 `json:"group_id"`
	ArtifactKey          string                 `json:"artifact_key"`
	PackageTarget        string                 `json:"package_target"`
	WorkloadIDs          []GateID               `json:"workload_ids"`
	StartedAtUnixMS      int64                  `json:"started_at_unix_ms"`
	CompletedAtUnixMS    int64                  `json:"completed_at_unix_ms"`
	DurationMS           int64                  `json:"duration_ms"`
	ArtifactSHA256       string                 `json:"artifact_sha256,omitempty"`
	ArtifactSize         int64                  `json:"artifact_size"`
	CacheHits            uint64                 `json:"cache_hits"`
	CacheMisses          uint64                 `json:"cache_misses"`
	CachePuts            uint64                 `json:"cache_puts"`
	Status               ResultStatus           `json:"status"`
	ExitCode             int                    `json:"exit_code"`
	ErrorText            string                 `json:"error_text,omitempty"`
	CompileCommandDigest string                 `json:"compile_command_digest"`
	ProfileDigest        string                 `json:"profile_digest"`
	ResourceClassID      string                 `json:"resource_class_id"`
}

// Validate 校验编译观察的阶段、身份、时序、证据和结果闭包。
func (execution CompileGroupExecution) Validate() error {
	validators := []func() error{
		execution.validateIdentity,
		execution.validateWorkloads,
		execution.validateTiming,
		execution.validateEvidence,
		execution.validateOutcome,
	}
	for _, validate := range validators {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

func (execution CompileGroupExecution) validateIdentity() error {
	validators := []func() error{
		execution.validateScope,
		execution.validateArtifactIdentity,
		execution.validateCompileDigests,
		execution.validateResourceClass,
	}
	for _, validate := range validators {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

func (execution CompileGroupExecution) validateScope() error {
	if execution.Scope != cicontract.TimingScopeCompileGroup || execution.Phase != cicontract.TimingTestBinaryCompile {
		return errors.New("compile group execution scope or phase is invalid")
	}
	return nil
}

func (execution CompileGroupExecution) validateArtifactIdentity() error {
	if execution.GroupID == "" || !digestPattern.MatchString(execution.GroupID) {
		return errors.New("compile group execution identity is invalid")
	}
	if !digestPattern.MatchString(execution.ArtifactKey) || !isCanonicalCompileGroupPackageTarget(execution.PackageTarget) {
		return errors.New("compile group execution artifact identity is invalid")
	}
	return nil
}

// validateCompileDigests 校验 profile 与实际编译命令的身份摘要。
func (execution CompileGroupExecution) validateCompileDigests() error {
	if !digestPattern.MatchString(execution.ProfileDigest) {
		return errors.New("compile group execution command or profile digest is invalid")
	}
	return execution.validateCompileCommandDigest()
}

// validateCompileCommandDigest 保持已启动与未启动执行的命令摘要边界。
func (execution CompileGroupExecution) validateCompileCommandDigest() error {
	if execution.HasMeasuredObservation() {
		if !digestPattern.MatchString(execution.CompileCommandDigest) {
			return errors.New("measured compile group execution command digest is invalid")
		}
		return nil
	}
	if execution.CompileCommandDigest != "" {
		return errors.New("unstarted compile group execution must not have a command digest")
	}
	return nil
}

func (execution CompileGroupExecution) validateResourceClass() error {
	if strings.TrimSpace(execution.ResourceClassID) != execution.ResourceClassID || execution.ResourceClassID == "" || strings.ContainsAny(execution.ResourceClassID, " \t\r\n\x00") {
		return errors.New("compile group execution resource class is required")
	}
	return nil
}

func (execution CompileGroupExecution) validateWorkloads() error {
	if len(execution.WorkloadIDs) == 0 {
		return errors.New("compile group execution workload IDs are empty")
	}
	seen := make(map[GateID]struct{}, len(execution.WorkloadIDs))
	for _, id := range execution.WorkloadIDs {
		if id == "" {
			return errors.New("compile group execution workload ID is empty")
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("compile group execution workload ID %q is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// validateTiming 校验未启动执行的空时序或已启动执行的真实区间。
func (execution CompileGroupExecution) validateTiming() error {
	if execution.HasMeasuredObservation() {
		return execution.validateMeasuredInterval()
	}
	return execution.validateUnstartedTiming()
}

// validateMeasuredInterval 校验已启动执行具有严格递增且相符的毫秒区间。
func (execution CompileGroupExecution) validateMeasuredInterval() error {
	if execution.StartedAtUnixMS <= 0 || execution.CompletedAtUnixMS <= execution.StartedAtUnixMS ||
		execution.DurationMS != execution.CompletedAtUnixMS-execution.StartedAtUnixMS || execution.DurationMS <= 0 {
		return errors.New("compile group execution timing is invalid")
	}
	return nil
}

// validateUnstartedTiming 禁止未启动失败携带任何部分时序。
func (execution CompileGroupExecution) validateUnstartedTiming() error {
	if execution.StartedAtUnixMS != 0 || execution.CompletedAtUnixMS != 0 || execution.DurationMS != 0 {
		return errors.New("unstarted compile group execution timing is incomplete")
	}
	return nil
}

// validateEvidence 校验编译产物和缓存计数的可审计证据。
func (execution CompileGroupExecution) validateEvidence() error {
	if err := execution.validateArtifactAndCacheCounts(); err != nil {
		return err
	}
	if err := execution.validateUnstartedEvidence(); err != nil {
		return err
	}
	if err := execution.validateArtifactDigest(); err != nil {
		return err
	}
	return execution.validateErrorText()
}

// validateArtifactAndCacheCounts 校验产物大小非负且缓存计数不溢出。
func (execution CompileGroupExecution) validateArtifactAndCacheCounts() error {
	if execution.ArtifactSize < 0 || execution.CacheHits > ^uint64(0)-execution.CacheMisses {
		return errors.New("compile group execution artifact or cache counts are invalid")
	}
	return nil
}

// validateUnstartedEvidence 禁止未启动失败携带产物或缓存证据。
func (execution CompileGroupExecution) validateUnstartedEvidence() error {
	if execution.HasMeasuredObservation() {
		return nil
	}
	if execution.ArtifactSHA256 != "" || execution.ArtifactSize != 0 || execution.CacheHits != 0 || execution.CacheMisses != 0 || execution.CachePuts != 0 {
		return errors.New("unstarted compile group execution must not have artifact or cache evidence")
	}
	return nil
}

// validateArtifactDigest 校验存在时的编译产物摘要格式。
func (execution CompileGroupExecution) validateArtifactDigest() error {
	if execution.ArtifactSHA256 != "" && !digestPattern.MatchString(execution.ArtifactSHA256) {
		return errors.New("compile group execution artifact digest is invalid")
	}
	return nil
}

// validateErrorText 校验失败说明的长度与单行安全边界。
func (execution CompileGroupExecution) validateErrorText() error {
	if len(execution.ErrorText) > compileGroupErrorTextBytes || strings.ContainsAny(execution.ErrorText, "\x00\r\n") {
		return errors.New("compile group execution error text is invalid")
	}
	return nil
}

// validateOutcome 校验编译状态、退出码以及成功或失败证据的一致性。
func (execution CompileGroupExecution) validateOutcome() error {
	if !validPlanGateExit(execution.Status, execution.ExitCode) {
		return errors.New("compile group execution status or exit code is invalid")
	}
	if execution.Status == ResultStatusPassed {
		if execution.ArtifactSHA256 == "" || execution.ArtifactSize <= 0 || execution.ErrorText != "" {
			return errors.New("passed compile group execution lacks artifact evidence")
		}
		return nil
	}
	if execution.ErrorText == "" {
		return errors.New("failed compile group execution lacks error text")
	}
	return nil
}

// HasMeasuredObservation 判断 go test -c 是否实际到达 Cmd.Start。
func (execution CompileGroupExecution) HasMeasuredObservation() bool {
	return execution.StartedAtUnixMS != 0 || execution.CompletedAtUnixMS != 0 || execution.DurationMS != 0 || execution.CompileCommandDigest != ""
}

// ValidateCompileGroupExecutions 将 worker ledger 绑定回 manifest 的精确顺序和身份。
func ValidateCompileGroupExecutions(groups []CompileGroup, executions []CompileGroupExecution) error {
	if len(groups) != len(executions) {
		return errors.New("compile group execution count does not match manifest")
	}
	for index, group := range groups {
		execution := executions[index]
		if err := validateCompileGroupExecutionMatch(index, group, execution); err != nil {
			return err
		}
	}
	return nil
}

// validateCompileGroupExecutionMatch 校验一条 ledger 与 manifest group 的所有身份字段相等。
func validateCompileGroupExecutionMatch(index int, group CompileGroup, execution CompileGroupExecution) error {
	if err := execution.Validate(); err != nil {
		return fmt.Errorf("compile_group_executions[%d]: %w", index, err)
	}
	artifactKey, err := CompileArtifactKey(group)
	if err != nil {
		return err
	}
	if execution.GroupID != group.GroupID || execution.ArtifactKey != artifactKey || execution.PackageTarget != group.PackageTarget || !slices.Equal(execution.WorkloadIDs, group.WorkloadIDs) || execution.ProfileDigest != group.ProfileDigest || execution.ResourceClassID != group.ResourceClassID {
		return fmt.Errorf("compile_group_executions[%d] does not match manifest group %q", index, group.GroupID)
	}
	return nil
}

// Validate 校验 manifest 身份、Go selector 的完整覆盖和 fail-fast 约束。
func (manifest ShardExecutionManifest) Validate() error {
	if err := manifest.validateHeader(); err != nil {
		return err
	}
	gateSet, err := manifest.validateGateIDs()
	if err != nil {
		return err
	}
	grouped, err := manifest.validateGroups(gateSet)
	if err != nil {
		return err
	}
	if err := validateCompileGroupCoverage(manifest.GateIDs, grouped); err != nil {
		return err
	}
	if manifest.ManifestDigest != "" {
		return validateShardManifestDigest(manifest)
	}
	return nil
}

// validateHeader 校验 manifest 版本、profile、绑定 digest 和 shard gate 集合。
func (manifest ShardExecutionManifest) validateHeader() error {
	if manifest.SchemaVersion != ShardExecutionManifestSchemaVersion {
		return fmt.Errorf("shard execution manifest schema_version must equal %d", ShardExecutionManifestSchemaVersion)
	}
	if err := manifest.Profile.Validate(); err != nil {
		return fmt.Errorf("shard execution manifest profile: %w", err)
	}
	if !digestPattern.MatchString(manifest.PlanDigest) {
		return errors.New("shard execution manifest plan_digest is invalid")
	}
	if !digestPattern.MatchString(manifest.ShardIdentity) {
		return errors.New("shard execution manifest shard_identity is required")
	}
	if !isCanonicalSourceTreeSHA(manifest.SourceTreeSHA) {
		return errors.New("shard execution manifest source_tree_sha is required")
	}
	if len(manifest.GateIDs) == 0 {
		return errors.New("shard execution manifest gate_ids are empty")
	}
	if err := validateContainerShardGateIDs(manifest.Profile, manifest.GateIDs); err != nil {
		return fmt.Errorf("shard execution manifest gate_ids: %w", err)
	}
	return nil
}

// isCanonicalSourceTreeSHA 判断 source tree 是否是小写 40 或 64 位 hex SHA。
func isCanonicalSourceTreeSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func validateShardManifestDigest(manifest ShardExecutionManifest) error {
	claimed := manifest.ManifestDigest
	manifest.ManifestDigest = ""
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if digestPlanLog(encoded) != claimed {
		return errors.New("shard execution manifest digest does not match canonical bytes")
	}
	return nil
}

func (manifest ShardExecutionManifest) validateGateIDs() (map[GateID]struct{}, error) {
	gateSet := make(map[GateID]struct{}, len(manifest.GateIDs))
	for _, id := range manifest.GateIDs {
		if id == "" {
			return nil, errors.New("shard execution manifest gate ID is empty")
		}
		if _, duplicate := gateSet[id]; duplicate {
			return nil, fmt.Errorf("shard execution manifest gate ID %q is duplicated", id)
		}
		gateSet[id] = struct{}{}
	}
	return gateSet, nil
}

// validateGroups 校验 compile group 身份、成员子集、唯一覆盖和执行语义。
func (manifest ShardExecutionManifest) validateGroups(gateSet map[GateID]struct{}) (map[GateID]struct{}, error) {
	groupSet := make(map[string]struct{}, len(manifest.CompileGroups))
	artifactGroups := make(map[string]string, len(manifest.CompileGroups))
	grouped := make(map[GateID]struct{})
	for index, group := range manifest.CompileGroups {
		if err := group.Validate(); err != nil {
			return nil, fmt.Errorf("shard execution manifest compile_groups[%d]: %w", index, err)
		}
		if err := validateCompileGroupSemantics(group); err != nil {
			return nil, fmt.Errorf("shard execution manifest compile_groups[%d]: %w", index, err)
		}
		if _, duplicate := groupSet[group.GroupID]; duplicate {
			return nil, fmt.Errorf("shard execution manifest compile group %q is duplicated", group.GroupID)
		}
		if err := registerCompileArtifactGroup(group, artifactGroups); err != nil {
			return nil, fmt.Errorf("shard execution manifest compile_groups[%d]: %w", index, err)
		}
		groupSet[group.GroupID] = struct{}{}
		for _, workloadID := range group.WorkloadIDs {
			if _, ok := gateSet[workloadID]; !ok {
				return nil, fmt.Errorf("compile group workload %q is not in shard gate_ids", workloadID)
			}
			if _, duplicate := grouped[workloadID]; duplicate {
				return nil, fmt.Errorf("compile group workload %q is covered more than once", workloadID)
			}
			if err := validateCompileGroupSelector(workloadID, group.PackageTarget, group.SemanticKey); err != nil {
				return nil, err
			}
			grouped[workloadID] = struct{}{}
		}
	}
	return grouped, nil
}

// validateCompileGroupSemantics 校验组内 selector 的包、类型、parent 和语义键完全一致。
func validateCompileGroupSemantics(group CompileGroup) error {
	if len(group.WorkloadIDs) == 0 {
		return errors.New("compile group workload IDs are empty")
	}
	firstParent, firstKind, firstTarget, err := parseCompileGroupSelectorID(group.WorkloadIDs[0])
	if err != nil {
		return err
	}
	expectedSemantic, err := CompileGroupSemanticKeyForWorkloadID(group.WorkloadIDs[0])
	if err != nil {
		return fmt.Errorf("compile group workload %q semantic: %w", group.WorkloadIDs[0], err)
	}
	if group.SemanticKey != expectedSemantic {
		return errors.New("compile group semantic key does not match first workload")
	}
	if err := validateCompileGroupSelectorTarget(firstKind, firstTarget, group.PackageTarget); err != nil {
		return err
	}
	return validateCompileGroupMemberSemantics(group, firstParent, firstKind)
}

func parseCompileGroupSelectorID(id GateID) (GateID, WorkloadTargetKind, string, error) {
	parent, kind, target, targeted, err := ParseWorkloadID(string(id))
	if err != nil {
		return "", "", "", fmt.Errorf("compile group workload %q is malformed: %w", id, err)
	}
	if !targeted {
		return "", "", "", fmt.Errorf("compile group workload %q is not an exact selector", id)
	}
	return parent, kind, target, nil
}

// validateCompileGroupMemberSemantics 校验组内其余 selector 不发生语义漂移。
func validateCompileGroupMemberSemantics(group CompileGroup, firstParent GateID, firstKind WorkloadTargetKind) error {
	for _, workloadID := range group.WorkloadIDs[1:] {
		parent, kind, target, err := parseCompileGroupSelectorID(workloadID)
		if err != nil {
			return err
		}
		if parent != firstParent || kind != firstKind {
			return fmt.Errorf("compile group workload %q mixes parent or selector kind", workloadID)
		}
		semantic, semanticErr := CompileGroupSemanticKeyForWorkloadID(workloadID)
		if semanticErr != nil || semantic != group.SemanticKey {
			return fmt.Errorf("compile group workload %q mixes execution semantics", workloadID)
		}
		if err := validateCompileGroupSelectorTarget(kind, target, group.PackageTarget); err != nil {
			return err
		}
	}
	return nil
}

// validateCompileGroupSelectorTarget 校验 selector 类型的包目标与 group 一致。
func validateCompileGroupSelectorTarget(kind WorkloadTargetKind, target, packageTarget string) error {
	switch kind {
	case WorkloadTargetGoTest:
		parsed, err := ParseGoTestTarget(target)
		if err != nil || parsed.Package != packageTarget {
			return errors.New("compile group Go test package target drifted")
		}
	case WorkloadTargetGoBenchmark:
		parsed, err := ParseGoBenchmarkTarget(target)
		if err != nil || parsed.Package != packageTarget {
			return errors.New("compile group Go benchmark package target drifted")
		}
	default:
		return fmt.Errorf("compile group selector kind %q is unsupported", kind)
	}
	return nil
}

// validateCompileGroupCoverage 要求 shard 内所有可 group selector 都被精确投影。
func validateCompileGroupCoverage(gateIDs []GateID, grouped map[GateID]struct{}) error {
	for _, workloadID := range gateIDs {
		if isCompileGroupSelector(workloadID) {
			if _, ok := grouped[workloadID]; !ok {
				return fmt.Errorf("compile-group selector %q is missing from compile_groups", workloadID)
			}
		}
	}
	return nil
}

// ValidateBinding 将 manifest 与 worker 已准入的独立 profile、plan 身份绑定。
func (manifest ShardExecutionManifest) ValidateBinding(profile Profile, planDigest string) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if manifest.Profile != profile || manifest.PlanDigest != planDigest {
		return errors.New("shard execution manifest binding does not match worker admission")
	}
	return nil
}

// EncodeShardExecutionManifest 校验并输出带 canonical digest 的 manifest 字节。
func EncodeShardExecutionManifest(manifest ShardExecutionManifest) ([]byte, string, error) {
	manifest.ManifestDigest = ""
	if err := manifest.Validate(); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", fmt.Errorf("encode shard execution manifest: %w", err)
	}
	digest := digestPlanLog(encoded)
	manifest.ManifestDigest = digest
	encoded, err = json.Marshal(manifest)
	if err != nil {
		return nil, "", fmt.Errorf("encode shard execution manifest digest: %w", err)
	}
	return encoded, digest, nil
}

// LoadShardExecutionManifest 严格解码并重算一个 worker manifest。
func LoadShardExecutionManifest(reader io.Reader) (ShardExecutionManifest, error) {
	var manifest ShardExecutionManifest
	if err := decodeStrictJSON(reader, &manifest); err != nil {
		return ShardExecutionManifest{}, fmt.Errorf("decode shard execution manifest: %w", err)
	}
	claimedDigest := manifest.ManifestDigest
	manifest.ManifestDigest = ""
	_, digest, err := EncodeShardExecutionManifest(manifest)
	if err != nil {
		return ShardExecutionManifest{}, err
	}
	if claimedDigest == "" || claimedDigest != digest {
		return ShardExecutionManifest{}, errors.New("shard execution manifest digest does not match canonical bytes")
	}
	manifest.ManifestDigest = claimedDigest
	return manifest, nil
}

// LoadShardExecutionManifestFile 只从 gate-owned 固定路径读取 manifest。
func LoadShardExecutionManifestFile() (ShardExecutionManifest, error) {
	file, err := os.Open(ExecutorShardExecutionManifestPath)
	if err != nil {
		return ShardExecutionManifest{}, fmt.Errorf("open shard execution manifest: %w", err)
	}
	defer file.Close()
	return LoadShardExecutionManifest(file)
}
