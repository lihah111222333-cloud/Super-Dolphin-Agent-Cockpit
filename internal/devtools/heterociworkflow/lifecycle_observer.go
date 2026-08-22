package heterociworkflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// PreparedRunSnapshotSchemaVersion 标识首个 Start 前的完整生命周期观察快照。
const PreparedRunSnapshotSchemaVersion = "remote-ci-prepared-run/v1"

// LifecycleObserver 是 storage-neutral 的生命周期观察端口。
// 实现方负责在一次调用中持久化 reservation 前四阶段所需的事件事实；
// domain workflow 不知道 PG、事件表或具体日志编码。
type LifecycleObserver interface {
	ObservePreparedRun(context.Context, PreparedRunSnapshot) error
}

func (workflow *Workflow) observePreparedRun(ctx context.Context, request RunRequest, prepared *preparedRun) error {
	if workflow.deps.LifecycleObserver == nil {
		return nil
	}
	observation := clonePreparedRunSnapshot(PreparedRunSnapshot{
		SchemaVersion: PreparedRunSnapshotSchemaVersion, ExternalOperationID: request.ExternalOperationID,
		DurableRunID: request.DurableRunID, RequestDigest: request.RequestDigest, ManifestDigest: request.ManifestDigest,
		ExternalBindingDigest: request.ExternalBindingDigest, ManifestBlobSHA256: request.ManifestBlobSHA256,
		RuntimePolicyDigest:    request.RuntimePolicyDigest,
		TopologyManifestDigest: request.TopologyManifestDigest, ManifestPath: request.ManifestPath,
		RepositoryID: request.RepositoryID, SourceTreeSHA: request.SourceTreeSHA, RunID: request.RunID,
		HeartbeatTTL: request.HeartbeatTTL, Units: request.Units, Fence: prepared.state.fence, Manifest: prepared.manifest,
		SourceSnapshot: prepared.planned.SourceSnapshot, EffectiveSnapshot: prepared.planned.EffectiveSnapshot,
		Plan: prepared.planned.Plan, Decision: prepared.planned.Decision, Leases: prepared.planned.Leases,
		BindingPlan: prepared.bindingPlan, BindingDigest: prepared.bindingPlan.BindingDigest,
	})
	if err := observation.Validate(); err != nil {
		return fmt.Errorf("%w: validate prepared lifecycle observation: %w", ErrExecutorBinding, err)
	}
	if err := workflow.deps.LifecycleObserver.ObservePreparedRun(ctx, observation); err != nil {
		return fmt.Errorf("%w: observe prepared lifecycle: %w", ErrExecutorBinding, err)
	}
	return nil
}

// PreparedRunSnapshot 是首个 Start mutation 前的不可变、完整准备态投影。
// workflow 传入 observer 的是深拷贝；observer 修改收到的 slice 不会影响内部状态。
type PreparedRunSnapshot struct {
	SchemaVersion          string                   `json:"schema_version"`
	ExternalOperationID    heteroci.OperationID     `json:"external_operation_id"`
	DurableRunID           heteroci.TopologyRunID   `json:"durable_run_id"`
	RequestDigest          string                   `json:"request_digest"`
	ManifestDigest         string                   `json:"manifest_digest"`
	ExternalBindingDigest  string                   `json:"external_binding_digest"`
	ManifestBlobSHA256     string                   `json:"manifest_blob_sha256"`
	RuntimePolicyDigest    string                   `json:"runtime_policy_digest"`
	TopologyManifestDigest string                   `json:"topology_manifest_digest"`
	ManifestPath           string                   `json:"manifest_path"`
	RepositoryID           string                   `json:"repository_id"`
	SourceTreeSHA          string                   `json:"source_tree_sha"`
	RunID                  string                   `json:"run_id"`
	HeartbeatTTL           time.Duration            `json:"heartbeat_ttl"`
	Units                  []heteroci.ExecutionUnit `json:"units"`
	Fence                  heteroci.OwnerFence      `json:"fence"`
	Manifest               heteroci.Manifest        `json:"manifest"`
	SourceSnapshot         heteroci.SnapshotSet     `json:"source_snapshot"`
	EffectiveSnapshot      heteroci.SnapshotSet     `json:"effective_snapshot"`
	Plan                   heteroci.PackingPlan     `json:"plan"`
	Decision               heteroci.RoutingDecision `json:"decision"`
	Leases                 []heteroci.RoutingLease  `json:"leases"`
	BindingPlan            ExecutorBindingPlan      `json:"binding_plan"`
	BindingDigest          string                   `json:"binding_digest"`
}

