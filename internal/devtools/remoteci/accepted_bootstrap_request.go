package remoteci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

const (
	// FullRequestKeyEnvironment 等字段由 coordinator 与候选 Gate installer 共享，
	// 只描述 current full request，不改变 accepted bootstrap 请求。
	FullRequestKeyEnvironment     = "SUPER_DOLPHIN_REMOTE_FULL_REQUEST_KEY"
	FullRequestSHA256Environment  = "SUPER_DOLPHIN_REMOTE_FULL_REQUEST_SHA256"
	FullManifestDigestEnvironment = "SUPER_DOLPHIN_REMOTE_FULL_MANIFEST_DIGEST"
	// acceptedBootstrapShardRequestSchemaVersion 是 accepted ImageCache
	// schema-14 顶层请求版本，独立于当前可演进的 ShardRequest 常量。
	acceptedBootstrapShardRequestSchemaVersion uint32 = 14
	// acceptedBootstrapCompileGroupSchemaVersion 是 accepted nested schema-1
	// compile group 的 identity 版本。
	acceptedBootstrapCompileGroupSchemaVersion uint32 = 1
	// acceptedCompileGroupExecutionPath 是 accepted commit 7b11f3 的冻结字面量。
	acceptedCompileGroupExecutionPath   = "same-eci-shard-worker-test-binary-compile-no-cross-shard-cas/v1"
	acceptedCompileSemanticGoTestNormal = "go-test-selector/v1/race=false"
	acceptedCompileSemanticGoTestRace   = "go-test-selector/v1/race=true"
	acceptedCompileSemanticGoBenchmark  = "go-benchmark-selector/v1/race=false"
)

// acceptedCompileGroup 是 accepted gate 严格接收的 schema-1 nested wire。
// 该类型只存在于 coordinator/materializer/candidate installer 投影边界。
type acceptedCompileGroup struct {
	GroupID             string        `json:"group_id"`
	PackageTarget       string        `json:"package_target"`
	SemanticKey         string        `json:"semantic_key"`
	SharedInputDigest   string        `json:"shared_input_digest"`
	ProfileDigest       string        `json:"profile_digest"`
	ResourceClassID     string        `json:"resource_class_id"`
	WorkloadIDs         []gate.GateID `json:"workload_ids"`
	CompileEstimateMS   int64         `json:"compile_estimate_ms"`
	BodyEstimateMS      int64         `json:"body_estimate_ms"`
	EstimatedDurationMS int64         `json:"estimated_duration_ms"`
}

// BootstrapShardRequest 是 accepted schema-14 顶层请求的冻结 v1 投影。
// full current request 仍只能走 DecodeShardRequest，不能双向兼容。
type BootstrapShardRequest struct {
	SchemaVersion                uint32                   `json:"schema_version"`
	AgentTokenDigest             string                   `json:"agent_token_digest"`
	JobID                        string                   `json:"job_id"`
	ShardIdentity                string                   `json:"shard_identity"`
	Profile                      gate.Profile             `json:"profile"`
	PlanDigest                   string                   `json:"plan_digest"`
	BaselineManifest             string                   `json:"runner_manifest_digest"`
	ImageCacheSnapshotID         string                   `json:"image_cache_snapshot_id"`
	OCIProjectCache              *BaselineOCIProjectCache `json:"oci_project_cache"`
	RunnerBaseTree               string                   `json:"runner_base_tree"`
	BaselineRuntimeImage         string                   `json:"baseline_runtime_image,omitempty"`
	BaselineToolchainDigest      string                   `json:"baseline_toolchain_digest,omitempty"`
	Source                       gate.SourceSpec          `json:"source"`
	SourceTreeSHA                string                   `json:"source_tree_sha"`
	SourceBundleKey              string                   `json:"source_bundle_key"`
	SourceBundleSHA256           string                   `json:"source_bundle_sha256"`
	SourceBundleSize             int64                    `json:"source_bundle_size"`
	ManifestKey                  string                   `json:"manifest_key"`
	ManifestSHA256               string                   `json:"manifest_sha256"`
	CandidateGateSourceSHA256    string                   `json:"candidate_gate_source_sha256"`
	CandidateGateToolchainSHA256 string                   `json:"candidate_gate_toolchain_sha256"`
	GateIDs                      []gate.GateID            `json:"gate_ids"`
	CompileGroups                []acceptedCompileGroup   `json:"compile_groups"`
	ShardExecutionManifestDigest string                   `json:"shard_execution_manifest_digest"`
	Calibration                  bool                     `json:"calibration"`
	ResourceClass                shardresource.Class      `json:"resource_class"`
	CalibrationResource          *shardresource.Class     `json:"calibration_resource,omitempty"`
}

