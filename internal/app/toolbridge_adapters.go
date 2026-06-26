package app

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	uipreferencestore "github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	"go.uber.org/fx"
)

// toolbridgeAdaptersModule 将具体 store/module/provider 实现接到 toolbridge 的窄接口端口。
// toolbridge 平台包不直接导入业务层，只有 app assembly 这一层知道各模块的具体类型。
func toolbridgeAdaptersModule() fx.Option {
	return fx.Provide(
		provideToolbridgeAgentThreadLookup,
		provideToolbridgeThreadConfigOverrideStore,
		provideToolbridgeUIPreferenceReader,
		provideToolbridgeWorkDirResolver,
	)
}

// toolbridgeCodexBindingModule 以 fx.Invoke 形式接入 Codex tool handler 绑定。
func toolbridgeCodexBindingModule() fx.Option {
	return fx.Invoke(bindToolbridgeCodexHandlers)
}

// ----- binding store 适配器 -----

type agentThreadLookupAdapter struct {
	inner bindingstore.Store
}

// GetThreadByAgent 通过 binding store 查询 agent 对应的 thread。
// store 未注入时返回空结果，让可选 toolbridge 能在测试装配中保持 no-op。
func (a agentThreadLookupAdapter) GetThreadByAgent(ctx context.Context, agentID string) (string, error) {
	if a.inner == nil {
		return "", nil
	}
	return a.inner.GetThreadByAgent(ctx, agentID)
}

// GetBindingByAgent 读取 agent 的 tool call binding 并转换成 toolbridge wire 结构。
func (a agentThreadLookupAdapter) GetBindingByAgent(ctx context.Context, agentID string) (toolbridge.ToolCallBinding, error) {
	if a.inner == nil {
		return toolbridge.ToolCallBinding{}, nil
	}
	binding, err := a.inner.GetByAgentID(ctx, agentID)
	if err != nil || binding == nil {
		return toolbridge.ToolCallBinding{}, err
	}
	return toolCallBindingFromStore(binding), nil
}

// GetBindingByProviderThread 通过 provider/thread id 反查 tool call binding。
func (a agentThreadLookupAdapter) GetBindingByProviderThread(ctx context.Context, provider, providerThreadID string) (toolbridge.ToolCallBinding, error) {
	if a.inner == nil {
		return toolbridge.ToolCallBinding{}, nil
	}
	binding, err := a.inner.GetByProviderThread(ctx, provider, providerThreadID)
	if err != nil || binding == nil {
		return toolbridge.ToolCallBinding{}, err
	}
	return toolCallBindingFromStore(binding), nil
}

// toolCallBindingFromStore 将 store binding 裁剪成 platform/toolbridge 可见字段。
func toolCallBindingFromStore(binding *bindingstore.Binding) toolbridge.ToolCallBinding {
	if binding == nil {
		return toolbridge.ToolCallBinding{}
	}
	return toolbridge.ToolCallBinding{
		AgentID:            strings.TrimSpace(binding.AgentID),
		Provider:           strings.TrimSpace(binding.Provider),
		ProviderThreadID:   strings.TrimSpace(binding.ProviderThreadID),
		CodexThreadID:      strings.TrimSpace(binding.CodexThreadID),
		CWD:                strings.TrimSpace(binding.Cwd),
		ParentAgentID:      strings.TrimSpace(binding.ParentAgentID),
		CodexHome:          strings.TrimSpace(binding.CodexHome),
		CodexInstanceKey:   strings.TrimSpace(binding.CodexInstanceKey),
		CodexModelProvider: strings.TrimSpace(binding.CodexModelProvider),
	}
}

// provideToolbridgeAgentThreadLookup 在 binding store 存在时提供 AgentThreadLookup 端口。
func provideToolbridgeAgentThreadLookup(store bindingstore.Store) toolbridge.AgentThreadLookup {
	if store == nil {
		return nil
	}
	return agentThreadLookupAdapter{inner: store}
}

// ----- thread store 适配器 -----

type threadConfigOverrideAdapter struct {
	inner threadstore.Store
}

// GetConfigOverride 读取 thread 的 runtime 配置覆盖。
// store 未注入时返回 nil，表示当前装配没有 thread config 覆盖来源。
func (a threadConfigOverrideAdapter) GetConfigOverride(ctx context.Context, threadID string) (json.RawMessage, error) {
	if a.inner == nil {
		return nil, nil
	}
	row, err := a.inner.GetByThreadID(ctx, threadID)
	if err != nil || row == nil {
		return nil, err
	}
	return row.ConfigOverride, nil
}

