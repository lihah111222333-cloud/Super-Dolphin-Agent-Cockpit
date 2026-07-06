//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

func TestFailNodeAndCancelDownstream_FailFastFalse_OnlyMarksCurrent(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"B"}, status: "pending", agent: "agent-c"},
	})

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:   "dag-1",
		NodeKey:  "A",
		RunID:    runID,
		Reason:   "launch transient exhausted",
		FailFast: false,
	})
	if err != nil {
		t.Fatalf("fail A error = %v", err)
	}
	if res.Node == nil || res.Node.Status != "failed" {
		t.Fatalf("res.Node = %+v, want status=failed", res.Node)
	}
	requireFailNodeOldStatus(t, res, "running")
	if len(res.CanceledDownstream) != 0 {
		t.Fatalf("canceled = %v, want []", res.CanceledDownstream)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", runID)].Status; got != "pending" {
		t.Fatalf("B status = %q, want pending (no fail-fast cascade)", got)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "C", runID)].Status; got != "pending" {
		t.Fatalf("C status = %q, want pending (no fail-fast cascade)", got)
	}
	// Primary failure result must be tagged as exhausted_retries.
	var reason failNodeReason
	if err := json.Unmarshal(res.Node.Result, &reason); err != nil {
		t.Fatalf("unmarshal A result: %v", err)
	}
	if reason.Kind != "exhausted_retries" || reason.Reason != "launch transient exhausted" {
		t.Fatalf("primary reason = %+v, want kind=exhausted_retries", reason)
	}
}

func TestFailNodeAndCancelDownstream_LocksPrimaryBeforeReadingOldStatus(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
	})
	db.beforeFailNonTerminal = func(dagKey, nodeKey string) {
		if dagKey != "dag-1" || nodeKey != "A" {
			return
		}
		key := dagRunNodeKey(dagKey, nodeKey, runID)
		row := db.nodes[key]
		row.Status = "done"
		db.nodes[key] = row
	}

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:  "dag-1",
		NodeKey: "A",
		RunID:   runID,
		Reason:  "retry exhausted",
	})
	if err != nil {
		t.Fatalf("FailNodeAndCancelDownstream error = %v, want locked primary update", err)
	}
	if res.OldStatus != "running" {
		t.Fatalf("OldStatus = %q, want locked running status", res.OldStatus)
	}
	if res.Node == nil || res.Node.Status != "failed" {
		t.Fatalf("res.Node = %+v, want failed", res.Node)
	}
}

func TestFailNodeAndCancelDownstream_PrimaryTerminalFence(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "done", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})

	_, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:   "dag-1",
		NodeKey:  "A",
		RunID:    runID,
		Reason:   "late materialization failure",
		FailFast: true,
	})
	if err == nil {
		t.Fatalf("FailNodeAndCancelDownstream on done primary error = nil, want fence rejection")
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "A", runID)].Status; got != "done" {
		t.Fatalf("A status = %q, want done (terminal primary must not be rewritten)", got)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", runID)].Status; got != "pending" {
		t.Fatalf("B status = %q, want pending (cascade must not run after primary fence rejection)", got)
	}
}

func TestFailNodeAndCancelDownstreamRejectsStaleWakeupAttempt(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})
	wakeup := newDispatchingWakeup(now, 7, "worker-a", 30*time.Second)
	wakeup.AttemptCount = 2
	db.wakeups[7] = wakeup
	key := dagRunNodeKey("dag-1", "A", runID)
	node := db.nodes[key]
	node.ActiveWakeupID = sqlc.Int8ValuePtr(&wakeup.ID)
	db.nodes[key] = node

	_, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:        "dag-1",
		NodeKey:       "A",
		RunID:         runID,
		Reason:        "late stale failure",
		FailFast:      true,
		WakeupID:      7,
		WakeupAttempt: 1,
	})
	if err == nil {
		t.Fatal("FailNodeAndCancelDownstream() stale attempt error = nil, want fence rejection")
	}
	requireRunNodeStatus(t, db, runID, "A", "running")
	requireRunNodeStatus(t, db, runID, "B", "pending")
}

