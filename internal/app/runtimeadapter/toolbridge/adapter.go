package toolbridgeadapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	mcpserver "github.com/anthropic-ai/super-agent-v3/internal/module/mcp_server"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	uipreferencestore "github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	"go.uber.org/fx"
)

// Module 将具体 store/module/provider 实现接到 toolbridge 的窄接口端口，
// 并在调用者 root scope 保留 readiness、decorate、invoke 的原始装配顺序。
var Module = fx.Options(
	fx.Module("toolbridgeadapter",
		fx.Provide(
			provideToolbridgeAgentThreadLookup,
			provideToolbridgeThreadConfigOverrideStore,
			provideToolbridgeUIPreferenceReader,
			provideToolbridgeWorkDirResolver,
			provideToolbridgeMCPToolLifecycleBackfiller,
			provideToolbridgeMCPToolLifecyclePolicyReader,
		),
	),
	fx.Provide(
		newCodexToolbridgeReadinessProbe,
		provideToolbridgeReadinessProbe,
	),
	fx.Decorate(decorateSessionStarterWithToolbridgeReadiness),
	fx.Invoke(bindToolbridgeCodexHandlers),
)

// ----- binding store 适配器 -----

type agentThreadLookupAdapter struct {
	inner bindingstore.Store
}

// GetThreadByAgent 通过 binding store 查询 agent 对应的 thread。
func (a agentThreadLookupAdapter) GetThreadByAgent(ctx context.Context, agentID string) (string, error) {
	if a.inner == nil {
		return "", errors.New("toolbridge: binding store is not configured")
	}
	return a.inner.GetThreadByAgent(ctx, agentID)
}

