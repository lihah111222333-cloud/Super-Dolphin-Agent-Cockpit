package multilsp

import (
	"context"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// DidOpen 把文档打开事件转给 LSP。
func (m *manager) DidOpen(ctx context.Context, uri, languageID string, version int, text string) error {
	ref, err := m.resolveDocumentRef(ctx, uri, languageID)
	if err != nil {
		return err
	}
	if !m.shouldUseClientForLanguage(ref.languageID) {
		return nil
	}
	operation, err := m.beginDocumentOperation(ctx, ref.uri, documentOperationDidOpen)
	if err != nil {
		return err
	}
	defer operation.release()
	cfg, err := m.resolveWorkspaceForDocument(ctx, ref)
	if err != nil {
		return err
	}
	scope, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		return err
	}
	recoveryUsed := false
	client, err := m.ensureClientForDocumentMutation(ctx, cfg)
	if err != nil {
		m.logDidOpenWin122State("ensure_failed", cfg, client, recoveryUsed, err)
		// 生产 Windows clangd 可能在首次显式 open 的初始化边界返回
		// transport closed + 精确 Win122；失败 provisional owner 已由 ensure 清理，
		// 此处只允许同一 DidOpen 操作再 ensure 一次，其他错误立即返回。
		if !shouldRecoverBootstrapDidOpenWin122(ctx, nil, err) {
			return err
		}
		recoveryUsed = true
		client, err = m.ensureClientForDocumentMutation(ctx, cfg)
		m.logDidOpenWin122State("ensure_retry_result", cfg, client, recoveryUsed, err)
		if err != nil {
			return err
		}
	}
	if client == nil {
		return ErrClientClosed
	}
	identity, err := m.workspaceClientIdentityForConfig(cfg, client)
	if err != nil {
		return err
	}
	state, change, reservation, err := m.prepareExplicitDocumentOpen(ref, cfg, scope, identity, version, text)
	if err != nil {
		return err
	}
	sendErr := m.withPooledClient(client, func() error {
		return sendExplicitDocumentOpen(ctx, client, state, change, text)
	})
	if sendErr != nil {
		m.logDidOpenWin122State("did_open_failed", cfg, client, recoveryUsed, sendErr)
	}
	if sendErr != nil && !recoveryUsed && shouldRecoverBootstrapDidOpenWin122(ctx, client, sendErr) {
		reservation.cancel()
		recoveryUsed = true
		replacement, rebuildErr := m.rebuildClientAfterFailure(ctx, client, false)
		if rebuildErr != nil {
			return errors.Join(sendErr, fmt.Errorf("DidOpen Win122 client rebuild: %w", rebuildErr))
		}
		if replacement == nil {
			return errors.Join(sendErr, ErrClientClosed)
		}
		client = replacement
		identity, err = m.workspaceClientIdentityForConfig(cfg, client)
		if err != nil {
			return err
		}
		state, change, reservation, err = m.prepareExplicitDocumentOpen(ref, cfg, scope, identity, version, text)
		if err != nil {
			return err
		}
		sendErr = m.withPooledClient(client, func() error {
			return sendExplicitDocumentOpen(ctx, client, state, change, text)
		})
	}
	if sendErr != nil {
		reservation.cancel()
		return sendErr
	}
	operation.commitMutation()
	return reservation.commitForRecipient(client)
}

