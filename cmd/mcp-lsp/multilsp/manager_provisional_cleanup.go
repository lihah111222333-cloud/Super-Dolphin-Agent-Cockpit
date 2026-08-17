package multilsp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processobserve"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

func sameCleanPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func tracePathWithinRoot(root, target string) bool {
	if root == "" || target == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func workspaceRootCWDRelation(root, cwd string) string {
	if sameCleanPath(root, cwd) {
		return "self"
	}
	if tracePathWithinRoot(root, cwd) {
		return "ancestor"
	}
	if tracePathWithinRoot(cwd, root) {
		return "descendant"
	}
	return "unrelated"
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// recordResolvedScopeTrace 只保存 scope 组成的摘要/枚举，便于定位 key 漂移而不泄露路径或配置内容。
func recordResolvedScopeTrace(m *manager, resolved ResolvedLSPToolScope) {
	if m == nil {
		return
	}
	m.resolvedManagerKeyDigest = provisionalWorkspaceHash(resolved.ManagerKey)
	m.resolvedScopeKeyDigest = provisionalWorkspaceHash(resolved.ScopeKey)
	m.resolvedWorkspaceKeyDigest = provisionalWorkspaceHash(resolved.WorkspaceKey)
	m.resolvedAgentIDPresent = strings.TrimSpace(resolved.AgentID) != ""
	m.resolvedThreadIDPresent = strings.TrimSpace(resolved.ThreadID) != ""
	m.turnCallExcluded = true
	m.workspaceRootEqualsCWD = sameCleanPath(resolved.WorkspaceRoot, resolved.CWD)
	m.workspaceRootVsCWDRelation = workspaceRootCWDRelation(resolved.WorkspaceRoot, resolved.CWD)
	m.workspaceRootDepth = strings.Count(filepath.Clean(resolved.WorkspaceRoot), string(filepath.Separator))
	m.workspaceRootUTF16Units = len(utf16.Encode([]rune(resolved.WorkspaceRoot)))
	m.targetWithinWorkspace = tracePathWithinRoot(resolved.WorkspaceRoot, resolved.TargetPath)
	m.workspaceRootsCount = len(resolved.WorkspaceRoots)
	if strings.EqualFold(resolved.LanguageID, "java") {
		m.workspaceHasPOM = pathExists(filepath.Join(resolved.WorkspaceRoot, "pom.xml"))
		m.workspaceHasGradle = pathExists(filepath.Join(resolved.WorkspaceRoot, "build.gradle")) || pathExists(filepath.Join(resolved.WorkspaceRoot, "build.gradle.kts"))
		m.workspaceHasGradleKTS = pathExists(filepath.Join(resolved.WorkspaceRoot, "build.gradle.kts"))
	}
	m.resolvedLanguageID = resolved.LanguageID
	m.resolvedRootKind = string(resolved.RootKind)
	m.workspaceRootDigest = provisionalWorkspaceHash(resolved.WorkspaceRoot)
	m.languageWorkspaceRootDigest = provisionalWorkspaceHash(resolved.LanguageWorkspaceRoot)
	m.projectRootDigest = provisionalWorkspaceHash(resolved.ProjectRoot)
	m.languageSpecificEmpty = len(resolved.LanguageSpecific) == 0
	if encoded, err := json.Marshal(resolved.LanguageSpecific); err == nil {
		m.languageSpecificDigest = provisionalWorkspaceHash(string(encoded))
	}
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

// managerTraceFields 返回不含路径、命令行和协议内容的公共 manager 诊断字段。
// tool_family/callsite 固定标识 manager.ensure_client；具体工具请求仍由外层调用日志绑定。
func (m *manager) managerTraceFields() []any {
	poolDigest := m.poolInstanceDigest
	if poolDigest == "" && m.pool != nil {
		poolDigest = provisionalWorkspaceHash(fmt.Sprintf("%p", m.pool))
	}
	return []any{
		"pool_instance_id_digest", poolDigest,
		"resolved_manager_key_digest", m.resolvedManagerKeyDigest,
		"resolved_scope_key_digest", m.resolvedScopeKeyDigest,
		"resolved_workspace_key_digest", m.resolvedWorkspaceKeyDigest,
		"resolved_agent_id_present", m.resolvedAgentIDPresent,
		"resolved_thread_id_present", m.resolvedThreadIDPresent,
		"turn_call_excluded", m.turnCallExcluded,
		"registry_instance_id_digest", provisionalWorkspaceHash(m.instanceID),
		"tool_family", "lsp",
		"callsite", "manager.ensure_client",
		"resolved_language_id", m.resolvedLanguageID,
		"resolved_root_kind", m.resolvedRootKind,
		"workspace_root_equals_cwd", m.workspaceRootEqualsCWD,
		"workspace_root_vs_cwd_relation", m.workspaceRootVsCWDRelation,
		"workspace_root_depth", m.workspaceRootDepth,
		"workspace_root_utf16_units", m.workspaceRootUTF16Units,
		"target_within_workspace", m.targetWithinWorkspace,
		"workspace_has_pom", m.workspaceHasPOM,
		"workspace_has_gradle", m.workspaceHasGradle,
		"workspace_has_gradle_kts", m.workspaceHasGradleKTS,
		"workspace_roots_count", m.workspaceRootsCount,
		"workspace_root_digest", m.workspaceRootDigest,
		"language_workspace_root_digest", m.languageWorkspaceRootDigest,
		"project_root_digest", m.projectRootDigest,
		"language_specific_digest", m.languageSpecificDigest,
		"language_specific_empty", m.languageSpecificEmpty,
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
	// 这里只记录脱敏 scope 摘要与代际，帮助区分同一 MCP 下的 client replacement；
	// 不写入 URI、绝对路径或命令行，保持跨平台日志语义一致。
	if m.logger != nil {
		fields := append([]any{"event", "create_begin", "manager_instance_id_digest", provisionalWorkspaceHash(m.instanceID), "workspace_config_key_digest", provisionalWorkspaceHash(cfg.key), "generation", generation, "language", cfg.languageID, "creation_reason", "ensure_client"}, m.managerTraceFields()...)
		m.logger.Info("LSP client instance", fields...)
	}
	client, err := newClientFromFactory(m.factory, cfg, handler)
	if err != nil {
		if m.logger != nil {
			m.logger.Warn("LSP client instance", "event", "create_failed", "manager_instance_id_digest", provisionalWorkspaceHash(m.instanceID), "workspace_config_key_digest", provisionalWorkspaceHash(cfg.key), "generation", generation, "language", cfg.languageID, "creation_reason", "ensure_client")
		}
		return nil, m.handleClientFactoryFailure(cfg.key, generation, err)
	}
	if m.logger != nil {
		fields := append([]any{"event", "create_ready", "manager_instance_id_digest", provisionalWorkspaceHash(m.instanceID), "client_instance_id_digest", lspClientInstanceDigest(cfg, generation), "workspace_config_key_digest", provisionalWorkspaceHash(cfg.key), "generation", generation, "language", cfg.languageID, "transport_pid", lspClientTransportPID(client), "client_state", "provisional"}, m.managerTraceFields()...)
		m.logger.Info("LSP client instance", fields...)
	}
	configureClientWorkspace(client, cfg)
	client, err = initializeClientWithWindows122Retry(
		ctx,
		client,
		func(candidate Client) error { return candidate.Initialize(ctx, cfg.rootURI) },
		func() (Client, error) {
			generation = m.workspaceGeneration.Add(1)
			if m.logger != nil {
				m.logger.Info("LSP client instance", "event", "create_begin", "manager_instance_id_digest", provisionalWorkspaceHash(m.instanceID), "client_instance_id_digest", lspClientInstanceDigest(cfg, generation), "workspace_config_key_digest", provisionalWorkspaceHash(cfg.key), "generation", generation, "language", cfg.languageID, "creation_reason", "windows_122_startup_retry")
			}
			replacement, replacementErr := newClientFromFactory(m.factory, cfg, handler)
			if replacementErr != nil {
				return nil, m.handleClientFactoryFailure(cfg.key, generation, replacementErr)
			}
			configureClientWorkspace(replacement, cfg)
			return replacement, nil
		},
		func(candidate Client) error { return m.cleanupProvisionalClient(cfg.key, generation, candidate, false) },
	)
	if err != nil {
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
		if m.logger != nil {
			fields := append([]any{"event", "registry_hit", "workspace_config_key_digest", provisionalWorkspaceHash(cfg.key), "generation", generation, "language", cfg.languageID, "client_state", "existing_wins"}, m.managerTraceFields()...)
			m.logger.Info("LSP client instance", fields...)
		}
		existing := workspace.client
		m.mu.Unlock()
		cleanupErr := m.cleanupProvisionalClient(cfg.key, generation, client, true)
		return existing, joinProvisionalClientError(nil, cleanupErr)
	}
	if m.logger != nil {
		fields := append([]any{"event", "registry_miss", "workspace_config_key_digest", provisionalWorkspaceHash(cfg.key), "generation", generation, "language", cfg.languageID, "client_state", "registered"}, m.managerTraceFields()...)
		m.logger.Info("LSP client instance", fields...)
	}
	attempt := newWorkspaceBootstrapAttempt(ctx)
	m.workspaces[cfg.key] = &workspaceClient{
		key:              cfg.key,
		rootPath:         cfg.rootPath,
		rootURI:          cfg.rootURI,
		languageID:       cfg.languageID,
		env:              append([]string(nil), cfg.env...),
		initOptions:      cloneAnyMap(cfg.initOptions),
		workspaceFolders: cloneWorkspaceFolders(cfg.workspaceFolders),
		client:           client,
		generation:       generation,
		state:            workspaceStateBootstrapping,
		bootstrapAttempt: attempt,
	}
	if m.bootstrapAttempts == nil {
		m.bootstrapAttempts = make(map[*workspaceBootstrapAttempt]struct{})
	}
	m.bootstrapAttempts[attempt] = struct{}{}
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

// lspClientInstanceDigest 以 scope/language/generation 生成脱敏实例关联键；不写 URI
// 或命令行，供 manager 与 transport 生命周期日志做内部关联。
func lspClientInstanceDigest(cfg workspaceConfig, generation uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", provisionalWorkspaceHash(cfg.key), cfg.languageID, generation)))
	return hex.EncodeToString(sum[:8])
}

func lspClientTransportPID(client Client) int {
	concrete, ok := concreteClient(client)
	if !ok {
		return 0
	}
	return concrete.serverProcessID()
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
