package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
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
	pool            *ServerPool
	listTools       func(context.Context) ([]codexprotocol.DynamicToolSchema, error)
}

type driver struct {
	logger          *slog.Logger
	serverURL       string
	eventDispatcher *unified.EventDispatcher
	approvals       *rpc.ApprovalManager
	reporter        contract.RuntimeReporter
	manager         *ServerManager
	pool            *ServerPool
	listTools       func(context.Context) ([]codexprotocol.DynamicToolSchema, error)
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
	Cwd                   string                            `json:"cwd,omitempty"`
	Model                 string                            `json:"model,omitempty"`
	ModelProvider         string                            `json:"modelProvider,omitempty"`
	BaseInstructions      string                            `json:"baseInstructions,omitempty"`
	DeveloperInstructions string                            `json:"developerInstructions,omitempty"`
	ApprovalPolicy        string                            `json:"approvalPolicy,omitempty"`
	Personality           string                            `json:"personality,omitempty"`
	Summary               string                            `json:"summary,omitempty"`
	Effort                string                            `json:"effort,omitempty"`
	Sandbox               json.RawMessage                   `json:"sandbox,omitempty"`
	DynamicTools          []codexprotocol.DynamicToolSchema `json:"dynamicTools,omitempty"`
}

type threadResumeParams struct {
	ThreadID              string `json:"threadId"`
	Cwd                   string `json:"cwd,omitempty"`
	Model                 string `json:"model,omitempty"`
	BaseInstructions      string `json:"baseInstructions,omitempty"`
	ApprovalPolicy        string `json:"approvalPolicy,omitempty"`
	DeveloperInstructions string `json:"developerInstructions,omitempty"`
	Sandbox               string `json:"sandbox,omitempty"`
	Summary               string `json:"summary,omitempty"`
	Effort                string `json:"effort,omitempty"`
	Personality           string `json:"personality,omitempty"`
}

func NewDriverFactory(
	logger *slog.Logger,
	dispatcher *unified.EventDispatcher,
	approvals *rpc.ApprovalManager,
	reporter contract.RuntimeReporter,
	manager *ServerManager,
	pool *ServerPool,
) *DriverFactory {
	factory := &DriverFactory{
		logger:          logger,
		eventDispatcher: dispatcher,
		approvals:       approvals,
		reporter:        reporter,
		manager:         manager,
		pool:            pool,
	}
	factory.DriverFactory = contract.DriverFactory{
		Name: "codex",
		Create: func() contract.Driver {
			return newDriver(logger, dispatcher, approvals, reporter, manager, pool, factory.currentListTools())
		},
	}
	return factory
}

func (f *DriverFactory) SetListTools(fn func(context.Context) ([]codexprotocol.DynamicToolSchema, error)) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listTools = fn
}

func (f *DriverFactory) currentListTools() func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.listTools
}

func newDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher, approvals *rpc.ApprovalManager, reporter contract.RuntimeReporter, manager *ServerManager, pool *ServerPool, listTools ...func(context.Context) ([]codexprotocol.DynamicToolSchema, error)) contract.Driver {
	if logger == nil {
		logger = pkglogger.Get()
	}
	serverURL := strings.TrimSpace(os.Getenv("CODEX_APP_SERVER_URL"))
	if serverURL == "" && manager != nil && manager.Running() {
		serverURL = manager.ServerURL()
	}
	var listToolsFn func(context.Context) ([]codexprotocol.DynamicToolSchema, error)
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
		pool:            pool,
		listTools:       listToolsFn,
	}
}

func (d *driver) Name() string { return "codex" }

func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	opts, err := d.resolveSessionOptions(ctx, req)
	if err != nil {
		return nil, err
	}
	s, err := newSessionWithOptions(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals, d.manager, opts...)
	if err != nil {
		return nil, err
	}
	// P22 P1c: explicit runtime start. newSession no longer spawns
	// reader / health goroutines, so StartSession is the sole production
	// launch point for this session's runtime handle. Start BEFORE any
	// subsequent transport.Call (startDynamicSession dispatches RPCs whose
	// responses require the runtime-owned reader to be live).
	if s.runtime != nil {
		s.runtime.Start()
	}
	baseInstructions, developerInstructions := startAssemblyInstructions(req)
	s.setRuntimeConfig(req.Config)
	if baseInstructions != "" {
		s.setRuntimeConfigValue("baseInstructions", baseInstructions)
	}
	if developerInstructions != "" {
		s.setRuntimeConfigValue("developerInstructions", developerInstructions)
	}
	s.setApprovalPolicy(resolveApprovalPolicy(req.Config))
	return d.startDynamicSession(ctx, s, req)
}

