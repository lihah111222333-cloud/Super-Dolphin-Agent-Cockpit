package multilsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

const managerShutdownTimeout = 5 * time.Second

// BackgroundRunner satisfies the multilsp.Manager contract (see
// manager.go) by returning the pool's recycler as a
// platformrunner.Runner. A nil receiver or nil pool yields nil so the
// root collector can safely drop it from `group:"runners"`. P22 P2
// LSP-S1.
func (m *manager) BackgroundRunner() platformrunner.Runner {
	if m == nil || m.pool == nil {
		return nil
	}
	return m.pool.RecyclerRunner()
}

func (m *manager) EnsureClient(ctx context.Context, filePath, languageID string) (Client, error) {
	if strings.TrimSpace(filePath) != "" {
		ref, err := m.resolveDocumentRef(ctx, filePath, languageID)
		if err != nil {
			return nil, err
		}
		return m.ensureClientForFile(ctx, ref.absPath, ref.languageID)
	}
	return m.ensureClientForLanguage(ctx, languageID)
}

func (m *manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()

	// Let any in-flight initialization observe the closed state and clean up before shutdown.
	m.waitForEnsureOperations()

	clients := m.collectAndClearClients()
	m.AdvanceDiagnosticGeneration()
	var firstErr error
	if m.pool != nil && m.pool.primary == m {
		firstErr = firstNonNilError(firstErr, m.pool.Close())
	}
	firstErr = firstNonNilError(firstErr, shutdownClients(clients))
	closeBootstrapCoordinator(m)
	return firstErr
}

func (m *manager) waitForEnsureOperations() {
	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	_ = m.closed
}

func (m *manager) collectAndClearClients() []Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	clients := make([]Client, 0, len(m.workspaces))
	for _, workspace := range m.workspaces {
		if workspace != nil && workspace.client != nil {
			clients = append(clients, workspace.client)
		}
	}
	clear(m.workspaces)
	return clients
}

