package heterociworkflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// CapacityBlockedSnapshotSchemaVersion 标识 PlanAndReserve 容量阻塞的准备态投影。
const CapacityBlockedSnapshotSchemaVersion = "remote-ci-capacity-blocked/v1"

// CapacityBlockedObserver 是 storage-neutral 的容量阻塞观察端口。
type CapacityBlockedObserver interface {
	ObserveCapacityBlocked(context.Context, CapacityBlockedRunSnapshot) error
}

// observeCapacityBlocked 重建并校验 canonical blocker plan，再调用一次容量阻塞 observer。
func (workflow *Workflow) observeCapacityBlocked(ctx context.Context, request RunRequest, fence heteroci.OwnerFence, manifest heteroci.Manifest, source heteroci.SnapshotSet, now time.Time, blocker *heteroci.CapacityBlockerError) error {
	plan, planErr := heteroci.BuildAnchorWavePlan(manifest, source, request.Units, now, request.HeartbeatTTL)
	if planErr == nil {
		return errors.New("capacity blocker disappeared while rebuilding canonical plan")
	}
	rebuiltBlocker, ok := capacityBlockerError(planErr)
	if !ok || rebuiltBlocker.PackingPlanDigest != blocker.PackingPlanDigest {
		return fmt.Errorf("capacity blocker canonical plan drift: %w", planErr)
	}
	projection, err := blocker.Project(request.ManifestDigest)
	if err != nil {
		return fmt.Errorf("project capacity blocker: %w", err)
	}
	snapshot := CapacityBlockedRunSnapshot{
		SchemaVersion: CapacityBlockedSnapshotSchemaVersion, ExternalOperationID: request.ExternalOperationID,
		DurableRunID: request.DurableRunID, RequestDigest: request.RequestDigest, ManifestDigest: request.ManifestDigest,
		ExternalBindingDigest: request.ExternalBindingDigest, ManifestBlobSHA256: request.ManifestBlobSHA256,
		RuntimePolicyDigest: request.RuntimePolicyDigest, TopologyManifestDigest: request.TopologyManifestDigest,
		ManifestPath: request.ManifestPath, RepositoryID: request.RepositoryID, SourceTreeSHA: request.SourceTreeSHA,
		RunID: request.RunID, HeartbeatTTL: request.HeartbeatTTL, Units: request.Units, Fence: fence,
		Manifest: manifest, SourceSnapshot: source, Plan: plan, PlanDigest: blocker.PackingPlanDigest, Projection: projection,
	}
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("validate capacity blocked snapshot: %w", err)
	}
	if workflow.deps.CapacityBlockedObserver != nil {
		observation := cloneCapacityBlockedSnapshot(snapshot)
		if err := observation.Validate(); err != nil {
			return fmt.Errorf("validate capacity blocked observation: %w", err)
		}
		if err := workflow.deps.CapacityBlockedObserver.ObserveCapacityBlocked(ctx, observation); err != nil {
			return fmt.Errorf("observe capacity blocked lifecycle: %w", err)
		}
	}
	return &CapacityBlockedRunError{Snapshot: cloneCapacityBlockedSnapshot(snapshot), Cause: blocker}
}

// CapacityBlockedRunSnapshot 保存容量阻塞时唯一可发布的准备态事实。
// 它不包含可执行 shards；Plan 仅用于 canonical digest 和 blocker 重放。
type CapacityBlockedRunSnapshot struct {
	SchemaVersion          string                             `json:"schema_version"`
	ExternalOperationID    heteroci.OperationID               `json:"external_operation_id"`
	DurableRunID           heteroci.TopologyRunID             `json:"durable_run_id"`
	RequestDigest          string                             `json:"request_digest"`
	ManifestDigest         string                             `json:"manifest_digest"`
	ExternalBindingDigest  string                             `json:"external_binding_digest"`
	ManifestBlobSHA256     string                             `json:"manifest_blob_sha256"`
	RuntimePolicyDigest    string                             `json:"runtime_policy_digest"`
	TopologyManifestDigest string                             `json:"topology_manifest_digest"`
	ManifestPath           string                             `json:"manifest_path"`
	RepositoryID           string                             `json:"repository_id"`
	SourceTreeSHA          string                             `json:"source_tree_sha"`
	RunID                  string                             `json:"run_id"`
	HeartbeatTTL           time.Duration                      `json:"heartbeat_ttl"`
	Units                  []heteroci.ExecutionUnit           `json:"units"`
	Fence                  heteroci.OwnerFence                `json:"fence"`
	Manifest               heteroci.Manifest                  `json:"manifest"`
	SourceSnapshot         heteroci.SnapshotSet               `json:"source_snapshot"`
	Plan                   heteroci.PackingPlan               `json:"plan"`
	PlanDigest             string                             `json:"plan_digest"`
	Projection             heteroci.CapacityBlockerProjection `json:"projection"`
}

// Validate 校验容量阻塞快照的 request audit、manifest、source snapshot 和 canonical blocker projection。
func (snapshot CapacityBlockedRunSnapshot) Validate() error {
	if snapshot.SchemaVersion != CapacityBlockedSnapshotSchemaVersion {
		return errors.New("capacity blocked snapshot schema is invalid")
	}
	return snapshot.validateCapacityBlockedPayload()
}

