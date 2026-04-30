package taskdag

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

func TestCompleteNodeAndScheduleDownstream_Sequential_AtoB_BtoC(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"B"}, status: "pending", agent: "agent-c"},
	})

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "A",
		Status:  "done",
		Result:  json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	if res.Node == nil || res.Node.Status != "done" {
		t.Fatalf("complete A node = %+v", res.Node)
	}
	if got := scheduledKeys(res.ScheduledDownstream); !equalStrings(got, []string{"B"}) {
		t.Fatalf("after A scheduled = %v, want [B]", got)
	}
	// C must not be scheduled — depends_on=[B] still pending.
	if pendingForNode(db, "C") != 0 {
		t.Fatalf("C wakeup count = %d, want 0", pendingForNode(db, "C"))
	}
	// idempotency_key for B must follow plan convention.
	if got := res.ScheduledDownstream[0].IdempotencyKey; got != "dag/dag-1/B/start" {
		t.Fatalf("B idempotency_key = %q, want dag/dag-1/B/start", got)
	}
	// Payload must include upstream output path for A.
	bWakeup := lookupPendingWakeup(t, db, "B")
	assertUpstreamPayload(t, bWakeup, "agent-b", "A", "dag/dag-1/A/output.json")

	// Now mark B running (dispatcher would do this); complete B → C ready.
	transitionToRunning(t, db, "B")
	res2, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "B",
		Status:  "done",
		Result:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete B error = %v", err)
	}
	if got := scheduledKeys(res2.ScheduledDownstream); !equalStrings(got, []string{"C"}) {
		t.Fatalf("after B scheduled = %v, want [C]", got)
	}
}

func TestCompleteNodeAndScheduleDownstream_Parallel_FanOut(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: "agent-c"},
	})

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey:  "dag-1",
		NodeKey: "A",
		Status:  "done",
		Result:  json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	if got := scheduledKeys(res.ScheduledDownstream); !equalStrings(got, []string{"B", "C"}) {
		t.Fatalf("scheduled = %v, want [B C]", got)
	}
}

func TestCompleteNodeAndScheduleDownstream_Diamond_WaitsForAllUpstreams(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: "agent-c"},
		{key: "D", deps: []string{"B", "C"}, status: "pending", agent: "agent-d"},
	})

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", Status: "done", Result: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("complete A error = %v", err)
	}
	transitionToRunning(t, db, "B")
	transitionToRunning(t, db, "C")

	resB, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "B", Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete B error = %v", err)
	}
	if got := scheduledKeys(resB.ScheduledDownstream); len(got) != 0 {
		t.Fatalf("after B alone scheduled = %v, want []", got)
	}

	resC, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "C", Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete C error = %v", err)
	}
	if got := scheduledKeys(resC.ScheduledDownstream); !equalStrings(got, []string{"D"}) {
		t.Fatalf("after C scheduled = %v, want [D]", got)
	}
}

func TestCompleteNodeAndScheduleDownstream_PreExistingWakeupSkippedByIdempotencyKey(t *testing.T) {
	t.Parallel()

	// Seed a downstream wakeup row that already carries the idempotency
	// key the auto-scheduler would generate. CompleteNode-of-A must observe
	// the SQL ON CONFLICT path: zero new wakeup rows inserted, the result
	// slice empty, and the original wakeup unaffected.
	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})
	db.wakeupSeq = 1
	db.wakeups[1] = sqlc.TaskDagWakeup{
		ID:             1,
		DagKey:         "dag-1",
		NodeKey:        "B",
		WakeupKind:     "node_start",
		TargetAgentID:  "agent-b",
		PromptPayload:  []byte(`{"agent_id":"agent-b","note":"pre-seeded"}`),
		IdempotencyKey: "dag/dag-1/B/start",
		Status:         "pending",
		NextRetryAt:    timestamptzValue(now),
		CreatedAt:      timestamptzValue(now),
		UpdatedAt:      timestamptzValue(now),
	}

	res, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete error = %v", err)
	}
	if res.Node == nil || res.Node.Status != "done" {
		t.Fatalf("complete A node = %+v", res.Node)
	}
	if len(res.ScheduledDownstream) != 0 {
		t.Fatalf("scheduled = %v, want [] (pre-existing wakeup must dedupe)", res.ScheduledDownstream)
	}
	if pendingForNode(db, "B") != 1 {
		t.Fatalf("B wakeup count = %d, want 1 (no duplicate inserted)", pendingForNode(db, "B"))
	}
	// Original payload must not be overwritten by the conflicting INSERT.
	if w := db.wakeups[1]; string(w.PromptPayload) != `{"agent_id":"agent-b","note":"pre-seeded"}` {
		t.Fatalf("payload overwritten: %s", string(w.PromptPayload))
	}
}

func TestCompleteNodeAndScheduleDownstream_SecondCompleteOnDoneNodeIsNoRowsError(t *testing.T) {
	t.Parallel()

	// Calling CompleteNode twice on the same upstream node hits the SQL
	// status-IN ('running','awaiting_verify') fence on the second call.
	// The expected behaviour is the underlying not-found error surfaces;
	// the tx is short-circuited so no downstream scheduling runs and no
	// duplicate wakeup row is created.
	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", Status: "done", Result: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("first complete error = %v", err)
	}
	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A", Status: "done", Result: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("second complete error = nil, want fence rejection")
	}
	if pendingForNode(db, "B") != 1 {
		t.Fatalf("B wakeup count = %d, want 1", pendingForNode(db, "B"))
	}
}

