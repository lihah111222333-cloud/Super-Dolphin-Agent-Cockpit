package toolsurface

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
)

// PrepareFunc 按本轮 provider session scope 准备 Codex dynamic tools。
// 实现方负责绑定 MCP manifest 和 provider thread 边界，失败应阻断 turn/start。
type PrepareFunc func(context.Context, contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error)

// ListFunc 提供旧路径的全量 dynamic tools 列表。
// 仅在未注入 PrepareFunc 时使用，避免会话级 scope 还未接入的调用方静默丢工具。
type ListFunc func(context.Context) ([]codexprotocol.DynamicToolSchema, error)

// TurnInput 汇总 Codex turn/start 准备 dynamicTools 所需的 provider 侧上下文。
// ProviderThreadID、CWD 和 manifest 共同限定工具绑定范围，缺失时应 fail-fast。
type TurnInput struct {
	Enabled          bool
	AgentID          string
	UIThreadID       string
	LocalThreadID    string
	ProviderThreadID string
	SurfaceID        string
	CWD              string
	WorkspaceRoots   []string
	Manifest         dto.MCPManifest
	Prepare          PrepareFunc
	List             ListFunc
}

// PrepareTurn 用本轮 MCP manifest 准备并返回要发给 Codex turn/start 的 dynamicTools。
func PrepareTurn(ctx context.Context, input TurnInput) ([]codexprotocol.DynamicToolSchema, error) {
	if !input.Enabled {
		return nil, nil
	}
	if input.Prepare == nil {
		if input.List != nil {
			return input.List(ctx)
		}
		return nil, nil
	}
	if len(input.Manifest.Binaries) == 0 {
		return nil, nil
	}
	scope, err := surfaceScope(input)
	if err != nil {
		return nil, err
	}
	return input.Prepare(ctx, scope)
}

func surfaceScope(input TurnInput) (contract.CodexToolSurfaceScope, error) {
	if strings.TrimSpace(input.CWD) == "" {
		return contract.CodexToolSurfaceScope{}, errors.New("codexapp: turn dynamic tools cwd is required")
	}
	if strings.TrimSpace(input.ProviderThreadID) == "" {
		return contract.CodexToolSurfaceScope{}, errors.New("codexapp: turn dynamic tools provider thread id is required")
	}
	return contract.CodexToolSurfaceScope{
		SurfaceID:        strings.TrimSpace(input.SurfaceID),
		AgentID:          strings.TrimSpace(input.AgentID),
		UIThreadID:       strings.TrimSpace(input.UIThreadID),
		LocalThreadID:    strings.TrimSpace(input.LocalThreadID),
		ProviderThreadID: strings.TrimSpace(input.ProviderThreadID),
		CWD:              strings.TrimSpace(input.CWD),
		WorkspaceRoots:   append([]string(nil), input.WorkspaceRoots...),
		Manifest:         input.Manifest,
	}, nil
}
