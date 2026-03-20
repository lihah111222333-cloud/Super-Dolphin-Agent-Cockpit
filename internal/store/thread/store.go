package thread

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func (s *store) GetByThreadID(ctx context.Context, threadID string) (*Thread, error) {
	row, err := s.q.GetAgentThreadByID(ctx, threadID)
	if err != nil {
		return nil, err
	}
	result := mapThread(row)
	return &result, nil
}

func (s *store) GetByPort(ctx context.Context, port int32) (*Thread, error) {
	row, err := s.q.GetAgentThreadByPort(ctx, port)
	if err != nil {
		return nil, err
	}
	result := mapThread(row)
	return &result, nil
}

func (s *store) ListAll(ctx context.Context) ([]Thread, error) {
	rows, err := s.q.ListAgentThreads(ctx)
	if err != nil {
		return nil, err
	}
	return mapThreads(rows), nil
}

func (s *store) ListRunning(ctx context.Context) ([]Thread, error) {
	rows, err := s.q.ListRunningAgentThreads(ctx)
	if err != nil {
		return nil, err
	}
	return mapThreads(rows), nil
}

func (s *store) ListRecoverable(ctx context.Context) ([]Thread, error) {
	rows, err := s.q.ListRecoverableAgentThreads(ctx)
	if err != nil {
		return nil, err
	}
	return mapThreads(rows), nil
}

func (s *store) ListRunningAgents(ctx context.Context) ([]RunningAgent, error) {
	rows, err := s.q.ListRunningAgents(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]RunningAgent, len(rows))
	for i, row := range rows {
		result[i] = RunningAgent{
			ThreadID: row.ThreadID,
			Port:     row.Port,
			PID:      row.PID,
			Status:   row.Status,
		}
	}
	return result, nil
}

func (s *store) Upsert(ctx context.Context, params UpsertParams) error {
	return s.q.UpsertAgentThread(ctx, sqlc.UpsertAgentThreadParams{
		ThreadID:      params.ThreadID,
		Prompt:        params.Prompt,
		Model:         params.Model,
		Cwd:           params.Cwd,
		Status:        params.Status,
		Port:          params.Port,
		PID:           params.PID,
		CreatedAt:     params.CreatedAt,
		UpdatedAt:     params.UpdatedAt,
		OwnerThreadID: params.OwnerThreadID,
	})
}

func (s *store) UpdateStatus(ctx context.Context, params UpdateStatusParams) error {
	return s.q.UpdateAgentThreadStatus(ctx, sqlc.UpdateAgentThreadStatusParams{
		ThreadID:  params.ThreadID,
		Status:    params.Status,
		UpdatedAt: params.UpdatedAt,
	})
}

func (s *store) DeleteByThreadID(ctx context.Context, threadID string) error {
	return s.q.DeleteAgentThreadByID(ctx, sqlc.DeleteAgentThreadByIDParams{
		ThreadID: threadID,
	})
}

func (s *store) ResetRunning(ctx context.Context) error {
	return s.q.ResetRunningAgentThreads(ctx)
}

func (s *store) ExpireStale(ctx context.Context, params ExpireStaleParams) (int64, error) {
	return s.q.ExpireStaleAgentThreads(ctx, sqlc.ExpireStaleAgentThreadsParams{
		UpdatedAt: params.UpdatedAt,
		Cutoff:    params.Cutoff,
	})
}

func (s *store) RunningExists(ctx context.Context, threadID string) (bool, error) {
	return s.q.AgentThreadRunningExists(ctx, threadID)
}

func (s *store) ListCwds(ctx context.Context) ([]ThreadCwd, error) {
	rows, err := s.q.ListAgentThreadCwds(ctx)
	if err != nil {
		return nil, err
	}
	return mapThreadCwds(rows), nil
}

func (s *store) ListCwdsByPrefix(ctx context.Context, prefix string) ([]ThreadCwd, error) {
	rows, err := s.q.ListAgentThreadCwdsByPrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}
	return mapThreadCwds(rows), nil
}

func mapThread(row sqlc.AgentThread) Thread {
	return Thread{
		ThreadID:        row.ThreadID,
		AgentID:         row.AgentID,
		Prompt:          row.Prompt,
		Model:           row.Model,
		Cwd:             row.Cwd,
		Status:          row.Status,
		Port:            row.Port,
		PID:             row.PID,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		FinishedAt:      row.FinishedAt,
		LastEventType:   row.LastEventType,
		ErrorMessage:    row.ErrorMessage,
		WorkspaceRunKey: row.WorkspaceRunKey,
		OwnerThreadID:   row.OwnerThreadID,
	}
}

func mapThreads(rows []sqlc.AgentThread) []Thread {
	result := make([]Thread, len(rows))
	for i, row := range rows {
		result[i] = mapThread(row)
	}
	return result
}

func mapThreadCwds(rows []sqlc.AgentThreadCwdRow) []ThreadCwd {
	result := make([]ThreadCwd, len(rows))
	for i, row := range rows {
		result[i] = ThreadCwd{
			ThreadID: row.ThreadID,
			Cwd:      row.Cwd,
		}
	}
	return result
}
