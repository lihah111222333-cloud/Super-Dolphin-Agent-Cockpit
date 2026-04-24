package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return platformconfig.WithTimeout(ctx, d)
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func initializeParams() map[string]any {
	return map[string]any{
		"clientInfo":   map[string]any{"name": "super-agent-v3", "version": "1.0"},
		"capabilities": map[string]any{"experimentalApi": true},
	}
}

func newTurnHandle(localID, providerID string) *turnHandle {
	return &turnHandle{
		localID:    strings.TrimSpace(localID),
		providerID: strings.TrimSpace(providerID),
		done:       make(chan struct{}),
	}
}

func (h *turnHandle) LocalID() string       { return h.localID }
func (h *turnHandle) Done() <-chan struct{} { return h.done }

func (h *turnHandle) ProviderID() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.providerID
}

func (h *turnHandle) Err() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.err
}

func (h *turnHandle) setProviderID(providerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.providerID = strings.TrimSpace(providerID)
}

func (h *turnHandle) complete(err error) {
	h.once.Do(func() {
		h.mu.Lock()
		h.err = err
		h.mu.Unlock()
		close(h.done)
	})
}

func cloneCaps(src dto.CapabilitySet) dto.CapabilitySet {
	out := make(dto.CapabilitySet, len(src))
	maps.Copy(out, src)
	return out
}

func (s *session) ThreadID() string {
	if s == nil {
		return ""
	}
	threadID, _ := s.threadID.Load().(string)
	return strings.TrimSpace(threadID)
}

func (s *session) RolloutPath() string {
	tid := s.ThreadID()
	if tid == "" {
		return ""
	}
	// P21 Track B: honour codexHome when the session was started with a
	// multi-provider binding. An empty codexHome keeps the legacy
	// ~/.codex lookup for single-provider deployments.
	path, err := findRolloutPath(tid, s.runtimeConfigString("codexHome"))
	if err != nil {
		return ""
	}
	return path
}

func (s *session) setThreadID(threadID string) {
	if s == nil {
		return
	}
	s.threadID.Store(strings.TrimSpace(threadID))
}

func (s *session) configureThread(ctx context.Context, patch dto.ThreadConfigPatch) error {
	threadID, err := requireThreadID(s)
	if err != nil {
		return err
	}
	if err := s.applyConfigSet(ctx, threadID, patch); err != nil {
		return err
	}
	if err := s.applyConfigSlashCommands(ctx, threadID, patch); err != nil {
		return err
	}
	s.updateRuntimeConfigFromPatch(patch)
	return nil
}

func (s *session) applyConfigSet(_ context.Context, _ string, patch dto.ThreadConfigPatch) error {
	// V2 parity: model/effort are stored locally and applied via
	// turn/start params (turnStartParams.Model / Effort) on the next turn.
	// codex app-server does not have a thread/config/set RPC.
	if patch.Model != nil {
		s.setRuntimeConfigValue("model", strings.TrimSpace(*patch.Model))
	}
	if patch.Effort != nil {
		s.setRuntimeConfigValue("effort", strings.TrimSpace(*patch.Effort))
	}
	return nil
}

func (s *session) applyConfigSlashCommands(ctx context.Context, threadID string, patch dto.ThreadConfigPatch) error {
	if err := s.applySlashConfig(ctx, threadID, "thread/personality/set", "personality", patch.Personality); err != nil {
		return err
	}
	return s.applySlashConfig(ctx, threadID, "thread/approvals/set", "policy", patch.Approvals)
}

func (s *session) updateRuntimeConfigFromPatch(patch dto.ThreadConfigPatch) {
	if patch.Approvals != nil {
		approval := strings.TrimSpace(*patch.Approvals)
		s.setApprovalPolicy(approval)
		s.setRuntimeConfigValue("approvalPolicy", approval)
		s.setRuntimeConfigValue("approval_policy", approval)
		s.setRuntimeConfigValue("approvals", approval)
	}
	if patch.Personality != nil {
		s.setRuntimeConfigValue("personality", strings.TrimSpace(*patch.Personality))
	}
}

func (s *session) applySlashConfig(ctx context.Context, threadID, method, key string, value *string) error {
	if value == nil {
		return nil
	}
	arg := strings.TrimSpace(*value)
	if arg == "" {
		return nil
	}
	_, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, method, map[string]any{"threadId": threadID, key: arg, "args": arg})
	return err
}

