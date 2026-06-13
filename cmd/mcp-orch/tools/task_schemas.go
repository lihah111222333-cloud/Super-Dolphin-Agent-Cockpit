package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

var applyOpsOpEnum = []string{
	string(nodeexec.OpKindUpdateDAG),
	string(nodeexec.OpKindAddNode),
	string(nodeexec.OpKindUpdateNode),
	string(nodeexec.OpKindRemoveNode),
}

func applyOpsOpSchema() Schema {
	return ObjectSchema(map[string]Schema{
		"op": EnumStringSchema("Operation discriminator.", applyOpsOpEnum...),
		"node": ObjectSchema(map[string]Schema{
			"node_key":    StringSchema("Node key for add_node."),
			"title":       StringSchema("Node title for add_node."),
			"node_type":   EnumStringSchema("Node type for add_node.", "agent", "automation", "hybrid"),
			"assigned_to": StringSchema("Optional assignee for add_node."),
			"depends_on":  ArraySchema(StringSchema("Dependency node key."), "Dependency node keys."),
			"config":      RawObjectSchema("Optional node config for add_node."),
		}, "node_key", "title", "node_type"),
		"node_key": StringSchema("Target node key for update_node/remove_node."),
		"patch":    RawObjectSchema("Patch object for update_dag or update_node."),
	}, "op")
}

// createDAGSchema 创建DAGschema。
func createDAGSchema() Schema {
	return ObjectSchema(map[string]Schema{
		"agent_id":            StringSchema("Creator orchestration agent ID."),
		"dag_key":             StringSchema("Unique DAG key."),
		"title":               StringSchema("DAG title."),
		"description":         StringSchema("Optional DAG description."),
		"trigger":             StringSchema("Flat schedule shortcut; same as schedule.trigger. scheduled is rejected here; use task_dag_apply_ops with base_version and cron_expr."),
		"default_retry":       IntegerSchema("Flat schedule shortcut; same as schedule.default_retry."),
		"default_timeout_sec": IntegerSchema("Flat schedule shortcut; same as schedule.default_timeout_sec."),
		"fail_fast":           BooleanSchema("Flat schedule shortcut; same as schedule.fail_fast."),
		"max_concurrency":     IntegerSchema("Flat schedule shortcut; same as schedule.max_concurrency."),
		"queue_policy":        StringSchema("Flat schedule shortcut; same as schedule.queue_policy."),
		"schedule": ObjectSchema(map[string]Schema{
			"trigger":             StringSchema("Create-time trigger metadata. scheduled is rejected here; use task_dag_apply_ops with base_version and cron_expr."),
			"default_retry":       IntegerSchema("Default retry count for nodes."),
			"default_timeout_sec": IntegerSchema("Default node timeout in seconds."),
			"fail_fast":           BooleanSchema("Stop scheduling new nodes on first failure."),
			"max_concurrency":     IntegerSchema("Maximum parallel runnable nodes."),
			"queue_policy":        StringSchema("Ready-queue policy."),
		}),
		"final_node_key": StringSchema("Optional node_key that produces the run-level final_output."),
		"nodes": ArraySchema(ObjectSchema(map[string]Schema{
			"node_key":    StringSchema("Unique node key within the DAG."),
			"title":       StringSchema("Node title."),
			"node_type":   StringSchema("Optional node type."),
			"assigned_to": StringSchema("Optional assignee."),
			"depends_on":  ArraySchema(StringSchema("Dependency node key."), "Node dependency keys."),
			"command_ref": StringSchema("Optional command card key."),
			"on_failure":  StringSchema("Flat execution shortcut; same as execution.on_failure."),
			"pool":        StringSchema("Flat execution shortcut; same as execution.pool."),
			"priority":    IntegerSchema("Flat execution shortcut; same as execution.priority."),
			"retry":       IntegerSchema("Flat execution shortcut; same as execution.retry."),
			"timeout_sec": IntegerSchema("Flat execution shortcut; same as execution.timeout_sec."),
			"config":      RawObjectSchema("Optional full node config; executable agent nodes require config.exec.provider=claude or provider=codex with codex_home/codex_instance_key/codex_model_provider. For automation nodes use config.exec.command_ref."),
			"execution": ObjectSchema(map[string]Schema{
				"on_failure":  StringSchema("Failure policy override."),
				"pool":        StringSchema("Execution pool name."),
				"priority":    IntegerSchema("Queue priority."),
				"retry":       IntegerSchema("Retry override."),
				"timeout_sec": IntegerSchema("Timeout override in seconds."),
			}),
		}, "node_key", "title"), "Optional DAG nodes."),
	}, "dag_key", "title")
}

