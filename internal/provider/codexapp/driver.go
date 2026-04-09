package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type driver struct {
	logger          *slog.Logger
	serverURL       string
	eventDispatcher *unified.EventDispatcher
	approvals       *rpc.ApprovalManager
	reporter        contract.RuntimeReporter
	manager         *ServerManager
	cfg             *platformconfig.Config
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

var buildCodexMCPManifest = dto.BuildManifest

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
	cfg *platformconfig.Config,
) contract.DriverFactory {
	return contract.DriverFactory{
		Name: "codex",
		Create: func() contract.Driver {
			return newDriver(logger, dispatcher, approvals, reporter, manager, cfg)
		},
	}
}

func newDriver(logger *slog.Logger, eventDispatcher *unified.EventDispatcher, approvals *rpc.ApprovalManager, reporter contract.RuntimeReporter, manager *ServerManager, cfg *platformconfig.Config) contract.Driver {
	if logger == nil {
		logger = pkglogger.Get()
	}
	serverURL := strings.TrimSpace(os.Getenv("CODEX_APP_SERVER_URL"))
	if serverURL == "" && manager != nil && manager.Running() {
		serverURL = manager.ServerURL()
	}
	return &driver{
		logger:          logger,
		serverURL:       serverURL,
		eventDispatcher: eventDispatcher,
		approvals:       approvals,
		reporter:        reporter,
		manager:         manager,
		cfg:             cfg,
	}
}

func (d *driver) Name() string { return "codex" }

func (d *driver) usingManagedServer() bool {
	return d.manager != nil && d.manager.Running()
}

func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	s, err := newSession(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals, d.manager)
	if err != nil {
		return nil, err
	}
	s.setRuntimeConfig(req.Config)
	s.setApprovalPolicy(resolveApprovalPolicy(req.Config))

	var result startResult
	if d.cfg != nil && d.cfg.Provider.DynamicToolsEnabled && d.listTools != nil {
		tools, err := d.listTools(ctx)
		if err != nil {
			cleanupFailedSession(s, "force stop failed on dynamic tools list error")
			return nil, fmt.Errorf("dynamic tools list: %w", err)
		}
		result, err = startRemoteThreadWithDynamicTools(ctx, s.transport, req, tools)
		if err != nil {
			cleanupFailedSession(s, "force stop failed on start error")
			return nil, err
		}
	} else {
		// Skip MCP injection when connected to the globally managed server;
		// MCP servers were already initialized at app startup by ServerManager.
		if !d.usingManagedServer() {
			if err := d.injectCodexMCPServers(ctx, s, req); err != nil {
				cleanupFailedSession(s, "force stop failed on mcp injection error")
				return nil, err
			}
		}
		result, err = startRemoteThread(ctx, s.transport, req)
		if err != nil {
			cleanupFailedSession(s, "force stop failed on start error")
			return nil, err
		}
	}
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
	return s, nil
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

func startRemoteThread(ctx context.Context, t *transport, req dto.StartSessionRequest) (startResult, error) {
	return startRemoteThreadWithParams(ctx, t, req, buildThreadStartParams(req))
}

