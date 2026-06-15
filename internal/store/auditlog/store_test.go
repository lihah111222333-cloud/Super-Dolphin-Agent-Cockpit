package auditlog

import (
	"context"
	"errors"
	"testing"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type fakeQuerier struct {
	events    []sqlc.ListAuditEventsRow
	insertErr error
	listErr   error
	inserted  []sqlc.InsertAuditEventParams
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{}
}

func (f *fakeQuerier) ListAuditEvents(_ context.Context, p sqlc.ListAuditEventsParams) ([]sqlc.ListAuditEventsRow, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if int(p.LimitCount) > 0 && int(p.LimitCount) < len(f.events) {
		return f.events[:p.LimitCount], nil
	}
	return f.events, nil
}

func (f *fakeQuerier) InsertAuditEvent(_ context.Context, p sqlc.InsertAuditEventParams) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, p)
	f.events = append(f.events, sqlc.ListAuditEventsRow{
		ID:        int64(len(f.events) + 1),
		Ts:        p.Ts,
		EventType: p.EventType,
		Action:    p.Action,
		Result:    p.Result,
		Actor:     p.Actor,
		Target:    p.Target,
		Detail:    p.Detail,
		Level:     p.Level,
		Extra:     p.Extra,
	})
	return nil
}

func TestStore_Insert_Success(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	s := newStoreForTest(fq)
	err := s.Insert(context.Background(), InsertParams{
		EventType: "dag",
		Action:    "create",
		Result:    "ok",
		Actor:     "tester",
		Extra:     []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if len(fq.inserted) != 1 {
		t.Fatalf("expected 1 insert, got %d", len(fq.inserted))
	}
	if fq.inserted[0].EventType != "dag" {
		t.Fatalf("inserted EventType = %q, want dag", fq.inserted[0].EventType)
	}
	if fq.inserted[0].Ts == 0 {
		t.Fatalf("inserted Ts = 0, want Go epoch milliseconds")
	}
}

func TestStore_InsertRejectsInvalidExtraJSON(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	s := newStoreForTest(fq)

	err := s.Insert(context.Background(), InsertParams{EventType: "dag", Extra: []byte(`not-json`)})
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}
	if len(fq.inserted) != 0 {
		t.Fatalf("Insert() called query despite invalid JSON: %+v", fq.inserted)
	}
}

func TestStore_Insert_DBError(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	fq.insertErr = errors.New("disk full")
	s := newStoreForTest(fq)
	err := s.Insert(context.Background(), InsertParams{EventType: "dag", Extra: []byte(`{}`)})
	if err == nil {
		t.Fatal("expected error from Insert")
	}
	var se *platformdb.StoreError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StoreError, got %T: %v", err, err)
	}
}

func TestStore_List_Success(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	s := newStoreForTest(fq)
	s.Insert(context.Background(), InsertParams{EventType: "dag", Action: "create", Extra: []byte(`{}`)})
	s.Insert(context.Background(), InsertParams{EventType: "dag", Action: "delete", Extra: []byte(`{}`)})
	got, err := s.List(context.Background(), ListFilter{Limit: 100})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List() returned %d items, want 2", len(got))
	}
}

func TestStore_List_Empty(t *testing.T) {
	t.Parallel()
	s := newStoreForTest(newFakeQuerier())
	got, err := s.List(context.Background(), ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List() returned %d items, want 0", len(got))
	}
}

func TestStore_List_DBError(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	fq.listErr = errors.New("timeout")
	s := newStoreForTest(fq)
	_, err := s.List(context.Background(), ListFilter{Limit: 10})
	if err == nil {
		t.Fatal("expected error from List")
	}
}
