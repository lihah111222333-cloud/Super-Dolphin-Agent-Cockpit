package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

const (
	lspErrorContentModified       = -32801
	transientLSPRequestMaxRetries = 3
)

// request 包装一次可重试的 LSP JSON-RPC 请求。
// 只对幂等读类能力重试瞬时 content modified 或死连接；非可重放方法会先重建 client 再返回错误。
func (m *manager) request(ctx context.Context, client Client, method string, params any) (json.RawMessage, error) {
	if client == nil {
		return nil, fmt.Errorf("request %s: client is nil", method)
	}
	for attempt := 0; ; attempt++ {
		raw, err := m.requestOnce(ctx, client, method, params)
		if err == nil {
			return raw, nil
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
	m.touchWorkspaceActivity(client)
	var raw json.RawMessage
	err := m.withPooledClient(client, func() error {
		var requestErr error
		raw, requestErr = client.Request(ctx, method, params)
		return requestErr
	})
	return raw, err
}

func (m *manager) handleRequestFailure(ctx context.Context, client Client, method string, params any, err error) (json.RawMessage, error) {
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
	detached := m.detachClient(client)
	if detached == nil || detached.client == nil {
		return nil, ErrClientClosed
	}
	m.AdvanceDiagnosticGeneration()
	_ = shutdownClients([]Client{detached.client})
	cfg := workspaceConfigFromClient(*detached)
	replacement, ensureErr := m.ensureClient(ctx, cfg)
	if ensureErr != nil {
		return nil, ensureErr
	}
	if restore {
		if err := restoreBootstrappedWorkspace(ctx, m, cfg); err != nil {
			return replacement, err
		}
	}
	return replacement, nil
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