// createDAGRequestFromInput 从input创建DAG请求。
func createDAGRequestFromInput(in CreateDAGInput, trustedAgentID string) (contract.CreateDAGRequest, error) {
	agentID, err := resolveCreateDAGAgentID(in.AgentID, trustedAgentID)
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	dagKey, err := requireTrimmed(in.DagKey, "dag_key")
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	title, err := requireTrimmed(in.Title, "title")
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	nodes, err := createDAGNodesFromInput(in.Nodes)
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	if err := validateCreateDAGNodesForCreate(nodes); err != nil {
		return contract.CreateDAGRequest{}, err
	}
	finalNodeKey, err := normalizeFinalNodeKey(in.FinalNodeKey, nodes)
	if err != nil {
		return contract.CreateDAGRequest{}, createDAGInvalidInputError(
			err,
			"Set final_node_key to one of the nodes[].node_key values or omit it.",
		)
	}
	schedule, err := createDAGEffectiveSchedule(in)
	if err != nil {
		return contract.CreateDAGRequest{}, createDAGInvalidInputError(
			err,
			"Do not pass conflicting flat schedule shortcuts and nested schedule fields.",
		)
	}
	if err := rejectScheduledCreateDAGSchedule(schedule); err != nil {
		return contract.CreateDAGRequest{}, err
	}
	metadata, err := encodeJSONRaw(createDAGMetadata(schedule, finalNodeKey))
	if err != nil {
		return contract.CreateDAGRequest{}, err
	}
	return contract.CreateDAGRequest{
		DagKey:      dagKey,
		Title:       title,
		Description: strings.TrimSpace(in.Description),
		CreatedBy:   agentID,
		Metadata:    metadata,
		Nodes:       nodes,
	}, nil
}

func resolveCreateDAGAgentID(publicAgentID, trustedAgentID string) (string, error) {
	publicAgentID = strings.TrimSpace(publicAgentID)
	trustedAgentID = strings.TrimSpace(trustedAgentID)
	if trustedAgentID != "" {
		if publicAgentID != "" && publicAgentID != trustedAgentID {
			return "", createDAGInvalidInputError(
				fmt.Errorf("agent_id %q conflicts with trusted _agentId %q", publicAgentID, trustedAgentID),
				"Omit agent_id or pass the same value as the trusted tool scope _agentId.",
			)
		}
		return trustedAgentID, nil
	}
	return requireTrimmed(publicAgentID, "agent_id")
}

func createDAGInvalidInputError(err error, hint string) error {
	return mcpcommon.NewCodedToolError("invalid_input", err, false, hint)
}

func rejectScheduledCreateDAGSchedule(schedule DAGScheduleInput) error {
	if strings.TrimSpace(schedule.Trigger) != "scheduled" {
		return nil
	}
	return createDAGInvalidInputError(
		fmt.Errorf("schedule.trigger=scheduled requires task_dag_apply_ops with trigger and cron_expr; task_create_dag is create-only"),
		"Use task_dag_apply_ops with base_version plus trigger=scheduled and cron_expr after creating the DAG.",
	)
}

func validateCreateDAGNodesForCreate(nodes []contract.CreateDAGNodeRequest) error {
	if err := validateRootAgentAssignees(nodes); err != nil {
		return err
	}
	return validateAgentNodeLaunchConfigs(nodes)
}

func validateRootAgentAssignees(nodes []contract.CreateDAGNodeRequest) error {
	for i, node := range nodes {
		if !isRunnableRootAgentNode(node) {
			continue
		}
		if strings.TrimSpace(node.AssignedTo) != "" {
			continue
		}
		return fmt.Errorf("nodes[%d].assigned_to required for root agent node %q with config.exec; task_start_dag cannot automatically dispatch unassigned roots", i, node.NodeKey)
	}
	return nil
}

