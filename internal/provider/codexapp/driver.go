package codexapp

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
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
		ID string `json:"id"`
	} `json:"thread"`
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
		logger = slog.Default()
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
	s, err := newSession(d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals)
	if err != nil {
		return nil, err
	}
	s.setApprovalPolicy(resolveApprovalPolicy(req.Config))
	if err := initializeSession(ctx, s.transport); err != nil {
		_ = s.ForceStop()
		return nil, err
	}
	threadID, err := startRemoteThread(ctx, s.transport, req)
	if err != nil {
		_ = s.ForceStop()
		return nil, err
	}
	s.setThreadID(threadID)
	d.reportRuntime(s.agentID)
	return s, nil
}

func (d *driver) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	s, err := newSession(d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals)
	if err != nil {
		return nil, err
	}
	if err := initializeSession(ctx, s.transport); err != nil {
		_ = s.ForceStop()
		return nil, err
	}
	threadID, err := resumeRemoteThread(ctx, s.transport, req)
	if err != nil {
		_ = s.ForceStop()
		return nil, err
	}
	s.setThreadID(threadID)
	d.restoreApprovalPolicy(ctx, s, threadID)
	d.reportRuntime(s.agentID)
	return s, nil
}

func (d *driver) restoreApprovalPolicy(ctx context.Context, s *session, threadID string) {
	if d == nil || s == nil {
		return
	}
	cfg, err := s.ReadConfig(ctx, threadID)
	if err != nil {
		if d.logger != nil {
			d.logger.Warn("codexapp: restore approval policy failed",
				"agent_id", s.agentID,
				"thread_id", strings.TrimSpace(threadID),
				"error", err)
		}
		return
	}
	s.setApprovalPolicy(approvalPolicyFromThreadConfig(cfg))
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

func initializeSession(ctx context.Context, t *transport) error {
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := t.Call(callCtx, "initialize", initializeParams())
	return err
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

func startRemoteThread(ctx context.Context, t *transport, req dto.StartSessionRequest) (string, error) {
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
		return "", err
	}
	return decodeThreadID(raw, "")
}

func resumeRemoteThread(ctx context.Context, t *transport, req dto.ResumeSessionRequest) (string, error) {
	callCtx, cancel := withTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := t.Call(callCtx, "thread/resume", threadResumeParams{
		ThreadID: strings.TrimSpace(req.ThreadID),
		Model:    strings.TrimSpace(req.Model),
	})
	if err != nil {
		return "", err
	}
	return decodeThreadID(raw, req.ThreadID)
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
