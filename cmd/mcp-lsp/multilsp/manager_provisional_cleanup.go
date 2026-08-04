package multilsp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

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
	prepared, prepareErr := m.preparePendingCleanupAttempts(states)
	if prepareErr != nil {
		m.retainProvisionalClientCleanups(key, prepared)
		return prepareErr
	}
	states = prepared
	remaining, completedErr, retryErr := retryPendingClientShutdownsWithObserver(states, m.observeCleanupFailure)
	m.retainProvisionalClientCleanups(key, remaining)
	return errors.Join(completedErr, retryErr)
}

// cleanupProvisionalClient 清理尚未登记的 client，并在进程级 Close 失败时保留唯一 owner。
func (m *manager) cleanupProvisionalClient(key string, generation uint64, client Client, initialized bool) error {
	if client == nil {
		return nil
	}
	state, stateErr := m.newPendingClientShutdown(key, generation, client, nil)
	if stateErr != nil {
		m.retainProvisionalClientCleanups(key, []pendingClientShutdown{state})
		return stateErr
	}
	state.shutdownDone = !initialized
	prepared, prepareErr := m.preparePendingCleanupAttempts([]pendingClientShutdown{state})
	if prepareErr != nil {
		m.retainProvisionalClientCleanups(key, prepared)
		return prepareErr
	}
	state = prepared[0]
	remaining, completedErr, retryErr := retryPendingClientShutdownsWithObserver(
		[]pendingClientShutdown{state},
		m.observeCleanupFailure,
	)
	m.retainProvisionalClientCleanups(key, remaining)
	return errors.Join(completedErr, retryErr)
}

// takeProvisionalClientCleanups 原子摘取指定 workspace 的 pending cleanup owner。
func (m *manager) takeProvisionalClientCleanups(key string) []pendingClientShutdown {
	m.mu.Lock()
	defer m.mu.Unlock()
	states := m.provisionalCleanups[key]
	delete(m.provisionalCleanups, key)
	return states
}

// provisionalCleanupKeys 返回仍持有 provisional owner 的 workspace key，仅用于内部扫描。
func (m *manager) provisionalCleanupKeys() []string {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.provisionalCleanups))
	for key, states := range m.provisionalCleanups {
		if len(states) > 0 {
			keys = append(keys, key)
		}
	}
	return keys
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
	generation := m.workspaceGeneration.Add(1)
	client, err := newClientFromFactory(m.factory, cfg, handler)
	if err != nil {
		return nil, m.handleClientFactoryFailure(cfg.key, generation, err)
	}
	configureClientWorkspace(client, cfg)
	if err := client.Initialize(ctx, cfg.rootURI); err != nil {
		cleanupErr := m.cleanupProvisionalClient(cfg.key, generation, client, false)
		return nil, joinProvisionalClientError(fmt.Errorf("initialize LSP client: %w", err), cleanupErr)
	}

	m.mu.Lock()
	if m.closed || m.retiring {
		m.mu.Unlock()
		cleanupErr := m.cleanupProvisionalClient(cfg.key, generation, client, true)
		return nil, joinProvisionalClientError(ErrManagerClosed, cleanupErr)
	}
	if workspace := m.workspaces[cfg.key]; workspace != nil && workspace.client != nil {
		existing := workspace.client
		m.mu.Unlock()
		cleanupErr := m.cleanupProvisionalClient(cfg.key, generation, client, true)
		return existing, joinProvisionalClientError(nil, cleanupErr)
	}
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

// handleClientFactoryFailure 保留 factory 交出的 exact owner，并把不可观测的失败直接阻断。
func (m *manager) handleClientFactoryFailure(key string, generation uint64, factoryErr error) error {
	wrappedErr := fmt.Errorf("create LSP client: %w", factoryErr)
	owner := processTreeCleanupOwnerFromError(factoryErr)
	if owner == nil {
		return wrappedErr
	}
	state, stateErr := m.newPendingClientShutdown(key, generation, nil, owner)
	if stateErr != nil {
		m.retainProvisionalClientCleanups(key, []pendingClientShutdown{state})
		return errors.Join(wrappedErr, stateErr)
	}
	prepared, prepareErr := m.preparePendingCleanupAttempts([]pendingClientShutdown{state})
	if prepareErr != nil {
		m.retainProvisionalClientCleanups(key, prepared)
		return errors.Join(wrappedErr, prepareErr)
	}
	state = prepared[0]
	observationErr := m.observeCleanupFailure(state, factoryErr)
	state.observationLogged = true
	m.retainProvisionalClientCleanups(key, []pendingClientShutdown{state})
	return errors.Join(wrappedErr, observationErr)
}

// ensureProcessObserver lazily creates the process-local, no-signal observer.
// It deliberately has no durable receipt or process-control capability.
func (m *manager) ensureProcessObserver() (*processobserve.Observer, error) {
	if m == nil {
		return nil, errors.New("process observer manager is nil")
	}
	m.processObserverMu.Lock()
	defer m.processObserverMu.Unlock()
	if m.processObserver != nil {
		return m.processObserver, nil
	}
	if m.processObservationStore == nil {
		m.processObservationStore = processobserve.NewMemoryStore()
	}
	observer, err := processobserve.NewObserver(m.processObservationStore)
	if err != nil {
		return nil, fmt.Errorf("initialize process observer: %w", err)
	}
	m.processObserver = observer
	return observer, nil
}

