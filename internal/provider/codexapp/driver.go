package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type driver struct {
	logger          *slog.Logger
	serverURL       string
	eventDispatcher *unified.EventDispatcher
	approvals       *rpc.ApprovalManager
	reporter        contract.RuntimeReporter
}

var _ contract.Driver = (*driver)(nil)

var codexCapabilities = dto.CapabilitySet{
	dto.CapMessageSend:    true,
	dto.CapThreadList:     true,
	dto.CapThreadFork:     true,
	dto.CapContextCompact: true,
	dto.CapTurnOverride:   true,
	dto.CapModelSwitch:    true,
}

type threadRPCResult struct {
	Thread struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	} `json:"thread"`
	Model         string `json:"model"`
	ModelProvider string `json:"modelProvider"`
}

type threadStartParams struct {
	Cwd                   string          `json:"cwd,omitempty"`
	Model                 string          `json:"model,omitempty"`
	ModelProvider         string          `json:"modelProvider,omitempty"`
	BaseInstructions      string          `json:"baseInstructions,omitempty"`
	DeveloperInstructions string          `json:"developerInstructions,omitempty"`
	ApprovalPolicy        string          `json:"approvalPolicy,omitempty"`
	Personality           string          `json:"personality,omitempty"`
	Summary               string          `json:"summary,omitempty"`
	Effort                string          `json:"effort,omitempty"`
	Sandbox               json.RawMessage `json:"sandbox,omitempty"`
}

type threadResumeParams struct {
	ThreadID string `json:"threadId"`
	Cwd      string `json:"cwd,omitempty"`
	Model    string `json:"model,omitempty"`
}

func NewDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher, approvals *rpc.ApprovalManager, reporter contract.RuntimeReporter) contract.Driver {
	return newDriver(logger, eventDispatcher, approvals, reporter)
}

func newDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher, approvals *rpc.ApprovalManager, reporter contract.RuntimeReporter) contract.Driver {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &driver{
		logger:          logger,
		serverURL:       strings.TrimSpace(os.Getenv("CODEX_APP_SERVER_URL")),
		eventDispatcher: eventDispatcher,
		approvals:       approvals,
		reporter:        reporter,
	}
}

func (d *driver) Name() string { return "codex" }

func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	s, err := newSession(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals)
	if err != nil {
		return nil, err
	}
	s.setRuntimeConfig(req.Config)
	s.setApprovalPolicy(resolveApprovalPolicy(req.Config))
	result, err := startRemoteThread(ctx, s.transport, req)
	if err != nil {
		shared.LogIgnoredError(d.logger, "force stop failed on start error", s.ForceStop())
		return nil, err
	}
	s.setThreadID(result.threadID)
	if result.model != "" {
		s.setRuntimeConfigValue("model", result.model)
	}
	if result.cwd != "" {
		s.setRuntimeConfigValue("cwd", result.cwd)
	}
	if port := extractPort(s.transport.serverURL); port > 0 {
		s.setRuntimeConfigValue("port", port)
	}
	d.reportRuntime(s.agentID)
	return s, nil
}

func (d *driver) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	s, err := newSession(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals)
	if err != nil {
		return nil, err
	}
	threadID, err := resumeRemoteThread(ctx, s.transport, req)
	if err != nil {
		shared.LogIgnoredError(d.logger, "force stop failed on resume error", s.ForceStop())
		return nil, err
	}
	s.setThreadID(threadID)
	d.restoreApprovalPolicy(ctx, s, threadID)
	d.reportRuntime(s.agentID)
	return s, nil
}

func (d *driver) restoreApprovalPolicy(_ context.Context, s *session, _ string) {
	if d == nil || s == nil {
		return
	}
	// codex app-server has no per-thread config read API;
	// approval policy is preserved in the session's local state.
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
		Port:     runtimePortFromServerURL(d.serverURL),
		Provider: d.Name(),
	}); err != nil {
		d.logger.Warn("codexapp: report runtime failed", "agent_id", agentID, "error", err)
	}
}