// validateAgentNodeLaunchConfigs 校验代理节点启动配置。
func validateAgentNodeLaunchConfigs(nodes []contract.CreateDAGNodeRequest) error {
	for i, node := range nodes {
		if !isAgentNodeType(node.NodeType) || !hasNodeExecConfig(node.Config) {
			continue
		}
		cfg, err := nodeexec.ParseAgentConfig(node.Config)
		if err != nil {
			return fmt.Errorf("nodes[%d].config: %w", i, err)
		}
		provider := strings.ToLower(strings.TrimSpace(cfg.Exec.Provider))
		switch provider {
		case "":
			return fmt.Errorf("nodes[%d].config.exec.provider required for agent node %q; set provider to claude or codex", i, node.NodeKey)
		case "claude":
			continue
		case "codex":
			if missing := missingCodexIdentityFields(cfg.Exec); len(missing) != 0 {
				return fmt.Errorf("nodes[%d].config.exec provider=codex for agent node %q requires %s", i, node.NodeKey, strings.Join(missing, ", "))
			}
		default:
			return fmt.Errorf("nodes[%d].config.exec.provider invalid for agent node %q: must be claude or codex", i, node.NodeKey)
		}
	}
	return nil
}

func isAgentNodeType(nodeType string) bool {
	nodeType = strings.TrimSpace(nodeType)
	return nodeType == "" || nodeType == "agent"
}

func missingCodexIdentityFields(exec nodeexec.AgentExecConfig) []string {
	var missing []string
	if strings.TrimSpace(exec.CodexHome) == "" {
		missing = append(missing, "codex_home")
	}
	if strings.TrimSpace(exec.CodexInstanceKey) == "" {
		missing = append(missing, "codex_instance_key")
	}
	if strings.TrimSpace(exec.CodexModelProvider) == "" {
		missing = append(missing, "codex_model_provider")
	}
	return missing
}

func isRunnableRootAgentNode(node contract.CreateDAGNodeRequest) bool {
	nodeType := strings.TrimSpace(node.NodeType)
	return (nodeType == "" || nodeType == "agent") && len(node.DependsOn) == 0 && hasNodeExecConfig(node.Config)
}

func hasNodeExecConfig(raw json.RawMessage) bool {
	if !hasExplicitRawJSON(raw) {
		return false
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		return false
	}
	exec, ok := config["exec"]
	return ok && hasExplicitRawJSON(exec)
}

// createDAGNodesFromInput 从input创建DAG节点。
func createDAGNodesFromInput(nodes []CreateDAGNodeInput) ([]contract.CreateDAGNodeRequest, error) {
	mapped := make([]contract.CreateDAGNodeRequest, 0, len(nodes))
	for i, node := range nodes {
		nodeKey, err := requireTrimmed(node.NodeKey, fmt.Sprintf("nodes[%d].node_key", i))
		if err != nil {
			return nil, err
		}
		title, err := requireTrimmed(node.Title, fmt.Sprintf("nodes[%d].title", i))
		if err != nil {
			return nil, err
		}
		nodeType := createDAGNodeType(node)
		node.NodeType = nodeType
		config, err := createDAGNodeConfig(node)
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, contract.CreateDAGNodeRequest{
			NodeKey:    nodeKey,
			Title:      title,
			NodeType:   nodeType,
			AssignedTo: strings.TrimSpace(node.AssignedTo),
			DependsOn:  append([]string(nil), node.DependsOn...),
			CommandRef: strings.TrimSpace(node.CommandRef),
			Config:     config,
		})
	}
	if err := validateCreateDAGNodeTopology(mapped); err != nil {
		return nil, err
	}
	return mapped, nil
}

func createDAGNodeType(node CreateDAGNodeInput) string {
	nodeType := strings.TrimSpace(node.NodeType)
	if nodeType == "" && strings.TrimSpace(node.CommandRef) != "" {
		return "automation"
	}
	return nodeType
}

func validateCreateDAGNodeTopology(nodes []contract.CreateDAGNodeRequest) error {
	specs := make([]nodeexec.NodeSpec, 0, len(nodes))
	for _, node := range nodes {
		specs = append(specs, nodeexec.NodeSpec{
			NodeKey:   node.NodeKey,
			Title:     node.Title,
			NodeType:  node.NodeType,
			DependsOn: append([]string(nil), node.DependsOn...),
			Config:    node.Config,
		})
	}
	if err := nodeexec.ValidateAddNodeTopology(specs); err != nil {
		return createDAGInvalidInputError(
			fmt.Errorf("nodes topology invalid: %w", err),
			"Fix duplicate node_key, unknown depends_on, or cycles before calling task_create_dag.",
		)
	}
	return nil
}

