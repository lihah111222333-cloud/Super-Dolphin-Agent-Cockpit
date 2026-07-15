package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// applyOpsOpEnum 是 raw ops schema 接受的 nodeexec 操作集合。
// 只暴露已实现的模板编辑能力，保留能力必须先完成运行时闭环再加入。
var applyOpsOpEnum = []string{
	string(nodeexec.OpKindUpdateDAG),
	string(nodeexec.OpKindAddNode),
	string(nodeexec.OpKindUpdateNode),
	string(nodeexec.OpKindRemoveNode),
}

// creatableNodeTypeEnum 限制创建入口能落库的节点类型。
// hybrid 暂不放开，避免用户创建出调度器无法稳定执行的 DAG。
var creatableNodeTypeEnum = []string{"agent", "automation"}

// applyOpsOpSchema 构建 task_dag_apply_ops raw ops 的 schema。
// schema 与 nodeexec.OpKind 共用枚举，保持工具描述和解码器一致。
func applyOpsOpSchema() Schema {
	return ObjectSchema(map[string]Schema{
		"op": EnumStringSchema("Operation discriminator.", applyOpsOpEnum...),
		"node": ObjectSchema(map[string]Schema{
			"node_key":    StringSchema("Node key for add_node."),
			"title":       StringSchema("Node title for add_node."),
			"node_type":   EnumStringSchema("Node type for add_node. Hybrid is reserved until runtime support is complete.", creatableNodeTypeEnum...),
			"assigned_to": StringSchema("Optional assignee for add_node."),
			"depends_on":  ArraySchema(StringSchema("Dependency node key."), "Dependency node keys."),
			"reads":       ArraySchema(StringSchema("Readable artifact or node output reference."), "Node read dependencies persisted for workflow inspection."),
			"writes":      ArraySchema(StringSchema("Writable artifact or node output reference."), "Node write targets persisted for workflow inspection."),
			"config":      RawObjectSchema("Optional node config for add_node."),
		}, "node_key", "title", "node_type"),
		"node_key": StringSchema("Target node key for update_node/remove_node."),
		"patch":    RawObjectSchema("Patch object for update_dag or update_node."),
	}, "op")
}

// createDAGSchema 构建 task_create_dag 入参 schema。
// 创建入口只接受 create-only 字段；定时触发和并发版本写入必须走 apply_ops。
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
			"node_type":   EnumStringSchema("Optional node type. Hybrid is reserved until runtime support is complete.", creatableNodeTypeEnum...),
			"assigned_to": StringSchema("Optional assignee."),
			"depends_on":  ArraySchema(StringSchema("Dependency node key."), "Node dependency keys."),
			"reads":       ArraySchema(StringSchema("Readable artifact or node output reference."), "Node read dependencies persisted for workflow inspection."),
			"writes":      ArraySchema(StringSchema("Writable artifact or node output reference."), "Node write targets persisted for workflow inspection."),
			"command_ref": StringSchema("Optional command card key."),
			"retry":       IntegerSchema("Flat execution shortcut; same as execution.retry."),
			"timeout_sec": IntegerSchema("Flat execution shortcut; same as execution.timeout_sec."),
			"config": RawObjectSchema(
				"Optional full node config. Executable agent nodes require config.exec, non-empty config.first_turn, " +
					"and provider=claude or provider=codex with codex_home/codex_instance_key/codex_model_provider. " +
					"Use top-level config.outputs for output routing; do not use legacy config.input, " +
					"config.input.task, config.input.outputs, config.output_file, config.prompt_key, " +
					"config.provider, config.model, or config.cwd.",
			),
			"execution": ObjectSchema(map[string]Schema{
				"retry":       IntegerSchema("Retry override."),
				"timeout_sec": IntegerSchema("Timeout override in seconds."),
			}),
		}, "node_key", "title"), "Optional DAG nodes."),
	}, "dag_key", "title")
}

// createDAGRequestFromInput 将工具入参转换为服务层 DAG 创建请求。
// 所有会被 JSON 解码静默丢弃的旧字段都在这里 fail-fast，避免坏 DAG 落库。
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

// resolveCreateDAGAgentID 统一公开 agent_id 与可信工具作用域中的 agent ID。
// 两者冲突时拒绝请求，避免调用方伪造创建者身份。
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

// createDAGInvalidInputError 为创建入口封装带 hint 的 invalid_input 错误。
func createDAGInvalidInputError(err error, hint string) error {
	return mcpcommon.NewCodedToolError("invalid_input", err, false, hint)
}

