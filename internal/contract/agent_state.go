package contract

import (
	"context"
	"strings"
	"time"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
)

// IsActiveAgentState 是 UI、生命周期和测试共用的 active 状态判定。
// 空状态、archived、stopped、failed 都视为非活跃；函数保持纯计算，适合在计数和轮询路径频繁调用。
func IsActiveAgentState(state string) bool {
	switch agentdto.AgentState(strings.TrimSpace(state)) {
	case "", agentdto.StateStopped, agentdto.StateFailed, "archived":
		return false
	default:
		return true
	}
}

// WorkflowPlanStatus 表示多代理工作流计划当前所处阶段。
type WorkflowPlanStatus string

const (
	WorkflowPlanStatusDraft     WorkflowPlanStatus = "draft"
	WorkflowPlanStatusActive    WorkflowPlanStatus = "active"
	WorkflowPlanStatusBlocked   WorkflowPlanStatus = "blocked"
	WorkflowPlanStatusCompleted WorkflowPlanStatus = "completed"
	WorkflowPlanStatusCancelled WorkflowPlanStatus = "cancelled"
)

// AgentRole 表示一个 AgentTask 期望承担的协作角色。
type AgentRole string

const (
	AgentRolePlanner     AgentRole = "planner"
	AgentRoleImplementer AgentRole = "implementer"
	AgentRoleReviewer    AgentRole = "reviewer"
	AgentRoleValidator   AgentRole = "validator"
	AgentRoleIntegrator  AgentRole = "integrator"
)

// AgentTaskStatus 表示 AgentTask 的可调度状态。
type AgentTaskStatus string

const (
	AgentTaskStatusPending   AgentTaskStatus = "pending"
	AgentTaskStatusReady     AgentTaskStatus = "ready"
	AgentTaskStatusRunning   AgentTaskStatus = "running"
	AgentTaskStatusBlocked   AgentTaskStatus = "blocked"
	AgentTaskStatusReviewing AgentTaskStatus = "reviewing"
	AgentTaskStatusDone      AgentTaskStatus = "done"
	AgentTaskStatusFailed    AgentTaskStatus = "failed"
	AgentTaskStatusCancelled AgentTaskStatus = "cancelled"
)

// ReviewGateStatus 表示审查入口是否仍阻塞验收。
type ReviewGateStatus string

const (
	ReviewGateStatusOpen             ReviewGateStatus = "open"
	ReviewGateStatusChangesRequested ReviewGateStatus = "changes_requested"
	ReviewGateStatusPassed           ReviewGateStatus = "passed"
	ReviewGateStatusCancelled        ReviewGateStatus = "cancelled"
)

// ReviewGateReReviewState 表示审查入口的复审状态。
type ReviewGateReReviewState string

const (
	ReviewGateReReviewNotRequired ReviewGateReReviewState = "not_required"
	ReviewGateReReviewRequested   ReviewGateReReviewState = "requested"
	ReviewGateReReviewInProgress  ReviewGateReReviewState = "in_progress"
	ReviewGateReReviewCompleted   ReviewGateReReviewState = "completed"
)

// CrossValidationStatus 表示独立复核和仲裁是否已经收敛。
type CrossValidationStatus string

const (
	CrossValidationStatusPending    CrossValidationStatus = "pending"
	CrossValidationStatusAgreed     CrossValidationStatus = "agreed"
	CrossValidationStatusDisagreed  CrossValidationStatus = "disagreed"
	CrossValidationStatusArbitrated CrossValidationStatus = "arbitrated"
)

// ArtifactKind 表示工作流产物的大类，便于 UI 和审查工具选择展示方式。
type ArtifactKind string

const (
	ArtifactKindDocument     ArtifactKind = "document"
	ArtifactKindPatch        ArtifactKind = "patch"
	ArtifactKindLog          ArtifactKind = "log"
	ArtifactKindTrace        ArtifactKind = "trace"
	ArtifactKindTestOutput   ArtifactKind = "test_output"
	ArtifactKindScreenshot   ArtifactKind = "screenshot"
	ArtifactKindMergeRequest ArtifactKind = "merge_request"
	ArtifactKindOther        ArtifactKind = "other"
)

// ArtifactLifecycle 表示产物从草稿到被验收或丢弃的生命周期。
type ArtifactLifecycle string

const (
	ArtifactLifecycleDraft     ArtifactLifecycle = "draft"
	ArtifactLifecycleCandidate ArtifactLifecycle = "candidate"
	ArtifactLifecycleReviewed  ArtifactLifecycle = "reviewed"
	ArtifactLifecycleAccepted  ArtifactLifecycle = "accepted"
	ArtifactLifecycleMerged    ArtifactLifecycle = "merged"
	ArtifactLifecycleDiscarded ArtifactLifecycle = "discarded"
)

// AcceptanceStatus 表示验收记录的最终判断。
type AcceptanceStatus string

const (
	AcceptanceStatusPending          AcceptanceStatus = "pending"
	AcceptanceStatusAccepted         AcceptanceStatus = "accepted"
	AcceptanceStatusAcceptedWithRisk AcceptanceStatus = "accepted_with_risk"
	AcceptanceStatusRejected         AcceptanceStatus = "rejected"
)