// acceptedBootstrapManifest 是 accepted 固定路径上暂时存在的 v1 manifest。
// 当前 worker 不读取它；候选 installer 只用它做一次稳定 identity 交叉校验。
type acceptedBootstrapManifest struct {
	SchemaVersion  uint32                 `json:"schema_version"`
	Profile        gate.Profile           `json:"profile"`
	PlanDigest     string                 `json:"plan_digest"`
	ShardIdentity  string                 `json:"shard_identity"`
	SourceTreeSHA  string                 `json:"source_tree_sha"`
	GateIDs        []gate.GateID          `json:"gate_ids"`
	CompileGroups  []acceptedCompileGroup `json:"compile_groups"`
	ManifestDigest string                 `json:"manifest_digest"`
}

// Validate 实现 strict JSON 需要的 accepted v1 manifest 校验接口。
func (manifest acceptedBootstrapManifest) Validate() error {
	return validateAcceptedBootstrapManifest(manifest)
}

// Validate 校验 accepted 顶层 schema-14 与 v1 manifest 摘要绑定。
func (request BootstrapShardRequest) Validate() error {
	if request.SchemaVersion != acceptedBootstrapShardRequestSchemaVersion {
		return fmt.Errorf("accepted bootstrap shard request schema_version must equal %d", acceptedBootstrapShardRequestSchemaVersion)
	}
	projectedGateIDs, err := ProjectAcceptedGateIDs(request.GateIDs)
	if err != nil {
		return err
	}
	if !slices.Equal(projectedGateIDs, request.GateIDs) {
		return errors.New("accepted bootstrap shard request gate_ids are not projected")
	}
	common := request.AsShardRequest()
	if err := validateAcceptedBootstrapRequestCore(common, request.GateIDs); err != nil {
		return err
	}
	manifest := acceptedBootstrapManifestFromRequest(request)
	_, digest, err := encodeAcceptedBootstrapManifest(manifest)
	if err != nil {
		return err
	}
	if request.ShardExecutionManifestDigest != digest {
		return errors.New("accepted bootstrap shard execution manifest digest drifted")
	}
	return nil
}

