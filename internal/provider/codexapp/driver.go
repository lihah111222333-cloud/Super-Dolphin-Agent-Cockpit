package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

type driver struct {
	logger          *slog.Logger
	serverURL       string
	eventDispatcher *unified.EventDispatcher
	approvals       *rpc.ApprovalManager
}

var _ contract.Driver = (*driver)(nil)

var codexCapabilities = dto.CapabilitySet{
	dto.CapMessageSend:  true,
	dto.CapThreadList:   true,
	dto.CapThreadFork:   true,
	dto.CapTurnOverride: true,
	dto.CapModelSwitch:  true,
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

func NewDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher, approvals *rpc.ApprovalManager) contract.Driver {
	return newDriver(logger, eventDispatcher, approvals)
}

func newDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher, approvals *rpc.ApprovalManager) contract.Driver {
	if logger == nil {
		logger = slog.Default()
	}
	return &driver{
		logger:          logger,
		serverURL:       strings.TrimSpace(os.Getenv("CODEX_APP_SERVER_URL")),
		eventDispatcher: eventDispatcher,
		approvals:       approvals,
	}
}

func (d *driver) Name() string { return "codex" }

func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	s, err := newSession(d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals)
	if err != nil {
		return nil, err
	}
	if err := initializeSession(ctx, s.transport); err != nil {
		_ = s.ForceStop()
		return nil, err
	}
	threadID, err := startRemoteThread(ctx, s.transport, req)
	if err != nil {
		_ = s.ForceStop()
		return nil, err
	}
	s.threadID = threadID
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
	s.threadID = threadID
	return s, nil
}

func initializeSession(ctx context.Context, t *transport) error {
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := t.Call(callCtx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "super-agent-v3", "version": "1.0"},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	return err
}

func startRemoteThread(ctx context.Context, t *transport, req dto.StartSessionRequest) (string, error) {
	params := threadStartParams{
		Cwd:                   strings.TrimSpace(req.CWD),
		Model:                 strings.TrimSpace(req.Model),
		ModelProvider:         configString(req.Config, "modelProvider"),
		BaseInstructions:      strings.TrimSpace(req.Instructions),
		DeveloperInstructions: configString(req.Config, "developerInstructions"),
		ApprovalPolicy:        configString(req.Config, "approvalPolicy"),
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