func TestCompleteNodeAndScheduleDownstream_ConcurrentUpstreamsConvergeOnSameDownstream(t *testing.T) {
	t.Parallel()

	// Two upstreams complete and the SECOND one's enqueue must hit the
	// idempotency-key conflict (B was already queued by the first), so the
	// returned ScheduledDownstream slice excludes it.
	store, db, now := newTaskDAGTestStore()
	seedDAG(t, db, now, []seedNode{
		{key: "A1", deps: nil, status: "running", agent: "agent-a1"},
		{key: "A2", deps: nil, status: "running", agent: "agent-a2"},
		{key: "B", deps: []string{"A1", "A2"}, status: "pending", agent: "agent-b"},
	})

	if _, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A1", Status: "done", Result: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("complete A1 error = %v", err)
	}
	// A1 alone shouldn't enqueue B (A2 still pending → deps unsatisfied).
	if pendingForNode(db, "B") != 0 {
		t.Fatalf("after A1 only B count = %d, want 0", pendingForNode(db, "B"))
	}
	// Complete A2 → B becomes ready and is enqueued exactly once.
	resA2, err := store.CompleteNodeAndScheduleDownstream(context.Background(), CompleteNodeInput{
		DagKey: "dag-1", NodeKey: "A2", Status: "done", Result: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("complete A2 error = %v", err)
	}
	if got := scheduledKeys(resA2.ScheduledDownstream); !equalStrings(got, []string{"B"}) {
		t.Fatalf("after A2 scheduled = %v, want [B]", got)
	}
	if pendingForNode(db, "B") != 1 {
		t.Fatalf("B wakeup count = %d, want 1", pendingForNode(db, "B"))
	}
}

// --- helpers --------------------------------------------------------------

type seedNode struct {
	key    string
	deps   []string
	status string
	agent  string
}

func seedDAG(t *testing.T, db *fakeTaskDAGDB, now time.Time, nodes []seedNode) {
	t.Helper()
	for i, n := range nodes {
		depsJSON := []byte(`[]`)
		if len(n.deps) > 0 {
			b, err := json.Marshal(n.deps)
			if err != nil {
				t.Fatalf("marshal deps for %s: %v", n.key, err)
			}
			depsJSON = b
		}
		row := sqlc.TaskDagNode{
			ID:         int64(i + 1),
			DagKey:     "dag-1",
			NodeKey:    n.key,
			Title:      n.key,
			Status:     n.status,
			AssignedTo: n.agent,
			DependsOn:  depsJSON,
			Config:     []byte(`{}`),
			Result:     []byte(`{}`),
			CreatedAt:  timestamptzValue(now.Add(time.Duration(i) * time.Millisecond)),
			UpdatedAt:  timestamptzValue(now),
		}
		db.nodes[dagNodeKey("dag-1", n.key)] = row
	}
}

func transitionToRunning(t *testing.T, db *fakeTaskDAGDB, nodeKey string) {
	t.Helper()
	key := dagNodeKey("dag-1", nodeKey)
	row, ok := db.nodes[key]
	if !ok {
		t.Fatalf("node %s not found", nodeKey)
	}
	row.Status = "running"
	db.nodes[key] = row
}

func scheduledKeys(items []ScheduledDownstreamWakeup) []string {
	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.NodeKey)
	}
	sort.Strings(keys)
	return keys
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func pendingForNode(db *fakeTaskDAGDB, nodeKey string) int {
	count := 0
	for _, w := range db.wakeups {
		if w.NodeKey == nodeKey && w.Status == "pending" {
			count++
		}
	}
	return count
}

func lookupPendingWakeup(t *testing.T, db *fakeTaskDAGDB, nodeKey string) sqlc.TaskDagWakeup {
	t.Helper()
	for _, w := range db.wakeups {
		if w.NodeKey == nodeKey && w.Status == "pending" {
			return w
		}
	}
	t.Fatalf("no pending wakeup for node %s", nodeKey)
	return sqlc.TaskDagWakeup{}
}

func assertUpstreamPayload(t *testing.T, w sqlc.TaskDagWakeup, wantAgent, wantUpstreamKey, wantPath string) {
	t.Helper()
	var payload DownstreamWakeupPayload
	if err := json.Unmarshal(w.PromptPayload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v (raw=%s)", err, string(w.PromptPayload))
	}
	if payload.AgentID != wantAgent {
		t.Fatalf("payload.AgentID = %q, want %q", payload.AgentID, wantAgent)
	}
	if len(payload.UpstreamOutputs) != 1 {
		t.Fatalf("payload.UpstreamOutputs = %v, want 1 entry", payload.UpstreamOutputs)
	}
	got := payload.UpstreamOutputs[0]
	if got.NodeKey != wantUpstreamKey || got.Path != wantPath {
		t.Fatalf("upstream = %+v, want {%s %s}", got, wantUpstreamKey, wantPath)
	}
}
