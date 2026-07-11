package feedback

import (
	"context"
	"errors"
	"testing"
)

type fakeStore struct {
	inserts      []Event
	nextID       int64
	insertErr    error
	lastThreadID string
}

func (f *fakeStore) Insert(_ context.Context, ev Event) (Event, error) {
	f.lastThreadID = ev.ThreadID
	if f.insertErr != nil {
		return Event{}, f.insertErr
	}
	f.nextID++
	ev.ID = f.nextID
	f.inserts = append(f.inserts, ev)
	return ev, nil
}

func TestService_RecordRejectsEmpty(t *testing.T) {
	t.Parallel()
	svc := NewService(nil, &fakeStore{})
	_, err := svc.Record(context.Background(), RecordRequest{EventType: "thumbs_up"})
	if err == nil {
		t.Fatalf("expected error on empty thread_id")
	}
	_, err = svc.Record(context.Background(), RecordRequest{ThreadID: "t1"})
	if err == nil {
		t.Fatalf("expected error on empty event_type")
	}
}

func TestService_RecordNilStoreIsExplicitError(t *testing.T) {
	t.Parallel()
	svc := NewService(nil, nil)
	_, err := svc.Record(context.Background(), RecordRequest{ThreadID: "t1", EventType: "thumbs_up"})
	if !errors.Is(err, errServiceDisabled) {
		t.Fatalf("want errServiceDisabled, got %v", err)
	}
}

func TestService_RecordForwardsToStore(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	svc := NewService(nil, store)
	v := int64(5)
	result, err := svc.Record(context.Background(), RecordRequest{
		ThreadID:        "t1",
		TurnID:          "turn-1",
		AgentKey:        "sql_expert",
		PromptVersionID: &v,
		EventType:       "thumbs_down",
		Actor:           "user",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Recorded || result.ID != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(store.inserts) != 1 || store.inserts[0].ThreadID != "t1" ||
		store.inserts[0].EventType != "thumbs_down" ||
		store.inserts[0].AgentKey != "sql_expert" {
		t.Fatalf("store insert drift: %+v", store.inserts)
	}
}
