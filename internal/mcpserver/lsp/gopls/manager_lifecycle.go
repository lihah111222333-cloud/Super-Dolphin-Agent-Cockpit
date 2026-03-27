package gopls

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

const managerShutdownTimeout = 5 * time.Second

func (m *manager) EnsureClient(ctx context.Context, filePath, languageID string) (Client, error) {
	if strings.TrimSpace(filePath) != "" {
		ref, err := m.resolveDocumentRef(filePath, languageID)
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
	m.ensureMu.Lock()
	m.ensureMu.Unlock()

	m.mu.Lock()
	clients := make([]Client, 0, len(m.workspaces))
	for _, workspace := range m.workspaces {
		if workspace != nil && workspace.client != nil {
			clients = append(clients, workspace.client)
		}
	}
	clear(m.workspaces)
	m.mu.Unlock()

	m.AdvanceDiagnosticGeneration()
	var firstErr error
	if m.pool != nil && m.pool.primary == m {
		if err := m.pool.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, client := range clients {
		shutCtx, cancel := context.WithTimeout(context.Background(), managerShutdownTimeout)
		if err := client.Shutdown(shutCtx); err != nil && firstErr == nil {
			firstErr = err
		}
		cancel()
		if err := client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	closeBootstrapCoordinator(m)
	return firstErr
}

func (m *manager) ensureClientForFile(ctx context.Context, filePath, languageID string) (Client, error) {
	ref, err := m.resolveDocumentRef(filePath, languageID)
	if err != nil {
		return nil, err
	}
	cfg, err := m.resolveWorkspaceForDocument(ref)
	if err != nil {
		return nil, err
	}
	return m.ensureClient(ctx, cfg)
}

func (m *manager) ensureClientForLanguage(ctx context.Context, languageID string) (Client, error) {
	if !shouldUseClientForLanguage(languageID) {
		return nil, fmt.Errorf("language %q is not managed by gopls", languageID)
	}
	root := m.workspaceRoot
	if root == "" {
		return nil, ErrWorkspaceRootEmpty
	}
	cfg := workspaceConfig{
		key:        root,
		rootPath:   root,
		rootURI:    fileURIFromPath(root),
		languageID: normalizeLanguageID(languageID),
	}
	return m.ensureClient(ctx, cfg)
}

func (m *manager) ensureClient(ctx context.Context, cfg workspaceConfig) (Client, error) {
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil, ErrManagerClosed
	}
	if workspace := m.workspaces[cfg.key]; workspace != nil && workspace.client != nil {
		client := workspace.client
		m.mu.RUnlock()
		return client, nil
	}
	m.mu.RUnlock()

	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()

	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil, ErrManagerClosed
	}
	if workspace := m.workspaces[cfg.key]; workspace != nil && workspace.client != nil {
		client := workspace.client
		m.mu.RUnlock()
		return client, nil
	}
	m.mu.RUnlock()

	if m.factory == nil {
		return nil, ErrClientFactoryNil
	}
	client, err := m.factory.NewClient(m)
	if err != nil {
		return nil, fmt.Errorf("create gopls client: %w", err)
	}
	if err := client.Initialize(ctx, cfg.rootURI); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize gopls client: %w", err)
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = client.Shutdown(context.Background())
		_ = client.Close()
		return nil, ErrManagerClosed
	}
	if workspace := m.workspaces[cfg.key]; workspace != nil && workspace.client != nil {
		existing := workspace.client
		m.mu.Unlock()
		_ = client.Shutdown(context.Background())
		_ = client.Close()
		return existing, nil
	}
	m.workspaces[cfg.key] = &workspaceClient{
		key:      cfg.key,
		rootPath: cfg.rootPath,
		rootURI:  cfg.rootURI,
		client:   client,
	}
	m.mu.Unlock()
	return client, nil
}

func (m *manager) DidOpen(ctx context.Context, uri, languageID string, version int, text string) error {
	ref, err := m.resolveDocumentRef(uri, languageID)
	if err != nil {
		return err
	}
	if !shouldUseClientForLanguage(ref.languageID) {
		return nil
	}
	client, err := m.ensureClientForFile(ctx, ref.absPath, ref.languageID)
	if err != nil {
		return err
	}
	if m.pool != nil {
		m.pool.acquire(client)
		defer m.pool.release(client)
	}
	return client.DidOpen(ctx, ref.uri, ref.languageID, version, text)
}

func (m *manager) DidChange(ctx context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	ref, err := m.resolveDocumentRef(uri, "")
	if err != nil {
		return err
	}
	if !shouldUseClientForLanguage(ref.languageID) {
		return nil
	}
	client, err := m.ensureClientForFile(ctx, ref.absPath, ref.languageID)
	if err != nil {
		return err
	}
	if m.pool != nil {
		m.pool.acquire(client)
		defer m.pool.release(client)
	}
	return client.DidChange(ctx, ref.uri, version, changes)
}

func (m *manager) DidClose(ctx context.Context, uri string) error {
	ref, err := m.resolveDocumentRef(uri, "")
	if err != nil {
		return err
	}
	if !shouldUseClientForLanguage(ref.languageID) {
		return nil
	}
	client, err := m.ensureClientForFile(ctx, ref.absPath, ref.languageID)
	if err != nil {
		return err
	}
	if m.pool != nil {
		m.pool.acquire(client)
		defer m.pool.release(client)
	}
	return client.DidClose(ctx, ref.uri)
}

func (m *manager) BootstrapDocument(ctx context.Context, uri string) error {
	return m.bootstrapDocument(ctx, uri)
}

func (m *manager) LogMessage(params protocol.LogMessageParams) error {
	if m.logger == nil {
		return nil
	}
	switch params.Type {
	case protocol.LogMessageError:
		m.logger.Error("gopls", "message", params.Message)
	case protocol.LogMessageWarning:
		m.logger.Warn("gopls", "message", params.Message)
	default:
		m.logger.Debug("gopls", "message", params.Message)
	}
	return nil
}

func (m *manager) request(ctx context.Context, client Client, method string, params any) (json.RawMessage, error) {
	if client == nil {
		return nil, fmt.Errorf("request %s: client is nil", method)
	}
	if m.pool != nil {
		m.pool.acquire(client)
		defer m.pool.release(client)
	}
	raw, err := client.Request(ctx, method, params)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return raw, nil
}

func (m *manager) documentClient(ctx context.Context, uri string) (Client, documentRef, error) {
	ref, err := m.resolveDocumentRef(uri, "")
	if err != nil {
		return nil, documentRef{}, err
	}
	if !shouldUseClientForLanguage(ref.languageID) {
		return nil, ref, nil
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