// Validate 校验准备态快照的请求审计、拓扑、装箱和 binding identity 是否逐字段闭合。
func (snapshot PreparedRunSnapshot) Validate() error {
	if snapshot.SchemaVersion != PreparedRunSnapshotSchemaVersion {
		return errors.New("prepared run snapshot schema is invalid")
	}
	return snapshot.validatePreparedProjection()
}

func (snapshot PreparedRunSnapshot) validatePreparedProjection() error {
	validators := []func() error{
		snapshot.validateRequestIdentity,
		func() error {
			if err := snapshot.Manifest.Validate(); err != nil {
				return fmt.Errorf("prepared run manifest: %w", err)
			}
			return nil
		},
		func() error {
			if err := heteroci.ValidateExecutionUnits(snapshot.Units); err != nil {
				return fmt.Errorf("prepared run units: %w", err)
			}
			return nil
		},
		snapshot.validateSnapshotsAndPlan,
		snapshot.validateLeases,
		snapshot.validateBinding,
	}
	for _, validate := range validators {
		if err := validate(); err != nil {
			return err
		}
	}
	return nil
}

func (snapshot PreparedRunSnapshot) validateBinding() error {
	if err := ValidateDurableExecutorBindingPlan(snapshot.BindingPlan); err != nil {
		return fmt.Errorf("prepared run binding plan: %w", err)
	}
	if snapshot.BindingDigest == "" || snapshot.BindingDigest != snapshot.BindingPlan.BindingDigest {
		return errors.New("prepared run binding digest does not match binding plan")
	}
	return validatePreparedBindingAudit(snapshot)
}

// validatePreparedBindingAudit 校验 binding plan 与 request audit fields 完全一致。
func validatePreparedBindingAudit(snapshot PreparedRunSnapshot) error {
	plan := snapshot.BindingPlan
	if plan.ExternalOperationID != snapshot.ExternalOperationID || plan.DurableRunID != snapshot.DurableRunID ||
		plan.RequestDigest != snapshot.RequestDigest || plan.ManifestDigest != snapshot.ManifestDigest ||
		plan.ExternalBindingDigest != snapshot.ExternalBindingDigest || plan.ManifestBlobSHA256 != snapshot.ManifestBlobSHA256 ||
		plan.RuntimePolicyDigest != snapshot.RuntimePolicyDigest ||
		plan.TopologyManifestDigest != snapshot.TopologyManifestDigest {
		return errors.New("prepared run binding plan audit identity drifted")
	}
	return nil
}

// validateRequestIdentity 校验公开准备态的 request identity 和其派生摘要。
func (snapshot PreparedRunSnapshot) validateRequestIdentity() error {
	if err := snapshot.validatePreparedIdentityFields(); err != nil {
		return err
	}
	if err := snapshot.validatePreparedAuditIdentity(); err != nil {
		return err
	}
	return snapshot.validatePreparedDigests()
}

