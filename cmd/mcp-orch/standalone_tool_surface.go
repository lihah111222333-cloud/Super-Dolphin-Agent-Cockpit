package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// errStandaloneTaskCapabilityUnavailable marks a task-scoped tool that is
// reached through the standalone operator profile without task attribution.
// It is deliberately transport-local; it does not mint or infer a child task.
var errStandaloneTaskCapabilityUnavailable = errors.New("standalone task capability unavailable")

// standaloneHTTPToolProvider narrows only the standalone HTTP projection. The
// shared Registry and legacy stdio/HTTP providers remain unchanged.
type standaloneHTTPToolProvider struct {
	base registryToolProvider
}

// ListTools 根据服务端绑定的 task principal/attribution 收窄 standalone 工具列表，
// 未绑定任务身份的 operator session 不发布 task-scoped direct-peer 工具。
func (p standaloneHTTPToolProvider) ListTools(ctx context.Context) ([]mcpdto.MCPTool, error) {
	listed, err := p.base.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	for i := range listed {
		if listed[i].Name == "launch_agent" {
			if err := requireStandaloneLaunchModel(&listed[i]); err != nil {
				return nil, err
			}
		}
	}
	if standaloneTaskAttributionAvailable(ctx) {
		return listed, nil
	}
	filtered := listed[:0]
	for _, tool := range listed {
		if !tools.IsTaskScopedDirectPeerTool(tool.Name) {
			filtered = append(filtered, tool)
		}
	}
	return filtered, nil
}

// CallTool 在进入 registry handler 前拒绝隐藏的 task-scoped 工具，
// 保证 tools/list 与 tools/call 一致并阻断调用方猜测任务 scope。
func (p standaloneHTTPToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	if strings.TrimSpace(name) == "launch_agent" {
		if err := validateStandaloneLaunchModel(args); err != nil {
			return nil, err
		}
	}
	if tools.IsTaskScopedDirectPeerTool(name) && !standaloneTaskAttributionAvailable(ctx) {
		return nil, fmt.Errorf("tool %q requires task attribution: %w", strings.TrimSpace(name), errStandaloneTaskCapabilityUnavailable)
	}
	return p.base.CallTool(ctx, name, args)
}

func standaloneTaskAttributionAvailable(ctx context.Context) bool {
	if _, err := common.RequireCallerPrincipal(ctx); err != nil {
		return false
	}
	if _, err := common.RequireCallerAttribution(ctx); err != nil {
		return false
	}
	return true
}

// requireStandaloneLaunchModel 复制公开 schema 并表达 standalone 真实显式模型约束，不修改共享 registry。
func requireStandaloneLaunchModel(tool *mcpdto.MCPTool) error {
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		return fmt.Errorf("standalone launch schema: %w", err)
	}
	var required []string
	if err := json.Unmarshal(schema["required"], &required); err != nil {
		return fmt.Errorf("standalone launch required fields: %w", err)
	}
	required = append(required, "model")
	raw, err := json.Marshal(required)
	if err != nil {
		return fmt.Errorf("standalone launch required fields: %w", err)
	}
	schema["required"] = raw
	if err := restrictStandaloneLaunchModelField(schema); err != nil {
		return err
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("standalone launch schema: %w", err)
	}
	tool.InputSchema = encoded
	tool.Description = "Launch a managed standalone agent asynchronously with name, prompt, absolute cwd and an explicit model. Standalone requires explicit model; choose from list_models(provider=codex). See inputSchema field groups and examples for context and expert selection. Success confirms launch acceptance; keep agent_id and wait for the first report with get_agent_report(wait=true, after_report_seq=0). Reuse agent_id to retry the same launch; use send_message for a new turn."
	return nil
}

// validateStandaloneLaunchModel 在基础 handler 分配 agent 状态之前拒绝缺失、空白或非字符串模型。
func validateStandaloneLaunchModel(args json.RawMessage) error {
	var input struct {
		Model string `json:"model"`
	}
	err := json.Unmarshal(args, &input)
	if err == nil && strings.TrimSpace(input.Model) == "" {
		err = errors.New("standalone launch requires a non-empty model")
	}
	if err != nil {
		return common.NewCodedToolError("invalid_arguments", err, false, "next: choose an explicit model from list_models(provider=codex); no action was submitted")
	}
	return nil
}

// restrictStandaloneLaunchModelField 对局部 JSON 副本增加非空约束，和调用前置校验保持一致。
func restrictStandaloneLaunchModelField(schema map[string]json.RawMessage) error {
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(schema["properties"], &properties); err != nil {
		return fmt.Errorf("standalone launch properties: %w", err)
	}
	var model map[string]any
	if err := json.Unmarshal(properties["model"], &model); err != nil {
		return fmt.Errorf("standalone launch model schema: %w", err)
	}
	if model == nil {
		return errors.New("standalone launch model schema is missing")
	}
	model["pattern"] = `\S`
	raw, err := json.Marshal(model)
	if err != nil {
		return fmt.Errorf("standalone launch model schema: %w", err)
	}
	properties["model"] = raw
	raw, err = json.Marshal(properties)
	if err != nil {
		return fmt.Errorf("standalone launch properties: %w", err)
	}
	schema["properties"] = raw
	return nil
}
