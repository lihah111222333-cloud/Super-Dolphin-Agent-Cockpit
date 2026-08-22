package heteroci

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

type executorBindingDigestMaterial struct {
	NodeID          string       `json:"node_id"`
	NodeIDDigest    string       `json:"node_id_digest"`
	ExecutorKind    ExecutorKind `json:"executor_kind"`
	BatchIDs        []string     `json:"batch_ids"`
	ShardDigest     string       `json:"shard_digest"`
	BatchSetDigest  string       `json:"batch_set_digest"`
	UnitSetDigest   string       `json:"unit_set_digest"`
	NodeFactsDigest string       `json:"node_facts_digest"`
}

// DeriveExecutorBindingBatchID 是 workflow 与 PG owner 共用的唯一 batch identity 算法。
func DeriveExecutorBindingBatchID(runID, nodeID string, wave int, unitIDs []string) (string, error) {
	if runID == "" || nodeID == "" || len(unitIDs) == 0 {
		return "", fmt.Errorf("executor binding batch identity is invalid")
	}
	parts := []string{runID, string(ExecutorKindLocalNode), nodeID, strconv.Itoa(wave)}
	parts = append(parts, unitIDs...)
	return topologyDigest("heteroci-workflow-batch/v2", parts...), nil
}

// DeriveExecutorBindingNodeIDDigest 是 workflow 与 PG owner 共用的 node identity digest 算法。
func DeriveExecutorBindingNodeIDDigest(nodeID string, kind ExecutorKind) (string, error) {
	if nodeID == "" || kind != ExecutorKindLocalNode {
		return "", fmt.Errorf("executor binding node identity is invalid")
	}
	return "sha256:" + topologyDigest("heteroci-workflow-executor-node-id/v1", nodeID, string(kind)), nil
}

// DeriveExecutorBindingItemDigest 计算单个 node binding item 的 canonical digest。
func DeriveExecutorBindingItemDigest(binding ExecutorBinding) (string, error) {
	if binding.NodeID == "" || binding.ExecutorKind != ExecutorKindLocalNode || len(binding.BatchIDs) == 0 {
		return "", fmt.Errorf("executor binding item is invalid")
	}
	material := executorBindingDigestMaterial{NodeID: binding.NodeID, NodeIDDigest: binding.NodeIDDigest, ExecutorKind: binding.ExecutorKind, BatchIDs: append([]string(nil), binding.BatchIDs...), ShardDigest: binding.ShardDigest, BatchSetDigest: binding.BatchSetDigest, UnitSetDigest: binding.UnitSetDigest, NodeFactsDigest: binding.NodeFactsDigest}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", err
	}
	return "sha256:" + topologyDigest("heteroci-workflow-executor-binding/v1", string(encoded)), nil
}

// DeriveExecutorBindingAggregateDigest 计算整次 request binding 的 canonical aggregate digest。
func DeriveExecutorBindingAggregateDigest(plan ExecutorBindingPlan) (string, error) {
	items := make([]string, len(plan.Bindings))
	for index, binding := range plan.Bindings {
		item, err := DeriveExecutorBindingItemDigest(binding)
		if err != nil {
			return "", err
		}
		items[index] = item
	}
	parts := []string{string(plan.ExternalOperationID), string(plan.DurableRunID), plan.RequestDigest, plan.ManifestDigest, plan.TopologyManifestDigest, plan.PlanDigest}
	parts = append(parts, items...)
	return "sha256:" + topologyDigest("heteroci-workflow-executor-binding-aggregate/v1", parts...), nil
}

// DeriveExecutorBindingPlanDigest 计算 binding plan fields 的 canonical digest，不包含 BindingDigest 自身。
func DeriveExecutorBindingPlanDigest(plan ExecutorBindingPlan) (string, error) {
	bindings := make([]executorBindingDigestMaterial, len(plan.Bindings))
	for index, binding := range plan.Bindings {
		bindings[index] = executorBindingDigestMaterial{NodeID: binding.NodeID, NodeIDDigest: binding.NodeIDDigest, ExecutorKind: binding.ExecutorKind, BatchIDs: append([]string(nil), binding.BatchIDs...), ShardDigest: binding.ShardDigest, BatchSetDigest: binding.BatchSetDigest, UnitSetDigest: binding.UnitSetDigest, NodeFactsDigest: binding.NodeFactsDigest}
	}
	material := struct {
		SchemaVersion          string                          `json:"schema_version"`
		ExternalOperationID    OperationID                     `json:"external_operation_id"`
		DurableRunID           TopologyRunID                   `json:"durable_run_id"`
		RequestDigest          string                          `json:"request_digest"`
		ManifestDigest         string                          `json:"manifest_digest"`
		TopologyManifestDigest string                          `json:"topology_manifest_digest"`
		PlanDigest             string                          `json:"plan_digest"`
		AuthorityGeneration    RoutingOwnerEpoch               `json:"authority_generation"`
		Bindings               []executorBindingDigestMaterial `json:"bindings"`
	}{plan.SchemaVersion, plan.ExternalOperationID, plan.DurableRunID, plan.RequestDigest, plan.ManifestDigest, plan.TopologyManifestDigest, plan.PlanDigest, plan.AuthorityGeneration, bindings}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", err
	}
	return "sha256:" + topologyDigest("heteroci-workflow-executor-binding-plan/v1", string(encoded)), nil
}

