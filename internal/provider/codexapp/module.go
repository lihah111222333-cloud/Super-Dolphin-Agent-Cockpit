package codexapp

import (
	"context"
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
// ServerManager: shared codex app-server process (one process, N sessions)
// ---------------------------------------------------------------------------

// ServerManager owns a single codex app-server process and ensures MCP
// servers (mcp-lsp, mcp-orch, etc.) are initialized exactly once at
// application startup. Each agent session creates its own independent
// WebSocket connection to ServerURL(), providing natural isolation:
// one broken WS only affects the owning session.
type ServerManager struct {
	mu        sync.Mutex
	process   *transport // owns the local process; sessions use ServerURL() to connect independently
	serverURL string
	ready     bool
	err       error
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

// Running returns true if the shared app-server process is alive.
func (m *ServerManager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ready && m.process != nil && m.process.processRunning()
}

func (m *ServerManager) start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Write MCP config BEFORE spawning the app-server so it reads the
	// config on startup. The codex app-server does not support reloading
	// MCP config before any thread exists.
	m.writeMCPConfig()

	t := &transport{}
	if err := t.spawnLocal(); err != nil {
		m.err = err
		return err
	}
	// Perform a single health-check connection+initialize to verify the
	// process started correctly. Sessions will each create their own WS.
	startupCtx, cancel := withTimeout(ctx, transportReadyTimeout)
	defer cancel()
	if err := t.establish(startupCtx); err != nil {
		_ = t.Kill()
		m.err = err
		return err
	}
	m.process = t
	m.serverURL = t.serverURL
	m.ready = true
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
	if m.process == nil {
		return nil
	}
	pkglogger.Info("server_manager: stopping shared app-server")
	err := m.process.shutdownTransport(true)
	m.process = nil
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