type processTreeIdentityOwner interface {
	Identity() (hiddenexec.ProcessIdentity, error)
}

type processTreePIDOwner interface {
	PID() int
}

// processTreeOwnerIdentity 读取 owner 启动身份；缺少接口时返回明确的身份证据缺口。
func processTreeOwnerIdentity(owner processTreeCleanupTarget) (hiddenexec.ProcessIdentity, error, bool) {
	if owner == nil {
		return hiddenexec.ProcessIdentity{}, errors.New("process-tree owner is unavailable"), false
	}
	identityOwner, ok := owner.(processTreeIdentityOwner)
	if !ok {
		return hiddenexec.ProcessIdentity{}, errors.New("process-tree owner identity is unavailable"), false
	}
	identity, err := identityOwner.Identity()
	return identity, err, true
}

// processTreeOwnerPID 只接受大于 1 的 owner PID，拒绝制造 PID 0 观测。
func processTreeOwnerPID(owner processTreeCleanupTarget) (int, bool) {
	if owner == nil {
		return 0, false
	}
	if pidOwner, ok := owner.(processTreePIDOwner); ok {
		pid := pidOwner.PID()
		return pid, pid > 1
	}
	return 0, false
}

// observeCleanupFailure 按 exact owner/identity 证据选择回收挂起或只读观察路径。
func (m *manager) observeCleanupFailure(state pendingClientShutdown, cleanupErr error) error {
	if cleanupErr == nil {
		return m.logCleanupTerminal(state)
	}
	if state.owner == nil {
		return m.logCleanupPair(state, "lsp_cleanup_pending", "cleanup_pending", cleanupErr)
	}
	if identity, identityErr, hasIdentity := processTreeOwnerIdentity(state.owner); hasIdentity {
		if identityErr == nil && identity.PID > 1 {
			return m.logCleanupPair(state, "lsp_cleanup_pending", "cleanup_pending", cleanupErr)
		}
		if pid, ok := processTreeOwnerPID(state.owner); ok {
			reason := "identity_uncertain"
			if identityErr == nil {
				reason = "identity_unavailable"
			}
			return m.observeProcessPID(state, pid, reason)
		}
		reason := "identity_unavailable"
		if identityErr != nil {
			reason = "identity_uncertain"
		}
		return m.logCleanupPair(state, "lsp_cleanup_pending", reason, errors.Join(cleanupErr, identityErr))
	}
	if pid, ok := processTreeOwnerPID(state.owner); ok {
		return m.observeProcessPID(state, pid, "identity_unavailable")
	}
	return m.logCleanupPair(state, "lsp_cleanup_pending", "identity_unavailable", cleanupErr)
}

// observeProcessPID 以有界只读探测记录 PID 证据，不执行任何信号动作。
func (m *manager) observeProcessPID(state pendingClientShutdown, pid int, reasonOverride string) error {
	observer, err := m.ensureProcessObserver()
	if err != nil {
		return err
	}
	ctx, cancel := platformconfig.WithTimeout(context.Background(), time.Second)
	defer cancel()
	decisions, observeErr := observer.ObservePID(ctx, pid)
	m.logProcessObservationPair(state, decisions, reasonOverride)
	return observeErr
}

// logProcessObservationPair 输出同一 manager operation 下的 candidate/blocked 观测对。
func (m *manager) logProcessObservationPair(state pendingClientShutdown, decisions []processobserve.Decision, reasonOverride string) {
	if m == nil || m.logger == nil {
		return
	}
	for _, decision := range decisions {
		reasonValue := any(decision.Reason())
		if reasonOverride != "" {
			reasonValue = reasonOverride
		}
		base := []any{
			"operation_id", state.operationID,
			"lifecycle_id", state.lifecycleID,
			"workspace_hash", state.workspaceHashValue(),
			"generation", state.generation,
			"observation_operation_id", decision.OperationID(),
			"observation_event_id", decision.EventID(),
			"reason", reasonValue,
			"observer_reason", decision.Reason(),
			"signal_sent", false,
		}
		candidate := append(append([]any(nil), base...), "event", decision.CandidateProjection().Event())
		blocked := append(append([]any(nil), base...), "event", decision.BlockedProjection().Event())
		m.logger.Warn("LSP process observation", candidate...)
		m.logger.Warn("LSP process observation", blocked...)
	}
}

