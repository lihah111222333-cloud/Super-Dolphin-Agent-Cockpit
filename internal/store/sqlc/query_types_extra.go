package sqlc

import (
	"encoding/json"
	"time"
)

type UpsertWorkspaceRunParams struct {
	RunKey        string          `json:"run_key"`
	DagKey        string          `json:"dag_key"`
	SourceRoot    string          `json:"source_root"`
	WorkspacePath string          `json:"workspace_path"`
	Status        string          `json:"status"`
	CreatedBy     string          `json:"created_by"`
	UpdatedBy     string          `json:"updated_by"`
	Metadata      json.RawMessage `json:"metadata"`
	FinishedAt    *time.Time      `json:"finished_at"`
}

type ListWorkspaceRunsParams struct {
	Status string `json:"status"`
	DagKey string `json:"dag_key"`
	Limit  int32  `json:"limit"`
}

type UpdateWorkspaceRunStatusParams struct {
	Status    string          `json:"status"`
	UpdatedBy string          `json:"updated_by"`
	Metadata  json.RawMessage `json:"metadata"`
	RunKey    string          `json:"run_key"`
}

type TransitionWorkspaceRunStatusParams struct {
	Status     string          `json:"status"`
	UpdatedBy  string          `json:"updated_by"`
	Metadata   json.RawMessage `json:"metadata"`
	RunKey     string          `json:"run_key"`
	FromStatus string          `json:"from_status"`
}

type UpsertWorkspaceRunFileParams struct {
	RunKey             string `json:"run_key"`
	RelativePath       string `json:"relative_path"`
	BaselineSHA256     string `json:"baseline_sha256"`
	WorkspaceSHA256    string `json:"workspace_sha256"`
	SourceSHA256Before string `json:"source_sha256_before"`
	SourceSHA256After  string `json:"source_sha256_after"`
	State              string `json:"state"`
	LastError          string `json:"last_error"`
}

type GetWorkspaceRunFileParams struct {
	RunKey       string `json:"run_key"`
	RelativePath string `json:"relative_path"`
}

type ListWorkspaceRunFilesParams struct {
	RunKey string `json:"run_key"`
	State  string `json:"state"`
	Limit  int32  `json:"limit"`
}

type CreateTopologyApprovalParams struct {
	ID                   string          `json:"id"`
	RequestedBy          string          `json:"requested_by"`
	Reason               string          `json:"reason"`
	CreatedAt            time.Time       `json:"created_at"`
	ExpireAt             time.Time       `json:"expire_at"`
	ArchHash             string          `json:"arch_hash"`
	ProposedArchitecture json.RawMessage `json:"proposed_architecture"`
}

type InsertPromptVersionParams struct {
	PromptKey       string          `json:"prompt_key"`
	Title           string          `json:"title"`
	AgentKey        string          `json:"agent_key"`
	ToolName        string          `json:"tool_name"`
	PromptText      string          `json:"prompt_text"`
	Variables       json.RawMessage `json:"variables"`
	Tags            json.RawMessage `json:"tags"`
	Enabled         bool            `json:"enabled"`
	CreatedBy       string          `json:"created_by"`
	UpdatedBy       string          `json:"updated_by"`
	SourceUpdatedAt time.Time       `json:"source_updated_at"`
}

type UpsertPromptTemplateParams struct {
	PromptKey   string          `json:"prompt_key"`
	Title       string          `json:"title"`
	AgentKey    string          `json:"agent_key"`
	ToolName    string          `json:"tool_name"`
	PromptText  string          `json:"prompt_text"`
	Variables   json.RawMessage `json:"variables"`
	Tags        json.RawMessage `json:"tags"`
	Description string          `json:"description"`
	Enabled     bool            `json:"enabled"`
	CreatedBy   string          `json:"created_by"`
	UpdatedBy   string          `json:"updated_by"`
}

type ListPromptTemplatesParams struct {
	AgentKey string `json:"agent_key"`
	Keyword  string `json:"keyword"`
	Limit    int32  `json:"limit"`
}

type InsertCommandCardVersionParams struct {
	CardKey         string          `json:"card_key"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	CommandTemplate string          `json:"command_template"`
	ArgsSchema      json.RawMessage `json:"args_schema"`
	RiskLevel       string          `json:"risk_level"`
	Enabled         bool            `json:"enabled"`
	CreatedBy       string          `json:"created_by"`
	UpdatedBy       string          `json:"updated_by"`
	SourceUpdatedAt time.Time       `json:"source_updated_at"`
}

type UpsertCommandCardParams struct {
	CardKey         string          `json:"card_key"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	CommandTemplate string          `json:"command_template"`
	ArgsSchema      json.RawMessage `json:"args_schema"`
	RiskLevel       string          `json:"risk_level"`
	Enabled         bool            `json:"enabled"`
	CreatedBy       string          `json:"created_by"`
	UpdatedBy       string          `json:"updated_by"`
}

type ListCommandCardsParams struct {
	Keyword string `json:"keyword"`
	Limit   int32  `json:"limit"`
}

type CreateInteractionParams struct {
	ThreadID       string          `json:"thread_id"`
	ParentID       *int64          `json:"parent_id"`
	Sender         string          `json:"sender"`
	Receiver       string          `json:"receiver"`
	MsgType        string          `json:"msg_type"`
	Status         string          `json:"status"`
	RequiresReview bool            `json:"requires_review"`
	Payload        json.RawMessage `json:"payload"`
}

type ListInteractionsParams struct {
	ThreadID string `json:"thread_id"`
	Keyword  string `json:"keyword"`
	Limit    int32  `json:"limit"`
}

type ReviewInteractionParams struct {
	Status     string `json:"status"`
	ReviewedBy string `json:"reviewed_by"`
	ReviewNote string `json:"review_note"`
	ID         int64  `json:"id"`
}
