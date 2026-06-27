package insight

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"
)

func newTestService(t *testing.T, store insightReader) Service {
	t.Helper()
	return NewService(slog.Default(), store)
}

func TestServiceListRecentRejectsNegativeLimit(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, &fakeInsightStore{})
	if _, err := svc.ListRecent(context.Background(), -1); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("want ErrInvalidLimit, got %v", err)
	}
}

func TestServiceListByThreadRequiresThread(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, &fakeInsightStore{})
	if _, err := svc.ListByThread(context.Background(), "  ", 10); err == nil {
		t.Fatal("expected error for empty thread_id")
	}
}

func TestServiceListRecentMapsRows(t *testing.T) {
	t.Parallel()
	skills, _ := json.Marshal([]string{"a", "b"})
	success := false
	row := insightRecord{
		ID:             42,
		ThreadID:       "t",
		LocalTurnID:    "lt",
		ProviderTurnID: "pt",
		Status:         insightStatusFailed,
		Success:        &success,
		ToolCalls:      3,
		StartedAt:      time.Unix(1_700_000_000, 0).UTC(),
		CompletedAt:    time.Unix(1_700_000_005, 0).UTC(),
		SkillsSelected: skills,
	}
	store := &fakeInsightStore{
		listRecentFn: func(_ context.Context, limit int32) ([]insightRecord, error) {
			if limit != 5 {
				t.Fatalf("limit forward failed: %d", limit)
			}
			return []insightRecord{row}, nil
		},
	}
	svc := newTestService(t, store)
	snaps, err := svc.ListRecent(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListRecent error = %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("len = %d, want 1", len(snaps))
	}
	assertRecentInsightSnapshot(t, snaps[0])
}

func assertRecentInsightSnapshot(t *testing.T, s Snapshot) {
	t.Helper()
	if s.ID != 42 || s.Status != insightStatusFailed || s.ToolCalls != 3 {
		t.Fatalf("basic fields wrong: %+v", s)
	}
	if s.Success == nil || *s.Success != false {
		t.Fatalf("Success = %v, want pointer to false", s.Success)
	}
	if s.StartedAt == "" || s.CompletedAt == "" {
		t.Fatalf("time fields not formatted: %+v", s)
	}
	if len(s.SkillsSelected) != 2 || s.SkillsSelected[0] != "a" {
		t.Fatalf("SkillsSelected = %v", s.SkillsSelected)
	}
}

func TestServiceListObservedApprovalRequestsForwards(t *testing.T) {
	t.Parallel()
	var gotThread string
	store := &fakeInsightStore{
		listApprovalsFn: func(_ context.Context, threadID string, _ int32) ([]insightApprovalRow, error) {
			gotThread = threadID
			return []insightApprovalRow{{ID: 1, ThreadID: threadID, ApprovalRequests: 2}}, nil
		},
	}
	svc := newTestService(t, store)
	rows, err := svc.ListObservedApprovalRequests(context.Background(), "t", 0)
	if err != nil {
		t.Fatalf("ListObservedApprovalRequests error = %v", err)
	}
	if gotThread != "t" || len(rows) != 1 || rows[0].ApprovalRequests != 2 {
		t.Fatalf("wrong forwarded / mapped: threadID=%q rows=%+v", gotThread, rows)
	}
}
