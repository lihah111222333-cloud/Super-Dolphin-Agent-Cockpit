package feedback

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type fakeFeedbackStore struct {
	inserted contract.FeedbackEvent
	err      error
}

// Insert records the event passed by the service and returns a deterministic id.
func (s *fakeFeedbackStore) Insert(_ context.Context, ev contract.FeedbackEvent) (contract.FeedbackEvent, error) {
	s.inserted = ev
	if s.err != nil {
		return contract.FeedbackEvent{}, s.err
	}
	ev.ID = 42
	return ev, nil
}

// ListByThread satisfies contract.FeedbackEventStore for tests that only write.
func (s *fakeFeedbackStore) ListByThread(context.Context, string, int32) ([]contract.FeedbackEvent, error) {
	return nil, nil
}

// ListByAgentKey satisfies contract.FeedbackEventStore for tests that only write.
func (s *fakeFeedbackStore) ListByAgentKey(context.Context, string, int32) ([]contract.FeedbackEvent, error) {
	return nil, nil
}

// TestServiceRecordTrimsAndPersistsFeedbackEvent covers Record normalization
// before the service hands the event to the persistence port.
func TestServiceRecordTrimsAndPersistsFeedbackEvent(t *testing.T) {
	store := &fakeFeedbackStore{}
	svc := NewService(store)
	payload := json.RawMessage(`{"score":1}`)

	result, err := svc.Record(context.Background(), RecordRequest{
		ThreadID:  " thread-1 ",
		TurnID:    " turn-1 ",
		AgentKey:  " agent-main ",
		EventType: " thumbs_up ",
		Actor:     " user ",
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if result.ID != 42 || result.EventType != "thumbs_up" || !result.Recorded {
		t.Fatalf("unexpected result: %+v", result)
	}
	if store.inserted.ThreadID != "thread-1" || store.inserted.TurnID != "turn-1" ||
		store.inserted.AgentKey != "agent-main" || store.inserted.Actor != "user" {
		t.Fatalf("event fields were not normalized: %+v", store.inserted)
	}
	if string(store.inserted.Payload) != string(payload) {
		t.Fatalf("payload mismatch: got %s want %s", store.inserted.Payload, payload)
	}
}

// TestServiceRecordRequiresThreadAndEventType covers the required fields for
// user/system feedback capture.
func TestServiceRecordRequiresThreadAndEventType(t *testing.T) {
	svc := NewService(&fakeFeedbackStore{})

	_, err := svc.Record(context.Background(), RecordRequest{ThreadID: "thread-1"})
	if err == nil {
		t.Fatal("Record succeeded without event type")
	}
	if !strings.Contains(err.Error(), "thread_id and event_type") {
		t.Fatalf("unexpected error: %v", err)
	}
}
