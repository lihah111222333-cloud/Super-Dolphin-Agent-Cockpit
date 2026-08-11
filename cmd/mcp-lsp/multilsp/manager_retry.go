package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

const (
	lspErrorContentModified       = -32801
	transientLSPRequestMaxRetries = 3
)

type pendingClientShutdown struct {
	client            Client
	owner             processTreeCleanupTarget
	workspaceKey      string
	workspaceHash     string
	generation        uint64
	operationID       string
	lifecycleID       string
	shutdownDone      bool
	shutdownErr       error
	attempted         bool
	observationLogged bool
}

// retryPendingClientShutdownsWithObserver 按 owner/client 分支串行完成一次关闭事务。
func retryPendingClientShutdownsWithObserver(
	states []pendingClientShutdown,
	observe func(pendingClientShutdown, error) error,
) (remaining []pendingClientShutdown, completedErr, retryErr error) {
	remaining = make([]pendingClientShutdown, 0, len(states))
	for _, state := range states {
		if state.owner == nil && state.client == nil {
			continue
		}
		if state.owner != nil {
			state, keep, cleanupErr := retryPendingProcessTreeOwner(state, observe)
			if keep {
				remaining = append(remaining, state)
			}
			retryErr = errors.Join(retryErr, cleanupErr)
			continue
		}
		state, keep, shutdownErr, cleanupErr := retryPendingLSPClient(state, observe)
		if keep {
			remaining = append(remaining, state)
		}
		completedErr = errors.Join(completedErr, shutdownErr)
		retryErr = errors.Join(retryErr, cleanupErr)
	}
	return remaining, completedErr, retryErr
}

// retryPendingProcessTreeOwner 重试同一个 exact owner，失败时保持其 operation 身份。
func retryPendingProcessTreeOwner(state pendingClientShutdown, observe func(pendingClientShutdown, error) error) (pendingClientShutdown, bool, error) {
	cleanupErr := cleanupProcessTreeOwner(state.owner)
	if cleanupErr == nil {
		if observe != nil {
			return state, false, observe(state, nil)
		}
		return state, false, nil
	}
	if observe != nil && !state.observationLogged {
		state.observationLogged = true
		cleanupErr = errors.Join(cleanupErr, observe(state, cleanupErr))
	}
	return state, true, cleanupErr
}

// retryPendingLSPClient 完成 client Shutdown/Close；Close 失败时返回可重试状态。
func retryPendingLSPClient(state pendingClientShutdown, observe func(pendingClientShutdown, error) error) (pendingClientShutdown, bool, error, error) {
	if !state.shutdownDone {
		shutCtx, cancel := platformconfig.WithTimeout(context.Background(), managerShutdownTimeout)
		state.shutdownErr = state.client.Shutdown(shutCtx)
		cancel()
		state.shutdownDone = true
	}
	if err := state.client.Close(); err != nil {
		cleanupErr := errors.Join(state.shutdownErr, err)
		if observe != nil && !state.observationLogged {
			state.observationLogged = true
			cleanupErr = errors.Join(cleanupErr, observe(state, cleanupErr))
		}
		return state, true, nil, cleanupErr
	}
	var terminalErr error
	if observe != nil {
		terminalErr = observe(state, nil)
	}
	return state, false, state.shutdownErr, terminalErr
}

