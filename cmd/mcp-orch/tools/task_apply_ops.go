package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// applyOpsActionEnum 是扁平 action 入口允许的操作集合。
// schema 和 handler 共用这份枚举，避免工具描述允许但运行时拒绝的漂移。
var applyOpsActionEnum = []string{"update_dag", "add_node", "update_node", "remove_node", "apply_ops_raw"}

// HandleApplyOps 处理 DAG 模板 ops 写入工具调用。
// 输入会先统一转成 ApplyOpsRequest，raw ops 与扁平 action 混用时立即报错。
func HandleApplyOps(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in ApplyOpsInput) (any, error) {
		req, err := applyOpsRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		return svc.ApplyOps(ctx, req)
	})
}

// applyOpsRequestFromInput 解析 DAG 定位符并构造服务层写入请求。
// base_version 原样透传给服务层做 OCC，避免 handler 自行判断并发版本。
func applyOpsRequestFromInput(in ApplyOpsInput) (contract.ApplyOpsRequest, error) {
	dagKey, err := resolveDAGKeyInput(in.DagKey, in.Pos)
	if err != nil {
		return contract.ApplyOpsRequest{}, err
	}
	ops, err := applyOpsPayloadFromInput(in)
	if err != nil {
		return contract.ApplyOpsRequest{}, err
	}
	return contract.ApplyOpsRequest{
		DagKey:      dagKey,
		BaseVersion: in.BaseVersion,
		Ops:         ops,
	}, nil
}

// applyOpsPayloadFromInput 将 raw ops 或扁平 action 统一编码为 nodeexec.Ops JSON。
// raw ops 与扁平字段不能同时出现，防止调用方以为局部字段会覆盖 raw payload。
func applyOpsPayloadFromInput(in ApplyOpsInput) (json.RawMessage, error) {
	action := strings.TrimSpace(in.Action)
	if action == "" {
		if !hasExplicitRawJSON(in.Ops) {
			return nil, fmt.Errorf("ops is required when action is omitted")
		}
		return validatedApplyOpsPayload(append(json.RawMessage(nil), in.Ops...))
	}
	if _, err := requireEnum(action, "action", applyOpsActionEnum); err != nil {
		return nil, err
	}
	if action == "apply_ops_raw" {
		if !hasExplicitRawJSON(in.Ops) {
			return nil, fmt.Errorf("ops is required when action=apply_ops_raw")
		}
		return validatedApplyOpsPayload(append(json.RawMessage(nil), in.Ops...))
	}
	if hasExplicitRawJSON(in.Ops) {
		return nil, fmt.Errorf("ops cannot be combined with flat action %q", action)
	}
	op, err := flatApplyOpFromInput(action, in)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal([]map[string]any{op})
	if err != nil {
		return nil, err
	}
	return validatedApplyOpsPayload(raw)
}