func (d *driver) ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	s, err := newSession(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals, d.manager)
	if err != nil {
		return nil, err
	}
	// P22 P1c: explicit runtime start BEFORE resumeRemoteThread; the latter
	// issues a thread/resume RPC whose response lands via the runtime-owned
	// reader. If resume fails below, cleanupFailedSession → ForceStop →
	// runtime.Stop idempotently drains the runtime.
	if s.runtime != nil {
		s.runtime.Start()
	}
	threadID, err := resumeRemoteThread(ctx, s.transport, req)
	if err != nil {
		cleanupFailedSession(s, "force stop failed on resume error")
		return nil, err
	}
	s.setThreadID(threadID)
	if m := strings.TrimSpace(req.Model); m != "" {
		s.setRuntimeConfigValue("model", m)
	}
	baseInstructions, developerInstructions := promptSnapshotInstructions(req.PromptSnapshot)
	if baseInstructions != "" {
		s.setRuntimeConfigValue("baseInstructions", baseInstructions)
	}
	if developerInstructions != "" {
		s.setRuntimeConfigValue("developerInstructions", developerInstructions)
	}
	d.restoreApprovalPolicy(ctx, s, threadID)
	d.reportRuntime(s.agentID)
	return s, nil
}

func (s *session) AllowedModels(ctx context.Context) ([]string, error) {
	raw, err := callWithTimeout(ctx, callTargetFunc(s.callTransport), 10*time.Second, "model/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	models, err := decodeAllowedModels(raw)
	if err != nil {
		return nil, err
	}
	return ensureCodexModelPresent(models, "gpt-5.5"), nil
}

type startResult struct {
	threadID string
	model    string
	cwd      string
}

func resumeRemoteThread(ctx context.Context, t *transport, req dto.ResumeSessionRequest) (string, error) {
	resumeID := shared.FirstNonEmpty(req.ProviderThreadID, req.ThreadID)
	params := buildThreadResumeParams(req)
	params.ThreadID = strings.TrimSpace(resumeID)
	raw, err := callWithTimeout(ctx, t, 30*time.Second, "thread/resume", params)
	if err != nil {
		return "", err
	}
	return decodeThreadID(raw, resumeID)
}

func startAssemblyInstructions(req dto.StartSessionRequest) (string, string) {
	base := strings.TrimSpace(shared.FirstNonEmpty(
		req.StartAssembly.BaseInstructions,
		req.StartAssembly.Snapshot.BaseInstructions,
		req.Instructions,
	))
	developer := strings.TrimSpace(shared.FirstNonEmpty(
		req.StartAssembly.DeveloperInstructions,
		req.StartAssembly.Snapshot.DeveloperInstructions,
		configString(req.Config, "developerInstructions"),
		configString(req.Config, "developer_instructions"),
	))
	return base, developer
}

func promptSnapshotInstructions(snapshot dto.PromptAssemblySnapshot) (string, string) {
	return strings.TrimSpace(snapshot.BaseInstructions), strings.TrimSpace(snapshot.DeveloperInstructions)
}

func buildThreadResumeParams(req dto.ResumeSessionRequest) threadResumeParams {
	baseInstructions, developerInstructions := promptSnapshotInstructions(req.PromptSnapshot)
	return threadResumeParams{
		Cwd:                   strings.TrimSpace(req.CWD),
		Model:                 strings.TrimSpace(req.Model),
		BaseInstructions:      baseInstructions,
		DeveloperInstructions: developerInstructions,
		Effort:                strings.TrimSpace(req.Effort),
	}
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

func sortedConfigKeys(cfg map[string]any) []string {
	if len(cfg) == 0 {
		return nil
	}
	keys := make([]string, 0, len(cfg))
	for key := range cfg {
		keys = append(keys, strings.TrimSpace(key))
	}
	sort.Strings(keys)
	return keys
}

func hasAnyConfigKey(cfg map[string]any, keys ...string) bool {
	return hasAnyKey(cfg, keys...)
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
