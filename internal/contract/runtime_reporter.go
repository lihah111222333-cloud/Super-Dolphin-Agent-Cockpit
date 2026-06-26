package contract

import (
	"context"
	"time"
)

// RuntimeReport 是 provider 上报运行时元数据的 contract 层载荷。
type RuntimeReport struct {
	AgentID  string
	Port     int
	Provider string
}

// RuntimeReporter 允许进程内 provider 发布运行时元数据。
// provider 通过该端口上报端口和类型，不直接导入 orchestration 模块。
type RuntimeReporter interface {
	ReportRuntime(ctx context.Context, report RuntimeReport) error
}

// ChangeRequestStatus 表示 MR/PR 关联对象的当前生命周期状态。
type ChangeRequestStatus string

// ChangeRequest MR/PR 生命周期状态常量。
const (
	// ChangeRequestStatusDraft 表示变更请求还未提交到外部评审。
	ChangeRequestStatusDraft ChangeRequestStatus = "draft"
	// ChangeRequestStatusOpen 表示外部 MR/PR 已创建且仍在审查或验证中。
	ChangeRequestStatusOpen ChangeRequestStatus = "open"
	// ChangeRequestStatusMerged 表示外部变更已合并。
	ChangeRequestStatusMerged ChangeRequestStatus = "merged"
	// ChangeRequestStatusClosed 表示外部变更已关闭且未合并。
	ChangeRequestStatusClosed ChangeRequestStatus = "closed"
)

// ChangeRequestCheckStatus 表示关联 CI/check 的最新状态。
type ChangeRequestCheckStatus string

// ChangeRequest CI/check 状态常量。
const (
	// ChangeRequestCheckStatusPending 表示检查尚未开始。
	ChangeRequestCheckStatusPending ChangeRequestCheckStatus = "pending"
	// ChangeRequestCheckStatusRunning 表示检查正在执行。
	ChangeRequestCheckStatusRunning ChangeRequestCheckStatus = "running"
	// ChangeRequestCheckStatusPassed 表示检查已通过。
	ChangeRequestCheckStatusPassed ChangeRequestCheckStatus = "passed"
	// ChangeRequestCheckStatusFailed 表示检查失败。
	ChangeRequestCheckStatusFailed ChangeRequestCheckStatus = "failed"
)

// ChangeRequestReviewGateStatus 表示工作流侧审查门禁状态。
type ChangeRequestReviewGateStatus string

// ChangeRequest 审查门禁状态常量。
const (
	// ChangeRequestReviewGateOpen 表示审查门禁已开启但尚未通过。
	ChangeRequestReviewGateOpen ChangeRequestReviewGateStatus = "open"
	// ChangeRequestReviewGateBlocked 表示存在阻塞意见。
	ChangeRequestReviewGateBlocked ChangeRequestReviewGateStatus = "blocked"
	// ChangeRequestReviewGatePassed 表示审查门禁已通过。
	ChangeRequestReviewGatePassed ChangeRequestReviewGateStatus = "passed"
)

// ChangeRequest 关联一次 workflow run 与外部 MR/PR 生命周期，不承载具体代码平台写操作。
type ChangeRequest struct {
	ID            string                   `json:"id"`
	WorkflowRunID string                   `json:"workflow_run_id"`
	Branch        string                   `json:"branch"`
	Commits       []ChangeRequestCommit    `json:"commits,omitempty"`
	Checks        []ChangeRequestCheck     `json:"checks,omitempty"`
	ReviewGate    ChangeRequestReviewGate  `json:"review_gate,omitempty"`
	External      ChangeRequestExternalRef `json:"external,omitempty"`
	Status        ChangeRequestStatus      `json:"status"`
	CreatedAt     time.Time                `json:"created_at"`
	UpdatedAt     time.Time                `json:"updated_at"`
}

// ChangeRequestCommit 记录分支上的提交摘要，避免 contract 绑定某个 Git provider 的完整 payload。
type ChangeRequestCommit struct {
	SHA       string    `json:"sha"`
	Title     string    `json:"title,omitempty"`
	Author    string    `json:"author,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// ChangeRequestCheck 记录 CI/check 的最新状态，URL 只作为外部引用。
type ChangeRequestCheck struct {
	Name      string                   `json:"name"`
	Status    ChangeRequestCheckStatus `json:"status"`
	URL       string                   `json:"url,omitempty"`
	UpdatedAt time.Time                `json:"updated_at,omitempty"`
}

// ChangeRequestReviewGate 将代码审查阻塞状态连接到 workflow 工作台。
type ChangeRequestReviewGate struct {
	Status      ChangeRequestReviewGateStatus `json:"status"`
	Reviewer    string                        `json:"reviewer,omitempty"`
	BlockingIDs []string                      `json:"blocking_ids,omitempty"`
}

// ChangeRequestExternalRef 用通用外部引用表达 MR/PR，provider 特有字段不得泄漏进顶层 contract。
type ChangeRequestExternalRef struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	ID       string `json:"id,omitempty"`
}
