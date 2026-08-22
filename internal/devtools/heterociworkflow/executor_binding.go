package heterociworkflow

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

func workflowDigest(domain string, parts ...string) string {
	hash := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	write(domain)
	for _, part := range parts {
		write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// ExecutorBindingPlanSchemaVersion 标识 request-level durable executor binding plan。
const ExecutorBindingPlanSchemaVersion = "remote-ci-executor-binding/v1"

// ExecutorBindingSchemaVersion 是 binding item 使用的 canonical 协议版本。
const ExecutorBindingSchemaVersion = ExecutorBindingPlanSchemaVersion

// RunRequestSchemaVersion 标识严格解码的 external run request。
const RunRequestSchemaVersion = "remote-ci-run-request/v1"

type runRequestDigestMaterial struct {
	SchemaVersion          string                   `json:"schema_version"`
	ExternalOperationID    heteroci.OperationID     `json:"external_operation_id"`
	DurableRunID           heteroci.TopologyRunID   `json:"durable_run_id"`
	ManifestDigest         string                   `json:"manifest_digest"`
	TopologyManifestDigest string                   `json:"topology_manifest_digest"`
	RepositoryID           string                   `json:"repository_id"`
	SourceTreeSHA          string                   `json:"source_tree_sha"`
	RunID                  string                   `json:"run_id"`
	Units                  []heteroci.ExecutionUnit `json:"units"`
	HeartbeatTTLMillis     int64                    `json:"heartbeat_ttl_millis"`
}

// DeriveManifestDigest 返回 topology manifest canonical digest；portable target digest 由请求单独携带。
func DeriveManifestDigest(manifest heteroci.Manifest) (string, error) {
	return heteroci.DeriveTopologyManifestDigest(manifest)
}

// DeriveRunRequestDigest 对单位输入按 UnitID 排序，避免 caller 重排改变 request identity。
func DeriveRunRequestDigest(request RunRequest) (string, error) {
	units := append([]heteroci.ExecutionUnit(nil), request.Units...)
	sort.Slice(units, func(left, right int) bool { return units[left].UnitID < units[right].UnitID })
	material := runRequestDigestMaterial{SchemaVersion: RunRequestSchemaVersion, ExternalOperationID: request.ExternalOperationID, DurableRunID: request.DurableRunID, ManifestDigest: request.ManifestDigest, TopologyManifestDigest: request.TopologyManifestDigest, RepositoryID: request.RepositoryID, SourceTreeSHA: request.SourceTreeSHA, RunID: request.RunID, Units: units, HeartbeatTTLMillis: request.HeartbeatTTL.Milliseconds()}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("encode run request digest material: %w", err)
	}
	return "sha256:" + workflowDigest("remote-ci-run-request/v1", string(encoded)), nil
}

// NormalizeExecutorBindingPlan 返回按 node/lane/batch canonical 排序且不共享 caller slice 的计划。
func NormalizeExecutorBindingPlan(plan ExecutorBindingPlan) (ExecutorBindingPlan, error) {
	clone := plan
	clone.Bindings = make([]ExecutorBinding, len(plan.Bindings))
	for index, binding := range plan.Bindings {
		normalized, err := normalizeExecutorBinding(binding)
		if err != nil {
			return ExecutorBindingPlan{}, fmt.Errorf("normalize executor binding %d: %w", index, err)
		}
		clone.Bindings[index] = normalized
	}
	sort.Slice(clone.Bindings, func(left, right int) bool {
		if clone.Bindings[left].NodeID != clone.Bindings[right].NodeID {
			return clone.Bindings[left].NodeID < clone.Bindings[right].NodeID
		}
		return string(clone.Bindings[left].ExecutorKind) < string(clone.Bindings[right].ExecutorKind)
	})
	return clone, nil
}
func normalizeExecutorBinding(binding ExecutorBinding) (ExecutorBinding, error) {
	if binding.NodeID == "" || binding.ExecutorKind == "" {
		return ExecutorBinding{}, errors.New("executor binding node or executor kind is missing")
	}
	key := ExecutorKey{Kind: binding.ExecutorKind, NodeID: binding.NodeID}
	if err := key.Validate(); err != nil {
		return ExecutorBinding{}, err
	}
	if len(binding.BatchIDs) == 0 {
		return ExecutorBinding{}, errors.New("executor binding batch set is empty")
	}
	binding.BatchIDs = append([]BatchID(nil), binding.BatchIDs...)
	sort.Slice(binding.BatchIDs, func(left, right int) bool { return string(binding.BatchIDs[left]) < string(binding.BatchIDs[right]) })
	for index, batchID := range binding.BatchIDs {
		if !validWorkflowUnitID(string(batchID)) || (index > 0 && binding.BatchIDs[index-1] == batchID) {
			return ExecutorBinding{}, errors.New("executor binding batch set is invalid or duplicated")
		}
	}
	return binding, nil
}

// DeriveExecutorBindingDigest 委托给 storage-neutral domain，避免 PG/workflow 漂移。
func DeriveExecutorBindingDigest(binding ExecutorBinding) (string, error) {
	normalized, err := normalizeExecutorBinding(binding)
	if err != nil {
		return "", err
	}
	batchIDs := make([]string, len(normalized.BatchIDs))
	for index, batchID := range normalized.BatchIDs {
		batchIDs[index] = string(batchID)
	}
	return heteroci.DeriveExecutorBindingItemDigest(heteroci.ExecutorBinding{NodeID: normalized.NodeID, NodeIDDigest: normalized.NodeIDDigest, ExecutorKind: normalized.ExecutorKind, BatchIDs: batchIDs, ShardDigest: normalized.ShardDigest, BatchSetDigest: normalized.BatchSetDigest, UnitSetDigest: normalized.UnitSetDigest, NodeFactsDigest: normalized.NodeFactsDigest})
}

// DeriveExecutorBindingAggregateDigest 委托给 storage-neutral domain，避免 PG/workflow 漂移。
func DeriveExecutorBindingAggregateDigest(plan ExecutorBindingPlan) (string, error) {
	normalized, err := NormalizeExecutorBindingPlan(plan)
	if err != nil {
		return "", err
	}
	bindings := make([]heteroci.ExecutorBinding, len(normalized.Bindings))
	for index, binding := range normalized.Bindings {
		batchIDs := make([]string, len(binding.BatchIDs))
		for batchIndex, batchID := range binding.BatchIDs {
			batchIDs[batchIndex] = string(batchID)
		}
		bindings[index] = heteroci.ExecutorBinding{NodeID: binding.NodeID, NodeIDDigest: binding.NodeIDDigest, ExecutorKind: binding.ExecutorKind, BatchIDs: batchIDs, ShardDigest: binding.ShardDigest, BatchSetDigest: binding.BatchSetDigest, UnitSetDigest: binding.UnitSetDigest, NodeFactsDigest: binding.NodeFactsDigest}
	}
	return heteroci.DeriveExecutorBindingAggregateDigest(heteroci.ExecutorBindingPlan{ExternalOperationID: normalized.ExternalOperationID, DurableRunID: normalized.DurableRunID, RequestDigest: normalized.RequestDigest, ManifestDigest: normalized.ManifestDigest, TopologyManifestDigest: normalized.TopologyManifestDigest, PlanDigest: normalized.PlanDigest, Bindings: bindings})
}

// DeriveExecutorBindingNodeIDDigest 委托给 storage-neutral domain，避免 PG/workflow 漂移。
func DeriveExecutorBindingNodeIDDigest(nodeID string, kind heteroci.ExecutorKind) (string, error) {
	return heteroci.DeriveExecutorBindingNodeIDDigest(nodeID, kind)
}

// ValidateDurableExecutorBindingPlan 校验 PG readback 或 binder 输入的完整 durable identity。
func ValidateDurableExecutorBindingPlan(plan ExecutorBindingPlan) error {
	if err := validateDurableBindingIdentity(plan); err != nil {
		return err
	}
	normalized, err := NormalizeExecutorBindingPlan(plan)
	if err != nil {
		return err
	}
	if len(normalized.Bindings) == 0 || !executorBindingPlansEqual(plan.Bindings, normalized.Bindings) {
		return errors.New("executor binding plan is not canonical or empty")
	}
	if err := validateDurableBindingItems(normalized.Bindings); err != nil {
		return err
	}
	want, err := DeriveExecutorBindingAggregateDigest(plan)
	if err != nil || want != plan.BindingDigest {
		return errors.New("executor binding aggregate digest does not match canonical fields")
	}
	return nil
}
func validateDurableBindingIdentity(plan ExecutorBindingPlan) error {
	if plan.SchemaVersion != ExecutorBindingPlanSchemaVersion {
		return errors.New("executor binding plan schema is invalid")
	}
	if err := plan.ExternalOperationID.Validate(); err != nil {
		return fmt.Errorf("executor binding external operation is invalid: %w", err)
	}
	if err := plan.DurableRunID.Validate(); err != nil {
		return fmt.Errorf("executor binding durable run is invalid: %w", err)
	}
	if err := plan.AuthorityGeneration.Validate(); err != nil {
		return fmt.Errorf("executor binding authority generation is invalid: %w", err)
	}
	return validateDurableBindingDigests(plan.RequestDigest, plan.ManifestDigest, plan.PlanDigest, plan.BindingDigest)
}
func validateDurableBindingDigests(digests ...string) error {
	for _, digest := range digests {
		if !validWorkflowDigest(digest) {
			return errors.New("executor binding plan digest is invalid")
		}
	}
	return nil
}
func validateDurableBindingItems(bindings []ExecutorBinding) error {
	for _, binding := range bindings {
		if err := validateDurableBindingItem(binding); err != nil {
			return err
		}
	}
	return nil
}
func validateDurableBindingItem(binding ExecutorBinding) error {
	if err := validateDurableBindingDigests(binding.NodeIDDigest, binding.ShardDigest, binding.BatchSetDigest, binding.UnitSetDigest, binding.NodeFactsDigest); err != nil {
		return errors.New("executor binding item digest is invalid")
	}
	wantNode, err := DeriveExecutorBindingNodeIDDigest(binding.NodeID, binding.ExecutorKind)
	if err != nil || wantNode != binding.NodeIDDigest {
		return errors.New("executor binding node id digest does not match canonical fields")
	}
	return nil
}
func executorBindingPlansEqual(left, right []ExecutorBinding) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !sameExecutorBindingIdentity(left[index], right[index]) || !equalBatchIDs(left[index].BatchIDs, right[index].BatchIDs) {
			return false
		}
	}
	return true
}
func sameExecutorBindingIdentity(left, right ExecutorBinding) bool {
	return left.NodeID == right.NodeID && left.ExecutorKind == right.ExecutorKind && left.NodeIDDigest == right.NodeIDDigest && left.ShardDigest == right.ShardDigest && left.BatchSetDigest == right.BatchSetDigest && left.UnitSetDigest == right.UnitSetDigest && left.NodeFactsDigest == right.NodeFactsDigest
}
func equalBatchIDs(left, right []BatchID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
func buildExecutorBindingPlan(request RunRequest, prepared preparedRun) (ExecutorBindingPlan, error) {
	planDigest, err := deriveDurablePlanDigest(prepared.planned)
	if err != nil {
		return ExecutorBindingPlan{}, err
	}
	byKey := make(map[ExecutorKey][]BatchPlan, len(prepared.batches))
	for _, batch := range prepared.batches {
		byKey[batch.Batch.Key] = append(byKey[batch.Batch.Key], batch)
	}
	keys := make([]ExecutorKey, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool { return executorKeyLess(keys[left], keys[right]) })
	bindings := make([]ExecutorBinding, 0, len(keys))
	for _, key := range keys {
		binding, err := buildExecutorBinding(key, byKey[key], prepared.planned.EffectiveSnapshot, prepared.planned.Plan.NodeShards)
		if err != nil {
			return ExecutorBindingPlan{}, err
		}
		bindings = append(bindings, binding)
	}
	plan := ExecutorBindingPlan{SchemaVersion: ExecutorBindingPlanSchemaVersion, ExternalOperationID: request.ExternalOperationID, DurableRunID: prepared.runIdentity.DurableRunID, RequestDigest: request.RequestDigest, ManifestDigest: request.ManifestDigest, TopologyManifestDigest: request.TopologyManifestDigest, PlanDigest: planDigest, AuthorityGeneration: prepared.planned.Fence.OwnerEpoch, Bindings: bindings}
	plan.BindingDigest, err = DeriveExecutorBindingAggregateDigest(plan)
	if err != nil {
		return ExecutorBindingPlan{}, err
	}
	if err := ValidateDurableExecutorBindingPlan(plan); err != nil {
		return ExecutorBindingPlan{}, err
	}
	return plan, nil
}
func buildExecutorBinding(key ExecutorKey, batches []BatchPlan, snapshot heteroci.SnapshotSet, shards []heteroci.NodeShard) (ExecutorBinding, error) {
	if err := key.Validate(); err != nil {
		return ExecutorBinding{}, err
	}
	batchIDs := make([]BatchID, 0, len(batches))
	unitIDs := make([]string, 0)
	for _, batch := range batches {
		batchIDs = append(batchIDs, batch.Batch.BatchID)
		for _, unit := range batch.Batch.Units {
			unitIDs = append(unitIDs, unit.UnitID)
		}
	}
	sort.Slice(batchIDs, func(left, right int) bool { return string(batchIDs[left]) < string(batchIDs[right]) })
	sort.Strings(unitIDs)
	shardDigest, err := heteroci.DeriveExecutorBindingValueDigest("heteroci-workflow-executor-shard/v1", heteroci.ExecutorBindingMatchingShards(key.NodeID, shards))
	if err != nil {
		return ExecutorBinding{}, err
	}
	batchSetDigest, err := heteroci.DeriveExecutorBindingValueDigest("heteroci-workflow-executor-batch-set/v1", batchIDs)
	if err != nil {
		return ExecutorBinding{}, err
	}
	unitSetDigest, err := heteroci.DeriveExecutorBindingValueDigest("heteroci-workflow-executor-unit-set/v1", unitIDs)
	if err != nil {
		return ExecutorBinding{}, err
	}
	node, found := findSnapshotNode(snapshot, key.NodeID)
	if !found {
		return ExecutorBinding{}, fmt.Errorf("executor binding node facts are missing")
	}
	nodeFactsDigest, err := heteroci.DeriveExecutorBindingValueDigest("heteroci-workflow-executor-node-facts/v1", node)
	if err != nil {
		return ExecutorBinding{}, err
	}
	nodeIDDigest, err := DeriveExecutorBindingNodeIDDigest(key.NodeID, key.Kind)
	if err != nil {
		return ExecutorBinding{}, err
	}
	return ExecutorBinding{NodeID: key.NodeID, NodeIDDigest: nodeIDDigest, ExecutorKind: key.Kind, BatchIDs: batchIDs, ShardDigest: shardDigest, BatchSetDigest: batchSetDigest, UnitSetDigest: unitSetDigest, NodeFactsDigest: nodeFactsDigest}, nil
}
func findSnapshotNode(snapshot heteroci.SnapshotSet, nodeID string) (heteroci.NodeSnapshot, bool) {
	for _, node := range snapshot.Nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return heteroci.NodeSnapshot{}, false
}
func deriveDurablePlanDigest(planned heterociowner.DurableReservedRoutingPlan) (string, error) {
	return heteroci.DeriveTopologyReservedPlanDigest(heteroci.TopologyReservedPlan{Fence: planned.Fence, OperationID: planned.OperationID, SourceSnapshot: planned.SourceSnapshot, EffectiveSnapshot: planned.EffectiveSnapshot, Plan: planned.Plan, Decision: planned.Decision, Leases: planned.Leases})
}
func toDomainExecutorBindingPlan(plan ExecutorBindingPlan) heteroci.ExecutorBindingPlan {
	bindings := make([]heteroci.ExecutorBinding, len(plan.Bindings))
	for index, binding := range plan.Bindings {
		batchIDs := make([]string, len(binding.BatchIDs))
		for batchIndex, batchID := range binding.BatchIDs {
			batchIDs[batchIndex] = string(batchID)
		}
		bindings[index] = heteroci.ExecutorBinding{NodeID: binding.NodeID, NodeIDDigest: binding.NodeIDDigest, ExecutorKind: binding.ExecutorKind, BatchIDs: batchIDs, ShardDigest: binding.ShardDigest, BatchSetDigest: binding.BatchSetDigest, UnitSetDigest: binding.UnitSetDigest, NodeFactsDigest: binding.NodeFactsDigest}
	}
	return heteroci.ExecutorBindingPlan{SchemaVersion: plan.SchemaVersion, ExternalOperationID: plan.ExternalOperationID, DurableRunID: plan.DurableRunID, RequestDigest: plan.RequestDigest, ManifestDigest: plan.ManifestDigest, TopologyManifestDigest: plan.TopologyManifestDigest, PlanDigest: plan.PlanDigest, AuthorityGeneration: plan.AuthorityGeneration, Bindings: bindings, BindingDigest: plan.BindingDigest}
}
func fromDomainExecutorBindingPlan(plan heteroci.ExecutorBindingPlan) ExecutorBindingPlan {
	bindings := make([]ExecutorBinding, len(plan.Bindings))
	for index, binding := range plan.Bindings {
		batchIDs := make([]BatchID, len(binding.BatchIDs))
		for batchIndex, batchID := range binding.BatchIDs {
			batchIDs[batchIndex] = BatchID(batchID)
		}
		bindings[index] = ExecutorBinding{NodeID: binding.NodeID, NodeIDDigest: binding.NodeIDDigest, ExecutorKind: binding.ExecutorKind, BatchIDs: batchIDs, ShardDigest: binding.ShardDigest, BatchSetDigest: binding.BatchSetDigest, UnitSetDigest: binding.UnitSetDigest, NodeFactsDigest: binding.NodeFactsDigest}
	}
	return ExecutorBindingPlan{SchemaVersion: plan.SchemaVersion, ExternalOperationID: plan.ExternalOperationID, DurableRunID: plan.DurableRunID, RequestDigest: plan.RequestDigest, ManifestDigest: plan.ManifestDigest, TopologyManifestDigest: plan.TopologyManifestDigest, PlanDigest: plan.PlanDigest, AuthorityGeneration: plan.AuthorityGeneration, Bindings: bindings, BindingDigest: plan.BindingDigest}
}
func validatePersistedExecutorBinding(previous heteroci.OwnerFence, expected ExecutorBindingPlan, persisted heteroci.ExecutorBindingMutationResult) error {
	if err := persisted.Fence.Validate(); err != nil || !validNextFence(previous, persisted.Fence) {
		return fmt.Errorf("%w: persisted binding fence drifted", ErrExecutorBinding)
	}
	actual := fromDomainExecutorBindingPlan(persisted.Plan)
	if err := ValidateDurableExecutorBindingPlan(actual); err != nil {
		return fmt.Errorf("%w: persisted binding plan is invalid: %w", ErrExecutorBinding, err)
	}
	if !reflect.DeepEqual(expected, actual) {
		return fmt.Errorf("%w: persisted binding plan differs from request", ErrExecutorBinding)
	}
	return nil
}
func validateRunRequest(ctx context.Context, request RunRequest) error {
	if err := validateRunRequestContext(ctx); err != nil {
		return err
	}
	if err := validateRunRequestIdentity(request); err != nil {
		return err
	}
	if err := validateRunRequestPayload(request); err != nil {
		return err
	}
	wantRequestDigest, err := DeriveRunRequestDigest(request)
	if err != nil || wantRequestDigest != request.RequestDigest {
		return fmt.Errorf("%w: request digest does not match canonical input", ErrInvalidRequest)
	}
	return nil
}
func validateRunRequestContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: context is already done: %w", ErrInvalidRequest, err)
	}
	return nil
}
func validateRunRequestIdentity(request RunRequest) error {
	if request.ManifestPath == "" || strings.TrimSpace(request.ManifestPath) != request.ManifestPath {
		return fmt.Errorf("%w: manifest path is invalid", ErrInvalidRequest)
	}
	if request.SchemaVersion != RunRequestSchemaVersion {
		return fmt.Errorf("%w: request schema is invalid", ErrInvalidRequest)
	}
	if err := request.ExternalOperationID.Validate(); err != nil {
		return fmt.Errorf("%w: external operation identity is invalid: %w", ErrInvalidRequest, err)
	}
	if err := request.DurableRunID.Validate(); err != nil {
		return fmt.Errorf("%w: durable run identity is invalid: %w", ErrInvalidRequest, err)
	}
	return validateRunRequestDigests(request)
}
func validateRunRequestDigests(request RunRequest) error {
	if !validWorkflowDigest(request.RequestDigest) || !validWorkflowDigest(request.ManifestDigest) || !validWorkflowDigest(request.TopologyManifestDigest) {
		return fmt.Errorf("%w: request or manifest digest is invalid", ErrInvalidRequest)
	}
	return nil
}
func validateRunRequestPayload(request RunRequest) error {
	if err := heteroci.ValidateTopologyAuditIdentity(request.RepositoryID, request.SourceTreeSHA, request.RunID); err != nil {
		return fmt.Errorf("%w: run audit identity is invalid: %w", ErrInvalidRequest, err)
	}
	if err := validateWorkflowRunID(request.RunID); err != nil {
		return fmt.Errorf("%w: run identity is invalid", ErrInvalidRequest)
	}
	if request.HeartbeatTTL <= 0 || request.HeartbeatTTL.Milliseconds() <= 0 {
		return fmt.Errorf("%w: heartbeat TTL is invalid", ErrInvalidRequest)
	}
	if err := heteroci.ValidateExecutionUnits(request.Units); err != nil {
		return fmt.Errorf("%w: execution units are invalid: %w", ErrInvalidRequest, err)
	}
	return nil
}
func (workflow *Workflow) deriveRunIdentity(request RunRequest) (heteroci.TopologyRunIdentity, error) {
	runIdentity, err := heteroci.DeriveTopologyRunIdentity(request.RepositoryID, request.SourceTreeSHA, request.RunID)
	if err != nil {
		return heteroci.TopologyRunIdentity{}, fmt.Errorf("derive heteroci durable run identity: %w", err)
	}
	if request.DurableRunID != runIdentity.DurableRunID {
		return heteroci.TopologyRunIdentity{}, fmt.Errorf("%w: durable run identity does not match repository/run derivation", ErrInvalidRequest)
	}
	return runIdentity, nil
}
func (workflow *Workflow) loadAndValidateRunManifest(request RunRequest) (heteroci.Manifest, error) {
	manifest, err := workflow.loadRunManifest(request.ManifestPath)
	if err != nil {
		return heteroci.Manifest{}, err
	}
	manifestDigest, err := DeriveManifestDigest(manifest)
	if err != nil || manifestDigest != request.TopologyManifestDigest {
		return heteroci.Manifest{}, fmt.Errorf("%w: manifest digest does not match canonical manifest", ErrInvalidRequest)
	}
	return manifest, nil
}
func (workflow *Workflow) acquireRunFence(ctx context.Context) (heteroci.OwnerFence, error) {
	fence, err := workflow.deps.Owner.Acquire(ctx)
	if err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("acquire heteroci owner: %w", err)
	}
	if err := fence.Validate(); err != nil {
		return heteroci.OwnerFence{}, fmt.Errorf("validate acquired heteroci owner fence: %w", err)
	}
	return fence, nil
}

