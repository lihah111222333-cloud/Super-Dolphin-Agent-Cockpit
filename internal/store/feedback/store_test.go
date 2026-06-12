package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querierStub struct {
	insertFn        func(context.Context, sqlc.InsertAgentFeedbackEventParams) (sqlc.InsertAgentFeedbackEventRow, error)
	listByThreadFn  func(context.Context, sqlc.ListAgentFeedbackEventsByThreadParams) ([]sqlc.ListAgentFeedbackEventsByThreadRow, error)
	listByAgentFn   func(context.Context, sqlc.ListAgentFeedbackEventsByAgentParams) ([]sqlc.ListAgentFeedbackEventsByAgentRow, error)
	lastInsertParam sqlc.InsertAgentFeedbackEventParams
}

func (q *querierStub) InsertAgentFeedbackEvent(ctx context.Context, p sqlc.InsertAgentFeedbackEventParams) (sqlc.InsertAgentFeedbackEventRow, error) {
	q.lastInsertParam = p
	if q.insertFn != nil {
		return q.insertFn(ctx, p)
	}
	return sqlc.InsertAgentFeedbackEventRow{ID: 1, ThreadID: p.ThreadID, EventType: p.EventType}, nil
}
func (q *querierStub) ListAgentFeedbackEventsByThread(ctx context.Context, p sqlc.ListAgentFeedbackEventsByThreadParams) ([]sqlc.ListAgentFeedbackEventsByThreadRow, error) {
	if q.listByThreadFn != nil {
		return q.listByThreadFn(ctx, p)
	}
	return nil, nil
}
func (q *querierStub) ListAgentFeedbackEventsByAgent(ctx context.Context, p sqlc.ListAgentFeedbackEventsByAgentParams) ([]sqlc.ListAgentFeedbackEventsByAgentRow, error) {
	if q.listByAgentFn != nil {
		return q.listByAgentFn(ctx, p)
	}
	return nil, nil
}

func TestInsert_RequiresThreadID(t *testing.T) {
	t.Parallel()
	s := &store{q: &querierStub{}}
	_, err := s.Insert(context.Background(), Event{EventType: "thumbs_up"})
	if err == nil || !errors.Is(err, errEmptyThreadID) {
		t.Fatalf("want errEmptyThreadID, got %v", err)
	}
}

func TestInsert_RequiresEventType(t *testing.T) {
	t.Parallel()
	s := &store{q: &querierStub{}}
	_, err := s.Insert(context.Background(), Event{ThreadID: "t1"})
	if err == nil || !errors.Is(err, errEmptyEventType) {
		t.Fatalf("want errEmptyEventType, got %v", err)
	}
}

func TestInsert_DefaultPayloadIsEmptyObject(t *testing.T) {
	t.Parallel()
	q := &querierStub{}
	s := &store{q: q}
	_, err := s.Insert(context.Background(), Event{
		ThreadID:  "t1",
		EventType: "thumbs_up",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	payload, _ := q.lastInsertParam.Payload.([]byte)
	if string(payload) != `{}` {
		t.Fatalf("want default payload {}, got %s", string(payload))
	}
}

func TestInsert_ForwardsAllFields(t *testing.T) {
	t.Parallel()
	q := &querierStub{}
	s := &store{q: q}
	versionID := int64(42)
	_, err := s.Insert(context.Background(), Event{
		ThreadID:        "  t1  ",
		TurnID:          " turn-7 ",
		AgentKey:        " sql_expert ",
		PromptVersionID: &versionID,
		EventType:       " thumbs_down ",
		Actor:           " user ",
		Payload:         json.RawMessage(`{"note":"unhelpful"}`),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.lastInsertParam.ThreadID != "t1" || q.lastInsertParam.TurnID != "turn-7" ||
		q.lastInsertParam.AgentKey != "sql_expert" || q.lastInsertParam.EventType != "thumbs_down" ||
		q.lastInsertParam.Actor != "user" {
		t.Fatalf("forwarded wrong fields: %+v", q.lastInsertParam)
	}
	if q.lastInsertParam.PromptVersionID == nil || *q.lastInsertParam.PromptVersionID != 42 {
		t.Fatalf("wrong prompt_version_id: %v", q.lastInsertParam.PromptVersionID)
	}
}

func TestListByThread_RequiresThreadID(t *testing.T) {
	t.Parallel()
	s := &store{q: &querierStub{}}
	_, err := s.ListByThread(context.Background(), "", 10)
	if err == nil {
		t.Fatalf("expected error for empty thread_id")
	}
}

func TestListByThread_DefaultsLimit(t *testing.T) {
	t.Parallel()
	var captured sqlc.ListAgentFeedbackEventsByThreadParams
	q := &querierStub{
		listByThreadFn: func(_ context.Context, p sqlc.ListAgentFeedbackEventsByThreadParams) ([]sqlc.ListAgentFeedbackEventsByThreadRow, error) {
			captured = p
			return nil, nil
		},
	}
	s := &store{q: q}
	_, err := s.ListByThread(context.Background(), "t1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.Limit != 100 {
		t.Fatalf("want default limit 100, got %d", captured.Limit)
	}
}

func TestListByAgentKey_PassesThrough(t *testing.T) {
	t.Parallel()
	var captured sqlc.ListAgentFeedbackEventsByAgentParams
	q := &querierStub{
		listByAgentFn: func(_ context.Context, p sqlc.ListAgentFeedbackEventsByAgentParams) ([]sqlc.ListAgentFeedbackEventsByAgentRow, error) {
			captured = p
			return nil, nil
		},
	}
	s := &store{q: q}
	_, err := s.ListByAgentKey(context.Background(), "sql_expert", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.AgentKey != "sql_expert" || captured.Limit != 7 {
		t.Fatalf("wrong params: %+v", captured)
	}
}