func (s *session) runtimeConfigString(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeConfig == nil {
		return ""
	}
	v, _ := s.runtimeConfig[key].(string)
	return strings.TrimSpace(v)
}

func (s *session) setRuntimeConfigValue(key string, value any) {
	if strings.TrimSpace(key) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeConfig == nil {
		s.runtimeConfig = map[string]any{}
	}
	s.runtimeConfig[key] = value
}

func decodeAllowedModels(raw []byte) ([]string, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err == nil {
		// Try "models" key first, then "data" key (codex app-server format)
		if models := modelIDs(top["models"]); len(models) > 0 {
			return models, nil
		}
		if models := modelIDs(top["data"]); len(models) > 0 {
			return models, nil
		}
	}
	var list []any
	if err := json.Unmarshal(raw, &list); err == nil {
		if models := modelIDs(list); len(models) > 0 {
			return models, nil
		}
	}
	return nil, errors.New("codexapp: invalid model/list response")
}

func modelIDs(raw any) []string {
	list, _ := raw.([]any)
	out := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, item := range list {
		entry, _ := item.(map[string]any)
		id, _ := entry["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// ensureCodexModelPresent injects a model into the allowed list if the upstream
// model/list RPC has not yet been updated. This covers the window between a new
// model launch and the next Codex CLI release.
func ensureCodexModelPresent(models []string, target string) []string {
	for _, m := range models {
		if strings.EqualFold(m, target) {
			return models
		}
	}
	return append([]string{target}, models...)
}

// knownModelContextWindows maps model IDs to their correct context_window
// token counts. When the Codex CLI does not recognise a model it uses
// fallback metadata that reports a wrong (too small) context window.
// Entries here override that fallback so the UI shows the correct bar.
var knownModelContextWindows = map[string]int{
	"gpt-5.5": 872000,
	"gpt-5.4": 872000,
}

// contextWindowForModel returns the authoritative context window size for a
// model, or 0 if no override is registered.
func contextWindowForModel(model string) int {
	return knownModelContextWindows[strings.ToLower(strings.TrimSpace(model))]
}

func configString(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	value, _ := cfg[key].(string)
	return strings.TrimSpace(value)
}

func resolveApprovalPolicy(cfg map[string]any) string {
	for _, key := range []string{"approvalPolicy", "approval_policy"} {
		if value := configString(cfg, key); value != "" {
			return value
		}
	}
	// Default to "never" — UI approval flow is not yet wired,
	// so any other default would block MCP tool calls indefinitely.
	return "never"
}

func buildThreadStartParams(req dto.StartSessionRequest) threadStartParams {
	baseInstructions, developerInstructions := startAssemblyInstructions(req)
	return threadStartParams{
		Cwd:                   strings.TrimSpace(req.CWD),
		Model:                 strings.TrimSpace(req.Model),
		ModelProvider:         configString(req.Config, "modelProvider"),
		BaseInstructions:      baseInstructions,
		DeveloperInstructions: developerInstructions,
		ApprovalPolicy:        resolveApprovalPolicy(req.Config),
		Personality:           configString(req.Config, "personality"),
		Summary:               configString(req.Config, "summary"),
		Effort:                configString(req.Config, "effort"),
		Sandbox:               configJSON(req.Config, "sandbox"),
	}
}

func (d *driver) startDynamicSession(ctx context.Context, s *session, req dto.StartSessionRequest) (contract.Session, error) {
	if d == nil || d.listTools == nil {
		cleanupFailedSession(s, "force stop failed on missing dynamic tools provider")
		return nil, errors.New("codexapp: dynamic tools provider is not configured")
	}
	tools, err := d.listTools(ctx)
	if err != nil {
		cleanupFailedSession(s, "force stop failed on dynamic tools list error")
		return nil, fmt.Errorf("dynamic tools list: %w", err)
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	pkglogger.Info("codexapp: dynamic tools injected into thread/start",
		"agent_id", req.AgentID,
		"tool_count", len(tools),
		"tool_names", names,
	)
	result, err := startRemoteThreadWithDynamicTools(ctx, s.transport, req, tools)
	if err != nil {
		cleanupFailedSession(s, "force stop failed on start error")
		return nil, err
	}
	return d.finishStartedSession(s, result), nil
}

func (d *driver) finishStartedSession(s *session, result startResult) contract.Session {
	s.setThreadID(result.threadID)
	if result.model != "" {
		s.setRuntimeConfigValue("model", result.model)
	}
	if result.cwd != "" {
		s.setRuntimeConfigValue("cwd", result.cwd)
	}
	if port := parsePortFromURL(s.transport.serverURL); port > 0 {
		s.setRuntimeConfigValue("port", port)
	}
	d.reportRuntime(s.agentID)
	return s
}

func startRemoteThreadWithDynamicTools(ctx context.Context, t *transport, req dto.StartSessionRequest, tools []codexprotocol.DynamicToolSchema) (startResult, error) {
	params := buildThreadStartParams(req)
	params.DynamicTools = tools
	// dynamicTools schema is exposed to the model by the codex app-server
	// itself — no need to duplicate tool names in developerInstructions
	// (V2 parity: avoids wasting ~70k context tokens on tool catalog).
	return startRemoteThreadWithParams(ctx, t, req, params)
}

func startRemoteThreadWithParams(ctx context.Context, t *transport, req dto.StartSessionRequest, params threadStartParams) (startResult, error) {
	pkglogger.Info("codexapp: thread/start request",
		"agent_id", strings.TrimSpace(req.AgentID),
		"cwd", params.Cwd,
		"model", params.Model,
		"approval_policy", params.ApprovalPolicy,
		"config_keys", sortedConfigKeys(req.Config),
		"has_env", hasAnyConfigKey(req.Config, "env"),
		"has_mcp", hasAnyConfigKey(req.Config, "mcp", "mcpConfig", "mcp_config", "mcpServers", "mcp_servers"),
		"has_hooks", hasAnyConfigKey(req.Config, "hooks", "hookConfig", "hook_config"),
	)
	if len(params.DynamicTools) > 0 {
		firstTool, _ := json.Marshal(params.DynamicTools[0])
		pkglogger.Info("codexapp: thread/start payload debug",
			"dynamic_tools_count", len(params.DynamicTools),
			"first_tool_json", string(firstTool),
		)
	}
	raw, err := callWithTimeout(ctx, t, 30*time.Second, "thread/start", params)
	if err != nil {
		return startResult{}, err
	}
	return decodeStartResult(raw)
}

func (d *driver) restoreApprovalPolicy(ctx context.Context, s *session, threadID string) {
	if d == nil || s == nil {
		return
	}
	result, err := s.transport.Call(ctx, "thread/config/get", map[string]any{
		"threadId": threadID,
	})
	if err != nil {
		// RPC not available – fall back to local state.
		s.setRuntimeConfigValue("approvalPolicy", s.approvalPolicyValue())
		return
	}
	var resp map[string]any
	if err := json.Unmarshal(result, &resp); err != nil {
		s.setRuntimeConfigValue("approvalPolicy", s.approvalPolicyValue())
		return
	}
	effective, _ := resp["effective"].(map[string]any)
	if effective == nil {
		s.setRuntimeConfigValue("approvalPolicy", s.approvalPolicyValue())
		return
	}
	if approval, ok := effective["approvals"].(string); ok && strings.TrimSpace(approval) != "" {
		s.setApprovalPolicy(strings.TrimSpace(approval))
		s.setRuntimeConfigValue("approvalPolicy", strings.TrimSpace(approval))
		return
	}
	s.setRuntimeConfigValue("approvalPolicy", s.approvalPolicyValue())
}

func (d *driver) reportRuntime(agentID string) {
	if d == nil || d.reporter == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return
	}
	ctx, cancel := platformconfig.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// TODO: Prefer a provider-reported control/runtime port once the Codex App
	// protocol exposes one explicitly; for now we fall back to the configured
	// app-server endpoint port after session startup succeeds.
	if err := d.reporter.ReportRuntime(ctx, contract.RuntimeReport{
		AgentID:  agentID,
		Port:     parsePortFromURL(d.serverURL),
		Provider: d.Name(),
	}); err != nil {
		d.logger.Warn("codexapp: report runtime failed", "agent_id", agentID, "error", err)
	}
}