// validatePreparedIdentityFields 校验准备态公开字段的格式和必填边界。
func (snapshot PreparedRunSnapshot) validatePreparedIdentityFields() error {
	if snapshot.ExternalOperationID.Validate() != nil || snapshot.DurableRunID.Validate() != nil {
		return errors.New("prepared run operation or durable run identity is invalid")
	}
	if snapshot.HeartbeatTTL <= 0 || snapshot.HeartbeatTTL.Milliseconds() <= 0 {
		return errors.New("prepared run heartbeat TTL is invalid")
	}
	for _, field := range []struct{ value, name string }{
		{snapshot.ManifestPath, "manifest path"}, {snapshot.RepositoryID, "repository identity"},
		{snapshot.SourceTreeSHA, "source tree identity"}, {snapshot.RunID, "run identity"},
	} {
		if field.value == "" || strings.TrimSpace(field.value) != field.value || filepathHasControl(field.value) {
			return fmt.Errorf("prepared run %s is invalid", field.name)
		}
	}
	return nil
}

func (snapshot PreparedRunSnapshot) validatePreparedAuditIdentity() error {
	if err := heteroci.ValidateTopologyAuditIdentity(snapshot.RepositoryID, snapshot.SourceTreeSHA, snapshot.RunID); err != nil {
		return fmt.Errorf("prepared run audit identity: %w", err)
	}
	identity, err := heteroci.DeriveTopologyRunIdentity(snapshot.RepositoryID, snapshot.SourceTreeSHA, snapshot.RunID)
	if err != nil || identity.DurableRunID != snapshot.DurableRunID {
		return errors.New("prepared run durable identity does not match audit fields")
	}
	return nil
}

// validatePreparedDigests 校验 request 与 manifest 摘要可由公开字段重算。
func (snapshot PreparedRunSnapshot) validatePreparedDigests() error {
	if err := validatePreparedAuditDigests(snapshot); err != nil {
		return err
	}
	request := RunRequest{SchemaVersion: RunRequestSchemaVersion, ExternalOperationID: snapshot.ExternalOperationID, DurableRunID: snapshot.DurableRunID, RequestDigest: snapshot.RequestDigest, ManifestDigest: snapshot.ManifestDigest, ExternalBindingDigest: snapshot.ExternalBindingDigest, ManifestBlobSHA256: snapshot.ManifestBlobSHA256, RuntimePolicyDigest: snapshot.RuntimePolicyDigest, TopologyManifestDigest: snapshot.TopologyManifestDigest, RepositoryID: snapshot.RepositoryID, SourceTreeSHA: snapshot.SourceTreeSHA, RunID: snapshot.RunID, Units: snapshot.Units, HeartbeatTTL: snapshot.HeartbeatTTL}
	want, err := DeriveRunRequestDigest(request)
	if err != nil || want != snapshot.RequestDigest {
		return errors.New("prepared run request digest does not match audit fields")
	}
	wantManifest, err := DeriveManifestDigest(snapshot.Manifest)
	if err != nil || wantManifest != snapshot.TopologyManifestDigest {
		return errors.New("prepared run topology manifest digest does not match manifest")
	}
	return nil
}

func validatePreparedAuditDigests(snapshot PreparedRunSnapshot) error {
	for _, digest := range []string{snapshot.RequestDigest, snapshot.ManifestDigest, snapshot.ExternalBindingDigest, snapshot.ManifestBlobSHA256, snapshot.RuntimePolicyDigest, snapshot.TopologyManifestDigest} {
		if !validPreparedDigest(digest) {
			return errors.New("prepared run digest identity is invalid")
		}
	}
	return nil
}

// validateSnapshotsAndPlan 校验 source/effective snapshot、packing plan 和 routing decision 的闭合关系。
func (snapshot PreparedRunSnapshot) validateSnapshotsAndPlan() error {
	if err := snapshot.validatePreparedSnapshotIdentity(); err != nil {
		return err
	}
	validationTime := time.UnixMilli(snapshot.EffectiveSnapshot.CapturedAtUnixMilli).UTC()
	plan := snapshot.Plan
	if plan.NodeShards == nil {
		plan.NodeShards = []heteroci.NodeShard{}
	}
	if plan.UnassignedUnitIDs == nil {
		plan.UnassignedUnitIDs = []string{}
	}
	return snapshot.validatePreparedSnapshotValues(plan, validationTime)
}