// GetBindingByAgent 读取 agent 的 tool call binding 并转换成 toolbridge wire 结构。
func (a agentThreadLookupAdapter) GetBindingByAgent(ctx context.Context, agentID string) (toolbridge.ToolCallBinding, error) {
	if a.inner == nil {
		return toolbridge.ToolCallBinding{}, errors.New("toolbridge: binding store is not configured")
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
		return toolbridge.ToolCallBinding{}, errors.New("toolbridge: binding store is not configured")
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
func (a threadConfigOverrideAdapter) GetConfigOverride(ctx context.Context, threadID string) (json.RawMessage, error) {
	if a.inner == nil {
		return nil, errors.New("toolbridge: thread config override store is not configured")
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
		return nil, errors.New("toolbridge: UI preference store is not configured")
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

// ----- MCP tool lifecycle 适配器 -----

type mcpToolLifecycleBackfillAdapter struct {
	inner mcpserver.Service
}

// BackfillMCPTools 将 toolbridge 的 discovery 观察结果转交给 mcp_server owner 服务。
func (a mcpToolLifecycleBackfillAdapter) BackfillMCPTools(ctx context.Context, req toolbridge.MCPToolLifecycleBackfillRequest) error {
	if a.inner == nil {
		return errors.New("mcp server service is not configured")
	}
	_, err := a.inner.BackfillMCPServerTools(ctx, mcpserver.BackfillMCPServerToolsRequest{
		WorkspaceRoot: req.WorkspaceRoot,
		ServerName:    req.ServerName,
		ManifestName:  req.ManifestName,
		Tools:         req.Tools,
	})
	return err
}

// provideToolbridgeMCPToolLifecycleBackfiller 把 mcp_server.Service 暴露成 toolbridge 窄端口。
func provideToolbridgeMCPToolLifecycleBackfiller(svc mcpserver.Service) toolbridge.MCPToolLifecycleBackfiller {
	if svc == nil {
		return nil
	}
	return mcpToolLifecycleBackfillAdapter{inner: svc}
}

type mcpToolLifecyclePolicyAdapter struct {
	inner mcpserver.Service
}

// ResolveMCPToolLifecycle 将 toolbridge 的只读策略查询转交给 mcp_server owner 服务。
func (a mcpToolLifecyclePolicyAdapter) ResolveMCPToolLifecycle(
	ctx context.Context,
	req contract.MCPToolLifecyclePolicyRequest,
) (contract.MCPToolLifecycleDecision, error) {
	if a.inner == nil {
		return contract.MCPToolLifecycleDecision{}, errors.New("mcp server service is not configured")
	}
	return a.inner.ResolveMCPToolLifecycle(ctx, req)
}

// provideToolbridgeMCPToolLifecyclePolicyReader 把 owner 只读策略端口注入 toolbridge。
func provideToolbridgeMCPToolLifecyclePolicyReader(svc mcpserver.Service) toolbridge.MCPToolLifecyclePolicyReader {
	if svc == nil {
		return nil
	}
	return mcpToolLifecyclePolicyAdapter{inner: svc}
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

	Manager   *codexapp.ServerManager
	Factory   *codexapp.DriverFactory
	Handler   *toolbridge.Handler
	Readiness *codexToolbridgeReadinessProbe
}

// bindToolbridgeCodexHandlers 将 Codex server/driver 的 tool 调用接到 toolbridge handler。
// 生产图缺少任一关键依赖都会报错，测试或 no-provider 图必须显式提供 stub。
func bindToolbridgeCodexHandlers(p codexBindingParams) error {
	if p.Manager == nil {
		return errors.New("toolbridge: codex ServerManager is not configured")
	}
	if p.Factory == nil {
		return errors.New("toolbridge: codex DriverFactory is not configured")
	}
	if p.Handler == nil {
		return errors.New("toolbridge: handler is not configured")
	}
	if p.Readiness == nil {
		return errors.New("toolbridge: readiness probe is not configured")
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
	p.Readiness.markReady()
	return nil
}

type codexToolbridgeReadinessProbe struct {
	mu    sync.RWMutex
	ready bool
}

func newCodexToolbridgeReadinessProbe() *codexToolbridgeReadinessProbe {
	return &codexToolbridgeReadinessProbe{}
}

func provideToolbridgeReadinessProbe(probe *codexToolbridgeReadinessProbe) contract.ToolbridgeReadinessProbe {
	return probe
}

func (p *codexToolbridgeReadinessProbe) markReady() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ready = true
}

// CheckToolbridgeReady 在 Codex provider 启动前确认工具桥已经完成绑定。
func (p *codexToolbridgeReadinessProbe) CheckToolbridgeReady(_ context.Context, provider string) error {
	if !strings.EqualFold(strings.TrimSpace(provider), "codex") {
		return nil
	}
	if p == nil {
		return errors.New("toolbridge: readiness probe is not configured")
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.ready {
		return errors.New("toolbridge: codex binding is not ready")
	}
	return nil
}

type toolbridgeReadySessionStarter struct {
	inner     contract.SessionStarter
	readiness contract.ToolbridgeReadinessProbe
}

func decorateSessionStarterWithToolbridgeReadiness(
	starter contract.SessionStarter,
	readiness contract.ToolbridgeReadinessProbe,
) contract.SessionStarter {
	return toolbridgeReadySessionStarter{inner: starter, readiness: readiness}
}

// StartSession 在 thread 生命周期进入 provider 前检查 Codex/toolbridge 绑定状态。
func (s toolbridgeReadySessionStarter) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	if err := s.checkReady(ctx, req.Provider); err != nil {
		return nil, err
	}
	return s.inner.StartSession(ctx, req)
}

// ResumeSession 在恢复 provider session 前复用同一条工具桥 readiness 护栏。
func (s toolbridgeReadySessionStarter) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	if err := s.checkReady(ctx, req.Provider); err != nil {
		return nil, err
	}
	return s.inner.ResumeSession(ctx, req)
}

func (s toolbridgeReadySessionStarter) checkReady(ctx context.Context, provider string) error {
	if s.inner == nil {
		return errors.New("session starter is not configured")
	}
	if s.readiness == nil {
		return errors.New("toolbridge: readiness probe is not configured")
	}
	return s.readiness.CheckToolbridgeReady(ctx, provider)
}