// rejectScheduledCreateDAGSchedule 拒绝在 create 阶段直接启用 scheduled。
// cron_expr 需要 base_version 保护，所以必须在创建后通过 apply_ops 显式开启。
func rejectScheduledCreateDAGSchedule(schedule DAGScheduleInput) error {
	if strings.TrimSpace(schedule.Trigger) != "scheduled" {
		return nil
	}
	return createDAGInvalidInputError(
		fmt.Errorf("schedule.trigger=scheduled requires task_dag_apply_ops with trigger and cron_expr; task_create_dag is create-only"),
		"Use task_dag_apply_ops with base_version plus trigger=scheduled and cron_expr after creating the DAG.",
	)
}

// validateCreateDAGNodesForCreate 汇总创建期节点校验。
// 先检查 root agent 可调度性，再检查 executable agent 的启动配置。
func validateCreateDAGNodesForCreate(nodes []contract.CreateDAGNodeRequest) error {
	if err := validateRootAgentAssignees(nodes); err != nil {
		return err
	}
	if err := validateAgentNodeLaunchConfigs(nodes); err != nil {
		return err
	}
	return nodeexec.ValidateNodeSpecsConfig(createDAGNodeSpecs(nodes))
}

func createDAGNodeSpecs(nodes []contract.CreateDAGNodeRequest) []nodeexec.NodeSpec {
	specs := make([]nodeexec.NodeSpec, 0, len(nodes))
	for _, node := range nodes {
		specs = append(specs, nodeexec.NodeSpec{
			NodeKey: node.NodeKey, NodeType: node.NodeType, Config: node.Config,
		})
	}
	return specs
}

// validateRootAgentAssignees 要求可直接启动的 root agent 已绑定 assigned_to。
// task_start_dag 不会替用户静默派发 root 节点，缺人时必须在创建前暴露出来。
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