// logDidOpenWin122State 记录显式 DidOpen 恢复边界的低敏状态，不记录路径、正文或命令行。
// 这些字段用于区分 provisional cleanup、bootstrap candidate 与 retry budget，不改变恢复判定。
func (m *manager) logDidOpenWin122State(stage string, cfg workspaceConfig, client Client, recoveryUsed bool, err error) {
	if m == nil || m.logger == nil {
		return
	}
	m.mu.RLock()
	workspace := m.workspaces[cfg.key]
	candidatePresent := workspace != nil && workspace.client != nil
	workspaceState := "absent"
	bootstrapAttemptPresent := false
	if workspace != nil {
		workspaceState = string(workspace.state)
		bootstrapAttemptPresent = workspace.bootstrapAttempt != nil
	}
	pendingCleanupCount := len(m.provisionalCleanups[cfg.key])
	bootstrapAttemptCount := len(m.bootstrapAttempts)
	m.mu.RUnlock()
	m.logger.Info("LSP DidOpen Win122 recovery state",
		"stage", stage,
		"manager_instance_id_digest", provisionalWorkspaceHash(m.instanceID),
		"workspace_config_key_digest", provisionalWorkspaceHash(cfg.key),
		"language", cfg.languageID,
		"candidate_present", candidatePresent,
		"workspace_state", workspaceState,
		"bootstrap_attempt_present", bootstrapAttemptPresent,
		"bootstrap_attempt_count", bootstrapAttemptCount,
		"pending_cleanup_count", pendingCleanupCount,
		"retry_budget_used", recoveryUsed,
		"client_present", client != nil,
		"error_present", err != nil,
	)
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
	operation, err := m.beginDocumentOperation(ctx, ref.uri, documentOperationDidChange)
	if err != nil {
		return err
	}
	defer func() {
		if operation != nil {
			operation.release()
		}
	}()
	cfg, err := m.resolveWorkspaceForDocument(ctx, ref)
	if err != nil {
		return err
	}
	scope, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		return err
	}
	client, err := m.ensureClientForDocumentMutation(ctx, cfg)
	if err != nil {
		return err
	}
	changeErr := m.deliverDocumentChange(ctx, client, ref, cfg, scope, version, changes)
	if !isClientDeadError(changeErr) {
		return changeErr
	}
	// 重建/恢复可能再次获取同一 URI gate；先释放当前 mutation gate，避免已知 dead client 清理自锁。
	operation.release()
	operation = nil
	return m.nonReplayableDeadClientError(ctx, client, changeErr)
}

func (m *manager) deliverDocumentChange(
	ctx context.Context,
	client Client,
	ref documentRef,
	cfg workspaceConfig,
	scope ResolvedLSPToolScope,
	version int,
	changes []protocol.TextDocumentContentChangeEvent,
) error {
	identity, err := m.workspaceClientIdentityForConfig(cfg, client)
	if err != nil {
		return err
	}
	text, full := fullDocumentChangeText(changes)
	reservation, sentVersion, managed, err := m.prepareExplicitDocumentChange(
		ref, cfg, scope, identity, version, text, full,
	)
	if err != nil {
		return err
	}
	sendErr := m.withPooledClient(client, func() error {
		return client.DidChange(ctx, ref.uri, sentVersion, changes)
	})
	if managed {
		return m.finishManagedDocumentChange(ctx, client, ref, reservation, sentVersion, text, full, sendErr)
	}
	if err := m.handleDidChangeFailure(ctx, client, ref, version, text, full, sendErr); err != nil {
		return err
	}
	return m.recordFullDocumentDidChangeIfNeeded(ctx, ref, sentVersion, text, full)
}

func (m *manager) finishManagedDocumentChange(
	ctx context.Context,
	client Client,
	ref documentRef,
	reservation *explicitDocumentReservation,
	version int,
	text string,
	full bool,
	sendErr error,
) error {
	if sendErr != nil {
		// 旧的 DidClose/DidOpen/rebuild 恢复路径已退役：受管文档 wire 失败必须显式返回，禁止通知重放。
		reservation.cancel()
		return sendErr
	}
	if err := reservation.commitForRecipient(client); err != nil {
		return err
	}
	return m.recordFullDocumentDidChangeIfNeeded(ctx, ref, version, text, full)
}

func (m *manager) recordFullDocumentDidChangeIfNeeded(
	ctx context.Context,
	ref documentRef,
	version int,
	text string,
	full bool,
) error {
	if !full || !fileExists(ref.absPath) {
		return nil
	}
	return m.recordFullDocumentDidChange(ctx, ref, version, text)
}

func (m *manager) handleDidChangeFailure(ctx context.Context, client Client, ref documentRef, version int, text string, full bool, err error) error {
	if err == nil {
		return nil
	}
	if isClientDeadError(err) {
		return err
	}
	if full && fileExists(ref.absPath) {
		return m.recoverFullDocumentDidChange(ctx, client, ref, version, text, err)
	}
	return err
}