func runtimePortFromServerURL(raw string) int {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(strings.TrimSpace(parsed.Port()))
	if err != nil || port <= 0 {
		return 0
	}
	return port
}

func (s *session) AllowedModels(ctx context.Context) ([]string, error) {
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := s.callTransport(callCtx, "model/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return decodeAllowedModels(raw)
}

func decodeAllowedModels(raw []byte) ([]string, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err == nil {
		if models := modelIDs(top["models"]); len(models) > 0 {
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

type startResult struct {
	threadID string
	model    string
	cwd      string
}

func startRemoteThread(ctx context.Context, t *transport, req dto.StartSessionRequest) (startResult, error) {
	params := threadStartParams{
		Cwd:                   strings.TrimSpace(req.CWD),
		Model:                 strings.TrimSpace(req.Model),
		ModelProvider:         configString(req.Config, "modelProvider"),
		BaseInstructions:      strings.TrimSpace(req.Instructions),
		DeveloperInstructions: configString(req.Config, "developerInstructions"),
		ApprovalPolicy:        resolveApprovalPolicy(req.Config),
		Personality:           configString(req.Config, "personality"),
		Summary:               configString(req.Config, "summary"),
		Effort:                configString(req.Config, "effort"),
		Sandbox:               configJSON(req.Config, "sandbox"),
	}
	callCtx, cancel := withTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := t.Call(callCtx, "thread/start", params)
	if err != nil {
		return startResult{}, err
	}
	return decodeStartResult(raw)
}

func resumeRemoteThread(ctx context.Context, t *transport, req dto.ResumeSessionRequest) (string, error) {
	callCtx, cancel := withTimeout(ctx, 30*time.Second)
	defer cancel()
	resumeID := firstNonEmpty(req.ProviderThreadID, req.ThreadID)
	raw, err := t.Call(callCtx, "thread/resume", threadResumeParams{
		ThreadID: strings.TrimSpace(resumeID),
		Model:    strings.TrimSpace(req.Model),
	})
	if err != nil {
		return "", err
	}
	return decodeThreadID(raw, resumeID)
}

func decodeStartResult(raw json.RawMessage) (startResult, error) {
	var resp threadRPCResult
	if err := json.Unmarshal(raw, &resp); err != nil {
		return startResult{}, fmt.Errorf("codexapp: decode thread/start: %w", err)
	}
	id := strings.TrimSpace(resp.Thread.ID)
	if id == "" {
		return startResult{}, errors.New("codexapp: empty thread id")
	}
	return startResult{
		threadID: id,
		model:    strings.TrimSpace(resp.Model),
		cwd:      strings.TrimSpace(resp.Thread.Cwd),
	}, nil
}

func decodeThreadID(raw json.RawMessage, fallback string) (string, error) {
	var resp threadRPCResult
	if err := json.Unmarshal(raw, &resp); err == nil {
		if id := strings.TrimSpace(resp.Thread.ID); id != "" {
			return id, nil
		}
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback, nil
	}
	return "", errors.New("codexapp: empty thread id")
}

func configString(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	value, _ := cfg[key].(string)
	return strings.TrimSpace(value)
}

func extractPort(serverURL string) int {
	parsed, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || parsed.Port() == "" {
		return 0
	}
	p, err := strconv.Atoi(parsed.Port())
	if err != nil || p <= 0 {
		return 0
	}
	return p
}

func resolveApprovalPolicy(cfg map[string]any) string {
	for _, key := range []string{"approvalPolicy", "approval_policy"} {
		if value := configString(cfg, key); value != "" {
			return value
		}
	}
	return ""
}

func approvalPolicyFromThreadConfig(cfg dto.ThreadConfig) string {
	return firstNonEmpty(cfg.Effective.Approvals, cfg.Override.Approvals)
}

func configJSON(cfg map[string]any, key string) json.RawMessage {
	if cfg == nil || cfg[key] == nil {
		return nil
	}
	raw, err := json.Marshal(cfg[key])
	if err != nil || string(raw) == "null" {
		return nil
	}
	return raw
}
