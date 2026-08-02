package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

const (
	lspErrorContentModified       = -32801
	transientLSPRequestMaxRetries = 3
)

type pendingClientShutdown struct {
	client       Client
	shutdownDone bool
	shutdownErr  error
}

// retryPendingClientShutdowns 对 Close 失败的 client 保留 owner；只有 Close 成功才移出重试集合。
func retryPendingClientShutdowns(states []pendingClientShutdown) (remaining []pendingClientShutdown, completedErr, retryErr error) {
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
		if err := state.client.Close(); err != nil {
			retryErr = firstNonNilError(retryErr, state.shutdownErr)
			retryErr = firstNonNilError(retryErr, err)
			remaining = append(remaining, state)
			continue
		}
		completedErr = firstNonNilError(completedErr, state.shutdownErr)
	}
	return remaining, completedErr, retryErr
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
		protocol.MethodTypeHierarchySubtypes,
		protocol.MethodWorkspaceSymbol:
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
		workspaceFolders: cloneWorkspaceFolders(workspace.workspaceFolders),
	}
}
