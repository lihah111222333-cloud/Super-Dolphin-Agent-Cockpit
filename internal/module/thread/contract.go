package thread

import (
	"context"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type Service interface {
	Start(ctx context.Context, req StartRequest) (StartResult, error)
	Resume(ctx context.Context, req ResumeRequest) error
	Fork(ctx context.Context, threadID string) (ForkResult, error)
	Recover(ctx context.Context, threadID string) error

	List(ctx context.Context) ([]Ref, error)
	Get(ctx context.Context, id string) (*Ref, error)
	ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error)
	ReadMessages(ctx context.Context, threadID string, limit int, before string) ([]dto.Message, error)
	Archive(ctx context.Context, threadID string) error
	Unarchive(ctx context.Context, threadID string) error
	ListByStatus(ctx context.Context, status string) ([]Ref, error)
	ListByCWD(ctx context.Context, cwdPrefix string) ([]Ref, error)
	SendCommand(ctx context.Context, threadID, command, args string) (any, error)
	SetName(ctx context.Context, threadID, name string) error
	Delete(ctx context.Context, threadID string) error
}

type StartRequest struct {
	Provider string
	AgentID  string
	CWD      string
	Model    string
	Prompt   string
}

type StartResult struct {
	ThreadID string
	AgentID  string
}

type ResumeRequest struct {
	Provider string
	AgentID  string
	ThreadID string
}

type ForkResult struct {
	NewThreadID string
}

type LaunchAgentRequest struct {
	AgentID  string
	Name     string
	ParentID string
	Cwd      string
	Command  []string
	Env      []string
}

type Ref struct {
	ID      string
	Name    string
	AgentID string
}
