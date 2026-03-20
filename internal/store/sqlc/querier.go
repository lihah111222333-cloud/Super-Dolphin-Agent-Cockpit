package sqlc

import (
	"context"
	"encoding/json"
)

type Querier interface {
	InsertSystemLog(ctx context.Context, arg InsertSystemLogParams) error
	ListSystemLogs(ctx context.Context, arg ListSystemLogsParams) ([]SystemLog, error)
	InsertAuditEvent(ctx context.Context, arg InsertAuditEventParams) error
	ListAuditEvents(ctx context.Context, arg ListAuditEventsParams) ([]AuditEvent, error)
	ListAILogSystemLogs(ctx context.Context, arg ListAILogSystemLogsParams) ([]SystemLog, error)
	ListBusExceptionLogs(ctx context.Context, arg ListBusExceptionLogsParams) ([]BusExceptionLog, error)
	GetUIPreferenceValue(ctx context.Context, arg GetUIPreferenceValueParams) (json.RawMessage, error)
	UpsertUIPreference(ctx context.Context, arg UpsertUIPreferenceParams) error
	ListUIPreferences(ctx context.Context, cwd string) ([]UIPreference, error)
	UpsertSharedFile(ctx context.Context, arg UpsertSharedFileParams) (SharedFile, error)
	GetSharedFile(ctx context.Context, path string) (SharedFile, error)
	ListSharedFiles(ctx context.Context, arg ListSharedFilesParams) ([]SharedFile, error)
	DeleteSharedFile(ctx context.Context, path string) (int64, error)
	GetAgentProviderBindingByProviderThread(ctx context.Context, arg GetAgentProviderBindingByProviderThreadParams) (AgentProviderBinding, error)
	UpsertAgentProviderBinding(ctx context.Context, arg UpsertAgentProviderBindingParams) error
	DeleteAgentProviderBindingByAgentID(ctx context.Context, agentID string) error
	UpdateAgentProviderBindingSessionUUID(ctx context.Context, arg UpdateAgentProviderBindingSessionUUIDParams) error
	UpdateAgentProviderBindingArchived(ctx context.Context, arg UpdateAgentProviderBindingArchivedParams) error
	GetAgentProviderBindingByAgentID(ctx context.Context, agentID string) (AgentProviderBinding, error)
	GetAgentThreadByID(ctx context.Context, threadID string) (AgentThread, error)
	GetAgentThreadByPort(ctx context.Context, port int32) (AgentThread, error)
	ListAgentThreads(ctx context.Context) ([]AgentThread, error)
	ListRunningAgents(ctx context.Context) ([]ListRunningAgentsRow, error)
	ListRunningAgentThreads(ctx context.Context) ([]AgentThread, error)
	ListRecoverableAgentThreads(ctx context.Context) ([]AgentThread, error)
	UpsertAgentThread(ctx context.Context, arg UpsertAgentThreadParams) error
	UpdateAgentThreadStatus(ctx context.Context, arg UpdateAgentThreadStatusParams) error
	DeleteAgentThreadByID(ctx context.Context, arg DeleteAgentThreadByIDParams) error
	ResetRunningAgentThreads(ctx context.Context) error
	ExpireStaleAgentThreads(ctx context.Context, arg ExpireStaleAgentThreadsParams) (int64, error)
	AgentThreadRunningExists(ctx context.Context, threadID string) (bool, error)
	ListAgentThreadCwds(ctx context.Context) ([]AgentThreadCwdRow, error)
	ListAgentThreadCwdsByPrefix(ctx context.Context, prefix string) ([]AgentThreadCwdRow, error)
	UpsertAgentStatus(ctx context.Context, arg UpsertAgentStatusParams) (AgentStatus, error)
	GetAgentStatus(ctx context.Context, agentID string) (AgentStatus, error)
	ListAgentStatuses(ctx context.Context, status string) ([]AgentStatus, error)
	AcquireCwdLock(ctx context.Context, arg AcquireCwdLockParams) (int64, error)
	ForceAcquireCwdLock(ctx context.Context, arg ForceAcquireCwdLockParams) (int64, error)
	ReleaseCwdLock(ctx context.Context, arg ReleaseCwdLockParams) (int64, error)
	HeartbeatCwdLock(ctx context.Context, arg HeartbeatCwdLockParams) error
	DeleteStaleCwdLocks(ctx context.Context) (int64, error)
	GetCwdLockHolder(ctx context.Context, cwd string) (CwdLockHolderRow, error)
	UpsertTaskAck(ctx context.Context, arg UpsertTaskAckParams) (TaskAck, error)
	ListTaskAcks(ctx context.Context, arg ListTaskAcksParams) ([]TaskAck, error)
	UpsertTaskDag(ctx context.Context, arg UpsertTaskDagParams) (TaskDag, error)
	ListTaskDags(ctx context.Context, arg ListTaskDagsParams) ([]TaskDag, error)
	GetTaskDag(ctx context.Context, dagKey string) (TaskDag, error)
	UpsertTaskDagNode(ctx context.Context, arg UpsertTaskDagNodeParams) (TaskDagNode, error)
	UpdateTaskDagNodeStatus(ctx context.Context, arg UpdateTaskDagNodeStatusParams) (TaskDagNode, error)
	ListTaskDagNodes(ctx context.Context, dagKey string) ([]TaskDagNode, error)
	ListRunningTaskDagNodesByAssignee(ctx context.Context, assignee string) ([]TaskDagNode, error)
	GetTaskDagForUpdate(ctx context.Context, dagKey string) (TaskDag, error)
	GetTaskDagNodesForUpdate(ctx context.Context, dagKey string) ([]TaskDagNode, error)
	BindRunningTaskDagNodeTurn(ctx context.Context, arg BindRunningTaskDagNodeTurnParams) (TaskDagNode, error)
	TouchRunningTaskDagNodeEvent(ctx context.Context, arg TouchRunningTaskDagNodeEventParams) (TaskDagNode, error)
	UpdateRunningTaskDagNodeStatus(ctx context.Context, arg UpdateRunningTaskDagNodeStatusParams) (TaskDagNode, error)
	UpdateAwaitingVerifyTaskDagNodeStatus(ctx context.Context, arg UpdateAwaitingVerifyTaskDagNodeStatusParams) (TaskDagNode, error)
	CompleteTaskDagNode(ctx context.Context, arg CompleteTaskDagNodeParams) (TaskDagNode, error)
	UpdateTaskDagNodeStatusFlexible(ctx context.Context, arg UpdateTaskDagNodeStatusFlexibleParams) (TaskDagNode, error)
	EnqueueTaskDagWakeup(ctx context.Context, arg EnqueueTaskDagWakeupParams) (int64, error)
	ClaimDueTaskDagWakeups(ctx context.Context, arg ClaimDueTaskDagWakeupsParams) ([]TaskDagWakeup, error)
	MarkTaskDagWakeupSent(ctx context.Context, arg MarkTaskDagWakeupSentParams) (int64, error)
	BindTaskDagWakeupTurn(ctx context.Context, arg BindTaskDagWakeupTurnParams) (int64, error)
	RetryTaskDagWakeup(ctx context.Context, arg RetryTaskDagWakeupParams) (int64, error)
	FailTaskDagWakeup(ctx context.Context, arg FailTaskDagWakeupParams) (int64, error)
	AcquireTaskDagWorkerLease(ctx context.Context, arg AcquireTaskDagWorkerLeaseParams) (int64, error)
	RenewTaskDagWorkerLease(ctx context.Context, arg RenewTaskDagWorkerLeaseParams) (int64, error)
	ReleaseTaskDagWorkerLease(ctx context.Context, arg ReleaseTaskDagWorkerLeaseParams) error
	ReclaimStaleDispatchingTaskDagWakeups(ctx context.Context) (int64, error)
	ListSentUnboundTaskDagWakeups(ctx context.Context, targetAgentID string) ([]TaskDagWakeup, error)
	ListPendingOrDispatchingTaskDagWakeups(ctx context.Context) ([]TaskDagWakeup, error)
	GetTaskDagWakeup(ctx context.Context, id int64) (TaskDagWakeup, error)
	InsertTaskTrace(ctx context.Context, arg InsertTaskTraceParams) (TaskTrace, error)
	ListTaskTraces(ctx context.Context, arg ListTaskTracesParams) ([]TaskTrace, error)
	UpsertWorkspaceRun(ctx context.Context, arg UpsertWorkspaceRunParams) (WorkspaceRun, error)
	GetWorkspaceRun(ctx context.Context, runKey string) (WorkspaceRun, error)
	ListWorkspaceRuns(ctx context.Context, arg ListWorkspaceRunsParams) ([]WorkspaceRun, error)
	UpdateWorkspaceRunStatus(ctx context.Context, arg UpdateWorkspaceRunStatusParams) (WorkspaceRun, error)
	TransitionWorkspaceRunStatus(ctx context.Context, arg TransitionWorkspaceRunStatusParams) (WorkspaceRun, error)
	UpsertWorkspaceRunFile(ctx context.Context, arg UpsertWorkspaceRunFileParams) (WorkspaceRunFile, error)
	GetWorkspaceRunFile(ctx context.Context, arg GetWorkspaceRunFileParams) (WorkspaceRunFile, error)
	ListWorkspaceRunFiles(ctx context.Context, arg ListWorkspaceRunFilesParams) ([]WorkspaceRunFile, error)
	CreateTopologyApproval(ctx context.Context, arg CreateTopologyApprovalParams) (TopologyApproval, error)
	ApproveTopologyApproval(ctx context.Context, reviewer, id string) (int64, error)
	RejectTopologyApproval(ctx context.Context, reviewer, id string) (int64, error)
	ListPendingTopologyApprovals(ctx context.Context) ([]TopologyApproval, error)
	GetPromptTemplate(ctx context.Context, promptKey string) (PromptTemplate, error)
	InsertPromptVersion(ctx context.Context, arg InsertPromptVersionParams) error
	UpsertPromptTemplate(ctx context.Context, arg UpsertPromptTemplateParams) (PromptTemplate, error)
	ListPromptTemplates(ctx context.Context, arg ListPromptTemplatesParams) ([]PromptTemplate, error)
	GetCommandCard(ctx context.Context, cardKey string) (CommandCard, error)
	DeleteCommandCard(ctx context.Context, cardKey string) (int64, error)
	InsertCommandCardVersion(ctx context.Context, arg InsertCommandCardVersionParams) error
	ListCommandCardVersions(ctx context.Context, cardKey string) ([]CommandCardVersion, error)
	UpsertCommandCard(ctx context.Context, arg UpsertCommandCardParams) (CommandCard, error)
	ListCommandCards(ctx context.Context, arg ListCommandCardsParams) ([]ListCommandCardsRow, error)
	CreateInteraction(ctx context.Context, arg CreateInteractionParams) (AgentInteraction, error)
	GetInteraction(ctx context.Context, id int64) (AgentInteraction, error)
	ListInteractions(ctx context.Context, arg ListInteractionsParams) ([]AgentInteraction, error)
	ReviewInteraction(ctx context.Context, arg ReviewInteractionParams) (AgentInteraction, error)
	PlaceholderDBQuery(ctx context.Context) ([]PlaceholderDBQueryRow, error)
}