func shutdownClients(clients []Client) error {
	var firstErr error
	for _, client := range clients {
		shutCtx, cancel := platformconfig.WithTimeout(context.Background(), managerShutdownTimeout)
		if err := client.Shutdown(shutCtx); err != nil && firstErr == nil {
			firstErr = err
		}
		cancel()
		if err := client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func firstNonNilError(current, next error) error {
	if current != nil || next == nil {
		return current
	}
	return next
}

func (m *manager) ensureClientForFile(ctx context.Context, filePath, languageID string) (Client, error) {
	ref, err := m.resolveDocumentRef(ctx, filePath, languageID)
	if err != nil {
		return nil, err
	}
	cfg, err := m.resolveWorkspaceForDocument(ctx, ref)
	if err != nil {
		return nil, err
	}
	return m.ensureClient(ctx, cfg)
}

func (m *manager) ensureClientForLanguage(ctx context.Context, languageID string) (Client, error) {
	root, langID, err := m.resolveLanguageWorkspace(ctx, languageID)
	if err != nil {
		return nil, err
	}
	client, err := m.ensureClient(ctx, workspaceConfig{
		key:        root,
		rootPath:   root,
		rootURI:    fileURIFromPath(root),
		languageID: langID,
	})
	if err != nil {
		return nil, err
	}
	m.bootstrapLanguageClient(ctx, client, root, langID)
	return client, nil
}

func (m *manager) resolveLanguageWorkspace(ctx context.Context, languageID string) (string, string, error) {
	if !shouldUseClientForLanguage(languageID) {
		return "", "", fmt.Errorf("language %q is not managed by the LSP manager", languageID)
	}
	root := m.effectiveWorkspaceRoot(ctx)
	if root == "" {
		return "", "", ErrWorkspaceRootEmpty
	}
	langID := normalizeLanguageID(languageID)
	if shouldUseJSTSWorkspace(langID) {
		return m.resolveJSTSWorkspaceRoot(root), langID, nil
	}
	if shouldUseJavaWorkspace(langID) {
		return m.resolveJavaWorkspaceRoot(root), langID, nil
	}
	return root, langID, nil
}

func (m *manager) resolveJSTSWorkspaceRoot(root string) string {
	if jsRoot, err := findJSTSProjectRoot(root); err == nil && jsRoot != "" {
		m.warnJSTS("jsts: found project root walking up", "root", jsRoot)
		return jsRoot
	}
	if jsRoot := findJSTSProjectRootWithin(root); jsRoot != "" {
		m.warnJSTS("jsts: found project root walking down", "root", jsRoot)
		return jsRoot
	}
	m.warnJSTS("jsts: no project root found", "workspaceRoot", root)
	return root
}

func (m *manager) bootstrapLanguageClient(ctx context.Context, client Client, root, languageID string) {
	if shouldUseJSTSWorkspace(languageID) {
		m.bootstrapJSTSClient(ctx, client, root, languageID)
	} else if shouldUseJavaWorkspace(languageID) {
		m.bootstrapJavaClient(ctx, client, root, languageID)
	}
}

func (m *manager) warnJSTS(message string, args ...any) {
	if m.logger != nil {
		m.logger.Warn(message, args...)
	}
}

// bootstrapJSTSClient opens the first JS/TS file found under root so that
// tsserver creates a project context for workspace-wide queries.
func (m *manager) bootstrapJSTSClient(ctx context.Context, client Client, root, languageID string) {
	target := findJSTSBootstrapFileWithin(root)
	if target == "" {
		m.warnJSTS("jsts: bootstrap - no JS/TS file found", "root", root)
		return
	}
	content, err := os.ReadFile(target)
	if err != nil {
		m.warnJSTS("jsts: bootstrap - failed to read file", "file", target, "err", err)
		return
	}
	m.warnJSTS("jsts: bootstrap - opening file for tsserver", "file", target, "root", root, "lang", languageID)
	_ = client.DidOpen(ctx, fileURIFromPath(target), languageID, 0, string(content))
}

func (m *manager) resolveJavaWorkspaceRoot(root string) string {
	if javaRoot, err := findJavaProjectRoot(root); err == nil && javaRoot != "" {
		m.logJava("java: found project root walking up", "root", javaRoot)
		return javaRoot
	}
	if javaRoot := findJavaProjectRootWithin(root); javaRoot != "" {
		m.logJava("java: found project root walking down", "root", javaRoot)
		return javaRoot
	}
	m.logJava("java: no project root found", "workspaceRoot", root)
	return root
}

func (m *manager) bootstrapJavaClient(ctx context.Context, client Client, root, languageID string) {
	target := findJavaBootstrapFileWithin(root)
	if target == "" {
		m.logJava("java: bootstrap - no .java file found", "root", root)
		return
	}
	content, err := os.ReadFile(target)
	if err != nil {
		m.logJava("java: bootstrap - failed to read file", "file", target, "err", err)
		return
	}
	m.logJava("java: bootstrap - opening file for jdtls", "file", target, "root", root)
	_ = client.DidOpen(ctx, fileURIFromPath(target), languageID, 0, string(content))
}

func (m *manager) logJava(message string, args ...any) {
	if m.logger != nil {
		m.logger.Warn(message, args...)
	}
}

func (m *manager) ensureClient(ctx context.Context, cfg workspaceConfig) (Client, error) {
	client, err := m.lookupExistingClient(cfg.key)
	if client != nil || err != nil {
		return client, err
	}

	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()

	client, err = m.lookupExistingClient(cfg.key)
	if client != nil || err != nil {
		return client, err
	}

	return m.createAndRegisterClient(ctx, cfg)
}

func (m *manager) lookupExistingClient(key string) (Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	if workspace := m.workspaces[key]; workspace != nil && workspace.client != nil {
		return workspace.client, nil
	}
	return nil, nil
}

func (m *manager) createAndRegisterClient(ctx context.Context, cfg workspaceConfig) (Client, error) {
	if m.factory == nil {
		return nil, ErrClientFactoryNil
	}
	capturedGen := m.diagGeneration.Load()
	client, err := m.factory.NewClient(cfg.rootPath, managerNotificationHandler{
		publishDiagnostics: func(params protocol.PublishDiagnosticsParams) error {
			return m.publishDiagnosticsForGeneration(params, capturedGen)
		},
		logMessage: m.LogMessage,
	})
	if err != nil {
		return nil, fmt.Errorf("create LSP client: %w", err)
	}
	if err := client.Initialize(ctx, cfg.rootURI); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize LSP client: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		_ = client.Shutdown(context.Background())
		_ = client.Close()
		return nil, ErrManagerClosed
	}
	if workspace := m.workspaces[cfg.key]; workspace != nil && workspace.client != nil {
		_ = client.Shutdown(context.Background())
		_ = client.Close()
		return workspace.client, nil
	}
	m.workspaces[cfg.key] = &workspaceClient{
		key:      cfg.key,
		rootPath: cfg.rootPath,
		rootURI:  cfg.rootURI,
		client:   client,
	}
	return client, nil
}

func (m *manager) DidOpen(ctx context.Context, uri, languageID string, version int, text string) error {
	return m.notifyDocument(ctx, uri, languageID, func(ctx context.Context, client Client, ref documentRef) error {
		return client.DidOpen(ctx, ref.uri, ref.languageID, version, text)
	})
}

func (m *manager) DidChange(ctx context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	return m.notifyDocument(ctx, uri, "", func(ctx context.Context, client Client, ref documentRef) error {
		return client.DidChange(ctx, ref.uri, version, changes)
	})
}

func (m *manager) DidClose(ctx context.Context, uri string) error {
	return m.notifyDocument(ctx, uri, "", func(ctx context.Context, client Client, ref documentRef) error {
		return client.DidClose(ctx, ref.uri)
	})
}

func (m *manager) BootstrapDocument(ctx context.Context, uri string) error {
	return m.bootstrapDocument(ctx, uri)
}

func (m *manager) BootstrapDocumentOpenOnly(ctx context.Context, uri string) error {
	return m.bootstrapDocumentOpenOnly(ctx, uri)
}

func (m *manager) LogMessage(params protocol.LogMessageParams) error {
	if m.logger == nil {
		return nil
	}
	switch params.Type {
	case protocol.LogMessageError:
		m.logger.Error("lsp", "message", params.Message)
	case protocol.LogMessageWarning:
		m.logger.Warn("lsp", "message", params.Message)
	default:
		m.logger.Debug("lsp", "message", params.Message)
	}
	return nil
}

func (m *manager) request(ctx context.Context, client Client, method string, params any) (json.RawMessage, error) {
	if client == nil {
		return nil, fmt.Errorf("request %s: client is nil", method)
	}
	var (
		raw json.RawMessage
		err error
	)
	err = m.withPooledClient(client, func() error {
		raw, err = client.Request(ctx, method, params)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return raw, nil
}

func (m *manager) documentClient(ctx context.Context, uri string) (Client, documentRef, error) {
	ref, err := m.resolveDocumentRef(ctx, uri, "")
	if err != nil {
		return nil, documentRef{}, err
	}
	if !shouldUseClientForLanguage(ref.languageID) {
		return nil, ref, nil
	}
	if err := m.bootstrapDocument(ctx, ref.uri); err != nil {
		return nil, documentRef{}, err
	}
	client, err := m.ensureClientForFile(ctx, ref.absPath, ref.languageID)
	if err != nil {
		return nil, documentRef{}, err
	}
	return client, ref, nil
}

func decodeInto[T any](raw json.RawMessage, out *T) error {
	if len(raw) == 0 || string(raw) == "null" {
		var zero T
		*out = zero
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	return nil
}
