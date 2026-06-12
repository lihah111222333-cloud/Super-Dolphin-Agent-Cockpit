package agentstatus

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type fakeQuerier struct {
	rows      map[string]sqlc.AgentStatus
	upsertErr error
	getErr    error
	listErr   error
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{rows: map[string]sqlc.AgentStatus{}}
}

func (f *fakeQuerier) UpsertAgentStatus(_ context.Context, p sqlc.UpsertAgentStatusParams) (sqlc.AgentStatus, error) {
	if f.upsertErr != nil {
		return sqlc.AgentStatus{}, f.upsertErr
	}
	now := time.Now().UnixMilli()
	row := sqlc.AgentStatus{
		AgentID:     p.AgentID,
		AgentName:   p.AgentName,
		SessionID:   p.SessionID,
		Status:      p.Status,
		StagnantSec: p.StagnantSec,
		Error:       p.Error,
		OutputTail:  p.OutputTail,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	f.rows[p.AgentID] = row
	return row, nil
}

func (f *fakeQuerier) GetAgentStatus(_ context.Context, arg sqlc.GetAgentStatusParams) (sqlc.AgentStatus, error) {
	agentID := arg.AgentID
	if f.getErr != nil {
		return sqlc.AgentStatus{}, f.getErr
	}
	row, ok := f.rows[agentID]
	if !ok {
		return sqlc.AgentStatus{}, errors.New("no rows in result set")
	}
	return row, nil
}

func (f *fakeQuerier) ListAgentStatuses(_ context.Context, arg sqlc.ListAgentStatusesParams) ([]sqlc.AgentStatus, error) {
	status := arg.Column1
	if f.listErr != nil {
		return nil, f.listErr
	}
	var result []sqlc.AgentStatus
	for _, row := range f.rows {
		if status == "" || row.Status == status {
			result = append(result, row)
		}
	}
	return result, nil
}

func TestStore_Upsert_Success(t *testing.T) {
	t.Parallel()
	s := newStoreForTest(newFakeQuerier())
	got, err := s.Upsert(context.Background(), UpsertParams{
		AgentID:   "agent-1",
		AgentName: "Test Agent",
		SessionID: "sess-1",
		Status:    "running",
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if got.AgentID != "agent-1" || got.AgentName != "Test Agent" {
		t.Fatalf("Upsert() returned wrong data: %+v", got)
	}
}

func TestStore_Upsert_DBError(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	fq.upsertErr = errors.New("connection refused")
	s := newStoreForTest(fq)
	_, err := s.Upsert(context.Background(), UpsertParams{AgentID: "agent-1"})
	if err == nil {
		t.Fatal("expected error from Upsert")
	}
	var se *platformdb.StoreError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StoreError, got %T: %v", err, err)
	}
}

func TestStore_Get_Success(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	s := newStoreForTest(fq)
	s.Upsert(context.Background(), UpsertParams{AgentID: "agent-1", Status: "idle"})
	got, err := s.Get(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.AgentID != "agent-1" {
		t.Fatalf("Get() AgentID = %q, want agent-1", got.AgentID)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	t.Parallel()
	s := newStoreForTest(newFakeQuerier())
	_, err := s.Get(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error from Get for missing agent")
	}
}

func TestStore_List_Success(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	s := newStoreForTest(fq)
	s.Upsert(context.Background(), UpsertParams{AgentID: "a1", Status: "running"})
	s.Upsert(context.Background(), UpsertParams{AgentID: "a2", Status: "idle"})
	got, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List() returned %d items, want 2", len(got))
	}
}

func TestStore_List_DBError(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	fq.listErr = errors.New("timeout")
	s := newStoreForTest(fq)
	_, err := s.List(context.Background(), "running")
	if err == nil {
		t.Fatal("expected error from List")
	}
}

func TestStore_Upsert_WithOutputTail(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	s := newStoreForTest(fq)
	tail := json.RawMessage(`{"last_line":"hello"}`)
	got, err := s.Upsert(context.Background(), UpsertParams{
		AgentID:    "agent-1",
		OutputTail: tail,
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if string(got.OutputTail) != string(tail) {
		t.Fatalf("OutputTail = %s, want %s", got.OutputTail, tail)
	}
}
