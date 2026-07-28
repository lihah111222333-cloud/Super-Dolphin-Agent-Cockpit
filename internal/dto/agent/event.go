package agent

import (
	"errors"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

const (
	OutcomeKindSuccess = "success"
	OutcomeKindFailure = "failure"
	OutcomeKindStopped = "stopped"
)

// Assignment 保存 agent 启动请求中的权威委派信息。
type Assignment struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	AssignedAt  time.Time `json:"assignedAt"`
}

// Validate 拒绝缺失标题、描述或分派时间的非完整委派。
func (a Assignment) Validate() error {
	if strings.TrimSpace(a.Title) == "" {
		return errors.New("assignment title is required")
	}
	if strings.TrimSpace(a.Description) == "" {
		return errors.New("assignment description is required")
	}
	if a.AssignedAt.IsZero() {
		return errors.New("assignment assignedAt is required")
	}
	return nil
}

// Progress 保存状态机权威状态；步骤计数只有结构化计划存在时才成对提供。
type Progress struct {
	Status         string    `json:"status"`
	CurrentStep    *string   `json:"currentStep"`
	CompletedSteps *int      `json:"completedSteps"`
	TotalSteps     *int      `json:"totalSteps"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Validate 拒绝空状态、空更新时间或不完整的结构化步骤计数。
func (p Progress) Validate() error {
	if strings.TrimSpace(p.Status) == "" {
		return errors.New("progress status is required")
	}
	if p.UpdatedAt.IsZero() {
		return errors.New("progress updatedAt is required")
	}
	if (p.CompletedSteps == nil) != (p.TotalSteps == nil) {
		return errors.New("progress completedSteps and totalSteps must be provided together")
	}
	if p.CompletedSteps != nil && (*p.CompletedSteps < 0 || *p.TotalSteps < 0 || *p.CompletedSteps > *p.TotalSteps) {
		return errors.New("progress step counts are invalid")
	}
	return nil
}

// Outcome 保存已完成 agent 的结构化终态；运行中的 agent 使用 nil。
type Outcome struct {
	Kind        string    `json:"kind"`
	Summary     string    `json:"summary,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	Code        string    `json:"code,omitempty"`
	Recoverable *bool     `json:"recoverable"`
	CompletedAt time.Time `json:"completedAt"`
}

// Validate 固定终态语义：成功必须有 summary，失败和停止必须有 reason。
func (o Outcome) Validate() error {
	if o.CompletedAt.IsZero() {
		return errors.New("outcome completedAt is required")
	}
	switch strings.TrimSpace(o.Kind) {
	case OutcomeKindSuccess:
		if strings.TrimSpace(o.Summary) == "" {
			return errors.New("success outcome summary is required")
		}
	case OutcomeKindFailure, OutcomeKindStopped:
		if strings.TrimSpace(o.Reason) == "" {
			return errors.New("non-success outcome reason is required")
		}
	default:
		return errors.New("outcome kind is invalid")
	}
	return nil
}

// BoardView 是 UI 快照与实时 patch 共用的精确 Agent 看板模型。
type BoardView struct {
	ID            string      `json:"id"`
	ThreadID      string      `json:"threadId"`
	ParentAgentID string      `json:"parentAgentId,omitempty"`
	Name          string      `json:"name"`
	Assignment    *Assignment `json:"assignment"`
	Progress      Progress    `json:"progress"`
	Outcome       *Outcome    `json:"outcome"`
}

// Validate 拒绝身份、进度或嵌套终态不完整的看板记录。
func (v BoardView) Validate() error {
	if strings.TrimSpace(v.ID) == "" || strings.TrimSpace(v.ThreadID) == "" || strings.TrimSpace(v.Name) == "" {
		return errors.New("agent board id, threadId and name are required")
	}
	if v.Assignment != nil {
		if err := v.Assignment.Validate(); err != nil {
			return err
		}
	}
	if err := v.Progress.Validate(); err != nil {
		return err
	}
	if v.Outcome != nil {
		return v.Outcome.Validate()
	}
	return nil
}

// StateChanged 表示 agent 生命周期状态发生迁移。
type StateChanged struct {
	shared.AgentSessionHeader
	OldState string     `json:"old_state"`
	NewState string     `json:"new_state"`
	Trigger  string     `json:"trigger"`
	Board    *BoardView `json:"board,omitempty"`
}

// AgentLaunched 表示新的 agent 进程或会话已进入可用状态。
type AgentLaunched struct {
	shared.AgentSessionHeader
	Model    string     `json:"model,omitempty"`
	CWD      string     `json:"cwd,omitempty"`
	Name     string     `json:"name,omitempty"`
	Provider string     `json:"provider,omitempty"`
	Board    *BoardView `json:"board,omitempty"`
}

// AgentStopped 表示 agent 已按请求停止或被强制停止。
type AgentStopped struct {
	shared.AgentSessionHeader
	Reason string     `json:"reason,omitempty"`
	Board  *BoardView `json:"board,omitempty"`
}

// AgentRecovering 表示现有 agent session 正在执行恢复尝试。
type AgentRecovering struct {
	shared.AgentSessionHeader
	Reason  string     `json:"reason"`
	Attempt int        `json:"attempt,omitempty"`
	Board   *BoardView `json:"board,omitempty"`
}

// AgentFailed 表示 agent 进入终止失败状态。
type AgentFailed struct {
	shared.AgentSessionHeader
	Error       string     `json:"error"`
	Recoverable bool       `json:"recoverable,omitempty"`
	Board       *BoardView `json:"board,omitempty"`
}

// Type 返回状态迁移事件分发用的类型编号。
func (StateChanged) Type() uint32 { return shared.EventTypeAgentStateChanged }

// Type 返回 agent 启动事件分发用的类型编号。
func (AgentLaunched) Type() uint32 { return shared.EventTypeAgentLaunched }

// Type 返回 agent 停止事件分发用的类型编号。
func (AgentStopped) Type() uint32 { return shared.EventTypeAgentStopped }

// Type 返回 agent 恢复事件分发用的类型编号。
func (AgentRecovering) Type() uint32 { return shared.EventTypeAgentRecovering }

// Type 返回 agent 失败事件分发用的类型编号。
func (AgentFailed) Type() uint32 { return shared.EventTypeAgentFailed }
