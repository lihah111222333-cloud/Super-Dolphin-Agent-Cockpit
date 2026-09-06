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
