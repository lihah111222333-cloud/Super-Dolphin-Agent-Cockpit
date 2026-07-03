package buslog

import (
	"context"
	"errors"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type fakeQuerier struct {
	rows      []sqlc.ListBusExceptionLogsRow
	detailRow sqlc.BusExceptionLog
	listErr   error
	getErr    error
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{}
}

func (f *fakeQuerier) ListBusExceptionLogs(_ context.Context, p sqlc.ListBusExceptionLogsParams) ([]sqlc.ListBusExceptionLogsRow, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if int(p.LimitCount) > 0 && int(p.LimitCount) < len(f.rows) {
		return f.rows[:p.LimitCount], nil
	}
	return f.rows, nil
}

func (f *fakeQuerier) GetBusExceptionLog(_ context.Context, _ sqlc.GetBusExceptionLogParams) (sqlc.BusExceptionLog, error) {
	if f.getErr != nil {
		return sqlc.BusExceptionLog{}, f.getErr
	}
	return f.detailRow, nil
}

func TestStore_List_Success(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	fq.rows = []sqlc.ListBusExceptionLogsRow{
		{Ts: time.Now().UnixMilli(), Category: "rpc", Severity: "error", Source: "dashboard", ToolName: "task_get_dag", Message: "failed"},
		{Ts: time.Now().UnixMilli(), Category: "bus", Severity: "warn", Source: "cron", ToolName: "", Message: "stale"},
	}
	s := newStoreForTest(fq)
	got, err := s.List(context.Background(), ListFilter{Limit: 100})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List() returned %d items, want 2", len(got))
	}
	if got[0].Category != "rpc" {
		t.Fatalf("List()[0].Category = %q, want rpc", got[0].Category)
	}
	if got[1].ToolName != "" {
		t.Fatalf("List()[1].ToolName = %q, want empty", got[1].ToolName)
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
	fq.listErr = errors.New("connection reset")
	s := newStoreForTest(fq)
	_, err := s.List(context.Background(), ListFilter{Limit: 10})
	if err == nil {
		t.Fatal("expected error from List")
	}
	var se *platformdb.StoreError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StoreError, got %T: %v", err, err)
	}
}

func TestStore_List_MapsFieldsCorrectly(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	fq.rows = []sqlc.ListBusExceptionLogsRow{
		{
			Ts:           time.Unix(1000, 0).UnixMilli(),
			Category:     "rpc",
			Severity:     "critical",
			Source:       "orchestration",
			ToolName:     "agent_launch",
			Message:      "timeout after 30s",
			Traceback:    "",
			Extra:        `{}`,
			HasTraceback: 1,
			HasExtra:     1,
		},
	}
	s := newStoreForTest(fq)
	got, err := s.List(context.Background(), ListFilter{Limit: 1})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List() returned %d items, want 1", len(got))
	}
	item := got[0]
	if item.Source != "orchestration" {
		t.Errorf("Source = %q, want orchestration", item.Source)
	}
	if item.Traceback != "" {
		t.Errorf("Traceback = %q, want empty list payload", item.Traceback)
	}
	if string(item.Extra) != `{}` {
		t.Errorf("Extra = %s, want lightweight object", item.Extra)
	}
	if !item.HasTraceback || !item.HasExtra {
		t.Errorf("flags = (%v, %v), want both true", item.HasTraceback, item.HasExtra)
	}
}

func TestStore_Get_MapsDetailFields(t *testing.T) {
	t.Parallel()
	fq := newFakeQuerier()
	fq.detailRow = sqlc.BusExceptionLog{
		ID:           7,
		Ts:           time.Unix(1000, 0).UnixMilli(),
		Category:     "rpc",
		Severity:     "critical",
		Source:       "orchestration",
		ToolName:     "agent_launch",
		Message:      "timeout after 30s",
		Traceback:    "goroutine 42\nstack trace",
		Extra:        `{"retry":true}`,
		HasTraceback: 1,
		HasExtra:     1,
	}
	s := newStoreForTest(fq)
	got, err := s.Get(context.Background(), 7)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Traceback != "goroutine 42\nstack trace" {
		t.Errorf("Traceback = %q, want multiline stack", got.Traceback)
	}
	if string(got.Extra) != `{"retry":true}` {
		t.Errorf("Extra = %s, want {\"retry\":true}", got.Extra)
	}
	if !got.HasTraceback || !got.HasExtra {
		t.Errorf("flags = (%v, %v), want both true", got.HasTraceback, got.HasExtra)
	}
}