// interruptStaleTurnOnResume cancels any in-progress turn that was left over
// from a previous app lifecycle.  After a full restart the local MCP servers
// (mcp-lsp, mcp-orch) are new processes and any pending MCP tool call from the
// old session will never receive a response, causing the agent to hang forever.
// Sending turn/interrupt is safe: it is a no-op when no turn is active.
func interruptStaleTurnOnResume(s *session, threadID string) {
	if s == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	_, err := callWithTimeout(s.ctx, s.transport, 10*time.Second, "turn/interrupt", map[string]any{
		"threadId": threadID,
		"source":   "resume_stale_turn_cleanup",
	})
	if s.logger != nil {
		if err != nil {
			s.logger.Debug("codexapp: stale turn interrupt on resume (expected if idle)",
				"thread_id", threadID, "error", err)
		} else {
			s.logger.Info("codexapp: interrupted stale turn on resume", "thread_id", threadID)
		}
	}
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

func (d *driver) injectCodexMCPServers(ctx context.Context, s *session, req dto.StartSessionRequest) error {
	manifest := buildCodexMCPManifest(dto.ManifestContext{
		AgentID:     strings.TrimSpace(req.AgentID),
		CWD:         strings.TrimSpace(req.CWD),
		ThreadCaps:  cloneCaps(codexCapabilities),
		BinaryDir:   providershared.ResolveBinaryDir(req.CWD, req.Config),
		Env:         providershared.StringMap(req.Config["env"]),
		AutoApprove: providershared.ConfigStringSlice(req.Config, "auto_approve", "autoApprove"),
	})
	managedNames := managedCodexMCPServerNames(manifest)
	if len(managedNames) == 0 {
		return nil
	}
	if skip, err := d.skipCodexMCPInjection(s, req, managedNames); err != nil || skip {
		return err
	}
	return d.reloadCodexMCPServers(ctx, s, req.CWD, manifest, managedNames)
}

func managedCodexMCPServerNames(manifest dto.MCPManifest) []string {
	managed := collectManagedBinaries(manifest)
	managedNames := make([]string, 0, len(managed))
	seenNames := make(map[string]struct{}, len(managed))
	for _, bin := range managed {
		name := strings.TrimSpace(bin.Name)
		if name == "" {
			continue
		}
		if _, ok := seenNames[name]; ok {
			continue
		}
		seenNames[name] = struct{}{}
		managedNames = append(managedNames, name)
	}
	return managedNames
}

func (d *driver) skipCodexMCPInjection(s *session, req dto.StartSessionRequest, managedNames []string) (bool, error) {
	if s == nil || s.transport == nil {
		return false, errors.New("codexapp: session transport is not available")
	}
	if s.transport.local {
		return false, nil
	}
	d.logger.Warn("codexapp: skipping mcp injection for external app-server",
		"agent_id", strings.TrimSpace(req.AgentID),
		"server_url", s.transport.serverURL,
		"managed_servers", managedNames,
	)
	return true, nil
}

func (d *driver) reloadCodexMCPServers(ctx context.Context, s *session, cwd string, manifest dto.MCPManifest, managedNames []string) error {
	watcher := newMCPReadyWatcher(managedNames)
	s.setMCPWatcher(watcher)
	defer s.setMCPWatcher(nil)

	configPath := resolveCodexConfigPath()
	if err := writeCodexMCPConfig(configPath, manifest, cwd); err != nil {
		return fmt.Errorf("mcp config write: %w", err)
	}
	if err := reloadCodexMCPConfig(ctx, s.transport); err != nil {
		return err
	}
	return waitForCodexMCPReady(ctx, s.transport, watcher, managedNames)
}

func reloadCodexMCPConfig(ctx context.Context, transport *transport) error {
	if _, err := callWithTimeout(ctx, transport, 10*time.Second, "config/mcpServer/reload", nil); err != nil {
		return fmt.Errorf("mcp reload: %w", err)
	}
	return nil
}

func waitForCodexMCPReady(ctx context.Context, transport *transport, watcher *mcpReadyWatcher, managedNames []string) error {
	readyCtx, readyCancel := withTimeout(ctx, 30*time.Second)
	errCh := make(chan error, 2)
	go func() { errCh <- watcher.Wait(readyCtx) }()
	go func() { errCh <- pollMCPStatus(readyCtx, transport, managedNames, 2*time.Second) }()
	err := <-errCh
	readyCancel()
	if err != nil {
		return fmt.Errorf("mcp ready: %w", err)
	}
	return nil
}

func resolveCodexConfigPath() string {
	if root := strings.TrimSpace(os.Getenv("CODEX_HOME")); root != "" {
		return filepath.Join(root, "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".codex", "config.toml")
	}
	return filepath.Join(home, ".codex", "config.toml")
}