// request 包装一次可重试的 LSP JSON-RPC 请求。
// inspect/xref 读链路的单步超时会重建 client 后重试一次；其余重试仍只覆盖瞬时 content modified 或死连接。
func (m *manager) request(ctx context.Context, client Client, method string, params any) (json.RawMessage, error) {
	if client == nil {
		return nil, fmt.Errorf("request %s: client is nil", method)
	}
	for attempt := 0; ; attempt++ {
		raw, err := m.requestOnce(ctx, client, method, params)
		if err == nil {
			return raw, nil
		}
		if isRetryableNavigationTimeout(ctx, method, err) {
			retried, retryErr := m.retryRequestAfterDeadClient(ctx, client, method, params, err)
			if retryErr == nil {
				return retried, nil
			}
			return nil, fmt.Errorf("%s: %w", method, retryErr)
		}
		if isRetryableTransientLSPRequestError(method, err) && attempt < transientLSPRequestMaxRetries {
			if waitErr := m.waitBeforeTransientLSPRequestRetry(ctx, attempt); waitErr != nil {
				return nil, fmt.Errorf("%s: %w", method, errors.Join(err, waitErr))
			}
			continue
		}
		return m.handleRequestFailure(ctx, client, method, params, err)
	}
}

func (m *manager) requestOnce(ctx context.Context, client Client, method string, params any) (json.RawMessage, error) {
	var raw json.RawMessage
	err := m.withPooledClient(client, func() error {
		var requestErr error
		raw, requestErr = client.Request(ctx, method, params)
		return requestErr
	})
	return raw, err
}