// createDAGMetadata 把 schedule 字段编码为 DAG metadata JSON 子树。
// 旧版 metadata 字段 (S15.1 删除 / migrations/0075_dag_v2_compat.sql 迁移) 不再处理：
// 数据库老行已一次性映射到 trigger 一等字段，tools 入参 schema 不再接受，
// 调用方如果传入会被忽略（向后兼容）。
func createDAGMetadata(schedule DAGScheduleInput, finalNodeKey string) map[string]any {
	metadata := map[string]any{"schedule": scheduleMap(schedule)}
	if finalNodeKey != "" {
		metadata["final_node_key"] = finalNodeKey
	}
	return metadata
}

func normalizeFinalNodeKey(raw string, nodes []contract.CreateDAGNodeRequest) (string, error) {
	finalNodeKey := strings.TrimSpace(raw)
	if finalNodeKey == "" {
		return "", nil
	}
	for _, node := range nodes {
		if node.NodeKey == finalNodeKey {
			return finalNodeKey, nil
		}
	}
	return "", fmt.Errorf("final_node_key %s does not match any node_key", finalNodeKey)
}

// createDAGNodeConfig 创建DAG节点配置。
func createDAGNodeConfig(node CreateDAGNodeInput) (json.RawMessage, error) {
	commandRef := strings.TrimSpace(node.CommandRef)
	isAutomation := strings.TrimSpace(node.NodeType) == "automation"
	execution, err := createDAGEffectiveExecution(node)
	if err != nil {
		return nil, err
	}
	if hasExplicitRawJSON(node.Config) {
		if isAutomation && commandRef != "" {
			return mergeAutomationCommandRef(node.Config, commandRef)
		}
		return append(json.RawMessage(nil), node.Config...), nil
	}
	config := nodeConfig(execution)
	if isAutomation && commandRef != "" {
		config, err = upsertAutomationCommandRef(config, commandRef)
		if err != nil {
			return nil, err
		}
	}
	return encodeJSONRaw(config)
}

func mergeAutomationCommandRef(raw json.RawMessage, commandRef string) (json.RawMessage, error) {
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("decode node config for command_ref merge: %w", err)
	}
	var err error
	config, err = upsertAutomationCommandRef(config, commandRef)
	if err != nil {
		return nil, err
	}
	return encodeJSONRaw(config)
}