func (snapshot PreparedRunSnapshot) validatePreparedSnapshotIdentity() error {
	if snapshot.Fence.OwnerEpoch <= 0 || snapshot.Fence.Revision <= 0 {
		return errors.New("prepared run fence is invalid")
	}
	if snapshot.SourceSnapshot.OwnerEpoch != snapshot.Fence.OwnerEpoch || snapshot.EffectiveSnapshot.OwnerEpoch != snapshot.Fence.OwnerEpoch {
		return errors.New("prepared run snapshot owner epoch drifted")
	}
	return nil
}

// validatePreparedSnapshotValues 校验两个快照及其派生 plan/decision 的内容一致性。
func (snapshot PreparedRunSnapshot) validatePreparedSnapshotValues(plan heteroci.PackingPlan, validationTime time.Time) error {
	if err := snapshot.EffectiveSnapshot.ValidateAgainst(snapshot.Manifest, validationTime, snapshot.HeartbeatTTL); err != nil {
		return fmt.Errorf("prepared run effective snapshot: %w", err)
	}
	sourceValidationTime := time.UnixMilli(snapshot.SourceSnapshot.CapturedAtUnixMilli).UTC()
	if err := snapshot.SourceSnapshot.ValidateAgainst(snapshot.Manifest, sourceValidationTime, snapshot.HeartbeatTTL); err != nil {
		return fmt.Errorf("prepared run source snapshot: %w", err)
	}
	if err := plan.ValidateAgainst(snapshot.Manifest, snapshot.EffectiveSnapshot, snapshot.Units, validationTime, snapshot.HeartbeatTTL); err != nil {
		return fmt.Errorf("prepared run packing plan: %w", err)
	}
	if err := snapshot.Decision.ValidateAgainst(snapshot.Manifest, snapshot.EffectiveSnapshot, snapshot.Units, plan, validationTime, snapshot.HeartbeatTTL); err != nil {
		return fmt.Errorf("prepared run routing decision: %w", err)
	}
	return nil
}

// validateLeases 校验 planned lease 与 decision assignment 的逐 unit 覆盖和身份一致性。
func (snapshot PreparedRunSnapshot) validateLeases() error {
	if len(snapshot.Leases) == 0 {
		return errors.New("prepared run leases are empty")
	}
	assignments := make(map[string]heteroci.RouteAssignment, len(snapshot.Decision.Assignments))
	for _, assignment := range snapshot.Decision.Assignments {
		assignments[assignment.UnitID] = assignment
	}
	seen := make(map[string]struct{}, len(snapshot.Leases))
	for _, lease := range snapshot.Leases {
		if err := validatePreparedLease(snapshot, lease, assignments); err != nil {
			return err
		}
		if _, exists := seen[lease.UnitID]; exists {
			return fmt.Errorf("prepared run lease %q is duplicated", lease.UnitID)
		}
		seen[lease.UnitID] = struct{}{}
	}
	return validatePreparedLeaseCoverage(snapshot.Units, seen)
}

// validatePreparedLease 校验单个 lease 与 decision assignment 的节点和生命周期身份。
func validatePreparedLease(snapshot PreparedRunSnapshot, lease heteroci.RoutingLease, assignments map[string]heteroci.RouteAssignment) error {
	assignment, exists := assignments[lease.UnitID]
	if !exists || lease.RunID != string(snapshot.DurableRunID) || lease.State != heteroci.LeaseStatePlanned || lease.DecisionDigest != snapshot.Decision.DecisionDigest ||
		lease.Executor != assignment.Executor || lease.NodeID != assignment.NodeID || lease.Endpoint != assignment.Endpoint ||
		lease.ProbeIdentity != assignment.ProbeIdentity || lease.ProbeBootID != assignment.ProbeBootID {
		return fmt.Errorf("prepared run lease %q is not planned against the decision", lease.UnitID)
	}
	return nil
}