// ExpectedArtifact 描述计划预期要产出的产物类型和用途。
type ExpectedArtifact struct {
	Name        string       `json:"name"`
	Kind        ArtifactKind `json:"kind,omitempty"`
	Description string       `json:"description,omitempty"`
}

// WorkflowPlan 记录多 Agent 协作的目标、边界、风险、验收和产物预期。
type WorkflowPlan struct {
	PlanKey            string             `json:"plan_key"`
	WorkflowRunID      *int64             `json:"workflow_run_id,omitempty"`
	DagKey             string             `json:"dag_key,omitempty"`
	Goal               string             `json:"goal"`
	NonGoals           []string           `json:"non_goals,omitempty"`
	Risks              []string           `json:"risks,omitempty"`
	AcceptanceCriteria []string           `json:"acceptance_criteria,omitempty"`
	EvalList           []string           `json:"eval_list,omitempty"`
	AllowedWriteScope  []string           `json:"allowed_write_scope,omitempty"`
	ExpectedArtifacts  []ExpectedArtifact `json:"expected_artifacts,omitempty"`
	Status             WorkflowPlanStatus `json:"status"`
	CreatedBy          string             `json:"created_by,omitempty"`
	UpdatedBy          string             `json:"updated_by,omitempty"`
	CreatedAt          time.Time          `json:"created_at,omitzero"`
	UpdatedAt          time.Time          `json:"updated_at,omitzero"`
}

// ContextRef 描述任务输入上下文中的可追溯引用。
type ContextRef struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Notes string `json:"notes,omitempty"`
}

// AgentTaskBudget 记录单个 AgentTask 的时间和 token 预算。
type AgentTaskBudget struct {
	MaxMinutes int `json:"max_minutes,omitempty"`
	MaxTokens  int `json:"max_tokens,omitempty"`
}

// AgentTask 是 15 分钟左右可验证任务单元，记录输入、输出、验证和依赖关系。
type AgentTask struct {
	TaskKey             string          `json:"task_key"`
	PlanKey             string          `json:"plan_key"`
	WorkflowRunID       *int64          `json:"workflow_run_id,omitempty"`
	DagKey              string          `json:"dag_key,omitempty"`
	NodeKey             string          `json:"node_key,omitempty"`
	Role                AgentRole       `json:"role"`
	Title               string          `json:"title"`
	InputContext        []ContextRef    `json:"input_context,omitempty"`
	OutputContract      string          `json:"output_contract"`
	VerificationCommand string          `json:"verification_command,omitempty"`
	Budget              AgentTaskBudget `json:"budget,omitzero"`
	DependsOn           []string        `json:"depends_on,omitempty"`
	OutputArtifactKeys  []string        `json:"output_artifact_keys,omitempty"`
	Status              AgentTaskStatus `json:"status"`
	AssignedAgent       string          `json:"assigned_agent,omitempty"`
	CreatedBy           string          `json:"created_by,omitempty"`
	UpdatedBy           string          `json:"updated_by,omitempty"`
	CreatedAt           time.Time       `json:"created_at,omitzero"`
	UpdatedAt           time.Time       `json:"updated_at,omitzero"`
}