// handleRequestFailure 区分发送前失绑定、可重放死连接与不可重放失败。
// 只有白名单内的幂等读请求能在发送前失绑定后按显式 URI 重建并重放。
func (m *manager) handleRequestFailure(ctx context.Context, client Client, method string, params any, err error) (json.RawMessage, error) {
	if errors.Is(err, ErrClientNotBound) {
		if !canAutoRetryDeadClientRequest(method) {
			return nil, fmt.Errorf("%s: %w", method, err)
		}
		retried, retryErr := m.retryRequestAfterUnboundClient(ctx, method, params, err)
		if retryErr != nil {
			return nil, fmt.Errorf("%s: %w", method, retryErr)
		}
		return retried, nil
	}
	if !isClientDeadError(err) {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if canAutoRetryDeadClientRequest(method) {
		if retried, retryErr := m.retryRequestAfterDeadClient(ctx, client, method, params, err); retryErr == nil {
			return retried, nil
		} else {
			err = errors.Join(err, retryErr)
		}
	} else {
		err = m.nonReplayableDeadClientError(ctx, client, err)
	}
	return nil, fmt.Errorf("%s: %w", method, err)
}

func canAutoRetryDeadClientRequest(method string) bool {
	switch method {
	case protocol.MethodHover,
		protocol.MethodDefinition,
		protocol.MethodImplementation,
		protocol.MethodTypeDefinition,
		protocol.MethodReferences,
		protocol.MethodDocumentSymbol,
		protocol.MethodCompletion,
		protocol.MethodSignatureHelp,
		protocol.MethodFoldingRange,
		protocol.MethodSemanticTokensFull,
		protocol.MethodPrepareCallHierarchy,
		protocol.MethodCallHierarchyIncoming,
		protocol.MethodCallHierarchyOutgoing,
		protocol.MethodPrepareTypeHierarchy,
		protocol.MethodTypeHierarchySupertypes,
		protocol.MethodTypeHierarchySubtypes:
		return true
	default:
		return false
	}
}

// isRetryableNavigationTimeout 只允许 inspect/xref 读链路在单步内部 deadline 后重建 client 并重试一次。
// 调用方 deadline 或取消已生效时必须立即返回，不能通过重试延长上层预算。
func isRetryableNavigationTimeout(ctx context.Context, method string, err error) bool {
	if ctx == nil || ctx.Err() != nil || !errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	switch method {
	case protocol.MethodHover,
		protocol.MethodDefinition,
		protocol.MethodImplementation,
		protocol.MethodTypeDefinition,
		protocol.MethodReferences,
		protocol.MethodDocumentSymbol,
		protocol.MethodSignatureHelp,
		protocol.MethodPrepareCallHierarchy,
		protocol.MethodCallHierarchyIncoming,
		protocol.MethodCallHierarchyOutgoing,
		protocol.MethodPrepareTypeHierarchy,
		protocol.MethodTypeHierarchySupertypes,
		protocol.MethodTypeHierarchySubtypes:
		return true
	default:
		return false
	}
}

func isRetryableTransientLSPRequestError(method string, err error) bool {
	if !canAutoRetryDeadClientRequest(method) {
		return false
	}
	var responseErr *responseError
	if !errors.As(err, &responseErr) {
		return false
	}
	return responseErr.Code == lspErrorContentModified
}

func (m *manager) waitBeforeTransientLSPRequestRetry(ctx context.Context, attempt int) error {
	delay := m.retryBaseDelay
	for range attempt {
		delay *= 2
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (m *manager) retryRequestAfterDeadClient(ctx context.Context, client Client, method string, params any, originalErr error) (json.RawMessage, error) {
	replacement, err := m.rebuildClientAfterFailure(ctx, client, true)
	if err != nil {
		return nil, errors.Join(originalErr, err)
	}
	if replacement == nil {
		return nil, errors.Join(originalErr, ErrClientClosed)
	}
	return m.requestOnce(ctx, replacement, method, params)
}

// retryRequestAfterUnboundClient 仅重放尚未发送的幂等读请求。
// workspace 必须来自请求参数中的 URI；提取失败时立即返回，禁止猜测或跨 workspace 重建。
func (m *manager) retryRequestAfterUnboundClient(
	ctx context.Context,
	method string,
	params any,
	originalErr error,
) (json.RawMessage, error) {
	cfg, err := m.workspaceConfigForRetryableRequest(ctx, params)
	if err != nil {
		return nil, errors.Join(originalErr, err)
	}
	replacement, err := m.ensureClient(ctx, cfg)
	if err != nil {
		return nil, errors.Join(originalErr, err)
	}
	if replacement == nil {
		return nil, errors.Join(originalErr, ErrClientClosed)
	}
	if err := restoreBootstrappedWorkspace(ctx, m, cfg); err != nil {
		return nil, errors.Join(originalErr, err)
	}
	raw, err := m.requestOnce(ctx, replacement, method, params)
	if err != nil {
		return nil, errors.Join(originalErr, err)
	}
	return raw, nil
}

type retryableRequestWorkspaceTarget struct {
	TextDocument struct {
		URI string
	}
	Item struct {
		URI string
	}
}

// workspaceConfigForRetryableRequest 只从请求参数的 textDocument/item URI 解析目标 workspace。
// URI 缺失、编码失败或解析失败均立即返回错误，禁止使用当前上下文猜测 workspace。
func (m *manager) workspaceConfigForRetryableRequest(ctx context.Context, params any) (workspaceConfig, error) {
	payload, err := json.Marshal(params)
	if err != nil {
		return workspaceConfig{}, fmt.Errorf("encode retryable LSP request workspace target: %w", err)
	}
	var target retryableRequestWorkspaceTarget
	if err := json.Unmarshal(payload, &target); err != nil {
		return workspaceConfig{}, fmt.Errorf("decode retryable LSP request workspace target: %w", err)
	}
	uri := target.TextDocument.URI
	if uri == "" {
		uri = target.Item.URI
	}
	if uri == "" {
		return workspaceConfig{}, fmt.Errorf("retryable LSP request %T has no document URI", params)
	}
	ref, err := m.resolveDocumentRef(ctx, uri, "")
	if err != nil {
		return workspaceConfig{}, fmt.Errorf("resolve retryable LSP request document %q: %w", uri, err)
	}
	cfg, err := m.resolveWorkspaceForDocument(ctx, ref)
	if err != nil {
		return workspaceConfig{}, fmt.Errorf("resolve retryable LSP request workspace %q: %w", uri, err)
	}
	return cfg, nil
}

func (m *manager) nonReplayableDeadClientError(ctx context.Context, client Client, err error) error {
	if repairErr := m.rebuildClientAfterNonReplayableFailure(ctx, client); repairErr != nil {
		return errors.Join(ErrClientClosed, err, repairErr)
	}
	return errors.Join(ErrClientClosed, err)
}

func (m *manager) rebuildClientAfterNonReplayableFailure(ctx context.Context, client Client) error {
	replacement, err := m.rebuildClientAfterFailure(ctx, client, true)
	if err != nil {
		return err
	}
	if replacement == nil {
		return ErrClientClosed
	}
	return nil
}

// rebuildClientAfterFailure 在 client 失效后摘除旧连接并按原 workspace 配置重建。
// restore=true 时会恢复 bootstrap 过的文档状态；诊断代际会先推进，防止旧推送覆盖新 client。
func (m *manager) rebuildClientAfterFailure(ctx context.Context, client Client, restore bool) (Client, error) {
	m.ensureMu.Lock()
	detached := m.detachClient(client)
	if detached == nil || detached.client == nil {
		m.ensureMu.Unlock()
		return nil, ErrClientClosed
	}
	m.AdvanceDiagnosticGeneration()
	shutdownErr, closeErr := shutdownWorkspaceClient(detached.client)
	if closeErr != nil {
		restoreDetachedWorkspaceClient(m, detached)
		m.ensureMu.Unlock()
		return nil, errors.Join(
			fmt.Errorf("close failed LSP client before rebuild: %w", closeErr),
			shutdownErr,
		)
	}
	cfg := workspaceConfigFromClient(*detached)
	m.ensureMu.Unlock()
	replacement, ensureErr := m.ensureClient(ctx, cfg)
	if ensureErr != nil {
		return nil, errors.Join(shutdownErr, ensureErr)
	}
	if restore {
		if err := restoreBootstrappedWorkspace(ctx, m, cfg); err != nil {
			return replacement, errors.Join(shutdownErr, err)
		}
	}
	return replacement, shutdownErr
}

func workspaceConfigFromClient(workspace workspaceClient) workspaceConfig {
	return workspaceConfig{
		key:              workspace.key,
		rootPath:         workspace.rootPath,
		rootURI:          workspace.rootURI,
		languageID:       workspace.languageID,
		env:              append([]string(nil), workspace.env...),
		initOptions:      cloneAnyMap(workspace.initOptions),
		workspaceFolders: cloneWorkspaceFolders(workspace.workspaceFolders),
	}
}

// leaseBoundClient 在同一临界区内确认绑定、更新活动时间并创建 manager-owned 租约。
// 返回后 manager 锁已释放，调用方不得持锁执行 LSP 或网络调用。
func (m *manager) leaseBoundClient(client Client) (leasedClient, bool, error) {
	return m.leaseClient(client, false)
}

// leaseBootstrappingClient 仅供 publish 前的 bootstrap owner 使用。
// bootstrap RPC 在 ensureMu 外运行；租约仍绑定同一 workspace generation，普通调用方不能触碰 Bootstrapping。
func (m *manager) leaseBootstrappingClient(client Client) (leasedClient, bool, error) {
	return m.leaseClient(client, true)
}

// leaseClient 按 workspace 代际创建 manager-owned 租约；bootstrap 事务才能放行 Bootstrapping 状态。
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
		if err := validateLeaseWorkspace(workspace, allowBootstrapping); err != nil {
			return leasedClient{}, false, err
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

// validateLeaseWorkspace 校验租约目标的代际与生命周期状态，阻止普通路径触碰未发布 client。
func validateLeaseWorkspace(workspace *workspaceClient, allowBootstrapping bool) error {
	if workspace.generation == 0 || workspace.state == "" {
		return ErrWorkspaceLifecycleInvalid
	}
	if workspace.state == workspaceStateBootstrapping && !allowBootstrapping {
		return ErrClientNotReady
	}
	if workspace.state == workspaceStateClosing || workspace.state == workspaceStateCleanupPending {
		return ErrClientNotBound
	}
	if workspace.state == workspaceStateBootstrapping && allowBootstrapping {
		return nil
	}
	switch workspace.state {
	case workspaceStateActive, workspaceStateIdleCountdown, workspaceStateRecheck:
		return nil
	default:
		return ErrWorkspaceLifecycleInvalid
	}
}

// withBootstrapPooledClient 在不持 ensureMu 时保护 bootstrap DidOpen；不会把 Bootstrapping 暴露给普通租约。
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
	return errors.Join(fn(), leased.Release())
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
		return markWorkspaceLeaseIdle(workspace, now)
	}
	if workspace.state != workspaceStateBootstrapping {
		workspace.state = workspaceStateActive
		workspace.idleSince = time.Time{}
	}
	return nil
}

func markWorkspaceLeaseIdle(workspace *workspaceClient, now time.Time) error {
	switch workspace.state {
	case workspaceStateBootstrapping:
		workspace.idleSince = time.Time{}
	case workspaceStateClosing, workspaceStateCleanupPending:
		return nil
	default:
		workspace.state = workspaceStateIdleCountdown
		workspace.idleSince = now
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
	if m.closed || m.retiring {
		return ErrManagerClosed
	}
	workspace := m.workspaces[key]
	if err := validatePublishWorkspace(workspace, client, generation); err != nil {
		return err
	}
	firstPublish := workspace.publishedAt.IsZero()
	if firstPublish {
		workspace.publishedAt = m.managerNow()
	}
	applyPublishedWorkspaceState(workspace, firstPublish)
	workspace.lastActivity = m.managerNow()
	return nil
}

// validatePublishWorkspace 确认 ready barrier 仍绑定同一 client 代际且未进入清理状态。
func validatePublishWorkspace(workspace *workspaceClient, client Client, generation uint64) error {
	if workspace == nil || workspace.client != client || workspace.generation != generation {
		return ErrStaleClientLease
	}
	if workspace.state == workspaceStateClosing || workspace.state == workspaceStateCleanupPending {
		return ErrClientNotBound
	}
	return nil
}

func applyPublishedWorkspaceState(workspace *workspaceClient, firstPublish bool) {
	if workspace.activeLeases == 0 {
		workspace.state = workspaceStateIdleCountdown
		if firstPublish || workspace.idleSince.IsZero() {
			workspace.idleSince = workspace.publishedAt
		}
		return
	}
	workspace.state = workspaceStateActive
	workspace.idleSince = time.Time{}
}

// idleEligible 是唯一的生命周期销毁资格判定；容量、RSS 或 probe 不能绕过完整 idle window。
func idleEligible(workspace *workspaceClient, now time.Time, timeout time.Duration) bool {
	if !idleWorkspaceBaseValid(workspace, now, timeout) {
		return false
	}
	return isIdleCountdownState(workspace.state) && now.Sub(workspace.idleSince) >= timeout
}

// idleWorkspaceBaseValid 校验租约归零、时间窗口和生命周期状态等共同前置条件。
func idleWorkspaceBaseValid(workspace *workspaceClient, now time.Time, timeout time.Duration) bool {
	if workspace == nil || workspace.client == nil || now.IsZero() || timeout <= 0 {
		return false
	}
	if workspace.activeLeases != 0 || workspace.idleSince.IsZero() || now.Before(workspace.idleSince) {
		return false
	}
	return workspace.generation != 0 && !isNonIdleLifecycleState(workspace.state)
}

func isNonIdleLifecycleState(state workspaceLifecycleState) bool {
	switch state {
	case workspaceStateBootstrapping, workspaceStateClosing, workspaceStateCleanupPending:
		return true
	default:
		return false
	}
}

func isIdleCountdownState(state workspaceLifecycleState) bool {
	return state == workspaceStateIdleCountdown || state == workspaceStateRecheck
}
