package feedback

import (
	"context"
	"errors"
	"strings"

	feedbackstore "github.com/anthropic-ai/super-agent-v3/internal/store/feedback"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

type service struct {
	store feedbackstore.Store
}

var _ Service = (*service)(nil)

// NewService creates the feedback service around the persistence port.
func NewService(store feedbackstore.Store) Service {
	return &service{store: store}
}

var errServiceDisabled = errors.New("feedback: service not wired (store is nil)")

// Record 记录feedback。
func (s *service) Record(ctx context.Context, req RecordRequest) (RecordResult, error) {
	ctx = util.NonNilContext(ctx)
	if s.store == nil {
		return RecordResult{}, errServiceDisabled
	}
	threadID := strings.TrimSpace(req.ThreadID)
	eventType := strings.TrimSpace(req.EventType)
	if threadID == "" || eventType == "" {
		return RecordResult{}, errors.New("feedback/record: thread_id and event_type are required")
	}
	ev, err := s.store.Insert(ctx, feedbackstore.Event{
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
