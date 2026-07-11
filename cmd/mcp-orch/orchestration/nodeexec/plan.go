package nodeexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// 本文件把 typed DAG ops 归一化为可持久化的变更计划。
// plan 层只做形状、拓扑和状态白名单校验；真正写库和版本 OCC 由 orchestration service 负责。
//
// 设计取舍：nodeexec 包不依赖 taskdag（types.go 注释明确解耦），故输入用
// nodeexec.ExistingNode 描述现有节点（只含 NodeKey + DependsOn），输出 specs
// 用 nodeexec.NodeSpec —— orchestration 层再把它映射成 taskdag.Node 写库。

// ErrAddNodePlan 是 PlanAddNodes 形状校验失败时的 sentinel。
// 调用方（service.applyTypedOps）需要把它包成 ErrApplyOpsInvalid。
var ErrAddNodePlan = errors.New("add_node plan: invalid request")

// ExistingNode 描述 DAG 现有节点的最小投影 —— PlanAddNodes 只关心 NodeKey
// 唯一性和 DependsOn 入环检测，其他字段无关。
type ExistingNode struct {
	NodeKey   string
	DependsOn []string
}

// PlanAddNodes 校验 add_node 操作并返回环检测用 adjacency 与待写入节点。
//  1. 校验单个 ops：node_key 非空 + 与现有 / 同批不重名 + 不自依赖。
//  2. 校验 depends_on 引用：必须命中 existing ∪ 同批 NodeKey；允许 ops 内
//     互相引用不论顺序。
//
// 返回：adjacency（含 existing + new，传给 DetectCycle）/ 通过校验的 NodeSpec
// 列表（保留 ops 原顺序，供 service 顺序 UpsertNode）/ 错误。
func PlanAddNodes(ops Ops, existing []ExistingNode) (map[string][]string, []NodeSpec, error) {
	adjacency, known := seedAdjacency(existing)
	accepted := make([]NodeSpec, 0, len(ops))
	for i, op := range ops {
		spec, err := addNodeSpecFromOp(i, op)
		if err != nil {
			return nil, nil, err
		}
		if err := registerNewNode(i, spec, known, adjacency); err != nil {
			return nil, nil, err
		}
		accepted = append(accepted, spec)
	}
	if err := verifyDependsOnIntegrity(accepted, known); err != nil {
		return nil, nil, err
	}
	if err := ValidateNodeSpecsConfig(accepted); err != nil {
		return nil, nil, err
	}
	return adjacency, accepted, nil
}

// ValidateCreateDAGNodes 复用 add_node 拓扑校验来检查创建 DAG 时的初始节点列表。
// 这里只关心 node_key 唯一性、depends_on 引用和环，不触碰持久化。
func ValidateCreateDAGNodes(nodes []contract.CreateDAGNodeRequest) error {
	specs := make([]NodeSpec, len(nodes))
	for i, n := range nodes {
		specs[i] = NodeSpec{NodeKey: n.NodeKey, Title: n.Title, NodeType: n.NodeType, DependsOn: n.DependsOn, Reads: n.Reads, Writes: n.Writes, Config: n.Config}
	}
	if err := ValidateAddNodeTopology(specs); err != nil {
		return err
	}
	return ValidateNodeSpecsConfig(specs)
}

// ValidateNodeSpecsConfig 校验新增节点的 typed config，避免非法 executable config 持久化。
func ValidateNodeSpecsConfig(specs []NodeSpec) error {
	for _, spec := range specs {
		if err := ValidatePersistableNodeConfig(spec.NodeType, spec.Config); err != nil {
			return fmt.Errorf("%w: add_node %q config invalid: %w", ErrAddNodePlan, spec.NodeKey, err)
		}
	}
	return nil
}

// ValidateAddNodeTopology 校验新增节点集合自身的拓扑合法性。
// 它会复用 PlanAddNodes 和 DetectCycle，作为 create/update 入口的轻量 fail-fast 检查。
func ValidateAddNodeTopology(specs []NodeSpec) error {
	ops := make(Ops, 0, len(specs))
	for _, spec := range specs {
		ops = append(ops, OpAddNode{Node: spec})
	}
	adjacency, _, err := PlanAddNodes(ops, nil)
	if err == nil {
		err = DetectCycle(adjacency)
	}
	return err
}

// seedAdjacency 把 existing 列表灌进 adjacency 与 known 集合，作为 PlanAddNodes
// 的初始状态。
func seedAdjacency(existing []ExistingNode) (map[string][]string, map[string]struct{}) {
	adjacency := make(map[string][]string, len(existing))
	known := make(map[string]struct{}, len(existing))
	for _, n := range existing {
		adjacency[n.NodeKey] = append([]string(nil), n.DependsOn...)
		known[n.NodeKey] = struct{}{}
	}
	return adjacency, known
}

