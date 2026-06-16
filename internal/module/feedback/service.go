package feedback

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

type service struct {
	store contract.FeedbackEventStore
}

var _ Service = (*service)(nil)

// NewService creates the feedback service around the persistence port.
func NewService(store contract.FeedbackEventStore) Service {
	return &service{store: store}
}

var errServiceDisabled = errors.New("feedback: service not wired (store is nil)")

// Record 记录feedback。
func (s *service) Record(ctx context.Context, req RecordRequest) (RecordResult, error) {
	ctx = kernel.NonNilContext(ctx)
	if s.store == nil {
		return RecordResult{}, errServiceDisabled
	}
	threadID := strings.TrimSpace(req.ThreadID)
	eventType := strings.TrimSpace(req.EventType)
	if threadID == "" || eventType == "" {
		return RecordResult{}, errors.New("feedback/record: thread_id and event_type are required")
	}
	ev, err := s.store.Insert(ctx, contract.FeedbackEvent{
		ThreadID:        threadID,
		TurnID:          strings.TrimSpace(req.TurnID),
		AgentKey:        strings.TrimSpace(req.AgentKey),
		PromptVersionID: req.PromptVersionID,
		EventType:       eventType,
		Actor:           strings.TrimSpace(req.Actor),
		Payload:         req.Payload,
	})
	if err != nil {
		return RecordResult{}, err
	}
	return RecordResult{ID: ev.ID, EventType: ev.EventType, Recorded: true}, nil
}