// validatedApplyOpsPayload 校验即将落库的 ops payload。
// 当前重点拦截保留能力，后续新增校验应放在这里保持所有入口一致。
func validatedApplyOpsPayload(raw json.RawMessage) (json.RawMessage, error) {
	if err := rejectReservedApplyOpsCapabilities(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// rejectReservedApplyOpsCapabilities 拒绝尚未闭环运行时能力的节点类型。
// 这里直接解码 typed ops，确保 raw 批量入口也不能绕过 create/update 的限制。
func rejectReservedApplyOpsCapabilities(raw json.RawMessage) error {
	var ops nodeexec.Ops
	if err := json.Unmarshal(raw, &ops); err != nil {
		return err
	}
	for i, op := range ops {
		add, ok := op.(nodeexec.OpAddNode)
		if !ok {
			continue
		}
		if err := validateCreatableNodeType(fmt.Sprintf("ops[%d].node.node_type", i), add.Node.NodeType); err != nil {
			return err
		}
	}
	return nil
}

// flatApplyOpFromInput 把单个扁平 action 转成 nodeexec 可识别的 op 对象。
// 每个分支只填充对应 action 的必要字段，未知 action 保持 fail-fast。
func flatApplyOpFromInput(action string, in ApplyOpsInput) (map[string]any, error) {
	switch action {
	case "update_dag":
		patch, err := flatDAGPatchFromInput(in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"op": "update_dag", "patch": patch}, nil
	case "add_node":
		node, err := flatAddNodeFromInput(in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"op": "add_node", "node": node}, nil
	case "update_node":
		nodeKey, err := requireTrimmed(in.NodeKey, "node_key")
		if err != nil {
			return nil, err
		}
		patch, err := flatNodePatchFromInput(in)
		if err != nil {
			return nil, err
		}
		return map[string]any{"op": "update_node", "node_key": nodeKey, "patch": patch}, nil
	case "remove_node":
		nodeKey, err := requireTrimmed(in.NodeKey, "node_key")
		if err != nil {
			return nil, err
		}
		return map[string]any{"op": "remove_node", "node_key": nodeKey}, nil
	default:
		return nil, fmt.Errorf("unsupported action %q", action)
	}
}

// flatAddNodeFromInput 构造 add_node 的最小节点对象。
// config 和 depends_on 只在显式传入时写入，避免无意覆盖后端默认行为。
func flatAddNodeFromInput(in ApplyOpsInput) (map[string]any, error) {
	nodeKey, err := requireTrimmed(in.NodeKey, "node_key")
	if err != nil {
		return nil, err
	}
	title, err := requireTrimmed(in.Title, "title")
	if err != nil {
		return nil, err
	}
	nodeType, err := requireTrimmed(in.NodeType, "node_type")
	if err != nil {
		return nil, err
	}
	node := map[string]any{
		"node_key":  nodeKey,
		"title":     title,
		"node_type": nodeType,
	}
	if value := strings.TrimSpace(in.AssignedTo); value != "" {
		node["assigned_to"] = value
	}
	if in.DependsOn != nil {
		node["depends_on"] = trimStringSlicePreserveEmpty(in.DependsOn)
	}
	if hasExplicitRawJSON(in.Config) {
		node["config"] = append(json.RawMessage(nil), in.Config...)
	}
	return node, nil
}

// flatDAGPatchFromInput 生成 update_dag patch。
// raw patch 与扁平字段互斥，防止同一字段出现两个来源时产生隐式优先级。
func flatDAGPatchFromInput(in ApplyOpsInput) (map[string]any, error) {
	if hasExplicitRawJSON(in.Patch) {
		if hasFlatDAGPatchFields(in) {
			return nil, fmt.Errorf("patch cannot be combined with flat update_dag fields")
		}
		return rawObjectMap(in.Patch, "patch")
	}
	patch := map[string]any{}
	if value := strings.TrimSpace(in.Title); value != "" {
		patch["title"] = value
	}
	if value := strings.TrimSpace(in.Description); value != "" {
		patch["description"] = value
	}
	if value := strings.TrimSpace(in.Trigger); value != "" {
		patch["trigger"] = value
	}
	if value := strings.TrimSpace(in.CronExpr); value != "" {
		patch["cron_expr"] = value
	}
	if value := strings.TrimSpace(in.OwnerID); value != "" {
		patch["owner_id"] = value
	}
	if len(patch) == 0 {
		return nil, fmt.Errorf("update_dag requires patch or at least one flat patch field")
	}
	return patch, nil
}

// flatNodePatchFromInput 生成 update_node patch。
// 空 patch 会被拒绝，避免产生“成功但没有实际改动”的写操作。
func flatNodePatchFromInput(in ApplyOpsInput) (map[string]any, error) {
	if hasExplicitRawJSON(in.Patch) {
		if hasFlatNodePatchFields(in) {
			return nil, fmt.Errorf("patch cannot be combined with flat update_node fields")
		}
		return rawObjectMap(in.Patch, "patch")
	}
	patch := map[string]any{}
	if value := strings.TrimSpace(in.Title); value != "" {
		patch["title"] = value
	}
	if value := strings.TrimSpace(in.AssignedTo); value != "" {
		patch["assigned_to"] = value
	}
	if in.DependsOn != nil {
		patch["depends_on"] = trimStringSlicePreserveEmpty(in.DependsOn)
	}
	if hasExplicitRawJSON(in.Config) {
		patch["config"] = append(json.RawMessage(nil), in.Config...)
	}
	if len(patch) == 0 {
		return nil, fmt.Errorf("update_node requires patch or at least one flat patch field")
	}
	return patch, nil
}

// rawObjectMap 将高级 raw patch 解为对象并保留字段名上下文。
// 非对象 JSON 会立即报错，因为 ops patch 只能表达对象级变更。
func rawObjectMap(raw json.RawMessage, field string) (map[string]any, error) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object: %w", field, err)
	}
	if obj == nil {
		return nil, fmt.Errorf("%s must be a JSON object", field)
	}
	return obj, nil
}

// hasFlatDAGPatchFields 判断 update_dag 是否使用了任一扁平字段。
// 该结果只用于和 raw patch 互斥检查，避免两个来源争夺同一 patch 语义。
func hasFlatDAGPatchFields(in ApplyOpsInput) bool {
	return strings.TrimSpace(in.Title) != "" ||
		strings.TrimSpace(in.Description) != "" ||
		strings.TrimSpace(in.Trigger) != "" ||
		strings.TrimSpace(in.CronExpr) != "" ||
		strings.TrimSpace(in.OwnerID) != ""
}

// hasFlatNodePatchFields 判断 update_node 是否使用了任一扁平字段。
// depends_on 的空切片也算显式输入，因为它表达“清空依赖”而不是未传字段。
func hasFlatNodePatchFields(in ApplyOpsInput) bool {
	return strings.TrimSpace(in.Title) != "" ||
		strings.TrimSpace(in.AssignedTo) != "" ||
		in.DependsOn != nil ||
		hasExplicitRawJSON(in.Config)
}

// trimStringSlicePreserveEmpty 清理字符串切片但保留显式空切片。
// nil 表示未传字段，空切片表示调用方明确要清空 depends_on。
func trimStringSlicePreserveEmpty(values []string) []string {
	if values == nil {
		return nil
	}
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if candidate := strings.TrimSpace(value); candidate != "" {
			trimmed = append(trimmed, candidate)
		}
	}
	if trimmed == nil {
		return []string{}
	}
	return trimmed
}