// DidClose 把文档关闭事件转给 LSP。
func (m *manager) DidClose(ctx context.Context, uri string) error {
	ref, err := m.resolveDocumentRef(ctx, uri, "")
	if err != nil {
		return err
	}
	operation, err := m.beginDocumentOperation(ctx, ref.uri, documentOperationDidClose)
	if err != nil {
		return err
	}
	defer operation.release()
	cfg, err := m.resolveWorkspaceForDocument(ctx, ref)
	if err != nil {
		return err
	}
	scope, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		return err
	}
	if err := m.failIfWorkspaceBootstrapping(cfg); err != nil {
		return err
	}
	state, managed := m.explicitDocumentForURI(ref.uri)
	if !managed {
		operation.commitMutation()
		return nil
	}
	client, bound := m.clientForExplicitDocument(state)
	if err := m.closeManagedDocumentWire(ctx, client, state, bound); err != nil {
		return err
	}
	operation.commitManagedCloseMutation()
	m.removeExplicitDocument(ref.uri)
	deleteBootstrapStateIfPresent(m, scope.bootstrapKey(), ref.uri)
	return nil
}

func (m *manager) closeManagedDocumentWire(
	ctx context.Context,
	client Client,
	state explicitDocumentState,
	bound bool,
) error {
	if !bound || !state.wireOpen {
		return nil
	}
	return m.withPooledClient(client, func() error { return client.DidClose(ctx, state.uri) })
}

func (m *manager) failIfWorkspaceBootstrapping(cfg workspaceConfig) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed || m.retiring {
		return ErrManagerClosed
	}
	workspace := m.workspaces[cfg.key]
	if workspace != nil && workspace.state == workspaceStateBootstrapping {
		return ErrClientNotReady
	}
	return nil
}

func (m *manager) applyManagedSnapshotUpdate(
	ctx context.Context,
	client Client,
	snapshot documentSnapshot,
	req *snapshotSyncRequest,
) (bool, error) {
	if handled, err := m.applyManagedReplacementSnapshot(ctx, client, snapshot, req); handled {
		return true, err
	}
	state, managed, err := m.managedSnapshotState(client, snapshot.ref.uri)
	if !managed || err != nil {
		return managed, err
	}
	if !managedSnapshotUpdateNeeded(state, snapshot, req.forceReopen) {
		req.version = state.lspVersion
		return true, nil
	}
	version := req.version
	if version <= state.lspVersion {
		version = state.lspVersion + 1
	}
	after := managedStateFromSnapshot(state, snapshot, version)
	reservation, err := m.reserveExplicitDocumentState(state, true, after)
	if err != nil {
		return true, err
	}
	callbackErr, releaseErr := m.callWithPooledClient(client, func() error {
		return m.deliverManagedSnapshotUpdate(ctx, client, state, snapshot, version, req.forceReopen, reservation)
	})
	if err := errors.Join(callbackErr, releaseErr); err != nil {
		reservation.cancel()
		return true, err
	}
	if err := reservation.commitForRecipient(client); err != nil {
		return true, err
	}
	req.version = version
	return true, nil
}

func (m *manager) applyManagedReplacementSnapshot(
	ctx context.Context,
	client Client,
	snapshot documentSnapshot,
	req *snapshotSyncRequest,
) (bool, error) {
	state, managed := m.explicitDocumentForURI(snapshot.ref.uri)
	if !managed {
		return false, nil
	}
	cfg, identity, err := m.workspaceClientConfigAndIdentity(client)
	if err != nil {
		return true, err
	}
	if err := validateManagedDocumentOwner(state, cfg, snapshot.ref.languageID); err != nil {
		return true, err
	}
	if state.clientGeneration == identity.generation {
		return false, nil
	}
	update, _, err := prepareExplicitDocumentReplacementOpen(state, identity.generation)
	if err != nil {
		return true, err
	}
	reservation, err := m.reserveExplicitDocumentState(state, true, update.after)
	if err != nil {
		return true, err
	}
	if err := m.withPooledClient(client, func() error {
		return client.DidOpen(ctx, update.after.uri, update.after.languageID, update.after.lspVersion, update.text)
	}); err != nil {
		reservation.cancel()
		return true, err
	}
	if err := reservation.commitForRecipient(client); err != nil {
		closeErr := m.withPooledClient(client, func() error { return client.DidClose(ctx, update.after.uri) })
		return true, errors.Join(err, closeErr)
	}
	req.version = update.after.lspVersion
	return true, nil
}

func validateManagedDocumentOwner(state explicitDocumentState, cfg workspaceConfig, languageID string) error {
	if state.configKey != cfg.key || state.languageID != languageID {
		return fmt.Errorf("managed document %s belongs to another workspace or language", state.uri)
	}
	return nil
}