// validateAcceptedBootstrapRequestCore 校验不含 v1 nested manifest 的顶层闭包。
func validateAcceptedBootstrapRequestCore(request ShardRequest, gateIDs []gate.GateID) error {
	validators := []func() error{
		request.validateIdentity,
		request.validateOCIProjectCache,
		request.validateSource,
		request.validateObjects,
		request.validateCalibrationResource,
		request.validateResourceClass,
		func() error { return validateGateIDs(gateIDs) },
	}
	for _, validate := range validators {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

// ProjectAcceptedCompileGroups 按 accepted schema-1 算法投影 current v2 groups；
// expansion-only nilness groups 不属于 accepted 编译闭包，投影时过滤，current
// request 本身仍保留精确 workload IDs。
func ProjectAcceptedCompileGroups(groups []gate.CompileGroup) ([]acceptedCompileGroup, error) {
	projected := make([]acceptedCompileGroup, 0, len(groups))
	for index, group := range groups {
		if len(group.WorkloadIDs) == 0 {
			return nil, fmt.Errorf("compile_groups[%d] accepted workload projection: workload IDs must not be empty", index)
		}
		workloadIDs, err := projectAcceptedCompileGroupWorkloadIDs(group.WorkloadIDs)
		if err != nil {
			return nil, fmt.Errorf("compile_groups[%d] accepted workload projection: %w", index, err)
		}
		if len(workloadIDs) == 0 {
			continue
		}
		group.WorkloadIDs = workloadIDs
		item, err := projectAcceptedCompileGroup(group)
		if err != nil {
			return nil, fmt.Errorf("compile_groups[%d] accepted projection: %w", index, err)
		}
		projected = append(projected, item)
	}
	return projected, nil
}

func projectAcceptedCompileGroupWorkloadIDs(ids []gate.GateID) ([]gate.GateID, error) {
	projected := make([]gate.GateID, 0, len(ids))
	filteredNilness := false
	for index, id := range ids {
		parent, _, _, targeted, err := gate.ParseWorkloadID(string(id))
		if err != nil {
			return nil, fmt.Errorf("workload_ids[%d]: %w", index, err)
		}
		if targeted && parent == gate.GateIDBackendNilness {
			filteredNilness = true
			continue
		}
		projected = append(projected, id)
	}
	if filteredNilness && len(projected) != 0 {
		return nil, errors.New("expansion-only nilness workload is mixed with accepted compile workload")
	}
	return projected, nil
}

// ProjectAcceptedGateIDs 将 current v2 的精确 workload ID 投影为 accepted v1 可解码的 gate 集合。
// expansion-only nilness workload 仍由 current request 保留；bootstrap 只携带其 canonical parent。
func ProjectAcceptedGateIDs(ids []gate.GateID) ([]gate.GateID, error) {
	projected := make([]gate.GateID, 0, len(ids))
	seen := make(map[gate.GateID]struct{}, len(ids))
	for index, id := range ids {
		parent, _, _, targeted, err := gate.ParseWorkloadID(string(id))
		if err != nil {
			return nil, fmt.Errorf("gate_ids[%d] accepted projection: %w", index, err)
		}
		if targeted && parent == gate.GateIDBackendNilness {
			id = parent
		}
		if _, duplicate := seen[id]; duplicate {
			if id == gate.GateIDBackendNilness {
				continue
			}
			return nil, fmt.Errorf("gate_ids[%d] accepted projection is duplicated", index)
		}
		seen[id] = struct{}{}
		projected = append(projected, id)
	}
	return projected, nil
}

func projectAcceptedCompileGroup(group gate.CompileGroup) (acceptedCompileGroup, error) {
	projected := acceptedCompileGroup{
		PackageTarget: group.PackageTarget, SemanticKey: group.SemanticKey,
		SharedInputDigest: group.SharedInputDigest, ProfileDigest: group.ProfileDigest,
		ResourceClassID: group.ResourceClassID, WorkloadIDs: slices.Clone(group.WorkloadIDs),
		CompileEstimateMS: group.CompileEstimateMS, BodyEstimateMS: group.BodyEstimateMS,
		EstimatedDurationMS: group.EstimatedDurationMS,
	}
	if err := validateAcceptedCompileGroupCore(projected); err != nil {
		return acceptedCompileGroup{}, err
	}
	groupID, err := acceptedCompileGroupID(projected)
	if err != nil {
		return acceptedCompileGroup{}, err
	}
	projected.GroupID = groupID
	return projected, nil
}

func validateAcceptedCompileGroupCore(group acceptedCompileGroup) error {
	validators := []func() error{
		func() error { return validateAcceptedCompileGroupIdentity(group) },
		func() error { return validateAcceptedCompileGroupWorkloads(group.WorkloadIDs) },
		func() error { return validateAcceptedCompileGroupEstimates(group) },
	}
	for _, validate := range validators {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateAcceptedCompileGroupIdentity 校验 accepted v1 编译输入和资源身份。
func validateAcceptedCompileGroupIdentity(group acceptedCompileGroup) error {
	if !acceptedCompileGroupPackageTarget(group.PackageTarget) {
		return errors.New("accepted compile group package target is invalid")
	}
	if err := validateAcceptedCompileGroupSemanticKey(group.SemanticKey); err != nil {
		return err
	}
	if !acceptedPrefixedDigest(group.SharedInputDigest) || !acceptedPrefixedDigest(group.ProfileDigest) {
		return errors.New("accepted compile group input digest is invalid")
	}
	if strings.TrimSpace(group.ResourceClassID) != group.ResourceClassID || group.ResourceClassID == "" {
		return errors.New("accepted compile group resource class is invalid")
	}
	return nil
}

// validateAcceptedCompileGroupSemanticKey 锁定 accepted commit 7b11f3 支持的三种语义。
func validateAcceptedCompileGroupSemanticKey(value string) error {
	if strings.TrimSpace(value) != value {
		return errors.New("accepted compile group semantic key is not canonical")
	}
	switch value {
	case acceptedCompileSemanticGoTestNormal, acceptedCompileSemanticGoTestRace, acceptedCompileSemanticGoBenchmark:
		return nil
	default:
		return fmt.Errorf("accepted compile group semantic key %q is unsupported", value)
	}
}

// validateAcceptedCompileGroupWorkloads 校验 accepted v1 成员非空且不重复。
func validateAcceptedCompileGroupWorkloads(workloadIDs []gate.GateID) error {
	if len(workloadIDs) == 0 {
		return errors.New("accepted compile group workload IDs must not be empty")
	}
	seen := make(map[gate.GateID]struct{}, len(workloadIDs))
	for _, workloadID := range workloadIDs {
		if workloadID == "" {
			return errors.New("accepted compile group workload ID is empty")
		}
		if _, duplicate := seen[workloadID]; duplicate {
			return fmt.Errorf("accepted compile group workload ID %q is duplicated", workloadID)
		}
		seen[workloadID] = struct{}{}
	}
	return nil
}

// validateAcceptedCompileGroupEstimates 校验 accepted v1 编译和正文估时闭包。
func validateAcceptedCompileGroupEstimates(group acceptedCompileGroup) error {
	if group.CompileEstimateMS <= 0 || group.BodyEstimateMS <= 0 || group.EstimatedDurationMS <= 0 ||
		group.CompileEstimateMS > int64(^uint64(0)>>1)-group.BodyEstimateMS ||
		group.EstimatedDurationMS != group.CompileEstimateMS+group.BodyEstimateMS {
		return errors.New("accepted compile group estimate closure is invalid")
	}
	return nil
}

func acceptedCompileGroupPackageTarget(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "/") ||
		strings.Contains(value, "...") || strings.ContainsAny(value, "\\\x00\r\n,") || !strings.HasPrefix(value, "./") {
		return false
	}
	withoutPrefix := strings.TrimPrefix(value, "./")
	return withoutPrefix != "" && path.Clean(withoutPrefix) == withoutPrefix
}

func acceptedCompileArtifactKey(group acceptedCompileGroup) (string, error) {
	if err := validateAcceptedCompileGroupCore(group); err != nil {
		return "", err
	}
	material := struct {
		SchemaVersion     uint32 `json:"schema_version"`
		ExecutionPath     string `json:"execution_path"`
		PackageTarget     string `json:"package_target"`
		SemanticKey       string `json:"semantic_key"`
		SharedInputDigest string `json:"shared_input_digest"`
		ProfileDigest     string `json:"profile_digest"`
	}{acceptedBootstrapCompileGroupSchemaVersion, acceptedCompileGroupExecutionPath, group.PackageTarget, group.SemanticKey, group.SharedInputDigest, group.ProfileDigest}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal accepted compile artifact identity: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func acceptedCompileGroupID(group acceptedCompileGroup) (string, error) {
	artifactKey, err := acceptedCompileArtifactKey(group)
	if err != nil {
		return "", err
	}
	material := struct {
		SchemaVersion uint32        `json:"schema_version"`
		ArtifactKey   string        `json:"artifact_key"`
		ResourceClass string        `json:"resource_class_id"`
		WorkloadIDs   []gate.GateID `json:"workload_ids"`
	}{acceptedBootstrapCompileGroupSchemaVersion, artifactKey, group.ResourceClassID, slices.Clone(group.WorkloadIDs)}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal accepted compile group identity: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// AcceptedBootstrapManifestFromRequest 构造 accepted 临时 v1 manifest。
func acceptedBootstrapManifestFromRequest(request BootstrapShardRequest) acceptedBootstrapManifest {
	return acceptedBootstrapManifest{
		SchemaVersion: gate.ShardExecutionManifestSchemaVersion, Profile: request.Profile,
		PlanDigest: request.PlanDigest, ShardIdentity: request.ShardIdentity,
		SourceTreeSHA: request.SourceTreeSHA, GateIDs: slices.Clone(request.GateIDs),
		CompileGroups: slices.Clone(request.CompileGroups),
	}
}

func encodeAcceptedBootstrapManifest(manifest acceptedBootstrapManifest) ([]byte, string, error) {
	manifest.ManifestDigest = ""
	if err := validateAcceptedBootstrapManifest(manifest); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", fmt.Errorf("encode accepted bootstrap manifest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	manifest.ManifestDigest = digest
	encoded, err = json.Marshal(manifest)
	if err != nil {
		return nil, "", fmt.Errorf("encode accepted bootstrap manifest digest: %w", err)
	}
	return encoded, digest, nil
}

// EncodeAcceptedBootstrapManifestForRequest 生成 accepted 暂时 manifest 的
// canonical bytes；当前 worker 不消费该 v1 模型，候选 installer 只作交叉校验。
func EncodeAcceptedBootstrapManifestForRequest(request BootstrapShardRequest) ([]byte, string, error) {
	gateIDs, err := ProjectAcceptedGateIDs(request.GateIDs)
	if err != nil {
		return nil, "", err
	}
	request.GateIDs = gateIDs
	return encodeAcceptedBootstrapManifest(acceptedBootstrapManifestFromRequest(request))
}

// ValidateAcceptedBootstrapManifestBytes 严格校验 fixed path 上的 accepted
// v1 manifest 与其声明摘要，避免候选 installer 接受脏文件。
func ValidateAcceptedBootstrapManifestBytes(data []byte, expectedDigest string) error {
	var manifest acceptedBootstrapManifest
	if err := gate.DecodeStrictJSON(data, &manifest); err != nil {
		return fmt.Errorf("decode accepted bootstrap manifest: %w", err)
	}
	claimed := manifest.ManifestDigest
	manifest.ManifestDigest = ""
	_, digest, err := encodeAcceptedBootstrapManifest(manifest)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != digest || (expectedDigest != "" && expectedDigest != digest) {
		return errors.New("accepted bootstrap manifest digest mismatch")
	}
	return nil
}

func validateAcceptedBootstrapManifest(manifest acceptedBootstrapManifest) error {
	if err := validateAcceptedBootstrapManifestHeader(manifest); err != nil {
		return err
	}
	gateSet, err := acceptedBootstrapManifestGateSet(manifest.GateIDs)
	if err != nil {
		return err
	}
	grouped, err := acceptedBootstrapManifestGroups(manifest.CompileGroups, gateSet)
	if err != nil {
		return err
	}
	return validateAcceptedBootstrapManifestCoverage(manifest.GateIDs, grouped)
}

func validateAcceptedBootstrapManifestHeader(manifest acceptedBootstrapManifest) error {
	if manifest.SchemaVersion != 1 || !acceptedPrefixedDigest(manifest.PlanDigest) || !acceptedPrefixedDigest(manifest.ShardIdentity) ||
		!acceptedOID(manifest.SourceTreeSHA) || len(manifest.GateIDs) == 0 {
		return errors.New("accepted bootstrap manifest identity is invalid")
	}
	return nil
}

func acceptedBootstrapManifestGateSet(gateIDs []gate.GateID) (map[gate.GateID]struct{}, error) {
	gateSet := make(map[gate.GateID]struct{}, len(gateIDs))
	for _, id := range gateIDs {
		if id == "" {
			return nil, errors.New("accepted bootstrap manifest gate ID is empty")
		}
		if _, duplicate := gateSet[id]; duplicate {
			return nil, fmt.Errorf("accepted bootstrap manifest gate ID %q is duplicated", id)
		}
		gateSet[id] = struct{}{}
	}
	return gateSet, nil
}

func acceptedBootstrapManifestGroups(groups []acceptedCompileGroup, gateSet map[gate.GateID]struct{}) (map[gate.GateID]struct{}, error) {
	grouped := make(map[gate.GateID]struct{})
	groupIDs := make(map[string]struct{}, len(groups))
	for index, group := range groups {
		if err := validateAcceptedBootstrapManifestGroup(index, group, gateSet, groupIDs, grouped); err != nil {
			return nil, err
		}
	}
	return grouped, nil
}

func validateAcceptedBootstrapManifestGroup(index int, group acceptedCompileGroup, gateSet map[gate.GateID]struct{}, groupIDs map[string]struct{}, grouped map[gate.GateID]struct{}) error {
	if err := validateAcceptedCompileGroupCore(group); err != nil {
		return fmt.Errorf("accepted bootstrap manifest compile_groups[%d]: %w", index, err)
	}
	id, err := acceptedCompileGroupID(group)
	if err != nil || id != group.GroupID {
		return fmt.Errorf("accepted bootstrap manifest compile_groups[%d] group identity drifted", index)
	}
	if _, duplicate := groupIDs[group.GroupID]; duplicate {
		return fmt.Errorf("accepted bootstrap manifest compile group %q is duplicated", group.GroupID)
	}
	groupIDs[group.GroupID] = struct{}{}
	return registerAcceptedBootstrapManifestWorkloads(group, gateSet, grouped)
}

func registerAcceptedBootstrapManifestWorkloads(group acceptedCompileGroup, gateSet map[gate.GateID]struct{}, grouped map[gate.GateID]struct{}) error {
	for _, workloadID := range group.WorkloadIDs {
		if _, ok := gateSet[workloadID]; !ok {
			return fmt.Errorf("accepted bootstrap manifest workload %q is outside gate_ids", workloadID)
		}
		if _, duplicate := grouped[workloadID]; duplicate {
			return fmt.Errorf("accepted bootstrap manifest workload %q is duplicated", workloadID)
		}
		grouped[workloadID] = struct{}{}
	}
	return nil
}

func validateAcceptedBootstrapManifestCoverage(gateIDs []gate.GateID, grouped map[gate.GateID]struct{}) error {
	for _, workloadID := range gateIDs {
		if gate.CompileGroupWorkloadSupported(workloadID) {
			if _, ok := grouped[workloadID]; !ok {
				return fmt.Errorf("accepted bootstrap manifest workload %q is not covered", workloadID)
			}
		}
	}
	return nil
}

// EncodeBootstrapShardRequest 生成 accepted 顶层 schema-14/v1 nested 请求。
func EncodeBootstrapShardRequest(request ShardRequest) ([]byte, string, error) {
	if err := request.Validate(); err != nil {
		return nil, "", err
	}
	groups, err := ProjectAcceptedCompileGroups(request.CompileGroups)
	if err != nil {
		return nil, "", err
	}
	gateIDs, err := ProjectAcceptedGateIDs(request.GateIDs)
	if err != nil {
		return nil, "", err
	}
	bootstrap := BootstrapShardRequest{
		SchemaVersion: acceptedBootstrapShardRequestSchemaVersion, AgentTokenDigest: request.AgentTokenDigest,
		JobID: request.JobID, ShardIdentity: request.ShardIdentity, Profile: request.Profile, PlanDigest: request.PlanDigest,
		BaselineManifest: request.BaselineManifest, ImageCacheSnapshotID: request.ImageCacheSnapshotID,
		OCIProjectCache: request.OCIProjectCache, RunnerBaseTree: request.RunnerBaseTree,
		BaselineRuntimeImage: request.BaselineRuntimeImage, BaselineToolchainDigest: request.BaselineToolchainDigest,
		Source: request.Source, SourceTreeSHA: request.SourceTreeSHA, SourceBundleKey: request.SourceBundleKey,
		SourceBundleSHA256: request.SourceBundleSHA256, SourceBundleSize: request.SourceBundleSize,
		ManifestKey: request.ManifestKey, ManifestSHA256: request.ManifestSHA256,
		CandidateGateSourceSHA256: request.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: request.CandidateGateToolchainSHA256,
		GateIDs: gateIDs, CompileGroups: groups,
		Calibration: request.Calibration, ResourceClass: request.ResourceClass, CalibrationResource: request.CalibrationResource,
	}
	manifest := acceptedBootstrapManifestFromRequest(bootstrap)
	_, manifestDigest, err := encodeAcceptedBootstrapManifest(manifest)
	if err != nil {
		return nil, "", err
	}
	bootstrap.ShardExecutionManifestDigest = manifestDigest
	if err := bootstrap.Validate(); err != nil {
		return nil, "", fmt.Errorf("validate accepted bootstrap request: %w", err)
	}
	data, err := json.Marshal(bootstrap)
	if err != nil {
		return nil, "", fmt.Errorf("encode accepted bootstrap request: %w", err)
	}
	if len(data) > ShardRequestMaxBytes {
		return nil, "", errors.New("accepted bootstrap shard request exceeds canonical byte limit")
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

// DecodeBootstrapShardRequest 严格读取 accepted-only projection，并执行完整校验。
func DecodeBootstrapShardRequest(data []byte) (BootstrapShardRequest, error) {
	if len(data) == 0 || len(data) > ShardRequestMaxBytes {
		return BootstrapShardRequest{}, errors.New("accepted bootstrap shard request exceeds canonical byte limit")
	}
	var request BootstrapShardRequest
	if err := gate.DecodeStrictJSON(data, &request); err != nil {
		return BootstrapShardRequest{}, fmt.Errorf("decode accepted bootstrap shard request: %w", err)
	}
	if err := request.Validate(); err != nil {
		return BootstrapShardRequest{}, fmt.Errorf("validate accepted bootstrap shard request: %w", err)
	}
	return request, nil
}

// AsShardRequest 返回 materializer 需要的顶层字段，不把 v1 groups 交给当前 worker。
func (request BootstrapShardRequest) AsShardRequest() ShardRequest {
	return ShardRequest{
		SchemaVersion: request.SchemaVersion, AgentTokenDigest: request.AgentTokenDigest, JobID: request.JobID,
		ShardIdentity: request.ShardIdentity, Profile: request.Profile, PlanDigest: request.PlanDigest,
		BaselineManifest: request.BaselineManifest, ImageCacheSnapshotID: request.ImageCacheSnapshotID,
		OCIProjectCache: request.OCIProjectCache, RunnerBaseTree: request.RunnerBaseTree,
		BaselineRuntimeImage: request.BaselineRuntimeImage, BaselineToolchainDigest: request.BaselineToolchainDigest,
		Source: request.Source, SourceTreeSHA: request.SourceTreeSHA, SourceBundleKey: request.SourceBundleKey,
		SourceBundleSHA256: request.SourceBundleSHA256, SourceBundleSize: request.SourceBundleSize,
		ManifestKey: request.ManifestKey, ManifestSHA256: request.ManifestSHA256,
		CandidateGateSourceSHA256: request.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: request.CandidateGateToolchainSHA256,
		GateIDs: slices.Clone(request.GateIDs), ShardExecutionManifestDigest: request.ShardExecutionManifestDigest,
		Calibration: request.Calibration, ResourceClass: request.ResourceClass, CalibrationResource: request.CalibrationResource,
	}
}

// ValidateBootstrapIdentity 交叉校验 bootstrap 与 full request 的稳定顶层身份。
func ValidateBootstrapIdentity(bootstrap BootstrapShardRequest, full ShardRequest) error {
	if bootstrap.SchemaVersion != acceptedBootstrapShardRequestSchemaVersion || full.SchemaVersion != ShardRequestSchemaVersion {
		return errors.New("remote shard request schema identity is invalid")
	}
	bootstrapComparable := bootstrap.AsShardRequest()
	fullComparable := full
	projectedGateIDs, err := ProjectAcceptedGateIDs(fullComparable.GateIDs)
	if err != nil {
		return err
	}
	fullComparable.GateIDs = projectedGateIDs
	bootstrapComparable.ShardExecutionManifestDigest = ""
	fullComparable.ShardExecutionManifestDigest = ""
	bootstrapComparable.CompileGroups = nil
	fullComparable.CompileGroups = nil
	if !reflect.DeepEqual(bootstrapComparable, fullComparable) {
		return errors.New("accepted bootstrap and full shard request stable identity drifted")
	}
	projected, err := ProjectAcceptedCompileGroups(full.CompileGroups)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(projected, bootstrap.CompileGroups) {
		return errors.New("accepted bootstrap and full compile-group projection drifted")
	}
	manifest := acceptedBootstrapManifestFromRequest(bootstrap)
	_, digest, err := encodeAcceptedBootstrapManifest(manifest)
	if err != nil {
		return err
	}
	if bootstrap.ShardExecutionManifestDigest != digest {
		return errors.New("accepted bootstrap manifest digest drifted")
	}
	return nil
}

func acceptedPrefixedDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil && strings.ToLower(value[len("sha256:"):]) == value[len("sha256:"):]
}

func acceptedOID(value string) bool {
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