// logCleanupPair 输出 known-owner cleanup_pending/reclaim_blocked 对并省略未知动作信号结论。
func (m *manager) logCleanupPair(state pendingClientShutdown, event, reason string, cleanupErr error) error {
	if m == nil || m.logger == nil {
		return nil
	}
	base := []any{
		"operation_id", state.operationID,
		"lifecycle_id", state.lifecycleID,
		"workspace_hash", state.workspaceHashValue(),
		"generation", state.generation,
		"reason", reason,
		"action_result", "unknown",
	}
	if cleanupErr != nil {
		base = append(base, platformshared.SafePayloadLogFields("cleanup_error", cleanupErr.Error())...)
		base = append(base, recyclerCleanupErrorFields(cleanupErr)...)
	}
	m.logger.Warn("LSP cleanup pending", append(append([]any(nil), base...), "event", event)...)
	m.logger.Warn("LSP cleanup pending", append(append([]any(nil), base...), "event", "lsp_reclaim_blocked")...)
	return nil
}

// logCleanupTerminal 记录成功回收的单条终态；成功不再伪造 pending/blocked 事件。
func (m *manager) logCleanupTerminal(state pendingClientShutdown) error {
	if m == nil || m.logger == nil {
		return nil
	}
	m.logger.Info("LSP cleanup completed",
		"operation_id", state.operationID,
		"lifecycle_id", state.lifecycleID,
		"workspace_hash", state.workspaceHashValue(),
		"generation", state.generation,
		"action_result", "completed",
		"event", "lsp_cleanup_succeeded",
	)
	return nil
}

// newPendingClientShutdown 分配带 manager 随机实例、workspace 哈希和代际的 cleanup 身份。
func (m *manager) newPendingClientShutdown(
	key string,
	generation uint64,
	client Client,
	owner processTreeCleanupTarget,
) (pendingClientShutdown, error) {
	sequence := m.provisionalOperation.Add(1)
	instanceID, instanceErr := m.provisionalInstanceID()
	operationID := fmt.Sprintf("lsp-provisional-%s-%d", instanceID, sequence)
	workspaceHash := provisionalWorkspaceHash(key)
	state := pendingClientShutdown{
		client:        client,
		owner:         owner,
		workspaceKey:  key,
		workspaceHash: workspaceHash,
		generation:    generation,
		operationID:   operationID,
		lifecycleID:   fmt.Sprintf("lsp-lifecycle-%s-ws-%s-g%d", instanceID, workspaceHash, generation),
	}
	if instanceErr != nil {
		state.operationID = ""
		state.lifecycleID = ""
	}
	return state, instanceErr
}

// preparePendingCleanupAttempts 为每次实际回收尝试分配新的 operation ID，并保持生命周期 ID 稳定。
func (m *manager) preparePendingCleanupAttempts(states []pendingClientShutdown) ([]pendingClientShutdown, error) {
	for index := range states {
		state := &states[index]
		if state.attempted || state.operationID == "" {
			operationID, err := m.nextProvisionalOperationID()
			if err != nil {
				return states, err
			}
			state.operationID = operationID
		}
		if state.lifecycleID == "" {
			instanceID, err := m.provisionalInstanceID()
			if err != nil {
				return states, err
			}
			state.lifecycleID = fmt.Sprintf("lsp-lifecycle-%s-ws-%s-g%d", instanceID, state.workspaceHashValue(), state.generation)
		}
		state.attempted = true
		state.observationLogged = false
	}
	return states, nil
}

func (m *manager) nextProvisionalOperationID() (string, error) {
	instanceID, err := m.provisionalInstanceID()
	if err != nil {
		return "", err
	}
	sequence := m.provisionalOperation.Add(1)
	return fmt.Sprintf("lsp-provisional-%s-%d", instanceID, sequence), nil
}

// workspaceHashValue 为旧测试状态提供稳定哈希回退，日志绝不读取原始 workspace key。
func (state pendingClientShutdown) workspaceHashValue() string {
	if state.workspaceHash != "" {
		return state.workspaceHash
	}
	return provisionalWorkspaceHash(state.workspaceKey)
}

// provisionalWorkspaceHash 复用 resource cohort 的 SHA-256 workspace 匿名化规则。
func provisionalWorkspaceHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// provisionalInstanceID 返回 manager 随机实例标识，确保 clone operation ID 不碰撞。
func (m *manager) provisionalInstanceID() (string, error) {
	if m == nil {
		return "", errors.New("provisional cleanup manager is nil")
	}
	m.processObserverMu.Lock()
	defer m.processObserverMu.Unlock()
	if m.instanceID == "" {
		return "", errors.New("provisional cleanup manager identity is empty")
	}
	return m.instanceID, nil
}

// newProvisionalInstanceID 读取加密随机实例标识并透传熵源错误。
func newProvisionalInstanceID(source io.Reader) (string, error) {
	if source == nil {
		source = rand.Reader
	}
	var raw [16]byte
	if _, err := io.ReadFull(source, raw[:]); err != nil {
		return "", fmt.Errorf("read provisional cleanup instance entropy: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// processTreeCleanupOwnerFromError 通过 errors.As 提取包装错误中的 exact cleanup owner。
func processTreeCleanupOwnerFromError(err error) processTreeCleanupTarget {
	if err == nil {
		return nil
	}
	var carrier interface {
		ProcessTreeOwner() processTreeCleanupTarget
	}
	if !errors.As(err, &carrier) {
		return nil
	}
	return carrier.ProcessTreeOwner()
}