// ValidateTopologyPlanBindingAudit 校验 plan journal 必须持久化的 request-level audit fields。
func ValidateTopologyPlanBindingAudit(request TopologyPlanRequest) error {
	for _, digest := range []string{request.RequestDigest, request.ManifestDigest, request.TopologyManifestDigest} {
		if !validTopologyDigest(digest) {
			return fmt.Errorf("topology plan binding audit digest is invalid")
		}
	}
	want, err := DeriveTopologyManifestDigest(request.Manifest)
	if err != nil || want != request.TopologyManifestDigest {
		return fmt.Errorf("topology plan manifest digest does not match manifest")
	}
	return nil
}
func validTopologyDigest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil
}

// DeriveExecutorBindingValueDigest 计算绑定集合、分片或 node facts 的 domain-separated digest。
func DeriveExecutorBindingValueDigest(domain string, value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return "sha256:" + topologyDigest(domain, string(encoded)), nil
}

// ValidateExecutorBindingPlanAgainstReservedPlan 检查 binding 与已持久 plan、node/shard/unit facts 的 exact 一致性。
func ValidateExecutorBindingPlanAgainstReservedPlan(plan ExecutorBindingPlan, reserved TopologyReservedPlan) error {
	if len(plan.Bindings) != len(reserved.Plan.NodeShards) {
		return fmt.Errorf("executor binding node set does not match persisted plan")
	}
	expected, unitSets, err := executorBindingExpectedSets(plan, reserved.Plan.NodeShards)
	if err != nil {
		return err
	}
	assignments := executorBindingAssignments(reserved.Decision.Assignments)
	if err := validateExecutorBindingItems(plan.Bindings, expected, unitSets, assignments, reserved.EffectiveSnapshot, reserved.Plan.NodeShards); err != nil {
		return err
	}
	wantBindingDigest, err := DeriveExecutorBindingAggregateDigest(plan)
	if err != nil || wantBindingDigest != plan.BindingDigest {
		return fmt.Errorf("executor binding aggregate digest drifted")
	}
	return nil
}
func executorBindingExpectedSets(plan ExecutorBindingPlan, shards []NodeShard) (map[string][]string, map[string][]string, error) {
	expected := make(map[string][]string, len(shards))
	unitSets := make(map[string][]string, len(shards))
	for _, shard := range shards {
		if _, duplicate := expected[shard.NodeID]; duplicate {
			return nil, nil, fmt.Errorf("persisted topology plan contains duplicate node shard")
		}
		for _, wave := range shard.Waves {
			batchID, err := DeriveExecutorBindingBatchID(string(plan.DurableRunID), shard.NodeID, wave.Wave, wave.UnitIDs)
			if err != nil {
				return nil, nil, err
			}
			expected[shard.NodeID] = append(expected[shard.NodeID], batchID)
			unitSets[shard.NodeID] = append(unitSets[shard.NodeID], wave.UnitIDs...)
		}
		sort.Strings(expected[shard.NodeID])
		sort.Strings(unitSets[shard.NodeID])
	}
	return expected, unitSets, nil
}
func executorBindingAssignments(assignments []RouteAssignment) map[string]RouteAssignment {
	result := make(map[string]RouteAssignment, len(assignments))
	for _, assignment := range assignments {
		result[assignment.UnitID] = assignment
	}
	return result
}
func validateExecutorBindingItems(bindings []ExecutorBinding, expected, unitSets map[string][]string, assignments map[string]RouteAssignment, snapshot SnapshotSet, shards []NodeShard) error {
	previousNode := ""
	for _, binding := range bindings {
		if binding.NodeID <= previousNode {
			return fmt.Errorf("executor binding node order is invalid")
		}
		previousNode = binding.NodeID
		if err := validateExecutorBindingItem(binding, expected[binding.NodeID], unitSets[binding.NodeID], assignments, snapshot, shards); err != nil {
			return err
		}
	}
	return nil
}
func validateExecutorBindingItem(binding ExecutorBinding, wantBatches, unitIDs []string, assignments map[string]RouteAssignment, snapshot SnapshotSet, shards []NodeShard) error {
	if err := validateExecutorBindingSets(wantBatches, binding.BatchIDs); err != nil {
		return err
	}
	if err := validateExecutorBindingAssignments(binding, unitIDs, assignments); err != nil {
		return err
	}
	return validateExecutorBindingDigests(binding, unitIDs, snapshot, shards)
}
func validateExecutorBindingSets(wantBatches, batchIDs []string) error {
	if len(wantBatches) == 0 || !equalExecutorBindingStrings(wantBatches, batchIDs) {
		return fmt.Errorf("executor binding batch set does not match persisted plan")
	}
	return nil
}
func validateExecutorBindingAssignments(binding ExecutorBinding, unitIDs []string, assignments map[string]RouteAssignment) error {
	for _, unitID := range unitIDs {
		assignment, found := assignments[unitID]
		if !found || assignment.NodeID != binding.NodeID || assignment.Executor != binding.ExecutorKind {
			return fmt.Errorf("executor binding unit assignment drifted")
		}
	}
	return nil
}
func validateExecutorBindingDigests(binding ExecutorBinding, unitIDs []string, snapshot SnapshotSet, shards []NodeShard) error {
	node, found := executorBindingNode(snapshot, binding.NodeID)
	if !found {
		return fmt.Errorf("executor binding node facts are missing")
	}
	nodeIDDigest, err := DeriveExecutorBindingNodeIDDigest(binding.NodeID, binding.ExecutorKind)
	if err != nil || binding.NodeIDDigest != nodeIDDigest {
		return fmt.Errorf("executor binding node id digest drifted")
	}
	digests, err := executorBindingExpectedDigests(binding, node, unitIDs, shards)
	if err != nil {
		return err
	}
	if binding.ShardDigest != digests[0] || binding.BatchSetDigest != digests[1] || binding.UnitSetDigest != digests[2] || binding.NodeFactsDigest != digests[3] {
		return fmt.Errorf("executor binding set or node facts digest drifted")
	}
	return nil
}
func executorBindingExpectedDigests(binding ExecutorBinding, node NodeSnapshot, unitIDs []string, shards []NodeShard) ([4]string, error) {
	shardDigest, err := DeriveExecutorBindingValueDigest("heteroci-workflow-executor-shard/v1", ExecutorBindingMatchingShards(binding.NodeID, shards))
	if err != nil {
		return [4]string{}, err
	}
	batchDigest, err := DeriveExecutorBindingValueDigest("heteroci-workflow-executor-batch-set/v1", binding.BatchIDs)
	if err != nil {
		return [4]string{}, err
	}
	unitDigest, err := DeriveExecutorBindingValueDigest("heteroci-workflow-executor-unit-set/v1", unitIDs)
	if err != nil {
		return [4]string{}, err
	}
	nodeDigest, err := DeriveExecutorBindingValueDigest("heteroci-workflow-executor-node-facts/v1", node)
	return [4]string{shardDigest, batchDigest, unitDigest, nodeDigest}, err
}
func equalExecutorBindingStrings(left, right []string) bool {
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
func executorBindingNode(snapshot SnapshotSet, nodeID string) (NodeSnapshot, bool) {
	for _, node := range snapshot.Nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return NodeSnapshot{}, false
}

// ExecutorBindingMatchingShards 返回指定 node 的 canonical shard/wave/unit 集合。
func ExecutorBindingMatchingShards(nodeID string, shards []NodeShard) []NodeShard {
	matched := make([]NodeShard, 0, 1)
	for _, shard := range shards {
		if shard.NodeID != nodeID {
			continue
		}
		copyShard := shard
		copyShard.Waves = append([]ExecutionWave(nil), shard.Waves...)
		sort.Slice(copyShard.Waves, func(left, right int) bool { return copyShard.Waves[left].Wave < copyShard.Waves[right].Wave })
		for index := range copyShard.Waves {
			copyShard.Waves[index].UnitIDs = append([]string(nil), copyShard.Waves[index].UnitIDs...)
			sort.Strings(copyShard.Waves[index].UnitIDs)
		}
		matched = append(matched, copyShard)
	}
	return matched
}