func managedSnapshotUpdateNeeded(state explicitDocumentState, snapshot documentSnapshot, forceReopen bool) bool {
	return state.diskFingerprint != snapshot.fingerprint || forceReopen || !state.wireOpen
}

func (m *manager) deliverManagedSnapshotUpdate(
	ctx context.Context,
	client Client,
	state explicitDocumentState,
	snapshot documentSnapshot,
	version int,
	forceReopen bool,
	reservation *explicitDocumentReservation,
) error {
	wireClosed, err := sendManagedSnapshotUpdate(ctx, client, state, snapshot, version, forceReopen)
	if err == nil {
		return nil
	}
	reservation.cancel()
	if wireClosed {
		m.markManagedDocumentWireClosed(state)
	}
	return err
}

func (m *manager) openManagedSnapshot(
	ctx context.Context,
	client Client,
	cfg workspaceConfig,
	snapshot documentSnapshot,
	req *snapshotSyncRequest,
) error {
	identity, err := m.workspaceClientIdentityForConfig(cfg, client)
	if err != nil {
		return err
	}
	version := req.version
	if version <= 0 {
		version = 1
	}
	state := explicitDocumentStateForSnapshot(snapshot, cfg, req.scope, identity, version)
	reservation, err := m.reserveExplicitDocumentState(explicitDocumentState{}, false, state)
	if err != nil {
		return err
	}
	if err := m.withPooledClient(client, func() error {
		if req.refreshStaleDiagnostics {
			return client.DidChange(ctx, state.uri, version, []protocol.TextDocumentContentChangeEvent{{Text: snapshot.text}})
		}
		return client.DidOpen(ctx, state.uri, state.languageID, version, snapshot.text)
	}); err != nil {
		reservation.cancel()
		return err
	}
	if err := reservation.commitForRecipient(client); err != nil {
		closeErr := m.withPooledClient(client, func() error { return client.DidClose(ctx, state.uri) })
		return errors.Join(err, closeErr)
	}
	req.version = version
	return nil
}

func (m *manager) managedSnapshotState(client Client, uri string) (explicitDocumentState, bool, error) {
	state, managed := m.explicitDocumentForURI(uri)
	if !managed {
		return explicitDocumentState{}, false, nil
	}
	if !state.diskClean {
		return explicitDocumentState{}, true, fmt.Errorf("refuse disk bootstrap for dirty managed document %s", uri)
	}
	cfg, identity, err := m.workspaceClientConfigAndIdentity(client)
	if err != nil {
		return explicitDocumentState{}, true, err
	}
	if state.configKey != cfg.key || state.clientGeneration != identity.generation {
		return explicitDocumentState{}, true, fmt.Errorf("managed document %s belongs to a different client generation", uri)
	}
	return state, true, nil
}

func sendManagedSnapshotUpdate(
	ctx context.Context,
	client Client,
	state explicitDocumentState,
	snapshot documentSnapshot,
	version int,
	forceReopen bool,
) (bool, error) {
	if !state.wireOpen {
		return false, client.DidOpen(ctx, snapshot.ref.uri, snapshot.ref.languageID, version, snapshot.text)
	}
	if forceReopen {
		if err := client.DidClose(ctx, snapshot.ref.uri); err != nil {
			return false, err
		}
		if err := client.DidOpen(ctx, snapshot.ref.uri, snapshot.ref.languageID, version, snapshot.text); err != nil {
			return true, err
		}
		return false, nil
	}
	return false, client.DidChange(ctx, snapshot.ref.uri, version, []protocol.TextDocumentContentChangeEvent{{Text: snapshot.text}})
}

func managedStateFromSnapshot(state explicitDocumentState, snapshot documentSnapshot, version int) explicitDocumentState {
	state.absPath = snapshot.ref.absPath
	state.lspVersion = version
	state.text = ""
	state.textFingerprint = snapshot.fingerprint
	state.diskFingerprint = snapshot.fingerprint
	state.fileBacked = true
	state.diskReadable = true
	state.diskClean = true
	state.fullTextKnown = false
	state.wireOpen = true
	return state
}

func (m *manager) markManagedDocumentWireClosed(expected explicitDocumentState) {
	m.explicitOpenMu.Lock()
	defer m.explicitOpenMu.Unlock()
	current, ok := m.explicitDocuments[expected.uri]
	if !ok || current != expected {
		return
	}
	current.wireOpen = false
	m.explicitDocuments[expected.uri] = current
}
