package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var applyOpsActionEnum = []string{"update_dag", "add_node", "update_node", "remove_node", "apply_ops_raw"}

// HandleApplyOps 处理应用ops。
func HandleApplyOps(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in ApplyOpsInput) (any, error) {
		req, err := applyOpsRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		return svc.ApplyOps(ctx, req)
	})
}

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

// applyOpsPayloadFromInput 从input应用ops载荷。
func applyOpsPayloadFromInput(in ApplyOpsInput) (json.RawMessage, error) {
	action := strings.TrimSpace(in.Action)
	if action == "" {
		if !hasExplicitRawJSON(in.Ops) {
			return nil, fmt.Errorf("ops is required when action is omitted")
		}
		return append(json.RawMessage(nil), in.Ops...), nil
	}
	if _, err := requireEnum(action, "action", applyOpsActionEnum); err != nil {
		return nil, err
	}
	if action == "apply_ops_raw" {
		if !hasExplicitRawJSON(in.Ops) {
			return nil, fmt.Errorf("ops is required when action=apply_ops_raw")
		}
		return append(json.RawMessage(nil), in.Ops...), nil
	}
	if hasExplicitRawJSON(in.Ops) {
		return nil, fmt.Errorf("ops cannot be combined with flat action %q", action)
	}
	op, err := flatApplyOpFromInput(action, in)
	if err != nil {
		return nil, err
	}
	return json.Marshal([]map[string]any{op})
}

// flatApplyOpFromInput 从input处理flat应用op。
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

// flatAddNodeFromInput 从input处理flatadd节点。
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

// flatDAGPatchFromInput 从input处理flatDAG补丁。
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

// flatNodePatchFromInput 从input处理flat节点补丁。
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

func hasFlatDAGPatchFields(in ApplyOpsInput) bool {
	return strings.TrimSpace(in.Title) != "" ||
		strings.TrimSpace(in.Description) != "" ||
		strings.TrimSpace(in.Trigger) != "" ||
		strings.TrimSpace(in.CronExpr) != "" ||
		strings.TrimSpace(in.OwnerID) != ""
}

func hasFlatNodePatchFields(in ApplyOpsInput) bool {
	return strings.TrimSpace(in.Title) != "" ||
		strings.TrimSpace(in.AssignedTo) != "" ||
		in.DependsOn != nil ||
		hasExplicitRawJSON(in.Config)
}

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