// upsertAutomationCommandRef 处理upsertautomation命令引用。
func upsertAutomationCommandRef(config map[string]any, commandRef string) (map[string]any, error) {
	if config == nil {
		config = make(map[string]any)
	}
	execRaw, hasExec := config["exec"]
	exec, _ := execRaw.(map[string]any)
	if hasExec && exec == nil {
		return nil, fmt.Errorf("node config exec must be an object when command_ref is set")
	}
	if exec == nil {
		exec = make(map[string]any)
		config["exec"] = exec
	}
	kind := strings.TrimSpace(stringValue(exec["kind"]))
	if kind == "" {
		exec["kind"] = "command_card"
	} else if kind != "command_card" {
		return nil, fmt.Errorf("node config exec.kind %q conflicts with command_ref shortcut", kind)
	}
	existingCommandRef := strings.TrimSpace(stringValue(exec["command_ref"]))
	if existingCommandRef == "" {
		exec["command_ref"] = commandRef
	} else if existingCommandRef != commandRef {
		return nil, fmt.Errorf("node config exec.command_ref %q conflicts with command_ref %q", existingCommandRef, commandRef)
	}
	return config, nil
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func hasExplicitRawJSON(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// createDAGEffectiveSchedule 创建DAGeffective计划。
func createDAGEffectiveSchedule(in CreateDAGInput) (DAGScheduleInput, error) {
	schedule := in.Schedule
	if err := mergeScheduleString(&schedule.Trigger, in.Trigger, "trigger"); err != nil {
		return DAGScheduleInput{}, err
	}
	if err := mergeScheduleInt(&schedule.DefaultRetry, in.DefaultRetry, "default_retry"); err != nil {
		return DAGScheduleInput{}, err
	}
	if err := mergeScheduleInt(&schedule.DefaultTimeoutSec, in.DefaultTimeoutSec, "default_timeout_sec"); err != nil {
		return DAGScheduleInput{}, err
	}
	if in.FailFast {
		schedule.FailFast = true
	}
	if err := mergeScheduleInt(&schedule.MaxConcurrency, in.MaxConcurrency, "max_concurrency"); err != nil {
		return DAGScheduleInput{}, err
	}
	if err := mergeScheduleString(&schedule.QueuePolicy, in.QueuePolicy, "queue_policy"); err != nil {
		return DAGScheduleInput{}, err
	}
	return schedule, nil
}

// createDAGEffectiveExecution 创建DAGeffectiveexecution。
func createDAGEffectiveExecution(node CreateDAGNodeInput) (*DAGExecutionInput, error) {
	if node.Execution == nil {
		if !hasFlatExecutionFields(node) {
			return nil, nil
		}
		return &DAGExecutionInput{
			OnFailure:  strings.TrimSpace(node.OnFailure),
			Pool:       strings.TrimSpace(node.Pool),
			Priority:   node.Priority,
			Retry:      node.Retry,
			TimeoutSec: node.TimeoutSec,
		}, nil
	}
	execution := *node.Execution
	if err := mergeExecutionString(&execution.OnFailure, node.OnFailure, "on_failure"); err != nil {
		return nil, err
	}
	if err := mergeExecutionString(&execution.Pool, node.Pool, "pool"); err != nil {
		return nil, err
	}
	if err := mergeScheduleInt(&execution.Priority, node.Priority, "priority"); err != nil {
		return nil, err
	}
	if err := mergeScheduleInt(&execution.Retry, node.Retry, "retry"); err != nil {
		return nil, err
	}
	if err := mergeScheduleInt(&execution.TimeoutSec, node.TimeoutSec, "timeout_sec"); err != nil {
		return nil, err
	}
	return &execution, nil
}

func hasFlatExecutionFields(node CreateDAGNodeInput) bool {
	return strings.TrimSpace(node.OnFailure) != "" ||
		strings.TrimSpace(node.Pool) != "" ||
		node.Priority != 0 ||
		node.Retry != 0 ||
		node.TimeoutSec != 0
}

func mergeScheduleString(dst *string, flatValue, field string) error {
	flat := strings.TrimSpace(flatValue)
	if flat == "" {
		return nil
	}
	nested := strings.TrimSpace(*dst)
	if nested != "" && nested != flat {
		return fmt.Errorf("%s conflicts with schedule.%s", field, field)
	}
	*dst = flat
	return nil
}

func mergeExecutionString(dst *string, flatValue, field string) error {
	flat := strings.TrimSpace(flatValue)
	if flat == "" {
		return nil
	}
	nested := strings.TrimSpace(*dst)
	if nested != "" && nested != flat {
		return fmt.Errorf("%s conflicts with execution.%s", field, field)
	}
	*dst = flat
	return nil
}

func mergeScheduleInt(dst *int, flatValue int, field string) error {
	if flatValue == 0 {
		return nil
	}
	if *dst != 0 && *dst != flatValue {
		return fmt.Errorf("%s conflicts with nested %s", field, field)
	}
	*dst = flatValue
	return nil
}

func nodeConfig(execution *DAGExecutionInput) map[string]any {
	if execution == nil {
		return nil
	}
	return map[string]any{"execution": executionMap(*execution)}
}

// scheduleMap 安排map。
func scheduleMap(in DAGScheduleInput) map[string]any {
	payload := make(map[string]any)
	if in.Trigger != "" {
		payload["trigger"] = strings.TrimSpace(in.Trigger)
	}
	if in.DefaultRetry != 0 {
		payload["default_retry"] = in.DefaultRetry
	}
	if in.DefaultTimeoutSec != 0 {
		payload["default_timeout_sec"] = in.DefaultTimeoutSec
	}
	if in.FailFast {
		payload["fail_fast"] = true
	}
	if in.MaxConcurrency != 0 {
		payload["max_concurrency"] = in.MaxConcurrency
	}
	if in.QueuePolicy != "" {
		payload["queue_policy"] = strings.TrimSpace(in.QueuePolicy)
	}
	return payload
}

// executionMap 处理executionmap。
func executionMap(in DAGExecutionInput) map[string]any {
	payload := make(map[string]any)
	if in.OnFailure != "" {
		payload["on_failure"] = strings.TrimSpace(in.OnFailure)
	}
	if in.Pool != "" {
		payload["pool"] = strings.TrimSpace(in.Pool)
	}
	if in.Priority != 0 {
		payload["priority"] = in.Priority
	}
	if in.Retry != 0 {
		payload["retry"] = in.Retry
	}
	if in.TimeoutSec != 0 {
		payload["timeout_sec"] = in.TimeoutSec
	}
	return payload
}