func TestFailNodeAndCancelDownstream_CascadeTerminalRaceSkips(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
	})
	db.beforeFailNonTerminal = func(dagKey, nodeKey string) {
		if dagKey != "dag-1" || nodeKey != "B" {
			return
		}
		key := dagRunNodeKey(dagKey, nodeKey, runID)
		row := db.nodes[key]
		row.Status = "done"
		db.nodes[key] = row
	}

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:   "dag-1",
		NodeKey:  "A",
		RunID:    runID,
		Reason:   "retry exhausted",
		FailFast: true,
	})
	if err != nil {
		t.Fatalf("FailNodeAndCancelDownstream error = %v, want nil when cascade row races terminal", err)
	}
	if res.Node == nil || res.Node.Status != "failed" {
		t.Fatalf("primary node = %+v, want failed", res.Node)
	}
	if len(res.CanceledDownstream) != 0 {
		t.Fatalf("CanceledDownstream = %+v, want empty for raced terminal downstream", res.CanceledDownstream)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", runID)].Status; got != "done" {
		t.Fatalf("B status = %q, want done (cascade race must not rewrite terminal)", got)
	}
}

func TestFailNodeAndCancelDownstream_FailFastTrue_CascadesTransitivePending(t *testing.T) {
	t.Parallel()

	// A 鈫?B 鈫?C ; A 鈫?D ; D done already (must not be touched).
	// Failing A with fail_fast=true must cascade-fail B + C, leave D done.
	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"B"}, status: "pending", agent: "agent-c"},
		{key: "D", deps: []string{"A"}, status: "done", agent: "agent-d"},
	})

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey:   "dag-1",
		NodeKey:  "A",
		RunID:    runID,
		Reason:   "launch transient exhausted",
		FailFast: true,
	})
	if err != nil {
		t.Fatalf("fail A error = %v", err)
	}
	requireFailNodeStatus(t, res, "failed")
	requireCanceledNodeKeys(t, res.CanceledDownstream, []string{"B", "C"})
	requireRunNodeStatus(t, db, runID, "B", "failed")
	requireRunNodeStatus(t, db, runID, "C", "failed")
	// D was already done 鈥?fail-fast must not rewrite terminal nodes.
	requireRunNodeStatus(t, db, runID, "D", "done")
	// Cascade reason on B should reference A as the originator.
	requireCascadeReason(t, db, runID, "B", "A")
}

func requireFailNodeStatus(t *testing.T, res *FailNodeResult, want string) {
	t.Helper()
	if res.Node == nil || res.Node.Status != want {
		t.Fatalf("res.Node = %+v, want status=%s", res.Node, want)
	}
}

func requireFailNodeOldStatus(t *testing.T, res *FailNodeResult, want string) {
	t.Helper()
	if res.OldStatus != want {
		t.Fatalf("OldStatus = %q, want %s", res.OldStatus, want)
	}
}

func requireCanceledNodeKeys(t *testing.T, canceled []CanceledDownstreamNode, want []string) {
	t.Helper()
	if got := canceledKeys(canceled); !equalStrings(got, want) {
		t.Fatalf("canceled = %v, want %v", got, want)
	}
}

func requireRunNodeStatus(t *testing.T, db *fakeTaskDAGDB, runID int64, nodeKey, want string) {
	t.Helper()
	if got := db.nodes[dagRunNodeKey("dag-1", nodeKey, runID)].Status; got != want {
		t.Fatalf("%s status = %q, want %s", nodeKey, got, want)
	}
}

func requireCascadeReason(t *testing.T, db *fakeTaskDAGDB, runID int64, nodeKey, causedBy string) {
	t.Helper()
	var reason failNodeReason
	if err := json.Unmarshal(db.nodes[dagRunNodeKey("dag-1", nodeKey, runID)].Result, &reason); err != nil {
		t.Fatalf("unmarshal %s result: %v", nodeKey, err)
	}
	if reason.Kind != "cascade" || reason.CausedByNode != causedBy {
		t.Fatalf("%s reason = %+v, want kind=cascade caused_by=%s", nodeKey, reason, causedBy)
	}
}

func TestFailNodeAndCancelDownstream_FailFastTrue_DiamondCascade(t *testing.T) {
	t.Parallel()

	// A 鈫?{B, C} 鈫?D ; D depends on both B and C.
	// Failing A with fail-fast must cascade B, C, AND D (transitive closure).
	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "pending", agent: "agent-b"},
		{key: "C", deps: []string{"A"}, status: "pending", agent: "agent-c"},
		{key: "D", deps: []string{"B", "C"}, status: "pending", agent: "agent-d"},
	})

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: runID, Reason: "boom", FailFast: true,
	})
	if err != nil {
		t.Fatalf("fail A error = %v", err)
	}
	gotKeys := canceledKeys(res.CanceledDownstream)
	if want := []string{"B", "C", "D"}; !equalStrings(gotKeys, want) {
		t.Fatalf("canceled = %v, want %v", gotKeys, want)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "D", runID)].Status; got != "failed" {
		t.Fatalf("D status = %q, want failed", got)
	}
}