// validateAgentNodeLaunchConfigs 校验创建阶段可自动拉起的 agent 配置。
// agent 显式 config 先做 raw shape 校验；exec 身份和 first_turn 只约束真正可执行的 agent 节点。
// hybrid 在创建入口已被拒绝，避免未闭环运行时能力落库成可执行 DAG。
func validateAgentNodeLaunchConfigs(nodes []contract.CreateDAGNodeRequest) error {
	for i, node := range nodes {
		switch strings.TrimSpace(node.NodeType) {
		case "", "agent":
			if hasExplicitRawJSON(node.Config) {
				configLabel := fmt.Sprintf("nodes[%d].config", i)
				if err := validateAgentConfigShape(node.Config, configLabel, node.NodeKey); err != nil {
					return err
				}
			}
			if !hasNodeExecConfig(node.Config) {
				continue
			}
			if err := validateExecutableAgentNodeLaunchConfig(i, node); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateExecutableAgentNodeLaunchConfig 校验可执行 agent 节点的创建期配置。
// raw shape 已在外层先查；这里只校验 typed exec 与 first_turn，避免非执行占位节点被误杀。
func validateExecutableAgentNodeLaunchConfig(i int, node contract.CreateDAGNodeRequest) error {
	configLabel := fmt.Sprintf("nodes[%d].config", i)
	cfg, err := nodeexec.ParseAgentConfig(node.Config)
	if err != nil {
		return fmt.Errorf("nodes[%d].config: %w", i, err)
	}
	label := configLabel + ".exec"
	if err := validateAgentExecLaunchConfig(cfg.Exec, label, node.NodeKey); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.FirstTurn) == "" {
		return fmt.Errorf("%s.first_turn required for executable agent node %q", configLabel, node.NodeKey)
	}
	return nil
}

// validateAgentConfigShape 拦截旧版或错位 agent config 字段。
// 这些字段会被 typed JSON 解码静默丢弃，必须在创建阶段报错，避免坏 DAG 落库后空跑。
func validateAgentConfigShape(raw json.RawMessage, label, nodeKey string) error {
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("%s must be an object for executable agent node %q: %w", label, nodeKey, err)
	}
	invalidPaths := legacyAgentConfigPaths(config, label)
	if len(invalidPaths) == 0 {
		return nil
	}
	return fmt.Errorf(
		"unsupported legacy agent config fields for node %q: %s; use %s.first_turn, %s.outputs, and %s.exec",
		nodeKey, strings.Join(invalidPaths, ", "), label, label, label,
	)
}

// legacyAgentConfigPaths 收集最常见的旧 schema 路径。
// 返回字段路径而不是布尔值，方便错误直接指出模型应该修正的具体位置。
func legacyAgentConfigPaths(config map[string]json.RawMessage, label string) []string {
	var paths []string
	if rawInput, ok := config["input"]; ok {
		paths = append(paths, label+".input")
		var input map[string]json.RawMessage
		if err := json.Unmarshal(rawInput, &input); err == nil {
			if _, ok := input["task"]; ok {
				paths = append(paths, label+".input.task")
			}
			if _, ok := input["outputs"]; ok {
				paths = append(paths, label+".input.outputs")
			}
		}
	}
	for _, field := range []string{"output_file", "prompt_key", "provider", "model", "cwd"} {
		if _, ok := config[field]; ok {
			paths = append(paths, label+"."+field)
		}
	}
	return paths
}

// validateAgentExecLaunchConfig 校验子 agent 启动所需的执行模板和运行身份。
// 创建入口必须 fail-fast，不在这里补默认 prompt；缺字段说明上游资源选择链路断开。
func validateAgentExecLaunchConfig(exec nodeexec.AgentExecConfig, label, nodeKey string) error {
	if strings.TrimSpace(exec.PromptKey) == "" && strings.TrimSpace(exec.AgentKey) == "" {
		return fmt.Errorf("%s.prompt_key or %s.agent_key required for agent node %q", label, label, nodeKey)
	}
	provider := strings.ToLower(strings.TrimSpace(exec.Provider))
	switch provider {
	case "":
		return fmt.Errorf("%s.provider required for agent node %q; set provider to claude or codex", label, nodeKey)
	case "claude":
		return nil
	case "codex":
		if missing := missingCodexIdentityFields(exec); len(missing) != 0 {
			return fmt.Errorf("%s provider=codex for agent node %q requires %s", label, nodeKey, strings.Join(missing, ", "))
		}
		return nil
	default:
		return fmt.Errorf("%s.provider invalid for agent node %q: must be claude or codex", label, nodeKey)
	}
}

// missingCodexIdentityFields 返回 Codex agent 启动身份缺失项。
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

// isRunnableRootAgentNode 判断节点是否会在 DAG 启动时立即进入调度。
func isRunnableRootAgentNode(node contract.CreateDAGNodeRequest) bool {
	nodeType := strings.TrimSpace(node.NodeType)
	return (nodeType == "" || nodeType == "agent") && len(node.DependsOn) == 0 && hasNodeExecConfig(node.Config)
}

// hasNodeExecConfig 只判断 config.exec 是否显式存在。
// 解析失败时返回 false，真正的 shape 错误由 validateAgentConfigShape 报出。
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

// createDAGNodesFromInput 将工具节点数组映射为服务层节点请求。
// 节点类型、配置和拓扑在这里集中校验，避免服务层接收半规范化输入。
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
		if err := validateCreatableNodeType(fmt.Sprintf("nodes[%d].node_type", i), nodeType); err != nil {
			return nil, err
		}
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
			Reads:      append([]string(nil), node.Reads...),
			Writes:     append([]string(nil), node.Writes...),
			CommandRef: strings.TrimSpace(node.CommandRef),
			Config:     config,
		})
	}
	if err := validateCreateDAGNodeTopology(mapped); err != nil {
		return nil, err
	}
	return mapped, nil
}

// validateCreatableNodeType 拦截创建和 ops 入口尚不支持的节点类型。
func validateCreatableNodeType(label, nodeType string) error {
	switch strings.TrimSpace(nodeType) {
	case "", "agent", "automation":
		return nil
	case "hybrid":
		return fmt.Errorf("%s hybrid is reserved until hybrid runtime lifecycle is implemented; use agent or automation", label)
	default:
		return fmt.Errorf("%s %q is unsupported; allowed: agent, automation", label, strings.TrimSpace(nodeType))
	}
}

// createDAGNodeType 推断未显式声明的 automation 节点。
// 只有 command_ref 快捷字段存在时才推断，普通节点继续保持 agent 默认语义。
func createDAGNodeType(node CreateDAGNodeInput) string {
	nodeType := strings.TrimSpace(node.NodeType)
	if nodeType == "" && strings.TrimSpace(node.CommandRef) != "" {
		return "automation"
	}
	return nodeType
}