// validatePlan 校验容量阻塞 plan 的 non-empty blocker projection 和 canonical digest。
func (snapshot CapacityBlockedRunSnapshot) validatePlan(now time.Time) error {
	plan := snapshot.Plan
	if plan.NodeShards == nil {
		plan.NodeShards = []heteroci.NodeShard{}
	}
	if plan.UnassignedUnitIDs == nil {
		plan.UnassignedUnitIDs = []string{}
	}
	if err := plan.ValidateAgainst(snapshot.Manifest, snapshot.SourceSnapshot, snapshot.Units, now, snapshot.HeartbeatTTL); err != nil {
		return fmt.Errorf("capacity blocked plan: %w", err)
	}
	digest, err := plan.CanonicalDigest()
	if err != nil || digest != snapshot.PlanDigest {
		return errors.New("capacity blocked plan digest does not match plan")
	}
	if len(snapshot.Plan.UnassignedUnitIDs) == 0 {
		return errors.New("capacity blocked plan has no unassigned units")
	}
	return nil
}

// CapacityBlockedRunError 表示 observer 成功后可由 composition 发布的 typed blocked receipt 输入。
type CapacityBlockedRunError struct {
	Snapshot CapacityBlockedRunSnapshot
	Cause    *heteroci.CapacityBlockerError
}

// Error 返回稳定的容量阻塞错误描述。
func (err *CapacityBlockedRunError) Error() string {
	if err == nil || err.Cause == nil {
		return "heteroci workflow capacity blocked"
	}
	return err.Cause.Error()
}

// Unwrap 保留底层 typed CapacityBlockerError 的分类能力。
func (err *CapacityBlockedRunError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

func capacityBlockerError(err error) (*heteroci.CapacityBlockerError, bool) {
	if pointer, ok := errors.AsType[*heteroci.CapacityBlockerError](err); ok && pointer != nil {
		return pointer, true
	}
	if value, ok := errors.AsType[heteroci.CapacityBlockerError](err); ok {
		copy := value
		return &copy, true
	}
	return nil, false
}

func cloneCapacityBlockedSnapshot(source CapacityBlockedRunSnapshot) CapacityBlockedRunSnapshot {
	clone := source
	clone.Units = append([]heteroci.ExecutionUnit(nil), source.Units...)
	clone.Manifest = clonePreparedManifest(source.Manifest)
	clone.SourceSnapshot = clonePreparedSnapshot(source.SourceSnapshot)
	clone.Plan = clonePreparedPackingPlan(source.Plan)
	clone.Projection.Blockers = append([]heteroci.CapacityBlocker(nil), source.Projection.Blockers...)
	return clone
}

// validateCapacityBlockedPayload 校验容量阻塞投影的 request、节点快照、plan 和 blocker identity。
func (snapshot CapacityBlockedRunSnapshot) validateCapacityBlockedPayload() error {
	request := RunRequest{SchemaVersion: RunRequestSchemaVersion, ExternalOperationID: snapshot.ExternalOperationID, DurableRunID: snapshot.DurableRunID, RequestDigest: snapshot.RequestDigest, ManifestDigest: snapshot.ManifestDigest, ExternalBindingDigest: snapshot.ExternalBindingDigest, ManifestBlobSHA256: snapshot.ManifestBlobSHA256, RuntimePolicyDigest: snapshot.RuntimePolicyDigest, TopologyManifestDigest: snapshot.TopologyManifestDigest, ManifestPath: snapshot.ManifestPath, RepositoryID: snapshot.RepositoryID, SourceTreeSHA: snapshot.SourceTreeSHA, RunID: snapshot.RunID, Units: snapshot.Units, HeartbeatTTL: snapshot.HeartbeatTTL}
	if err := validateRunRequestIdentity(request); err != nil {
		return fmt.Errorf("capacity blocked request identity: %w", err)
	}
	if err := validateRunRequestPayload(request); err != nil {
		return fmt.Errorf("capacity blocked request payload: %w", err)
	}
	if err := snapshot.Manifest.Validate(); err != nil {
		return fmt.Errorf("capacity blocked manifest: %w", err)
	}
	if err := heteroci.ValidateExecutionUnits(snapshot.Units); err != nil {
		return fmt.Errorf("capacity blocked units: %w", err)
	}
	if err := snapshot.validateCapacityBlockedSource(); err != nil {
		return err
	}
	return snapshot.validateCapacityBlockedProjection()
}

func (snapshot CapacityBlockedRunSnapshot) validateCapacityBlockedSource() error {
	if snapshot.Fence.OwnerEpoch <= 0 || snapshot.Fence.Revision <= 0 || snapshot.SourceSnapshot.OwnerEpoch != snapshot.Fence.OwnerEpoch {
		return errors.New("capacity blocked fence or source snapshot identity is invalid")
	}
	now := time.UnixMilli(snapshot.SourceSnapshot.CapturedAtUnixMilli).UTC()
	if err := snapshot.SourceSnapshot.ValidateAgainst(snapshot.Manifest, now, snapshot.HeartbeatTTL); err != nil {
		return fmt.Errorf("capacity blocked source snapshot: %w", err)
	}
	return snapshot.validatePlan(now)
}

func (snapshot CapacityBlockedRunSnapshot) validateCapacityBlockedProjection() error {
	if snapshot.Projection.ManifestDigest != snapshot.ManifestDigest || snapshot.Projection.PackingPlanDigest != snapshot.PlanDigest {
		return errors.New("capacity blocked projection identity drifted")
	}
	return snapshot.Projection.Validate()
}
