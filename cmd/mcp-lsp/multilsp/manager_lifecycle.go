package multilsp

import (
	"context"
	"encoding/json"
	"errors"
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
// BackgroundRunner 处理后台runner。
func (m *manager) BackgroundRunner() platformrunner.Runner {
	if m == nil || m.pool == nil {
		return nil
	}
	return m.pool.RecyclerRunner()
}

// EnsureClient 确保客户端。
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

// Close 关闭 LSP 管理器资源。
func (m *manager) Close() error {
	return m.close(true)
}

func (m *manager) closeWithoutPool() error {
	return m.close(false)
}

func (m *manager) close(closePool bool) error {
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
	if closePool && m.pool != nil && m.pool.primary == m {
		firstErr = firstNonNilError(firstErr, m.pool.closeManagersExcept(m))
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

// shutdownClients 处理shutdownclients。
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

type leasedClient struct {
	client  Client
	release func()
}

// Release 释放锁、租约或资源。
func (l leasedClient) Release() {
	if l.release != nil {
		l.release()
	}
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
	cfg, err := m.resolveLanguageWorkspace(ctx, languageID)
	if err != nil {
		return nil, err
	}
	client, err := m.ensureClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := m.bootstrapLanguageClient(ctx, client, cfg.rootPath, cfg.languageID); err != nil {
		return nil, err
	}
	return client, nil
}

func (m *manager) resolveLanguageWorkspace(ctx context.Context, languageID string) (workspaceConfig, error) {
	langID := normalizeLanguageID(languageID)
	if !m.shouldUseClientForLanguage(langID) {
		return workspaceConfig{}, fmt.Errorf("language %q is not managed by the LSP manager", languageID)
	}
	root, err := m.effectiveWorkspaceRoot(ctx)
	if err != nil {
		return workspaceConfig{}, err
	}
	if root == "" {
		return workspaceConfig{}, ErrWorkspaceRootEmpty
	}
	scope, adapter, err := m.resolveLanguageScope(ctx, langID, root, "")
	if err != nil {
		return workspaceConfig{}, err
	}
	return workspaceConfigForLanguageScope(scope, adapter)
}

// bootstrapLanguageClient 处理启动语言客户端。
func (m *manager) bootstrapLanguageClient(ctx context.Context, client Client, root, languageID string) error {
	if m != nil && m.disableInitialWorkspaceBootstrap {
		return nil
	}
	scope, adapter, err := m.resolveLanguageScope(ctx, languageID, root, "")
	if err != nil {
		return fmt.Errorf("resolve bootstrap policy for %s: %w", languageID, err)
	}
	policy := adapter.BootstrapPolicy(scope)
	target, err := findBootstrapFileWithin(root, policy.FirstSourceExtensions, policy.IgnoredDirNames)
	if err != nil {
		m.logBootstrapPolicy("bootstrap policy walk failed", "lang", languageID, "root", root, "err", err)
		return err
	}
	if target == "" {
		return nil
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("read bootstrap %s file %s: %w", languageID, target, err)
	}
	if err := client.DidOpen(ctx, fileURIFromPath(target), languageID, 0, string(content)); err != nil {
		return fmt.Errorf("bootstrap %s DidOpen %s: %w", languageID, target, err)
	}
	return nil
}

func (m *manager) logBootstrapPolicy(message string, args ...any) {
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

// leaseBoundClient 处理租约bound客户端。
func (m *manager) leaseBoundClient(client Client) (leasedClient, bool, error) {
	if client == nil {
		return leasedClient{client: client}, true, nil
	}
	if m == nil {
		return leasedClient{}, false, ErrManagerClosed
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return leasedClient{}, false, ErrManagerClosed
	}
	for _, workspace := range m.workspaces {
		if workspace != nil && workspace.client == client {
			return m.leaseClientLocked(client), true, nil
		}
	}
	return leasedClient{}, false, nil
}

func (m *manager) leaseClientLocked(client Client) leasedClient {
	leased := leasedClient{client: client}
	if m != nil && m.pool != nil && client != nil {
		m.pool.acquire(client)
		leased.release = func() {
			m.pool.release(client)
		}
	}
	return leased
}

// lookupExistingClient 处理lookupexisting客户端。
func (m *manager) lookupExistingClient(key string) (Client, error) {
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil, ErrManagerClosed
	}
	if workspace := m.workspaces[key]; workspace != nil && workspace.client != nil {
		client := workspace.client
		if clientHealthy(client) {
			m.mu.RUnlock()
			return client, nil
		}
		m.mu.RUnlock()
		detached := m.detachWorkspaceClient(key, client)
		if detached != nil && detached.client != nil {
			m.AdvanceDiagnosticGeneration()
			_ = shutdownClients([]Client{detached.client})
		}
		return nil, nil
	}
	m.mu.RUnlock()
	return nil, nil
}

// createAndRegisterClient 创建register客户端。
func (m *manager) createAndRegisterClient(ctx context.Context, cfg workspaceConfig) (Client, error) {
	if m.factory == nil {
		return nil, ErrClientFactoryNil
	}
	capturedGen := m.diagGeneration.Load()
	handler := managerNotificationHandler{
		publishDiagnostics: func(params protocol.PublishDiagnosticsParams) error {
			return m.publishDiagnosticsForGeneration(params, capturedGen)
		},
		logMessage: m.LogMessage,
	}
	if err := prepareWorkspaceDependencies(ctx, cfg); err != nil {
		return nil, err
	}
	client, err := newClientFromFactory(m.factory, cfg, handler)
	if err != nil {
		return nil, fmt.Errorf("create LSP client: %w", err)
	}
	configureClientWorkspace(client, cfg)
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
		key:              cfg.key,
		rootPath:         cfg.rootPath,
		rootURI:          cfg.rootURI,
		languageID:       cfg.languageID,
		env:              append([]string(nil), cfg.env...),
		workspaceFolders: cloneWorkspaceFolders(cfg.workspaceFolders),
		client:           client,
		lastActivity:     time.Now(),
	}
	return client, nil
}

func newClientFromFactory(factory ClientFactory, cfg workspaceConfig, handler protocol.NotificationHandler) (Client, error) {
	if len(cfg.env) > 0 {
		envFactory, ok := factory.(ClientFactoryWithEnv)
		if !ok {
			return nil, fmt.Errorf("client factory does not support environment overrides for %s", cfg.key)
		}
		return envFactory.NewClientWithEnv(cfg.rootPath, append([]string(nil), cfg.env...), handler)
	}
	return factory.NewClient(cfg.rootPath, handler)
}

func configureClientWorkspace(client Client, cfg workspaceConfig) {
	if setter, ok := client.(interface {
		setWorkspaceFolders([]protocol.WorkspaceFolder)
	}); ok {
		setter.setWorkspaceFolders(cfg.workspaceFolders)
	}
}

// DidOpen 把文档打开事件转给 LSP。
func (m *manager) DidOpen(ctx context.Context, uri, languageID string, version int, text string) error {
	openedURI := ""
	err := m.notifyDocument(ctx, uri, languageID, func(ctx context.Context, client Client, ref documentRef) error {
		openedURI = ref.uri
		return client.DidOpen(ctx, ref.uri, ref.languageID, version, text)
	})
	if err == nil && openedURI != "" {
		m.markExplicitDocumentOpen(openedURI)
	}
	return err
}

// DidChange 把文档变更事件转给 LSP。
func (m *manager) DidChange(ctx context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	ref, err := m.resolveDocumentRef(ctx, uri, "")
	if err != nil {
		return err
	}
	if !m.shouldUseClientForLanguage(ref.languageID) {
		return nil
	}
	client, err := m.ensureClientForFile(ctx, ref.absPath, ref.languageID)
	if err != nil {
		return err
	}
	m.touchWorkspaceActivity(client)
	err = m.withPooledClient(client, func() error {
		return client.DidChange(ctx, ref.uri, version, changes)
	})
	text, full := fullDocumentChangeText(changes)
	if err = m.handleDidChangeFailure(ctx, client, ref, version, text, full, err); err != nil {
		return err
	}
	if full && fileExists(ref.absPath) {
		return m.recordFullDocumentDidChange(ctx, ref, version, text)
	}
	return nil
}

func (m *manager) handleDidChangeFailure(ctx context.Context, client Client, ref documentRef, version int, text string, full bool, err error) error {
	if err == nil {
		return nil
	}
	if isClientDeadError(err) {
		return m.nonReplayableDeadClientError(ctx, client, err)
	}
	if full && fileExists(ref.absPath) {
		return m.recoverFullDocumentDidChange(ctx, client, ref, version, text, err)
	}
	return err
}

// DidClose 把文档关闭事件转给 LSP。
func (m *manager) DidClose(ctx context.Context, uri string) error {
	if err := m.notifyDocument(ctx, uri, "", func(ctx context.Context, client Client, ref documentRef) error {
		return client.DidClose(ctx, ref.uri)
	}); err != nil {
		return err
	}
	ref, _, scope, err := m.resolvedScopeForURI(ctx, uri, "")
	if err == nil {
		m.unmarkExplicitDocumentOpen(ref.uri)
		if coordinator, coordinatorErr := bootstrapCoordinatorFor(m); coordinatorErr == nil {
			coordinator.states.delete(scope.bootstrapKey(), ref.uri)
		}
	}
	return nil
}

func fullDocumentChangeText(changes []protocol.TextDocumentContentChangeEvent) (string, bool) {
	if len(changes) != 1 {
		return "", false
	}
	change := changes[0]
	if change.Range != nil || change.RangeLength != nil {
		return "", false
	}
	return change.Text, true
}

// recoverFullDocumentDidChange 恢复fulldocumentdidchange。
func (m *manager) recoverFullDocumentDidChange(ctx context.Context, client Client, ref documentRef, version int, text string, originalErr error) error {
	reopenErr := m.withPooledClient(client, func() error {
		if err := client.DidClose(ctx, ref.uri); err != nil {
			return err
		}
		return client.DidOpen(ctx, ref.uri, ref.languageID, version, text)
	})
	if reopenErr == nil {
		return nil
	}
	replacement, rebuildErr := m.rebuildClientAfterFailure(ctx, client, false)
	if rebuildErr != nil {
		return errors.Join(originalErr, reopenErr, rebuildErr)
	}
	if replacement == nil {
		return errors.Join(originalErr, reopenErr, ErrClientClosed)
	}
	if err := m.withPooledClient(replacement, func() error {
		return replacement.DidOpen(ctx, ref.uri, ref.languageID, version, text)
	}); err != nil {
		return errors.Join(originalErr, reopenErr, err)
	}
	return nil
}

// recordFullDocumentDidChange 记录fulldocumentdidchange。
func (m *manager) recordFullDocumentDidChange(ctx context.Context, ref documentRef, version int, text string) error {
	_, _, scope, err := m.resolvedScopeForURI(ctx, ref.uri, ref.languageID)
	if err != nil {
		return err
	}
	info, err := os.Stat(ref.absPath)
	if err != nil {
		return err
	}
	key := scope.cacheKey(ref.languageID, ref.uri)
	coordinator, err := bootstrapCoordinatorFor(m)
	if err != nil {
		return err
	}
	if err := coordinator.cache.Upsert(lspCacheValue{
		Key:             key,
		Version:         version,
		Fingerprint:     hashDocument([]byte(text)),
		ModTimeUnixNano: info.ModTime().UnixNano(),
		Size:            int64(len([]byte(text))),
	}); err != nil {
		return err
	}
	if err := coordinator.cache.RememberDocumentScope(ref.uri, scope, hashDocument([]byte(text))); err != nil {
		return err
	}
	coordinator.states.complete(scope.bootstrapKey(), ref.uri, hashDocument([]byte(text)), version)
	return nil
}

// BootstrapDocument 确保文档已打开并完成启动检查。
func (m *manager) BootstrapDocument(ctx context.Context, uri string) error {
	return m.bootstrapDocument(ctx, uri)
}

// BootstrapDocumentOpenOnly 处理启动document打开only。
func (m *manager) BootstrapDocumentOpenOnly(ctx context.Context, uri string) error {
	return m.bootstrapDocumentOpenOnly(ctx, uri)
}

// LogMessage 处理日志消息。
func (m *manager) LogMessage(params protocol.LogMessageParams) error {
	if m.logger == nil {
		return nil
	}
	switch effectiveLSPLogMessageType(params) {
	case protocol.LogMessageError:
		m.logger.Error("lsp", "message", params.Message)
	case protocol.LogMessageWarning:
		m.logger.Warn("lsp", "message", params.Message)
	default:
		m.logger.Debug("lsp", "message", params.Message)
	}
	return nil
}

type documentClientOptions struct {
	waitDiagnostics bool
}

func (m *manager) documentClient(ctx context.Context, uri string) (Client, documentRef, error) {
	return m.documentClientWithOptions(ctx, uri, documentClientOptions{waitDiagnostics: true})
}

func (m *manager) documentClientWithoutDiagnosticsWait(ctx context.Context, uri string) (Client, documentRef, error) {
	return m.documentClientWithOptions(ctx, uri, documentClientOptions{})
}

// documentClientWithOptions 处理带选项的document客户端。
func (m *manager) documentClientWithOptions(ctx context.Context, uri string, opts documentClientOptions) (Client, documentRef, error) {
	ref, err := m.resolveDocumentRef(ctx, uri, "")
	if err != nil {
		return nil, documentRef{}, err
	}
	if !m.shouldUseClientForLanguage(ref.languageID) {
		return nil, ref, nil
	}
	if err := m.bootstrapDocument(ctx, ref.uri); err != nil {
		return nil, documentRef{}, err
	}
	if opts.waitDiagnostics {
		if err := m.waitDocumentDiagnosticsReady(ctx, ref); err != nil {
			return nil, documentRef{}, err
		}
	}
	client, err := m.ensureClientForFile(ctx, ref.absPath, ref.languageID)
	if err != nil {
		return nil, documentRef{}, err
	}
	return client, ref, nil
}

// waitDocumentDiagnosticsReady 等待document诊断ready。
func (m *manager) waitDocumentDiagnosticsReady(ctx context.Context, ref documentRef) error {
	if _, ok := ctx.Deadline(); !ok {
		return nil
	}
	if ref.uri == "" || !m.shouldUseClientForLanguage(ref.languageID) || !fileExists(ref.absPath) {
		return nil
	}
	if m.isExplicitDocumentOpen(ref.uri) {
		return nil
	}
	return m.WaitDiagnosticsStable(ctx, []string{ref.uri})
}

func (m *manager) markExplicitDocumentOpen(uri string) {
	if m == nil || strings.TrimSpace(uri) == "" {
		return
	}
	m.explicitOpenMu.Lock()
	defer m.explicitOpenMu.Unlock()
	if m.explicitlyOpen == nil {
		m.explicitlyOpen = make(map[string]struct{})
	}
	m.explicitlyOpen[uri] = struct{}{}
}

func (m *manager) unmarkExplicitDocumentOpen(uri string) {
	if m == nil || strings.TrimSpace(uri) == "" {
		return
	}
	m.explicitOpenMu.Lock()
	defer m.explicitOpenMu.Unlock()
	delete(m.explicitlyOpen, uri)
}

func (m *manager) isExplicitDocumentOpen(uri string) bool {
	if m == nil || strings.TrimSpace(uri) == "" {
		return false
	}
	m.explicitOpenMu.RLock()
	defer m.explicitOpenMu.RUnlock()
	_, ok := m.explicitlyOpen[uri]
	return ok
}

func clientHealthy(client Client) bool {
	if client == nil {
		return false
	}
	if checked, ok := client.(HealthCheckedClient); ok {
		return checked.Healthy()
	}
	return true
}

// touchWorkspaceActivity updates the lastActivity timestamp for the
// workspace that owns the given client. This is called on every request
// and notification to track idle time for automatic shutdown.
// touchWorkspaceActivity 处理touch工作区activity。
func (m *manager) touchWorkspaceActivity(client Client) {
	if m == nil || client == nil {
		return
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, workspace := range m.workspaces {
		if workspace != nil && workspace.client == client {
			workspace.lastActivity = now
			return
		}
	}
}

// isClientDeadError 判断客户端dead错误是否可用。
func isClientDeadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrTransportClosed) || errors.Is(err, ErrClientClosed) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "transport closed") ||
		strings.Contains(message, "client closed") ||
		strings.Contains(message, "no longer bound to an active workspace") ||
		strings.Contains(message, "broken pipe") ||
		strings.Contains(message, "connection reset") ||
		strings.Contains(message, "use of closed")
}

// detachWorkspaceClient 处理detach工作区客户端。
func (m *manager) detachWorkspaceClient(key string, expected Client) *workspaceClient {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	workspace := m.workspaces[key]
	if workspace == nil || workspace.client == nil {
		return nil
	}
	if expected != nil && workspace.client != expected {
		return nil
	}
	delete(m.workspaces, key)
	return workspace
}

// detachClient 处理detach客户端。
func (m *manager) detachClient(client Client) *workspaceClient {
	if m == nil || client == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, workspace := range m.workspaces {
		if workspace != nil && workspace.client == client {
			delete(m.workspaces, key)
			return workspace
		}
	}
	return nil
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
