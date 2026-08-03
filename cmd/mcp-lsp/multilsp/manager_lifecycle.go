package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
)

const managerShutdownTimeout = 5 * time.Second

// ErrClientNotBound 表示 client 在回调发送前已被 workspace 回收。
// 调用方可据此区分“尚未发送”与 transport 发送后失败，避免重放非幂等通知。
var ErrClientNotBound = errors.New("LSP client is no longer bound to an active workspace")

// ErrStaleClientLease 表示租约释放时 workspace 已被替换或摘除。
// 旧租约不能触碰新代际的 activeLeases、idleSince 或 state。
var ErrStaleClientLease = errors.New("LSP client lease belongs to a stale workspace generation")

// ErrClientNotReady 表示 workspace 仍处于 Bootstrapping，尚未通过 ready publish barrier。
var ErrClientNotReady = errors.New("LSP client is still bootstrapping")

// ErrWorkspaceLifecycleInvalid 表示生产 workspace 缺失明确代际或未知状态。
var ErrWorkspaceLifecycleInvalid = errors.New("workspace lifecycle state or generation is invalid")

// BackgroundRunner 返回由 ManagerPool 持有的后台回收 runner。
// nil manager 或 nil pool 表示当前实例没有独立后台任务，根 runner 聚合器会直接跳过。
func (m *manager) BackgroundRunner() platformrunner.Runner {
	if m == nil || m.pool == nil {
		return nil
	}
	return m.pool.RecyclerRunner()
}

// EnsureClient 为文件或语言准备可用的 LSP client。
// 传入 filePath 时必须先解析到可信 workspace；空路径才退回语言级 client，避免跨 workspace 复用。
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
	if m == nil {
		return nil
	}
	if m.pool != nil && m.pool.primary == m {
		return m.pool.Close()
	}
	return m.closeWithoutPool()
}

func (m *manager) closeWithoutPool() error {
	_, err := m.closeWithoutPoolStatus()
	return err
}

// closeWithoutPoolStatus 关闭当前 manager，并保留 Close 失败的 client 供后续调用重试。
// done 只表示所有进程级 Close 已确认完成；graceful Shutdown 错误仍通过 err 稳定返回。
func (m *manager) closeWithoutPoolStatus() (done bool, err error) {
	if m == nil {
		return true, nil
	}
	m.closeMu.Lock()
	defer m.closeMu.Unlock()
	if m.closeComplete {
		return true, m.closeResult
	}
	if !m.closeInitialized {
		m.initializeClose()
	}

	remaining, completedErr, attemptErr := retryPendingClientShutdowns(m.closingClients)
	m.closingClients = remaining
	m.closeWarnings = firstNonNilError(m.closeWarnings, completedErr)
	if len(remaining) > 0 {
		return false, firstNonNilError(m.closeWarnings, attemptErr)
	}
	m.closeComplete = true
	m.closeResult = m.closeWarnings
	return true, m.closeResult
}

// initializeClose 原子封闭新请求，等待正在创建的 client 退出后取得唯一 cleanup owner。
func (m *manager) initializeClose() {
	m.mu.Lock()
	m.closed = true
	m.retiring = true
	m.mu.Unlock()

	// 等待正在初始化的 client 先看到 closed 状态并完成清理，再统一 shutdown。
	m.waitForEnsureOperations()
	m.closingClients = m.collectAndClearClientShutdowns()
	m.AdvanceDiagnosticGeneration()
	closeBootstrapCoordinator(m)
	m.closeInitialized = true
}

func (m *manager) waitForEnsureOperations() {
	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	_ = m.closed
}

// collectAndClearClientShutdowns 取得 registered 与 provisional client 的唯一 cleanup owner。
func (m *manager) collectAndClearClientShutdowns() []pendingClientShutdown {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := len(m.workspaces)
	for _, states := range m.provisionalCleanups {
		count += len(states)
	}
	states := make([]pendingClientShutdown, 0, count)
	for _, pending := range m.provisionalCleanups {
		states = append(states, pending...)
	}
	for _, workspace := range m.workspaces {
		if workspace != nil && workspace.client != nil {
			states = append(states, pendingClientShutdown{client: workspace.client})
		}
	}
	clear(m.workspaces)
	clear(m.provisionalCleanups)
	return states
}

func firstNonNilError(current, next error) error {
	if current != nil || next == nil {
		return current
	}
	return next
}