func TestFailNodeAndCancelDownstream_FailFastTrue_RunningDownstreamNotTouched(t *testing.T) {
	t.Parallel()

	// Once a downstream node is already running it must not be rewritten 鈥?
	// the running attempt finishes on its own; cascade only affects pending.
	store, db, now := newTaskDAGTestStore()
	runID := seedFailDownstreamRuntimeDAG(t, db, now, []seedNode{
		{key: "A", deps: nil, status: "running", agent: "agent-a"},
		{key: "B", deps: []string{"A"}, status: "running", agent: "agent-b"},
		{key: "C", deps: []string{"B"}, status: "pending", agent: "agent-c"},
	})

	res, err := store.FailNodeAndCancelDownstream(context.Background(), FailNodeInput{
		DagKey: "dag-1", NodeKey: "A", RunID: runID, Reason: "boom", FailFast: true,
	})
	if err != nil {
		t.Fatalf("fail A error = %v", err)
	}
	gotKeys := canceledKeys(res.CanceledDownstream)
	if want := []string{"C"}; !equalStrings(gotKeys, want) {
		t.Fatalf("canceled = %v, want %v (B was running, must skip; C still cascaded transitively)", gotKeys, want)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "B", runID)].Status; got != "running" {
		t.Fatalf("B status = %q, want running (must not be rewritten)", got)
	}
	if got := db.nodes[dagRunNodeKey("dag-1", "C", runID)].Status; got != "failed" {
		t.Fatalf("C status = %q, want failed", got)
	}
}

func seedFailDownstreamRuntimeDAG(t *testing.T, db *fakeTaskDAGDB, now time.Time, nodes []seedNode) int64 {
	t.Helper()
	runID := db.runs["run-1"].ID
	seedDAGRows(t, db, now, nodes, runID)
	return runID
}

func canceledKeys(items []CanceledDownstreamNode) []string {
	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.NodeKey)
	}
	sort.Strings(keys)
	return keys
}

// --- helpers --------------------------------------------------------------

func promotedKeys(items []PromotedDownstreamNode) []string {
	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.NodeKey)
	}
	sort.Strings(keys)
	return keys
}

type seedNode struct {
	key    string
	deps   []string
	status string
	agent  string
	thread string
}

const completeDownstreamRunID int64 = 501

func completeDownstreamRunIDValue() sqlc.Int8 {
	id := completeDownstreamRunID
	return sqlc.Int8ValuePtr(&id)
}

func seedDAG(t *testing.T, db *fakeTaskDAGDB, now time.Time, nodes []seedNode) {
	t.Helper()
	seedDAGRows(t, db, now, nodes, 0)
}

func seedRuntimeDAG(t *testing.T, db *fakeTaskDAGDB, now time.Time, nodes []seedNode) {
	t.Helper()
	seedRunningRunForTerminate(db, "dag-1", "run-complete", completeDownstreamRunID)
	seedDAGRows(t, db, now, nodes, completeDownstreamRunID)
}

func seedDAGRows(t *testing.T, db *fakeTaskDAGDB, now time.Time, nodes []seedNode, runID int64) {
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
			Config:     validAgentConfigForTest(t, n.agent),
			Result:     []byte(`{}`),
			CreatedAt:  timestamptzValue(now.Add(time.Duration(i) * time.Millisecond)),
			UpdatedAt:  timestamptzValue(now),
		}
		key := dagNodeKey("dag-1", n.key)
		if runID > 0 {
			id := runID
			row.RunID = sqlc.Int8ValuePtr(&id)
			key = dagRunNodeKey("dag-1", n.key, runID)
		}
		if n.thread != "" {
			row.SpawningThreadID = sqlc.Text{String: n.thread, Valid: true}
		}
		db.nodes[key] = row
	}
}

func transitionToRunning(t *testing.T, db *fakeTaskDAGDB, nodeKey string) {
	t.Helper()
	key := dagRunNodeKey("dag-1", nodeKey, completeDownstreamRunID)
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

func assertNoImplicitUpstreamPayload(t *testing.T, w sqlc.TaskDagWakeup, wantAgent string) {
	t.Helper()
	var payload DownstreamWakeupPayload
	if err := json.Unmarshal(w.PromptPayload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v (raw=%s)", err, string(w.PromptPayload))
	}
	if payload.AgentID != wantAgent {
		t.Fatalf("payload.AgentID = %q, want %q", payload.AgentID, wantAgent)
	}
	if len(payload.UpstreamOutputs) != 0 {
		t.Fatalf("payload.UpstreamOutputs = %v, want empty", payload.UpstreamOutputs)
	}
}
