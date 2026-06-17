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

// toolbridgeAdaptersModule provides the fx wiring that bridges concrete store,
// module, and provider types into toolbridge's narrow contract ports.
// This keeps internal/platform/toolbridge free of module/provider/store
// imports while the assembly seam (this file) legitimately knows all layers.
func toolbridgeAdaptersModule() fx.Option {
	return fx.Provide(
		provideToolbridgeAgentThreadLookup,
		provideToolbridgeThreadConfigOverrideStore,
		provideToolbridgeUIPreferenceReader,
		provideToolbridgeWorkDirResolver,
	)
}

// toolbridgeCodexBindingModule wires the codex handler binding as an fx.Invoke.
func toolbridgeCodexBindingModule() fx.Option {
	return fx.Invoke(bindToolbridgeCodexHandlers)
}

// ---------------------------------------------------------------------------
// Store adapters: binding → AgentThreadLookup
// ---------------------------------------------------------------------------

type agentThreadLookupAdapter struct {
	inner bindingstore.Store
}

// GetThreadByAgent 按代理读取线程。
func (a agentThreadLookupAdapter) GetThreadByAgent(ctx context.Context, agentID string) (string, error) {
	if a.inner == nil {
		return "", nil
	}
	return a.inner.GetThreadByAgent(ctx, agentID)
}

// GetBindingByAgent 按代理读取binding。
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

// GetBindingByProviderThread 按provider线程读取binding。
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

func provideToolbridgeAgentThreadLookup(store bindingstore.Store) toolbridge.AgentThreadLookup {
	if store == nil {
		return nil
	}
	return agentThreadLookupAdapter{inner: store}
}

// ---------------------------------------------------------------------------
// Store adapters: thread → ThreadConfigOverrideStore
// ---------------------------------------------------------------------------

type threadConfigOverrideAdapter struct {
	inner threadstore.Store
}

// GetConfigOverride 读取配置override。
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

func provideToolbridgeThreadConfigOverrideStore(store threadstore.Store) toolbridge.ThreadConfigOverrideStore {
	if store == nil {
		return nil
	}
	return threadConfigOverrideAdapter{inner: store}
}

// ---------------------------------------------------------------------------
// Store adapters: uipreference → UIPreferenceReader
// ---------------------------------------------------------------------------

type uiPreferenceReaderAdapter struct {
	inner uipreferencestore.Store
}

// GetMergedPreferences 读取mergedpreferences。
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

func decodeToolbridgeAdapterPreferenceValue(raw json.RawMessage) any {
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return strings.TrimSpace(string(raw))
}

func provideToolbridgeUIPreferenceReader(store uipreferencestore.Store) toolbridge.UIPreferenceReader {
	if store == nil {
		return nil
	}
	return uiPreferenceReaderAdapter{inner: store}
}

// ---------------------------------------------------------------------------
// WorkDirResolver adapter (binding store → difftracker.WorkDirResolver)
// ---------------------------------------------------------------------------

type toolbridgeResolverFunc func(context.Context, string) (string, error)

// ResolveAgentCWD 解析代理工作目录。
func (fn toolbridgeResolverFunc) ResolveAgentCWD(ctx context.Context, agentID string) (string, error) {
	return fn(ctx, agentID)
}

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

// ---------------------------------------------------------------------------
// Codex handler binding (type-conversion wrappers)
// ---------------------------------------------------------------------------

type codexBindingParams struct {
	fx.In

	Manager *codexapp.ServerManager `optional:"true"`
	Factory *codexapp.DriverFactory `optional:"true"`
	Handler *toolbridge.Handler     `optional:"true"`
}

// bindToolbridgeCodexHandlers 绑定toolbridgecodex处理器。
func bindToolbridgeCodexHandlers(p codexBindingParams) {
	if p.Manager == nil || p.Factory == nil || p.Handler == nil {
		return
	}
	// Adapt contract.ToolCallRawMessage ↔ codexapp.RawMessage
	p.Manager.SetToolHandler(func(ctx context.Context, msg codexapp.RawMessage) (any, error) {
		return p.Handler.HandleToolCall(ctx, contract.ToolCallRawMessage{
			ID:     msg.ID,
			Method: msg.Method,
			Params: msg.Params,
		})
	})
	// Adapt scoped []contract.DynamicToolSchema → []codexprotocol.DynamicToolSchema.
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