// addNodeSpecFromOp 把 Op 强转为 OpAddNode 并归一化 NodeSpec 字段。
func addNodeSpecFromOp(idx int, op Op) (NodeSpec, error) {
	add, ok := op.(OpAddNode)
	if !ok {
		return NodeSpec{}, fmt.Errorf("%w: ops[%d] not add_node", ErrAddNodePlan, idx)
	}
	spec := add.Node
	spec.NodeKey = strings.TrimSpace(spec.NodeKey)
	if spec.NodeKey == "" {
		return NodeSpec{}, fmt.Errorf("%w: ops[%d] add_node node_key required", ErrAddNodePlan, idx)
	}
	spec.DependsOn = NormalizeDependsOn(spec.DependsOn)
	return spec, nil
}

// registerNewNode 校验 spec 与现有图不冲突，更新 known + adjacency。
func registerNewNode(idx int, spec NodeSpec, known map[string]struct{}, adjacency map[string][]string) error {
	if _, dup := known[spec.NodeKey]; dup {
		return fmt.Errorf("%w: ops[%d] add_node node_key %q already exists", ErrAddNodePlan, idx, spec.NodeKey)
	}
	if slices.Contains(spec.DependsOn, spec.NodeKey) {
		return fmt.Errorf("%w: ops[%d] add_node %q depends on itself", ErrAddNodePlan, idx, spec.NodeKey)
	}
	known[spec.NodeKey] = struct{}{}
	adjacency[spec.NodeKey] = spec.DependsOn
	return nil
}

// verifyDependsOnIntegrity 二走：每个新节点 depends_on 必须可达到 known 集合。
func verifyDependsOnIntegrity(accepted []NodeSpec, known map[string]struct{}) error {
	for _, n := range accepted {
		for _, d := range n.DependsOn {
			if _, ok := known[d]; !ok {
				return fmt.Errorf("%w: add_node %q depends on unknown node %q", ErrAddNodePlan, n.NodeKey, d)
			}
		}
	}
	return nil
}

