package taskdag

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func TestSQLiteRunEventAppendGoldenPayloads(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	run := createSQLiteRunWithMetadata(t, ctx, store, "run-events-golden", "dag-events-golden", `{}`)

	cases := []struct {
		name    string
		payload json.RawMessage
		want    string
	}{
		{name: "object", payload: json.RawMessage(`{"kind":"object","n":1}`), want: `[{"kind":"object","n":1}]`},
		{name: "array", payload: json.RawMessage(`[1,2,3]`), want: `[[1,2,3]]`},
		{name: "string", payload: json.RawMessage(`"hello"`), want: `["hello"]`},
		{name: "null", payload: json.RawMessage(`null`), want: `[null]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetSQLiteRunEvents(t, ctx, db, run.ID, `[]`)
			if _, err := store.appendTaskDagRunEvent(ctx, "dag-events-golden", run.ID, tc.payload); err != nil {
				t.Fatalf("appendTaskDagRunEvent(%s) error = %v", tc.name, err)
			}
			assertSQLiteRunEventsJSON(t, ctx, db, run.ID, tc.want)
		})
	}
}

func TestSQLiteRunEventAppendKeepsLastFifty(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	run := createSQLiteRunWithMetadata(t, ctx, store, "run-events-ring", "dag-events-ring", `{}`)

	resetSQLiteRunEvents(t, ctx, db, run.ID, numberedRunEventsJSON(t, 50, 0))
	if _, err := store.appendTaskDagRunEvent(ctx, "dag-events-ring", run.ID, json.RawMessage(`{"seq":50}`)); err != nil {
		t.Fatalf("append 51st event error = %v", err)
	}
	assertSQLiteRunEventWindow(t, ctx, db, run.ID, 50, 1, 50)

	resetSQLiteRunEvents(t, ctx, db, run.ID, numberedRunEventsJSON(t, 51, 0))
	if _, err := store.appendTaskDagRunEvent(ctx, "dag-events-ring", run.ID, json.RawMessage(`{"seq":51}`)); err != nil {
		t.Fatalf("append to 51 existing events error = %v", err)
	}
	assertSQLiteRunEventWindow(t, ctx, db, run.ID, 50, 2, 51)
}

func TestSQLiteRunEventAppendConcurrentWritersDoNotOverwrite(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	db.SetMaxOpenConns(8)
	store := NewStore(db).(*store)
	run := createSQLiteRunWithMetadata(t, ctx, store, "run-events-concurrent", "dag-events-concurrent", `{}`)

	const writers = 32
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.appendTaskDagRunEvent(ctx, "dag-events-concurrent", run.ID, json.RawMessage(fmt.Sprintf(`{"seq":%d}`, i)))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent append error = %v", err)
		}
	}
	events := loadSQLiteRunEvents(t, ctx, db, run.ID)
	if len(events) != writers {
		t.Fatalf("events len = %d, want %d", len(events), writers)
	}
	seen := map[int]bool{}
	for _, event := range events {
		seq, ok := event["seq"].(float64)
		if !ok {
			t.Fatalf("event missing numeric seq: %#v", event)
		}
		seen[int(seq)] = true
	}
	for i := 0; i < writers; i++ {
		if !seen[i] {
			t.Fatalf("missing event seq %d in %#v", i, events)
		}
	}
}

func TestSQLiteRecordNodeSpawnRetryAppendsNodeSpawnEvent(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	seedSQLiteTaskDAGTemplate(t, ctx, store)
	run := createSQLiteTaskDAGRun(t, ctx, store, "run-node-spawn-event", "dag-multi")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-multi", run.ID)

	if _, err := store.RecordNodeSpawn(ctx, RecordNodeSpawnInput{DagKey: "dag-multi", NodeKey: "root", RunID: run.ID, ThreadID: "thread-1"}); err != nil {
		t.Fatalf("first RecordNodeSpawn() error = %v", err)
	}
	if _, err := store.RecordNodeSpawn(ctx, RecordNodeSpawnInput{DagKey: "dag-multi", NodeKey: "root", RunID: run.ID, ThreadID: "thread-2"}); err != nil {
		t.Fatalf("retry RecordNodeSpawn() error = %v", err)
	}
	events := loadSQLiteRunEvents(t, ctx, db, run.ID)
	if len(events) != 1 {
		t.Fatalf("node_spawn events len = %d, want 1: %#v", len(events), events)
	}
	if events[0]["kind"] != "node_spawn" || events[0]["prev_thread_id"] != "thread-1" || events[0]["thread_id"] != "thread-2" {
		t.Fatalf("node_spawn event = %#v, want retry thread chain", events[0])
	}
}

func resetSQLiteRunEvents(t *testing.T, ctx context.Context, db execDB, runID int64, events string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `UPDATE task_dag_runs SET events = ? WHERE id = ?`, events, runID); err != nil {
		t.Fatalf("reset run events: %v", err)
	}
}

func assertSQLiteRunEventsJSON(t *testing.T, ctx context.Context, db queryDB, runID int64, want string) {
	t.Helper()
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT events FROM task_dag_runs WHERE id = ?`, runID).Scan(&raw); err != nil {
		t.Fatalf("select run events: %v", err)
	}
	if raw != want {
		t.Fatalf("events = %s, want %s", raw, want)
	}
}

func assertSQLiteRunEventWindow(t *testing.T, ctx context.Context, db queryDB, runID int64, wantLen, wantFirst, wantLast int) {
	t.Helper()
	events := loadSQLiteRunEvents(t, ctx, db, runID)
	if len(events) != wantLen {
		t.Fatalf("events len = %d, want %d", len(events), wantLen)
	}
	if got := int(events[0]["seq"].(float64)); got != wantFirst {
		t.Fatalf("first kept seq = %d, want %d", got, wantFirst)
	}
	if got := int(events[len(events)-1]["seq"].(float64)); got != wantLast {
		t.Fatalf("last kept seq = %d, want %d", got, wantLast)
	}
}

func loadSQLiteRunEvents(t *testing.T, ctx context.Context, db queryDB, runID int64) []map[string]any {
	t.Helper()
	var raw []byte
	if err := db.QueryRowContext(ctx, `SELECT events FROM task_dag_runs WHERE id = ?`, runID).Scan(&raw); err != nil {
		t.Fatalf("select run events: %v", err)
	}
	var events []map[string]any
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatalf("unmarshal events %s: %v", raw, err)
	}
	return events
}

func numberedRunEventsJSON(t *testing.T, count, offset int) string {
	t.Helper()
	events := make([]map[string]int, 0, count)
	for i := 0; i < count; i++ {
		events = append(events, map[string]int{"seq": offset + i})
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal numbered events: %v", err)
	}
	return string(raw)
}

type execDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type queryDB interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
