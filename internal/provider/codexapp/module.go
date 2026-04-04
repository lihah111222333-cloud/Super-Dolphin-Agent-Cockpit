package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"go.uber.org/fx"
)

var Module = fx.Module("provider.codexapp",
	fx.Provide(
		NewServerManager,
		fx.Annotate(NewDriverFactory, fx.ResultTags(`group:"drivers"`)),
	),
	fx.Invoke(RegisterTranslators),
)

// ---------------------------------------------------------------------------
// ServerManager: shared codex app-server with one-time MCP initialization
// ---------------------------------------------------------------------------

// notifyHandler receives routed notifications for a single session.
type notifyHandler func(method string, params json.RawMessage)

// ServerManager owns a single shared codex app-server process and ensures
// MCP servers (mcp-lsp, mcp-orch, etc.) are initialized exactly once at
// application startup. All agent sessions connect to this shared server
// via Transport(), eliminating the per-agent MCP startup cost (~4-7s).
// Events are routed by threadId to the correct session.
type ServerManager struct {
	mu        sync.Mutex
	transport *transport
	serverURL string
	ready     bool
	err       error
	readStop  context.CancelFunc

	routeMu sync.RWMutex
	routes  map[string]notifyHandler // threadId -> handler
	globals map[string]notifyHandler // sessionKey -> handler (events w/o threadId)
}

// ServerManagerParams are the fx dependencies for NewServerManager.
type ServerManagerParams struct {
	fx.In
	Lifecycle fx.Lifecycle
}

// NewServerManager creates and registers a ServerManager with the fx lifecycle.
func NewServerManager(p ServerManagerParams) *ServerManager {
	m := &ServerManager{}
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error { return m.start(ctx) },
		OnStop:  func(ctx context.Context) error { return m.stop() },
	})
	return m
}

// ServerURL returns the ws:// address of the shared app-server.
func (m *ServerManager) ServerURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.serverURL
}

// Running returns true if the shared app-server is running and MCP ready.
func (m *ServerManager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ready && m.transport != nil && m.transport.processRunning()
}

// Transport returns the shared transport for sessions to make RPC calls.
func (m *ServerManager) Transport() *transport {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.transport
}

// Register adds a session handler for events matching the given threadID.
// Global events (no threadId) are fanned out to all registered handlers.
func (m *ServerManager) Register(sessionKey, threadID string, handler notifyHandler) {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	if threadID != "" {
		m.routes[threadID] = handler
	}
	m.globals[sessionKey] = handler
}

// Unregister removes a session's handlers.
func (m *ServerManager) Unregister(sessionKey, threadID string) {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	delete(m.routes, threadID)
	delete(m.globals, sessionKey)
}

// UpdateThreadID re-keys the route when a session's thread changes (e.g. fork).
func (m *ServerManager) UpdateThreadID(sessionKey, oldID, newID string) {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	handler, ok := m.routes[oldID]
	if ok {
		delete(m.routes, oldID)
	}
	if newID != "" && handler != nil {
		m.routes[newID] = handler
	}
}

func (m *ServerManager) routeNotification(method string, params json.RawMessage) {
	threadID := extractEventThreadID(params)
	if threadID != "" {
		m.routeMu.RLock()
		handler := m.routes[threadID]
		m.routeMu.RUnlock()
		if handler != nil {
			handler(method, params)
		}
		return
	}
	// Global event (no threadId): fan out to all sessions.
	m.routeMu.RLock()
	handlers := make([]notifyHandler, 0, len(m.globals))
	for _, h := range m.globals {
		handlers = append(handlers, h)
	}
	m.routeMu.RUnlock()
	for _, h := range handlers {
		h(method, params)
	}
}

func extractEventThreadID(params json.RawMessage) string {
	var envelope struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(params, &envelope)
	return strings.TrimSpace(envelope.ThreadID)
}

func (m *ServerManager) startReadLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	m.readStop = cancel
	go m.transport.ReadLoop(ctx, m.routeNotification)
}

func (m *ServerManager) start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.routes = make(map[string]notifyHandler)
	m.globals = make(map[string]notifyHandler)

	// Write MCP config BEFORE spawning the app-server so it reads the
	// config on startup. The codex app-server does not support reloading
	// MCP config before any thread exists.
	m.writeMCPConfig()

	t := &transport{}
	if err := t.spawnLocal(); err != nil {
		m.err = err
		return err
	}
	startupCtx, cancel := withTimeout(ctx, transportReadyTimeout)
	defer cancel()
	if err := t.establish(startupCtx); err != nil {
		_ = t.Kill()
		m.err = err
		return err
	}
	m.transport = t
	m.serverURL = t.serverURL

	m.ready = true
	m.startReadLoop()
	pkglogger.Info("server_manager: shared app-server ready", "server_url", m.serverURL)
	return nil
}

func (m *ServerManager) writeMCPConfig() {
	manifest := buildCodexMCPManifest(dto.ManifestContext{
		BinaryDir:   providershared.ResolveBinaryDir("", nil),
		ThreadCaps:  cloneCaps(codexCapabilities),
		AutoApprove: []string{"*"},
	})
	if len(collectManagedBinaries(manifest)) == 0 {
		return
	}
	if err := writeCodexMCPConfig(resolveCodexConfigPath(), manifest, ""); err != nil {
		pkglogger.Warn("server_manager: failed to write MCP config", pkglogger.FieldError, err)
	}
}

func (m *ServerManager) stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ready = false
	if m.readStop != nil {
		m.readStop()
		m.readStop = nil
	}
	if m.transport == nil {
		return nil
	}
	pkglogger.Info("server_manager: stopping shared app-server")
	err := m.transport.shutdownTransport(true)
	m.transport = nil
	m.serverURL = ""
	return err
}

func buildSkillPromptInput(skills []dto.SkillRef) (turnInputItem, bool) {
	sections := make([]string, 0, len(skills))
	for _, skill := range skills {
		section := strings.TrimSpace(skill.Prompt)
		if section == "" {
			continue
		}
		if name := strings.TrimSpace(skill.Name); name != "" {
			section = "[skill:" + name + "]\n" + section
		}
		sections = append(sections, section)
	}
	if len(sections) == 0 {
		return turnInputItem{}, false
	}
	text := strings.Join(sections, "\n\n")
	return newTextTurnInput("text", text), true
}

func resolveLocalTurnID(requested, fallback string) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	return strings.TrimSpace(fallback)
}
