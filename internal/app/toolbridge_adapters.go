package app

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/fbsd"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	uipreferencestore "github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	"go.uber.org/fx"
)

// ToolbridgeAdapters provides the fx wiring that bridges concrete store,
// module, and provider types into toolbridge's narrow contract ports.
// This keeps internal/platform/toolbridge free of module/provider/store
// imports while the assembly seam (this file) legitimately knows all layers.
var ToolbridgeAdapters = fx.Provide(
	provideToolbridgeSkillReadSectionTool,
	provideToolbridgeAgentThreadLookup,
	provideToolbridgeThreadConfigOverrideStore,
	provideToolbridgeUIPreferenceReader,
	provideToolbridgeWorkDirResolver,
)

// ToolbridgeCodexBinding wires the codex handler binding as an fx.Invoke.
var ToolbridgeCodexBinding = fx.Invoke(bindToolbridgeCodexHandlers)

// ---------------------------------------------------------------------------
// Skill section tool construction
// ---------------------------------------------------------------------------

type skillToolIn struct {
	fx.In
	Cfg     skilllibrary.Config `optional:"true"`
	Tracker *fbsd.Tracker       `optional:"true"`
}

func provideToolbridgeSkillReadSectionTool(in skillToolIn) *toolbridge.SkillReadSectionTool {
	if strings.TrimSpace(in.Cfg.CacheDir) == "" {
		return nil
	}
	var recorder contract.SkillCallRecorder
	if in.Tracker != nil {
		recorder = in.Tracker
	}
	return toolbridge.NewSkillReadSectionTool(
		in.Cfg.CacheDir,
		contract.SkillSectionReader(skilllibrary.ReadSection),
		recorder,
	)
}

// ---------------------------------------------------------------------------
// Store adapters: binding → AgentThreadLookup
// ---------------------------------------------------------------------------

type agentThreadLookupAdapter struct {
	inner bindingstore.Store
}

func (a agentThreadLookupAdapter) GetThreadByAgent(ctx context.Context, agentID string) (string, error) {
	if a.inner == nil {
		return "", nil
	}
	return a.inner.GetThreadByAgent(ctx, agentID)
}

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
	// Adapt []contract.DynamicToolSchema → []codexprotocol.DynamicToolSchema
	p.Factory.SetListTools(func(ctx context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		tools, err := p.Handler.ListToolsForCodex(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]codexprotocol.DynamicToolSchema, len(tools))
		for i, t := range tools {
			out[i] = codexprotocol.DynamicToolSchema(t)
		}
		return out, nil
	})
}
