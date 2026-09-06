package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

// errLegacyDirectPeerUnavailable marks direct-peer capabilities intentionally
// disabled on transports without a server-owned caller attribution binder.
var errLegacyDirectPeerUnavailable = errors.New("legacy direct-peer capability unavailable")

// legacyToolProvider narrows legacy stdio, peer HTTP, and bootstrap tool
// surfaces. ToolScope metadata is client-controlled on these paths and cannot
// establish an operator or task principal, so every direct-peer tool is
// hidden and rejected before registry handlers run.
type legacyToolProvider struct {
	base registryToolProvider
}

// ListTools 返回 legacy transport 的收窄工具面，隐藏所有 direct-peer 能力。
func (p legacyToolProvider) ListTools(ctx context.Context) ([]mcpdto.MCPTool, error) {
	listed, err := p.base.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	filtered := listed[:0]
	for _, tool := range listed {
		if tools.IsLegacyDirectPeerTool(tool.Name) {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered, nil
}

// CallTool 在进入 registry handler 前阻断 legacy transport 的 direct-peer 调用。
func (p legacyToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	if tools.IsLegacyDirectPeerTool(name) {
		return nil, fmt.Errorf("tool %q is disabled on legacy transport: %w", strings.TrimSpace(name), errLegacyDirectPeerUnavailable)
	}
	return p.base.CallTool(ctx, name, args)
}
