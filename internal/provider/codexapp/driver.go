package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type DriverFactory struct {
	contract.DriverFactory
	mu              sync.RWMutex
	logger          *slog.Logger
	eventDispatcher *unified.EventDispatcher
	approvals       *rpc.ApprovalManager
	reporter        contract.RuntimeReporter
	manager         *ServerManager
	listTools       func(context.Context) ([]DynamicToolSchema, error)
}

type driver struct {
	logger          *slog.Logger
	serverURL       string
	eventDispatcher *unified.EventDispatcher
	approvals       *rpc.ApprovalManager
	reporter        contract.RuntimeReporter
	manager         *ServerManager
	listTools       func(context.Context) ([]DynamicToolSchema, error)
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

type DynamicToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type threadStartParams struct {
	Cwd                   string              `json:"cwd,omitempty"`
	Model                 string              `json:"model,omitempty"`
	ModelProvider         string              `json:"modelProvider,omitempty"`
	BaseInstructions      string              `json:"baseInstructions,omitempty"`
	DeveloperInstructions string              `json:"developerInstructions,omitempty"`
	ApprovalPolicy        string              `json:"approvalPolicy,omitempty"`
	Personality           string              `json:"personality,omitempty"`
	Summary               string              `json:"summary,omitempty"`
	Effort                string              `json:"effort,omitempty"`
	Sandbox               json.RawMessage     `json:"sandbox,omitempty"`
	DynamicTools          []DynamicToolSchema `json:"dynamicTools,omitempty"`
}

type threadResumeParams struct {
	ThreadID string `json:"threadId"`
	Cwd      string `json:"cwd,omitempty"`
	Model    string `json:"model,omitempty"`
}

func NewDriverFactory(
	logger *slog.Logger,
	dispatcher *unified.EventDispatcher,
	approvals *rpc.ApprovalManager,
	reporter contract.RuntimeReporter,
	manager *ServerManager,
) *DriverFactory {
	factory := &DriverFactory{
		logger:          logger,
		eventDispatcher: dispatcher,
		approvals:       approvals,
		reporter:        reporter,
		manager:         manager,
	}
	factory.DriverFactory = contract.DriverFactory{
		Name: "codex",
		Create: func() contract.Driver {
			return newDriver(logger, dispatcher, approvals, reporter, manager, factory.currentListTools())
		},
	}
	return factory
}

func (f *DriverFactory) SetListTools(fn func(context.Context) ([]DynamicToolSchema, error)) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listTools = fn
}

func (f *DriverFactory) currentListTools() func(context.Context) ([]DynamicToolSchema, error) {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.listTools
}

func newDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher, approvals *rpc.ApprovalManager, reporter contract.RuntimeReporter, manager *ServerManager, listTools ...func(context.Context) ([]DynamicToolSchema, error)) contract.Driver {
	if logger == nil {
		logger = pkglogger.Get()
	}
	serverURL := strings.TrimSpace(os.Getenv("CODEX_APP_SERVER_URL"))
	if serverURL == "" && manager != nil && manager.Running() {
		serverURL = manager.ServerURL()
	}
	var listToolsFn func(context.Context) ([]DynamicToolSchema, error)
	if len(listTools) != 0 {
		listToolsFn = listTools[0]
	}
	return &driver{
		logger:          logger,
		serverURL:       serverURL,
		eventDispatcher: eventDispatcher,
		approvals:       approvals,
		reporter:        reporter,
		manager:         manager,
		listTools:       listToolsFn,
	}
}

func (d *driver) Name() string { return "codex" }

func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	s, err := newSession(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals, d.manager)
	if err != nil {
		return nil, err
	}
	s.setRuntimeConfig(req.Config)
	s.setApprovalPolicy(resolveApprovalPolicy(req.Config))
	return d.startDynamicSession(ctx, s, req)
}

func (d *driver) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	s, err := newSession(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals, d.manager)
	if err != nil {
		return nil, err
	}
	threadID, err := resumeRemoteThread(ctx, s.transport, req)
	if err != nil {
		cleanupFailedSession(s, "force stop failed on resume error")
		return nil, err
	}
	s.setThreadID(threadID)
	d.restoreApprovalPolicy(ctx, s, threadID)
	d.reportRuntime(s.agentID)
	return s, nil
}


func (s *session) AllowedModels(ctx context.Context) ([]string, error) {
	raw, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, "model/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return decodeAllowedModels(raw)
}

type startResult struct {
	threadID string
	model    string
	cwd      string
}

func resumeRemoteThread(ctx context.Context, t *transport, req dto.ResumeSessionRequest) (string, error) {
	resumeID := shared.FirstNonEmpty(req.ProviderThreadID, req.ThreadID)
	raw, err := callWithTimeout(ctx, t, 30*time.Second, "thread/resume", threadResumeParams{
		ThreadID: strings.TrimSpace(resumeID),
		Model:    strings.TrimSpace(req.Model),
	})
	if err != nil {
		return "", err
	}
	return decodeThreadID(raw, resumeID)
}

func decodeStartResult(raw json.RawMessage) (startResult, error) {
	resp, err := decodeThreadRPCResult(raw)
	if err != nil {
		return startResult{}, err
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
	if resp, err := decodeThreadRPCResult(raw); err == nil {
		if id := strings.TrimSpace(resp.Thread.ID); id != "" {
			return id, nil
		}
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback, nil
	}
	return "", errors.New("codexapp: empty thread id")
}