// NormalizeDependsOn 去空去重以便环检测 adjacency 拿到干净输入。不保顺序：
// 环检测不需序（邻接集合语义）。
func NormalizeDependsOn(deps []string) []string {
	if len(deps) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(deps))
	out := make([]string, 0, len(deps))
	for _, d := range deps {
		t := strings.TrimSpace(d)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// ErrUpdateNodePlan 是 PlanUpdateNodes 校验失败时的 sentinel。
// 调用方（service.applyTypedOps）会把它包成 ErrApplyOpsInvalid。
var ErrUpdateNodePlan = errors.New("update_node plan: invalid request")

// ExistingNodeFull 是 PlanUpdateNodes 需要的节点最小投影。
// status 用于阻止 running、retrying 或终态节点被在线编辑。
type ExistingNodeFull struct {
	NodeKey   string
	DependsOn []string
	Status    string
	NodeType  string
	Config    json.RawMessage
}

// UpdateNodeChange 是 PlanUpdateNodes 输出的已校验、待持久化的单条变更。
// 字段语义沿用 NodePatch：调用方在 store 层调 UpsertNode 时根据 Patch 三态
// 决定哪些列写入新值、哪些列保留旧值。
type UpdateNodeChange struct {
	NodeKey string
	Patch   NodePatch
}

// updateNodeStatusAllowed 列出 update_node 允许触发的节点 status 集合。
// 只有尚未执行或刚可执行的节点可编辑；运行中、退避中和终态节点都必须拒绝。
var updateNodeStatusAllowed = map[string]struct{}{
	string(NodeStatusPending): {},
	string(NodeStatusReady):   {},
}

// PlanUpdateNodes 走两遍 pass：
//  1. 校验单个 op：op_kind 必须 update_node / node_key 非空 / 目标节点存在
//     / 目标 status ∈ {pending, ready} / patch.depends_on 不自依赖。
//  2. 校验 depends_on 引用：必须命中现有节点 key 集合。
//
// 返回：adjacency（已应用 patch.depends_on 三态语义，传给 DetectCycle）/
// 已通过校验的 UpdateNodeChange 列表（保留 ops 原顺序，供 service 顺序
// UpsertNode）/ 错误。
func PlanUpdateNodes(ops Ops, existing []ExistingNodeFull) (map[string][]string, []UpdateNodeChange, error) {
	adjacency, byKey := seedAdjacencyFull(existing)
	known := knownKeys(byKey)
	changes := make([]UpdateNodeChange, 0, len(ops))
	for i, op := range ops {
		change, err := updateChangeFromOp(i, op)
		if err != nil {
			return nil, nil, err
		}
		node, ok := byKey[change.NodeKey]
		if !ok {
			return nil, nil, fmt.Errorf("%w: ops[%d] update_node node_key %q not found", ErrUpdateNodePlan, i, change.NodeKey)
		}
		if err := ensureUpdatableStatus(i, change.NodeKey, node.Status); err != nil {
			return nil, nil, err
		}
		if err := applyDependsOnPatch(i, change, adjacency, known); err != nil {
			return nil, nil, err
		}
		if err := validateUpdateNodeConfig(change, node); err != nil {
			return nil, nil, err
		}
		changes = append(changes, change)
	}
	return adjacency, changes, nil
}

func validateUpdateNodeConfig(change UpdateNodeChange, node ExistingNodeFull) error {
	config := node.Config
	if !emptyPatchConfig(change.Patch.Config) {
		config = change.Patch.Config
	}
	if err := ValidatePersistableNodeConfig(node.NodeType, config); err != nil {
		return fmt.Errorf("%w: update_node %q config invalid: %w", ErrUpdateNodePlan, change.NodeKey, err)
	}
	return nil
}

func emptyPatchConfig(raw json.RawMessage) bool {
	return len(raw) == 0 || strings.TrimSpace(string(raw)) == "null"
}

// seedAdjacencyFull 是 seedAdjacency 的 Full 版：把 ExistingNodeFull 列表
// 灌进 adjacency + 按 key 索引节点。
func seedAdjacencyFull(existing []ExistingNodeFull) (map[string][]string, map[string]ExistingNodeFull) {
	adjacency := make(map[string][]string, len(existing))
	byKey := make(map[string]ExistingNodeFull, len(existing))
	for _, n := range existing {
		adjacency[n.NodeKey] = append([]string(nil), n.DependsOn...)
		byKey[n.NodeKey] = n
	}
	return adjacency, byKey
}

// knownKeys 抽出 byKey 的 key 集合（PlanUpdateNodes 校验 depends_on 引用时用）。
func knownKeys(byKey map[string]ExistingNodeFull) map[string]struct{} {
	out := make(map[string]struct{}, len(byKey))
	for k := range byKey {
		out[k] = struct{}{}
	}
	return out
}

// updateChangeFromOp 把 Op 强转为 OpUpdateNode 并归一化 NodeKey。
func updateChangeFromOp(idx int, op Op) (UpdateNodeChange, error) {
	upd, ok := op.(OpUpdateNode)
	if !ok {
		return UpdateNodeChange{}, fmt.Errorf("%w: ops[%d] not update_node (got %s)", ErrUpdateNodePlan, idx, op.Kind())
	}
	key := strings.TrimSpace(upd.NodeKey)
	if key == "" {
		return UpdateNodeChange{}, fmt.Errorf("%w: ops[%d] update_node node_key required", ErrUpdateNodePlan, idx)
	}
	return UpdateNodeChange{NodeKey: key, Patch: upd.Patch}, nil
}

// ensureUpdatableStatus 校验 update_node 的目标仍处在可编辑状态。
// running、终态、retrying、waiting_human 一律拒绝，避免在线改写已调度节点。
func ensureUpdatableStatus(idx int, key, status string) error {
	if _, ok := updateNodeStatusAllowed[status]; !ok {
		return fmt.Errorf("%w: ops[%d] update_node %q status=%q not updatable (allowed: pending|ready)", ErrUpdateNodePlan, idx, key, status)
	}
	return nil
}

// applyDependsOnPatch 处理 patch.DependsOn 三态：
//   - nil 不改 → adjacency 保留 existing
//   - *[] / *[a,b] 改 → adjacency 写入新值（NormalizeDependsOn 去空去重）；
//     校验：不自依赖 / 引用 key 必须在 known 集合内
func applyDependsOnPatch(idx int, change UpdateNodeChange, adjacency map[string][]string, known map[string]struct{}) error {
	if change.Patch.DependsOn == nil {
		return nil
	}
	deps := NormalizeDependsOn(*change.Patch.DependsOn)
	for _, d := range deps {
		if d == change.NodeKey {
			return fmt.Errorf("%w: ops[%d] update_node %q depends on itself", ErrUpdateNodePlan, idx, change.NodeKey)
		}
		if _, ok := known[d]; !ok {
			return fmt.Errorf("%w: ops[%d] update_node %q depends on unknown node %q", ErrUpdateNodePlan, idx, change.NodeKey, d)
		}
	}
	adjacency[change.NodeKey] = deps
	return nil
}

// ErrRemoveNodePlan 是 PlanRemoveNodes 校验失败时的 sentinel。
// 调用方（service.applyTypedOps）会把它包成 ErrApplyOpsInvalid。
var ErrRemoveNodePlan = errors.New("remove_node plan: invalid request")

// RemoveNodeChange 是 PlanRemoveNodes 输出的已校验、待持久化删除。
type RemoveNodeChange struct {
	NodeKey string
}

// removeNodeStatusAllowed 列出 remove_node 允许的节点状态集合。
var removeNodeStatusAllowed = map[string]struct{}{
	string(NodeStatusPending): {},
	string(NodeStatusReady):   {},
}

// PlanRemoveNodes 校验 remove_node，并返回移除目标后的 adjacency。
// 它不会自动改写下游 depends_on；仍被其它节点依赖的目标会被拒绝。
func PlanRemoveNodes(ops Ops, existing []ExistingNodeFull, adjacency map[string][]string) (map[string][]string, []RemoveNodeChange, error) {
	pruned := cloneAdjacency(adjacency)
	byKey := indexExistingFull(existing)
	seen := make(map[string]int, len(ops))
	changes := make([]RemoveNodeChange, 0, len(ops))
	for i, op := range ops {
		change, err := removeChangeFromOp(i, op)
		if err != nil {
			return nil, nil, err
		}
		if prev, dup := seen[change.NodeKey]; dup {
			return nil, nil, fmt.Errorf("%w: ops[%d] and ops[%d] both remove node_key %q", ErrRemoveNodePlan, prev, i, change.NodeKey)
		}
		seen[change.NodeKey] = i
		node, ok := byKey[change.NodeKey]
		if !ok {
			return nil, nil, fmt.Errorf("%w: ops[%d] remove_node node_key %q not found", ErrRemoveNodePlan, i, change.NodeKey)
		}
		if err := ensureRemovableStatus(i, change.NodeKey, node.Status); err != nil {
			return nil, nil, err
		}
		if dependent := firstDependentOn(pruned, change.NodeKey); dependent != "" {
			return nil, nil, fmt.Errorf("%w: ops[%d] remove_node %q is depended on by %q", ErrRemoveNodePlan, i, change.NodeKey, dependent)
		}
		delete(pruned, change.NodeKey)
		changes = append(changes, change)
	}
	return pruned, changes, nil
}

// cloneAdjacency 深拷贝 adjacency map，避免 PlanRemoveNodes 修改调用方原始数据。
func cloneAdjacency(adjacency map[string][]string) map[string][]string {
	out := make(map[string][]string, len(adjacency))
	for k, deps := range adjacency {
		out[k] = append([]string(nil), deps...)
	}
	return out
}

// indexExistingFull 把 ExistingNodeFull 列表按 NodeKey 索引为 map。
func indexExistingFull(existing []ExistingNodeFull) map[string]ExistingNodeFull {
	out := make(map[string]ExistingNodeFull, len(existing))
	for _, n := range existing {
		out[n.NodeKey] = n
	}
	return out
}

// removeChangeFromOp 把 Op 强转为 OpRemoveNode 并归一化 NodeKey。
func removeChangeFromOp(idx int, op Op) (RemoveNodeChange, error) {
	rm, ok := op.(OpRemoveNode)
	if !ok {
		return RemoveNodeChange{}, fmt.Errorf("%w: ops[%d] not remove_node (got %s)", ErrRemoveNodePlan, idx, op.Kind())
	}
	key := strings.TrimSpace(rm.NodeKey)
	if key == "" {
		return RemoveNodeChange{}, fmt.Errorf("%w: ops[%d] remove_node node_key required", ErrRemoveNodePlan, idx)
	}
	return RemoveNodeChange{NodeKey: key}, nil
}

// ensureRemovableStatus 校验节点 status 必须 ∈ {pending, ready} 才允许删除。
func ensureRemovableStatus(idx int, key, status string) error {
	if _, ok := removeNodeStatusAllowed[status]; !ok {
		return fmt.Errorf("%w: ops[%d] remove_node %q status=%q not removable (allowed: pending|ready)", ErrRemoveNodePlan, idx, key, status)
	}
	return nil
}

// firstDependentOn 返回 adjacency 中第一个依赖 target 的节点 key，不存在返回空串。
func firstDependentOn(adjacency map[string][]string, target string) string {
	for nodeKey, deps := range adjacency {
		if nodeKey == target {
			continue
		}
		if slices.Contains(deps, target) {
			return nodeKey
		}
	}
	return ""
}