type leasedClient struct {
	client  Client
	release func() error
}

// Release 释放锁、租约或资源。
func (l leasedClient) Release() error {
	if l.release != nil {
		return l.release()
	}
	return nil
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
	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	client, err := m.ensureClientLocked(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := m.publishClient(client); err != nil {
		return nil, errors.Join(err, m.abortUnpublishedClient(client))
	}
	return client, nil
}

func (m *manager) ensureClientForLanguage(ctx context.Context, languageID string) (Client, error) {
	cfg, err := m.resolveLanguageWorkspace(ctx, languageID)
	if err != nil {
		return nil, err
	}
	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	client, err := m.ensureClientLocked(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := m.bootstrapLanguageClient(ctx, client, cfg.rootPath, cfg.languageID); err != nil {
		return nil, errors.Join(err, m.abortUnpublishedClient(client))
	}
	if err := m.publishClient(client); err != nil {
		return nil, errors.Join(err, m.abortUnpublishedClient(client))
	}
	return client, nil
}

// publishClient 查找当前代际并通过 ready barrier 发布 client。
func (m *manager) publishClient(client Client) error {
	if m == nil || client == nil {
		return nil
	}
	m.mu.RLock()
	var key string
	var generation uint64
	for candidateKey, workspace := range m.workspaces {
		if workspace != nil && workspace.client == client {
			key, generation = candidateKey, workspace.generation
			break
		}
	}
	m.mu.RUnlock()
	if key == "" {
		return ErrClientNotBound
	}
	return m.publishWorkspaceClient(key, client, generation)
}

// abortUnpublishedClient 终结 bootstrap/publish 失败的 owner；Close 失败时保留 CleanupPending。
func (m *manager) abortUnpublishedClient(client Client) error {
	if m == nil || client == nil {
		return nil
	}
	m.mu.Lock()
	var detached *workspaceClient
	for key, workspace := range m.workspaces {
		if workspace != nil && workspace.client == client && workspace.state == workspaceStateBootstrapping {
			workspace.state = workspaceStateClosing
			delete(m.workspaces, key)
			detached = workspace
			break
		}
	}
	m.mu.Unlock()
	if detached == nil {
		return nil
	}
	shutdownErr, closeErr := shutdownWorkspaceClient(detached.client)
	if closeErr != nil {
		restoreDetachedWorkspaceClient(m, detached)
	}
	return errors.Join(shutdownErr, closeErr)
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

// bootstrapLanguageClient 为语言级 client 打开一个启动文件以触发服务器索引。
// 禁用初始 bootstrap 时直接返回；目录遍历或 DidOpen 失败会向上返回，避免假装诊断可用。
func (m *manager) bootstrapLanguageClient(ctx context.Context, client Client, root, languageID string) error {
	if m != nil && m.disableInitialWorkspaceBootstrap {
		return nil
	}
	scope, adapter, err := m.resolveLanguageScope(ctx, languageID, root, "")
	if err != nil {
		return fmt.Errorf("resolve bootstrap policy for %s: %w", languageID, err)
	}
	policy := adapter.BootstrapPolicy(scope)
	target, err := findBootstrapFileWithin(ctx, root, policy.FirstSourceExtensions, policy.IgnoredDirNames)
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
	if err := m.withBootstrapPooledClient(client, func() error {
		return client.DidOpen(ctx, fileURIFromPath(target), languageID, 0, string(content))
	}); err != nil {
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
	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	client, err := m.ensureClientLocked(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := m.publishClient(client); err != nil {
		return nil, errors.Join(err, m.abortUnpublishedClient(client))
	}
	return client, nil
}

func (m *manager) ensureClientLocked(ctx context.Context, cfg workspaceConfig) (Client, error) {
	if err := m.retryProvisionalClientCleanups(cfg.key); err != nil {
		return nil, fmt.Errorf("cleanup provisional LSP client before ensure: %w", err)
	}
	client, err := m.lookupExistingClient(cfg.key)
	if client != nil || err != nil {
		return client, err
	}

	return m.createAndRegisterClient(ctx, cfg)
}

// managerNow 返回生命周期判断使用的单调快照时间；测试可注入 clock，生产路径不缓存墙钟值。
func (m *manager) managerNow() time.Time {
	if m != nil && m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// leaseBoundClient 在同一临界区内确认绑定、更新活动时间并创建 manager-owned 租约。
// 返回后 manager 锁已释放，调用方不得持锁执行 LSP 或网络调用。
func (m *manager) leaseBoundClient(client Client) (leasedClient, bool, error) {
	return m.leaseClient(client, false)
}

// leaseBootstrappingClient 仅供 ensureMu 持有的 bootstrap 事务使用。
// 它仍写同一 workspace activeLeases/generation，但普通调用方永远不能租用 Bootstrapping。
func (m *manager) leaseBootstrappingClient(client Client) (leasedClient, bool, error) {
	return m.leaseClient(client, true)
}

func (m *manager) leaseClient(client Client, allowBootstrapping bool) (leasedClient, bool, error) {
	if client == nil {
		return leasedClient{client: client}, true, nil
	}
	if m == nil {
		return leasedClient{}, false, ErrManagerClosed
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.retiring {
		return leasedClient{}, false, ErrManagerClosed
	}
	for key, workspace := range m.workspaces {
		if workspace == nil || workspace.client != client {
			continue
		}
		if workspace.generation == 0 || workspace.state == "" {
			return leasedClient{}, false, ErrWorkspaceLifecycleInvalid
		}
		if workspace.state == workspaceStateBootstrapping && !allowBootstrapping {
			return leasedClient{}, false, ErrClientNotReady
		}
		if workspace.state == workspaceStateClosing || workspace.state == workspaceStateCleanupPending {
			return leasedClient{}, false, ErrClientNotBound
		}
		validBootstrapState := workspace.state == workspaceStateBootstrapping && allowBootstrapping
		if !validBootstrapState && workspace.state != workspaceStateActive && workspace.state != workspaceStateIdleCountdown && workspace.state != workspaceStateRecheck {
			return leasedClient{}, false, ErrWorkspaceLifecycleInvalid
		}
		workspace.activeLeases++
		workspace.lastActivity = m.managerNow()
		if workspace.state != workspaceStateBootstrapping {
			workspace.state = workspaceStateActive
			workspace.idleSince = time.Time{}
		}
		generation := workspace.generation
		var once sync.Once
		var releaseErr error
		return leasedClient{
			client: client,
			release: func() error {
				once.Do(func() { releaseErr = m.releaseClientLease(key, client, generation) })
				return releaseErr
			},
		}, true, nil
	}
	return leasedClient{}, false, nil
}

// withBootstrapPooledClient 在 ensureMu 事务内保护 bootstrap DidOpen；不会把 Bootstrapping 暴露给普通租约。
func (m *manager) withBootstrapPooledClient(client Client, fn func() error) error {
	if client == nil {
		return fn()
	}
	leased, ok, err := m.leaseBootstrappingClient(client)
	if err != nil {
		return err
	}
	if !ok {
		return ErrClientNotBound
	}
	defer leased.Release()
	return fn()
}

// releaseClientLease 在 manager 锁内精确匹配 key/client/generation。
// stale release 只返回错误，不得改变替换后的 workspace。
func (m *manager) releaseClientLease(key string, client Client, generation uint64) error {
	if m == nil {
		return ErrStaleClientLease
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	workspace := m.workspaces[key]
	if workspace == nil || workspace.client != client || workspace.generation != generation {
		return ErrStaleClientLease
	}
	if workspace.activeLeases <= 0 {
		return fmt.Errorf("release client lease %s: active lease underflow", key)
	}
	workspace.activeLeases--
	now := m.managerNow()
	workspace.lastActivity = now
	if workspace.activeLeases == 0 {
		if workspace.state != workspaceStateBootstrapping && workspace.state != workspaceStateClosing && workspace.state != workspaceStateCleanupPending {
			workspace.state = workspaceStateIdleCountdown
			workspace.idleSince = now
		} else if workspace.state == workspaceStateBootstrapping {
			workspace.idleSince = time.Time{}
		}
	} else if workspace.state != workspaceStateBootstrapping {
		workspace.state = workspaceStateActive
		workspace.idleSince = time.Time{}
	}
	return nil
}

// publishWorkspaceClient 是 Bootstrapping -> Active/IdleCountdown 的唯一 ready barrier。
// initialize 或 factory 返回本身不会产生 idleSince。
func (m *manager) publishWorkspaceClient(key string, client Client, generation uint64) error {
	if m == nil {
		return ErrStaleClientLease
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	workspace := m.workspaces[key]
	if workspace == nil || workspace.client != client || workspace.generation != generation {
		return ErrStaleClientLease
	}
	if workspace.state == workspaceStateClosing || workspace.state == workspaceStateCleanupPending {
		return ErrClientNotBound
	}
	firstPublish := workspace.publishedAt.IsZero()
	if firstPublish {
		workspace.publishedAt = m.managerNow()
	}
	if workspace.activeLeases == 0 {
		workspace.state = workspaceStateIdleCountdown
		if firstPublish || workspace.idleSince.IsZero() {
			workspace.idleSince = workspace.publishedAt
		}
	} else {
		workspace.state = workspaceStateActive
		workspace.idleSince = time.Time{}
	}
	workspace.lastActivity = m.managerNow()
	return nil
}

// idleEligible 是唯一的生命周期销毁资格判定；容量、RSS 或 probe 不能绕过完整 idle window。
func idleEligible(workspace *workspaceClient, now time.Time, timeout time.Duration) bool {
	if workspace == nil || workspace.client == nil || now.IsZero() || timeout <= 0 {
		return false
	}
	if workspace.activeLeases != 0 || workspace.idleSince.IsZero() || now.Before(workspace.idleSince) {
		return false
	}
	if workspace.generation == 0 || workspace.state == workspaceStateBootstrapping || workspace.state == workspaceStateClosing || workspace.state == workspaceStateCleanupPending {
		return false
	}
	return (workspace.state == workspaceStateIdleCountdown || workspace.state == workspaceStateRecheck) && now.Sub(workspace.idleSince) >= timeout
}

// lookupExistingClient 返回健康的已缓存 workspace client。
// 发现死 client 时会先摘除、推进诊断代际并关闭旧实例，后续请求再创建新 client。
func (m *manager) lookupExistingClient(key string) (Client, error) {
	m.mu.RLock()
	if m.closed || m.retiring {
		m.mu.RUnlock()
		return nil, ErrManagerClosed
	}
	if workspace := m.workspaces[key]; workspace != nil && workspace.client != nil {
		if workspace.generation == 0 || workspace.state == "" {
			m.mu.RUnlock()
			return nil, ErrWorkspaceLifecycleInvalid
		}
		if workspace.state == workspaceStateBootstrapping {
			m.mu.RUnlock()
			return nil, ErrClientNotReady
		}
		if workspace.state == workspaceStateClosing || workspace.state == workspaceStateCleanupPending {
			m.mu.RUnlock()
			return nil, ErrClientNotBound
		}
		client := workspace.client
		if clientHealthy(client) {
			m.mu.RUnlock()
			return client, nil
		}
		m.mu.RUnlock()
		detached := m.detachWorkspaceClient(key, client)
		if detached != nil && detached.client != nil {
			m.AdvanceDiagnosticGeneration()
			shutdownErr, closeErr := shutdownWorkspaceClient(detached.client)
			if closeErr != nil {
				restoreDetachedWorkspaceClient(m, detached)
				return nil, errors.Join(
					fmt.Errorf("close unhealthy LSP client before rebuild: %w", closeErr),
					shutdownErr,
				)
			}
		}
		return nil, nil
	}
	m.mu.RUnlock()
	return nil, nil
}

func shutdownWorkspaceClient(client Client) (error, error) {
	shutCtx, cancel := platformconfig.WithTimeout(context.Background(), managerShutdownTimeout)
	shutdownErr := client.Shutdown(shutCtx)
	cancel()
	return shutdownErr, client.Close()
}

// restoreDetachedWorkspaceClient 在进程级 Close 失败时归还唯一 cleanup owner。
func restoreDetachedWorkspaceClient(mgr *manager, workspace *workspaceClient) {
	if mgr == nil || workspace == nil || workspace.client == nil {
		return
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if mgr.workspaces == nil {
		mgr.workspaces = make(map[string]*workspaceClient)
	}
	if mgr.workspaces[workspace.key] == nil {
		workspace.state = workspaceStateCleanupPending
		workspace.idleSince = time.Time{}
		mgr.workspaces[workspace.key] = workspace
	}
}

// retryProvisionalClientCleanups 在创建同 workspace client 前重试全部未完成 cleanup。
// Close 尚未成功时 owner 会归还 manager，调用方不得继续创建第二个 client。
func (m *manager) retryProvisionalClientCleanups(key string) error {
	states := m.takeProvisionalClientCleanups(key)
	remaining, cleanupErr := retryProvisionalClientShutdowns(states)
	m.retainProvisionalClientCleanups(key, remaining)
	return cleanupErr
}

// cleanupProvisionalClient 清理尚未登记的 client，并在进程级 Close 失败时保留唯一 owner。
func (m *manager) cleanupProvisionalClient(key string, client Client, initialized bool) error {
	if client == nil {
		return nil
	}
	state := pendingClientShutdown{client: client, shutdownDone: !initialized}
	remaining, cleanupErr := retryProvisionalClientShutdowns([]pendingClientShutdown{state})
	m.retainProvisionalClientCleanups(key, remaining)
	return cleanupErr
}

// retryProvisionalClientShutdowns 重试 provisional client cleanup，保留 Shutdown 与 Close 的全部错误。
func retryProvisionalClientShutdowns(states []pendingClientShutdown) (remaining []pendingClientShutdown, cleanupErr error) {
	remaining = make([]pendingClientShutdown, 0, len(states))
	for _, state := range states {
		if state.client == nil {
			continue
		}
		if !state.shutdownDone {
			shutCtx, cancel := platformconfig.WithTimeout(context.Background(), managerShutdownTimeout)
			state.shutdownErr = state.client.Shutdown(shutCtx)
			cancel()
			state.shutdownDone = true
		}
		closeErr := state.client.Close()
		cleanupErr = errors.Join(cleanupErr, state.shutdownErr, closeErr)
		if closeErr != nil {
			remaining = append(remaining, state)
		}
	}
	return remaining, cleanupErr
}

// takeProvisionalClientCleanups 原子摘取指定 workspace 的 pending cleanup owner。
func (m *manager) takeProvisionalClientCleanups(key string) []pendingClientShutdown {
	m.mu.Lock()
	defer m.mu.Unlock()
	states := m.provisionalCleanups[key]
	delete(m.provisionalCleanups, key)
	return states
}

// retainProvisionalClientCleanups 归还 Close 仍失败的 provisional client owner。
func (m *manager) retainProvisionalClientCleanups(key string, states []pendingClientShutdown) {
	if len(states) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.provisionalCleanups == nil {
		m.provisionalCleanups = make(map[string][]pendingClientShutdown)
	}
	m.provisionalCleanups[key] = append(m.provisionalCleanups[key], states...)
}

// joinProvisionalClientError 合并主流程失败与 provisional cleanup 失败并保留根因链。
func joinProvisionalClientError(primary, cleanupErr error) error {
	if cleanupErr == nil {
		return primary
	}
	return errors.Join(primary, fmt.Errorf("cleanup provisional LSP client: %w", cleanupErr))
}

// createAndRegisterClient 创建、初始化并登记新的 workspace client。
// 任一登记前失败都会同步尝试 cleanup；Close 失败时保留 owner 供 EnsureClient 或 Manager.Close 重试。
func (m *manager) createAndRegisterClient(ctx context.Context, cfg workspaceConfig) (Client, error) {
	if m.factory == nil {
		return nil, ErrClientFactoryNil
	}
	capturedGen := m.diagGeneration.Load()
	handler := managerNotificationHandler{
		captureDiagnostics: func(params protocol.PublishDiagnosticsParams) (capturedPublishDiagnostics, error) {
			return m.capturePublishDiagnostics(params, capturedGen)
		},
		publishDiagnostics: m.publishCapturedDiagnostics,
		logMessage:         m.LogMessage,
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
		cleanupErr := m.cleanupProvisionalClient(cfg.key, client, false)
		return nil, joinProvisionalClientError(fmt.Errorf("initialize LSP client: %w", err), cleanupErr)
	}

	m.mu.Lock()
	if m.closed || m.retiring {
		m.mu.Unlock()
		cleanupErr := m.cleanupProvisionalClient(cfg.key, client, true)
		return nil, joinProvisionalClientError(ErrManagerClosed, cleanupErr)
	}
	if workspace := m.workspaces[cfg.key]; workspace != nil && workspace.client != nil {
		existing := workspace.client
		m.mu.Unlock()
		cleanupErr := m.cleanupProvisionalClient(cfg.key, client, true)
		return existing, joinProvisionalClientError(nil, cleanupErr)
	}
	generation := m.workspaceGeneration.Add(1)
	m.workspaces[cfg.key] = &workspaceClient{
		key:              cfg.key,
		rootPath:         cfg.rootPath,
		rootURI:          cfg.rootURI,
		languageID:       cfg.languageID,
		env:              append([]string(nil), cfg.env...),
		workspaceFolders: cloneWorkspaceFolders(cfg.workspaceFolders),
		client:           client,
		generation:       generation,
		state:            workspaceStateBootstrapping,
	}
	m.mu.Unlock()
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

// ReopenDocumentForDiagnostics 从磁盘读取当前文档，并用同一 client 强制发送 didClose/didOpen。
// 调用方应先完成正常 bootstrap；失败时保留诊断缺失状态，禁止返回旧快照。
func (m *manager) ReopenDocumentForDiagnostics(ctx context.Context, uri string) error {
	m.diagnosticReopenMu.Lock()
	defer m.diagnosticReopenMu.Unlock()

	ref, cfg, err := m.bootstrapTarget(ctx, uri)
	if err != nil {
		return err
	}
	snapshot, err := readDocumentSnapshot(ref)
	if err != nil {
		return err
	}
	coordinator, err := bootstrapCoordinatorFor(m)
	if err != nil {
		return err
	}
	if err := coordinator.reopenSnapshotForDiagnostics(ctx, m, cfg, snapshot); err != nil {
		return fmt.Errorf("reopen %s for diagnostics: %w", ref.uri, err)
	}
	return nil
}

func (m *manager) invalidateDocumentDiagnosticsForReopen(scope ResolvedLSPToolScope, uri string) {
	key := diagnosticStoreKeyFor(scope, uri).String()
	m.diagMu.Lock()
	defer m.diagMu.Unlock()
	if m.diagnosticEpochs == nil {
		m.diagnosticEpochs = make(map[string]uint64)
	}
	m.diagnosticEpochs[key]++
	delete(m.diagnostics, key)
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

// recoverFullDocumentDidChange 在全文 DidChange 失败后尝试用 DidClose+DidOpen 恢复文档状态。
// 原 client 恢复失败时才重建 client，并把原始错误、重开错误和重建错误合并返回。
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
	replacement, rebuildErr := m.rebuildClientAfterFailure(ctx, client, true)
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

// recordFullDocumentDidChange 将成功的全文 DidChange 写入 bootstrap cache 并推进诊断 epoch。
// 缓存记录包含指纹、mtime 和 scope，后续诊断刷新可判断文档是否仍是同一内容版本。
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
	m.advanceDocumentDiagnosticEpoch(scope, ref.uri)
	m.deleteDiagnosticsOlderThanVersion(scope, ref.uri, version)
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

// BootstrapDocumentOpenOnly 只确保文档被打开，不等待诊断稳定。
// 该入口供只需要 LSP 建立文档上下文的工具使用，避免被诊断轮询额外阻塞。
func (m *manager) BootstrapDocumentOpenOnly(ctx context.Context, uri string) error {
	return m.bootstrapDocumentOpenOnly(ctx, uri)
}

// LogMessage 将 LSP 日志通知映射到项目日志等级。
// 没有 logger 时丢弃通知，避免语言服务器日志影响工具调用结果。
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

// documentClientWithOptions 解析文档、执行 bootstrap，并按选项返回可用 client。
// 不受当前 manager 管理的语言返回 nil client，调用方可走静态 fallback 而不是启动无关 LSP。
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

// waitDocumentDiagnosticsReady 在有外层 deadline 时等待单文档诊断稳定。
// 显式打开的文档或不存在文件不等待，避免交互编辑路径被后台诊断轮询阻塞。
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

// isClientDeadError 判断错误是否代表底层 LSP client 已不可继续复用。
// 除显式 sentinel 外，也兼容常见进程管道关闭文本，便于触发重建而不是继续写死连接。
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
		strings.Contains(message, "broken pipe") ||
		strings.Contains(message, "connection reset") ||
		strings.Contains(message, "use of closed")
}

// detachWorkspaceClient 从 workspace map 中摘除指定 client。
// expected 用于防止并发重建后误删新 client；函数只摘除不关闭，关闭责任留给调用方。
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

// detachClient 按 client 指针从任意 workspace 中摘除缓存项。
// 它用于错误恢复路径，调用方负责后续推进诊断代际和关闭旧连接。
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