// validateCreateDAGNodeTopology 复用 nodeexec 拓扑校验。
// 错误会被包装成带修复提示的 invalid_input，供工具调用方直接调整入参。
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

// createDAGMetadata 只写当前工具入口支持的 DAG metadata 子树。
// 旧 metadata 输入不再进入 schema；存量行已映射到一等字段，调用方继续传旧字段时保持忽略以兼容旧请求。
func createDAGMetadata(schedule DAGScheduleInput, finalNodeKey string) map[string]any {
	metadata := map[string]any{"schedule": scheduleMap(schedule)}
	if finalNodeKey != "" {
		metadata["final_node_key"] = finalNodeKey
	}
	return metadata
}

// normalizeFinalNodeKey 校验 final_node_key 必须指向本次创建的节点。
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

// createDAGNodeConfig 组装节点 config，并合并 automation command_ref 快捷字段。
// 显式 config 优先保留；只有 automation 且 command_ref 存在时才写入 exec.command_ref。
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

// mergeAutomationCommandRef 将 command_ref 快捷字段合并进显式 config。
// 若 config.exec.kind 已不是 command_card，会立即报错避免改写执行器类型。
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

// upsertAutomationCommandRef 写入 automation 的 exec.command_ref。
// 既有 command_ref 与快捷字段冲突时拒绝请求，避免隐式覆盖用户配置。
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

// stringValue 安全读取 map 中的字符串字段。
func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

// hasExplicitRawJSON 判断调用方是否显式提供了非 null JSON。
// 这用于区分“未传字段”和“传了空对象/空数组”两种不同意图。
func hasExplicitRawJSON(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

// createDAGEffectiveSchedule 合并扁平 schedule 快捷字段和嵌套 schedule。
// 同一字段两处给出不同值时立即报错，不设置隐藏优先级。
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
	if schedule.DefaultRetry < 0 {
		return DAGScheduleInput{}, fmt.Errorf("default_retry must be non-negative, got %d", schedule.DefaultRetry)
	}
	return schedule, nil
}

// createDAGEffectiveExecution 合并节点 execution 快捷字段和嵌套 execution。
// 没有任何 execution 字段时返回 nil，避免写入空配置噪音。
func createDAGEffectiveExecution(node CreateDAGNodeInput) (*DAGExecutionInput, error) {
	if node.Execution == nil {
		if !hasFlatExecutionFields(node) {
			return nil, nil
		}
		if node.Retry < 0 {
			return nil, fmt.Errorf("retry must be non-negative, got %d", node.Retry)
		}
		return &DAGExecutionInput{
			Retry:      node.Retry,
			TimeoutSec: node.TimeoutSec,
		}, nil
	}
	execution := *node.Execution
	if err := mergeScheduleInt(&execution.Retry, node.Retry, "retry"); err != nil {
		return nil, err
	}
	if err := mergeScheduleInt(&execution.TimeoutSec, node.TimeoutSec, "timeout_sec"); err != nil {
		return nil, err
	}
	if execution.Retry < 0 {
		return nil, fmt.Errorf("retry must be non-negative, got %d", execution.Retry)
	}
	return &execution, nil
}

// hasFlatExecutionFields 判断节点是否使用了任一扁平 execution 字段。
func hasFlatExecutionFields(node CreateDAGNodeInput) bool {
	return node.Retry != 0 || node.TimeoutSec != 0
}

// mergeScheduleString 合并扁平字符串 schedule 字段。
// 嵌套值与扁平值冲突时拒绝，防止工具调用方误以为某一边会优先生效。
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

// mergeScheduleInt 合并扁平整数快捷字段。
// 0 表示未传值，因此需要表达 0 的新语义时必须先调整入参模型。
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

// nodeConfig 根据 execution 构造最小节点 config。
func nodeConfig(execution *DAGExecutionInput) map[string]any {
	if execution == nil {
		return nil
	}
	return map[string]any{"execution": executionMap(*execution)}
}

// scheduleMap 将 schedule 输入压缩成 metadata 中的非零字段。
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

// executionMap 将 execution 输入压缩成 config 中的非零字段。
func executionMap(in DAGExecutionInput) map[string]any {
	payload := make(map[string]any)
	if in.Retry != 0 {
		payload["retry"] = in.Retry
	}
	if in.TimeoutSec != 0 {
		payload["timeout_sec"] = in.TimeoutSec
	}
	return payload
}