// provideToolbridgeThreadConfigOverrideStore 在 thread store 存在时提供配置覆盖读取端口。
func provideToolbridgeThreadConfigOverrideStore(store threadstore.Store) toolbridge.ThreadConfigOverrideStore {
	if store == nil {
		return nil
	}
	return threadConfigOverrideAdapter{inner: store}
}

// ----- UI preference store 适配器 -----

type uiPreferenceReaderAdapter struct {
	inner uipreferencestore.Store
}

// GetMergedPreferences 汇总指定 cwd 下的 UI preference。
// 空 key 会被丢弃，JSON 解码失败时保留原始字符串，避免损坏偏好值导致整次读取失败。
func (a uiPreferenceReaderAdapter) GetMergedPreferences(ctx context.Context, cwd string) (map[string]any, error) {
	if a.inner == nil {
		return nil, nil
	}
	rows, err := a.inner.List(ctx, strings.TrimSpace(cwd))
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	for _, row := range rows {
		key := strings.TrimSpace(row.Key)
		if key == "" {
			continue
		}
		values[key] = decodeToolbridgeAdapterPreferenceValue(row.Value)
	}
	return values, nil
}

// decodeToolbridgeAdapterPreferenceValue 将偏好值解成 JSON 值，非 JSON 内容按字符串透传。
func decodeToolbridgeAdapterPreferenceValue(raw json.RawMessage) any {
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return strings.TrimSpace(string(raw))
}

// provideToolbridgeUIPreferenceReader 在 UI preference store 存在时提供读取端口。
func provideToolbridgeUIPreferenceReader(store uipreferencestore.Store) toolbridge.UIPreferenceReader {
	if store == nil {
		return nil
	}
	return uiPreferenceReaderAdapter{inner: store}
}

// ----- 工作目录解析适配器 -----

type toolbridgeResolverFunc func(context.Context, string) (string, error)

// ResolveAgentCWD 通过函数适配器解析 agent 工作目录。
func (fn toolbridgeResolverFunc) ResolveAgentCWD(ctx context.Context, agentID string) (string, error) {
	return fn(ctx, agentID)
}

// provideToolbridgeWorkDirResolver 从 binding store 提供工作目录解析能力。
func provideToolbridgeWorkDirResolver(bindingStore bindingstore.Store) difftracker.WorkDirResolver {
	if bindingStore == nil {
		return nil
	}
	return toolbridgeResolverFunc(func(ctx context.Context, agentID string) (string, error) {
		if strings.TrimSpace(agentID) == "" {
			return "", nil
		}
		binding, err := bindingStore.GetByAgentID(ctx, agentID)
		if err != nil || binding == nil {
			return "", err
		}
		return strings.TrimSpace(binding.Cwd), nil
	})
}

// ----- Codex handler 绑定 -----

type codexBindingParams struct {
	fx.In

	Manager *codexapp.ServerManager `optional:"true"`
	Factory *codexapp.DriverFactory `optional:"true"`
	Handler *toolbridge.Handler     `optional:"true"`
}

// bindToolbridgeCodexHandlers 将 Codex server/driver 的 tool 调用接到 toolbridge handler。
// 任一依赖未注入时保持 no-op，避免非 Codex 装配启动失败。
func bindToolbridgeCodexHandlers(p codexBindingParams) {
	if p.Manager == nil || p.Factory == nil || p.Handler == nil {
		return
	}
	// 只在 assembly 层做 DTO 转换，provider 和 platform/toolbridge 不直接互相导入。
	p.Manager.SetToolHandler(func(ctx context.Context, msg codexapp.RawMessage) (any, error) {
		return p.Handler.HandleToolCall(ctx, contract.ToolCallRawMessage{
			ID:     msg.ID,
			Method: msg.Method,
			Params: msg.Params,
		})
	})
	// Codex protocol 使用自己的 dynamic tool schema，边界处做字段拷贝。
	p.Factory.SetPrepareTools(func(ctx context.Context, scope contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error) {
		tools, err := p.Handler.PrepareCodexToolSurface(ctx, scope)
		if err != nil {
			return nil, err
		}
		out := make([]codexprotocol.DynamicToolSchema, len(tools))
		for i, t := range tools {
			out[i] = codexprotocol.DynamicToolSchema(t)
		}
		return out, nil
	})
	p.Factory.SetBindTools(func(scope contract.CodexToolSurfaceScope) error {
		return p.Handler.BindCodexToolSurface(scope)
	})
	p.Factory.SetReleaseTools(func(scope contract.CodexToolSurfaceScope) error {
		return p.Handler.ReleaseCodexToolSurface(scope)
	})
}