// prepareExecution 校验 durable read-back、构建批次并在首个 Start 前绑定全部 executor。
func (workflow *Workflow) prepareExecution(ctx context.Context, request RunRequest, prepared *preparedRun) error {
	if err := validatePlannedProjection(prepared.acquired, prepared.ids.plan, request.Units, prepared.planned); err != nil {
		return err
	}
	state, err := workflow.deps.Owner.ReadRun(ctx, prepared.runIdentity.DurableRunID)
	if err != nil {
		return fmt.Errorf("%w: read durable run before execution: %w", ErrExecutionState, err)
	}
	if err := validatePreExecutionRunState(prepared.acquired, string(prepared.runIdentity.DurableRunID), prepared.manifest, request.Units, request.HeartbeatTTL, prepared.planned, state); err != nil {
		return err
	}
	now, err := workflow.now()
	if err != nil {
		return err
	}
	prepared.batches, err = BuildBatches(string(prepared.runIdentity.DurableRunID), prepared.manifest, prepared.planned, request.Units, now, request.HeartbeatTTL)
	if err != nil {
		return fmt.Errorf("%w: build execution batches: %w", ErrExecutionState, err)
	}
	prepared.bindingPlan, err = buildExecutorBindingPlan(request, *prepared)
	if err != nil {
		return fmt.Errorf("%w: build durable executor binding plan: %w", ErrExecutorBinding, err)
	}
	persisted, err := workflow.deps.Owner.BindExecutors(ctx, heteroci.ExecutorBindingMutation{Fence: prepared.planned.Fence, Plan: toDomainExecutorBindingPlan(prepared.bindingPlan)})
	if err != nil {
		return fmt.Errorf("%w: persist executor binding: %w", ErrExecutorBinding, err)
	}
	if err := validatePersistedExecutorBinding(prepared.planned.Fence, prepared.bindingPlan, persisted); err != nil {
		return err
	}
	prepared.bindingPlan = fromDomainExecutorBindingPlan(persisted.Plan)
	prepared.state.fence = persisted.Fence
	prepared.executors, err = workflow.bindExecutors(ctx, prepared.bindingPlan)
	return err
}