func validatePreparedLeaseCoverage(units []heteroci.ExecutionUnit, seen map[string]struct{}) error {
	if len(seen) != len(units) {
		return errors.New("prepared run lease coverage is incomplete")
	}
	for _, unit := range units {
		if _, exists := seen[unit.UnitID]; !exists {
			return fmt.Errorf("prepared run lease for unit %q is missing", unit.UnitID)
		}
	}
	return nil
}

func validPreparedDigest(value string) bool {
	return len(value) == len("sha256:")+64 && strings.HasPrefix(value, "sha256:") && strings.Trim(value[len("sha256:"):], "0123456789abcdef") == ""
}

func filepathHasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func clonePreparedRunSnapshot(source PreparedRunSnapshot) PreparedRunSnapshot {
	clone := source
	clone.Units = append([]heteroci.ExecutionUnit(nil), source.Units...)
	clone.Manifest = clonePreparedManifest(source.Manifest)
	clone.SourceSnapshot = clonePreparedSnapshot(source.SourceSnapshot)
	clone.EffectiveSnapshot = clonePreparedSnapshot(source.EffectiveSnapshot)
	clone.Plan = clonePreparedPackingPlan(source.Plan)
	clone.Decision = clonePreparedDecision(source.Decision)
	clone.Leases = append([]heteroci.RoutingLease(nil), source.Leases...)
	clone.BindingPlan = cloneExecutorBindingPlan(source.BindingPlan)
	return clone
}

func clonePreparedManifest(source heteroci.Manifest) heteroci.Manifest {
	clone := source
	clone.Nodes = append([]heteroci.NodeConfig(nil), source.Nodes...)
	for index := range clone.Nodes {
		clone.Nodes[index].Capabilities = append([]heteroci.CapabilityConfig(nil), source.Nodes[index].Capabilities...)
	}
	return clone
}

// clonePreparedSnapshot 深拷贝节点能力和 qualification receipt，隔离 observer 变更。
func clonePreparedSnapshot(source heteroci.SnapshotSet) heteroci.SnapshotSet {
	clone := source
	clone.Nodes = append([]heteroci.NodeSnapshot(nil), source.Nodes...)
	for index := range clone.Nodes {
		clone.Nodes[index].ActiveCapabilities = append([]heteroci.ActiveCapabilitySnapshot(nil), source.Nodes[index].ActiveCapabilities...)
		for capabilityIndex, capability := range clone.Nodes[index].ActiveCapabilities {
			if source.Nodes[index].ActiveCapabilities[capabilityIndex].Qualification != nil {
				receipt := *source.Nodes[index].ActiveCapabilities[capabilityIndex].Qualification
				capability.Qualification = &receipt
				clone.Nodes[index].ActiveCapabilities[capabilityIndex] = capability
			}
		}
	}
	return clone
}

func clonePreparedPackingPlan(source heteroci.PackingPlan) heteroci.PackingPlan {
	clone := source
	clone.NodeShards = append([]heteroci.NodeShard(nil), source.NodeShards...)
	for index := range clone.NodeShards {
		clone.NodeShards[index].Waves = append([]heteroci.ExecutionWave(nil), source.NodeShards[index].Waves...)
		for waveIndex := range clone.NodeShards[index].Waves {
			clone.NodeShards[index].Waves[waveIndex].UnitIDs = append([]string(nil), source.NodeShards[index].Waves[waveIndex].UnitIDs...)
		}
	}
	clone.UnassignedUnitIDs = append([]string(nil), source.UnassignedUnitIDs...)
	return clone
}

func clonePreparedDecision(source heteroci.RoutingDecision) heteroci.RoutingDecision {
	clone := source
	clone.Assignments = append([]heteroci.RouteAssignment(nil), source.Assignments...)
	return clone
}