// ReviewFinding 记录审查中发现的一条阻塞或非阻塞问题。
type ReviewFinding struct {
	FindingKey string `json:"finding_key,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Summary    string `json:"summary"`
	Evidence   string `json:"evidence,omitempty"`
}

// ReviewGate 记录某个 reviewer 对目标产物或任务的审查入口。
type ReviewGate struct {
	GateKey             string                  `json:"gate_key"`
	PlanKey             string                  `json:"plan_key"`
	TaskKey             string                  `json:"task_key,omitempty"`
	Reviewer            string                  `json:"reviewer"`
	TargetArtifactKey   string                  `json:"target_artifact_key,omitempty"`
	BlockingFindings    []ReviewFinding         `json:"blocking_findings,omitempty"`
	NonBlockingFindings []ReviewFinding         `json:"non_blocking_findings,omitempty"`
	ReReviewState       ReviewGateReReviewState `json:"re_review_state"`
	PassCondition       string                  `json:"pass_condition,omitempty"`
	Status              ReviewGateStatus        `json:"status"`
	CreatedBy           string                  `json:"created_by,omitempty"`
	UpdatedBy           string                  `json:"updated_by,omitempty"`
	CreatedAt           time.Time               `json:"created_at,omitzero"`
	UpdatedAt           time.Time               `json:"updated_at,omitzero"`
	ResolvedAt          *time.Time              `json:"resolved_at,omitempty"`
}

// EvidenceRef 指向复核、交接或验收中使用的证据。
type EvidenceRef struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Summary string `json:"summary,omitempty"`
}

// ReviewDisagreement 记录交叉复核中的争议点。
type ReviewDisagreement struct {
	Topic     string   `json:"topic"`
	Positions []string `json:"positions,omitempty"`
	Evidence  string   `json:"evidence,omitempty"`
}

// CrossValidation 记录多名 reviewer 的独立复核、争议证据和仲裁结果。
type CrossValidation struct {
	ValidationKey        string                `json:"validation_key"`
	PlanKey              string                `json:"plan_key"`
	TargetArtifactKey    string                `json:"target_artifact_key,omitempty"`
	IndependentReviewers []string              `json:"independent_reviewers,omitempty"`
	Disagreements        []ReviewDisagreement  `json:"disagreements,omitempty"`
	Evidence             []EvidenceRef         `json:"evidence,omitempty"`
	ArbitrationResult    string                `json:"arbitration_result,omitempty"`
	Status               CrossValidationStatus `json:"status"`
	CreatedBy            string                `json:"created_by,omitempty"`
	UpdatedBy            string                `json:"updated_by,omitempty"`
	CreatedAt            time.Time             `json:"created_at,omitzero"`
	UpdatedAt            time.Time             `json:"updated_at,omitzero"`
}

// HandoffPackage 记录当前目标、已完成工作、失败证据、残余风险和下一步动作。
type HandoffPackage struct {
	HandoffKey      string        `json:"handoff_key"`
	PlanKey         string        `json:"plan_key"`
	CurrentGoal     string        `json:"current_goal"`
	CompletedWork   []string      `json:"completed_work,omitempty"`
	AttemptedPaths  []string      `json:"attempted_paths,omitempty"`
	FailureEvidence []EvidenceRef `json:"failure_evidence,omitempty"`
	ResidualRisks   []string      `json:"residual_risks,omitempty"`
	NextActions     []string      `json:"next_actions,omitempty"`
	CreatedBy       string        `json:"created_by,omitempty"`
	CreatedAt       time.Time     `json:"created_at,omitzero"`
}

// WorkflowArtifact 记录多 Agent 工作流产生的文档、补丁、日志、截图或 MR 链接等产物。
type WorkflowArtifact struct {
	ArtifactKey   string            `json:"artifact_key"`
	PlanKey       string            `json:"plan_key"`
	TaskKey       string            `json:"task_key,omitempty"`
	WorkflowRunID *int64            `json:"workflow_run_id,omitempty"`
	DagKey        string            `json:"dag_key,omitempty"`
	NodeKey       string            `json:"node_key,omitempty"`
	Kind          ArtifactKind      `json:"kind"`
	URI           string            `json:"uri"`
	Title         string            `json:"title,omitempty"`
	Description   string            `json:"description,omitempty"`
	Lifecycle     ArtifactLifecycle `json:"lifecycle"`
	ProducedBy    string            `json:"produced_by,omitempty"`
	CreatedAt     time.Time         `json:"created_at,omitzero"`
	UpdatedAt     time.Time         `json:"updated_at,omitzero"`
}

// VerificationResult 记录一次自动验证命令及其结果摘要。
type VerificationResult struct {
	Command           string `json:"command"`
	Status            string `json:"status"`
	Summary           string `json:"summary,omitempty"`
	OutputArtifactKey string `json:"output_artifact_key,omitempty"`
}

// AcceptanceRecord 记录用户验收、自动验证、审查入口和仍保留的风险。
type AcceptanceRecord struct {
	AcceptanceKey         string               `json:"acceptance_key"`
	PlanKey               string               `json:"plan_key"`
	AcceptedBy            string               `json:"accepted_by,omitempty"`
	UserAccepted          bool                 `json:"user_accepted"`
	AutomatedVerification []VerificationResult `json:"automated_verification,omitempty"`
	ReviewGateKeys        []string             `json:"review_gate_keys,omitempty"`
	ResidualRisks         []string             `json:"residual_risks,omitempty"`
	Status                AcceptanceStatus     `json:"status"`
	Notes                 string               `json:"notes,omitempty"`
	CreatedAt             time.Time            `json:"created_at,omitzero"`
}

// AgentWorkflowService 是 MCP 工具和未来工作台共用的多 Agent 工作流边界。
type AgentWorkflowService interface {
	CreatePlan(ctx context.Context, plan WorkflowPlan) (WorkflowPlan, error)
	GetPlan(ctx context.Context, planKey string) (WorkflowPlan, error)
	CreateTask(ctx context.Context, task AgentTask) (AgentTask, error)
	TransitionTaskStatus(ctx context.Context, taskKey string, status AgentTaskStatus, updatedBy string) (AgentTask, error)
	OpenReviewGate(ctx context.Context, gate ReviewGate) (ReviewGate, error)
	ResolveReviewGate(ctx context.Context, gateKey string, status ReviewGateStatus, reReviewState ReviewGateReReviewState, updatedBy string) (ReviewGate, error)
	RecordCrossValidation(ctx context.Context, validation CrossValidation) (CrossValidation, error)
	CreateHandoffPackage(ctx context.Context, handoff HandoffPackage) (HandoffPackage, error)
	AttachArtifact(ctx context.Context, artifact WorkflowArtifact) (WorkflowArtifact, error)
	RecordAcceptance(ctx context.Context, record AcceptanceRecord) (AcceptanceRecord, error)
}
