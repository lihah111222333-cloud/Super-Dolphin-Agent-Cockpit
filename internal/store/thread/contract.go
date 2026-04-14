package thread

import (
	"context"
	"encoding/json"
)

type Store interface {
	GetByThreadID(ctx context.Context, threadID string) (*Thread, error)
	GetByPort(ctx context.Context, port int32) (*Thread, error)
	ListAll(ctx context.Context) ([]Thread, error)
	ListRunning(ctx context.Context) ([]Thread, error)
	ListRecoverable(ctx context.Context) ([]Thread, error)
	ListRunningAgents(ctx context.Context) ([]RunningAgent, error)
	Upsert(ctx context.Context, params UpsertParams) error
	SavePromptSnapshot(ctx context.Context, threadID string, snapshot PromptSnapshot) error
	LoadPromptSnapshot(ctx context.Context, threadID string) (*PromptSnapshot, error)
	UpdateStatus(ctx context.Context, params UpdateStatusParams) error
	DeleteByThreadID(ctx context.Context, threadID string) error
	ResetRunning(ctx context.Context) error
	ExpireStale(ctx context.Context, params ExpireStaleParams) (int64, error)
	RunningExists(ctx context.Context, threadID string) (bool, error)
	ListCwds(ctx context.Context) ([]ThreadCwd, error)
	ListCwdsByPrefix(ctx context.Context, prefix string) ([]ThreadCwd, error)
}

type UpsertParams struct {
	ThreadID       string
	Prompt         string
	Model          string
	Cwd            string
	Status         string
	Port           int32
	PID            int32
	CreatedAt      int64
	UpdatedAt      int64
	OwnerThreadID  string
	ConfigOverride json.RawMessage
}

type UpdateStatusParams struct {
	ThreadID  string
	Status    string
	UpdatedAt int64
}

type ExpireStaleParams struct {
	UpdatedAt int64
	Cutoff    int64
}

type Thread struct {
	ThreadID        string
	AgentID         string
	Prompt          string
	Model           string
	Cwd             string
	Status          string
	Port            int32
	PID             int32
	CreatedAt       int64
	UpdatedAt       int64
	FinishedAt      *int64
	LastEventType   string
	ErrorMessage    string
	WorkspaceRunKey string
	OwnerThreadID   string
	ConfigOverride  json.RawMessage
}

type PromptSnapshot struct {
	BaseInstructions      string            `json:"base_instructions"`
	DeveloperInstructions string            `json:"developer_instructions"`
	SectionSnapshot       map[string]string `json:"section_snapshot"`
	Generation            int64             `json:"generation"`
}

type RunningAgent struct {
	ThreadID string
	Port     int32
	PID      int32
	Status   string
}

type ThreadCwd struct {
	ThreadID string
	Cwd      string
}
