package feedback

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querier interface {
	InsertAgentFeedbackEvent(ctx context.Context, arg sqlc.InsertAgentFeedbackEventParams) (sqlc.InsertAgentFeedbackEventRow, error)
	ListAgentFeedbackEventsByThread(ctx context.Context, arg sqlc.ListAgentFeedbackEventsByThreadParams) ([]sqlc.ListAgentFeedbackEventsByThreadRow, error)
	ListAgentFeedbackEventsByAgent(ctx context.Context, arg sqlc.ListAgentFeedbackEventsByAgentParams) ([]sqlc.ListAgentFeedbackEventsByAgentRow, error)
}

type store struct {
	q querier
}

// NewStore 创建存储。
func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

var errEmptyThreadID = errors.New("feedback.Insert: thread_id is required")
var errEmptyEventType = errors.New("feedback.Insert: event_type is required")

// Insert 插入feedback存储。
func (s *store) Insert(ctx context.Context, ev Event) (Event, error) {
	threadID := strings.TrimSpace(ev.ThreadID)
	if threadID == "" {
		return Event{}, wrapErr(errEmptyThreadID, "insert")
	}
	eventType := strings.TrimSpace(ev.EventType)
	if eventType == "" {
		return Event{}, wrapErr(errEmptyEventType, "insert")
	}
	payload := ev.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	row, err := s.q.InsertAgentFeedbackEvent(ctx, sqlc.InsertAgentFeedbackEventParams{
		ThreadID:        threadID,
		TurnID:          strings.TrimSpace(ev.TurnID),
		AgentKey:        strings.TrimSpace(ev.AgentKey),
		EventType:       eventType,
		Actor:           strings.TrimSpace(ev.Actor),
		PromptVersionID: ev.PromptVersionID,
		Payload:         []byte(payload),
	})
	if err != nil {
		return Event{}, wrapErr(err, "insert")
	}
	return fromRow(row), nil
}

// ListByThread 按线程列出feedback存储。
func (s *store) ListByThread(ctx context.Context, threadID string, limit int32) ([]Event, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, wrapErr(errEmptyThreadID, "list_by_thread")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.ListAgentFeedbackEventsByThread(ctx, sqlc.ListAgentFeedbackEventsByThreadParams{
		ThreadID: threadID,
		Limit:    int64(limit),
	})
	if err != nil {
		return nil, wrapErr(err, "list_by_thread")
	}
	return mapRows(rows), nil
}

// ListByAgentKey 按代理键列出feedback存储。
func (s *store) ListByAgentKey(ctx context.Context, agentKey string, limit int32) ([]Event, error) {
	agentKey = strings.TrimSpace(agentKey)
	if agentKey == "" {
		return nil, wrapErr(errors.New("feedback.ListByAgentKey: agent_key is required"), "list_by_agent_key")
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.ListAgentFeedbackEventsByAgent(ctx, sqlc.ListAgentFeedbackEventsByAgentParams{
		AgentKey: agentKey,
		Limit:    int64(limit),
	})
	if err != nil {
		return nil, wrapErr(err, "list_by_agent_key")
	}
	return mapRows(rows), nil
}

func fromRow(row any) Event {
	switch r := row.(type) {
	case sqlc.InsertAgentFeedbackEventRow:
		return feedbackEventFromFields(r.ID, r.ThreadID, r.TurnID, r.AgentKey, r.PromptVersionID,
			r.EventType, r.Actor, r.Payload, r.CreatedAt)
	case sqlc.ListAgentFeedbackEventsByThreadRow:
		return feedbackEventFromFields(r.ID, r.ThreadID, r.TurnID, r.AgentKey, r.PromptVersionID,
			r.EventType, r.Actor, r.Payload, r.CreatedAt)
	case sqlc.ListAgentFeedbackEventsByAgentRow:
		return feedbackEventFromFields(r.ID, r.ThreadID, r.TurnID, r.AgentKey, r.PromptVersionID,
			r.EventType, r.Actor, r.Payload, r.CreatedAt)
	default:
		panic("unsupported feedback row type")
	}
}

func feedbackEventFromFields(
	id int64,
	threadID, turnID, agentKey string,
	promptVersionID *int64,
	eventType, actor string,
	payload []byte,
	createdAt int64,
) Event {
	return Event{
		ID:              id,
		ThreadID:        threadID,
		TurnID:          turnID,
		AgentKey:        agentKey,
		PromptVersionID: promptVersionID,
		EventType:       eventType,
		Actor:           actor,
		Payload:         append(json.RawMessage(nil), payload...),
		CreatedAt:       platformdb.TimeFromMillis(createdAt),
	}
}

func mapRows[T sqlc.ListAgentFeedbackEventsByThreadRow | sqlc.ListAgentFeedbackEventsByAgentRow](rows []T) []Event {
	out := make([]Event, len(rows))
	for i, row := range rows {
		out[i] = fromRow(row)
	}
	return out
}

func wrapErr(err error, op string) error {
	return platformdb.WrapStoreError(err, op, "feedback")
}
