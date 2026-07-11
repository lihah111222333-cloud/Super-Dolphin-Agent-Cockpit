package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	sharedevt "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// 测试替身只实现 subscriber 需要的窄接口，避免引入完整 taskdag.Store 或 *service。

type dagSubscriberLookupSpy struct {
	nodes      []taskdag.Node
	err        error
	calls      int32
	lastThread string
}

func (s *dagSubscriberLookupSpy) LookupNodesBySpawningThread(_ context.Context, threadID string) ([]taskdag.Node, error) {
	atomic.AddInt32(&s.calls, 1)
	s.lastThread = threadID
	if s.err != nil {
		return nil, s.err
	}
	out := make([]taskdag.Node, len(s.nodes))
	copy(out, s.nodes)
	return out, nil
}

type dagSubscriberFlowSpy struct {
	completeCalls []taskdag.CompleteNodeInput
	completeErr   error
	failCalls     []taskdag.FailNodeInput
	failErr       error
	enqueueCalls  []taskdag.EnqueueWakeupInput
	enqueueErr    error
	claimCalls    []taskdag.OutputMaterializationClaimInput
	claimErr      error
}

func (s *dagSubscriberFlowSpy) CompleteNodeAndScheduleDownstream(_ context.Context, input taskdag.CompleteNodeInput) (*taskdag.CompleteNodeWithDownstreamResult, error) {
	s.completeCalls = append(s.completeCalls, input)
	if s.completeErr != nil {
		return nil, s.completeErr
	}
	return &taskdag.CompleteNodeWithDownstreamResult{
		Node: &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, Status: input.Status},
	}, nil
}

func (s *dagSubscriberFlowSpy) FailNodeAndCancelDownstream(_ context.Context, input taskdag.FailNodeInput) (*taskdag.FailNodeResult, error) {
	s.failCalls = append(s.failCalls, input)
	if s.failErr != nil {
		return nil, s.failErr
	}
	return &taskdag.FailNodeResult{
		Node: &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, RunID: &input.RunID, Status: "failed"},
	}, nil
}

func (s *dagSubscriberFlowSpy) EnqueueWakeup(_ context.Context, input taskdag.EnqueueWakeupInput) (int64, error) {
	s.enqueueCalls = append(s.enqueueCalls, input)
	if s.enqueueErr != nil {
		return 0, s.enqueueErr
	}
	return 1, nil
}

func (s *dagSubscriberFlowSpy) ClaimNodeOutputMaterialization(_ context.Context, input taskdag.OutputMaterializationClaimInput) (*taskdag.Node, error) {
	s.claimCalls = append(s.claimCalls, input)
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	return &taskdag.Node{DagKey: input.DagKey, NodeKey: input.NodeKey, Status: "awaiting_verify", Result: input.Result}, nil
}

type dagSubscriberThreadSpy struct {
	thread *PersistedThread
	err    error
	calls  int32
}

func (s *dagSubscriberThreadSpy) GetByThreadID(_ context.Context, _ string) (*PersistedThread, error) {
	atomic.AddInt32(&s.calls, 1)
	return s.thread, s.err
}

func (s *dagSubscriberThreadSpy) UpdateStatus(context.Context, PersistedThreadStatusUpdate) error {
	return nil
}

type dagSubscriberStopSpy struct {
	stopErr error
	stopped []string
}

func (s *dagSubscriberStopSpy) StopAgent(_ context.Context, agentID string) error {
	s.stopped = append(s.stopped, agentID)
	return s.stopErr
}

type dagSubscriberSharedFileWriterSpy struct {
	writes []struct{ Path, Content string }
	err    error
}

func (s *dagSubscriberSharedFileWriterSpy) WriteSharedFile(_ context.Context, path, content string) error {
	if s.err != nil {
		return s.err
	}
	s.writes = append(s.writes, struct{ Path, Content string }{Path: path, Content: content})
	return nil
}

type dagSubscriberSharedFileReaderSpy struct {
	contents map[string]string
	err      error
	reads    []string
}

func (s *dagSubscriberSharedFileReaderSpy) ReadSharedFile(_ context.Context, path string) (string, bool, error) {
	s.reads = append(s.reads, path)
	if s.err != nil {
		return "", false, s.err
	}
	if s.contents == nil {
		return "", false, nil
	}
	content, ok := s.contents[path]
	return content, ok, nil
}

func dagSubscriberTestRunID(id int64) *int64 { return &id }

func expectedDAGSubscriberMarkerPath(path string) string {
	return "_internal/dag-output-ownership/" + path + ".metadata.json"
}

func findSharedFileWrite(t *testing.T, writer *dagSubscriberSharedFileWriterSpy, path string) string {
	t.Helper()
	for _, write := range writer.writes {
		if write.Path == path {
			return write.Content
		}
	}
	t.Fatalf("sharedfile write %q not found in %+v", path, writer.writes)
	return ""
}

func assertSharedFileOwnerMarker(t *testing.T, content, dagKey, nodeKey, threadID, turnID string, runID int64) {
	t.Helper()
	var marker map[string]any
	if err := json.Unmarshal([]byte(content), &marker); err != nil {
		t.Fatalf("owner marker is not JSON: %v; content=%q", err, content)
	}
	wantStrings := map[string]string{
		"dag_key":   dagKey,
		"node_key":  nodeKey,
		"thread_id": threadID,
		"turn_id":   turnID,
	}
	for key, want := range wantStrings {
		if got, _ := marker[key].(string); got != want {
			t.Fatalf("owner marker %s = %q, want %q; marker=%v", key, got, want, marker)
		}
	}
	if got, _ := marker["run_id"].(float64); int64(got) != runID {
		t.Fatalf("owner marker run_id = %v, want %d; marker=%v", marker["run_id"], runID, marker)
	}
	updatedAt, _ := marker["updated_at"].(string)
	if _, err := time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		t.Fatalf("owner marker updated_at = %q, want RFC3339Nano timestamp: %v", updatedAt, err)
	}
}

func dagSubscriberOwnerMarkerJSON(t *testing.T, dagKey, nodeKey, threadID, turnID string, runID int64) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"dag_key":    dagKey,
		"node_key":   nodeKey,
		"run_id":     runID,
		"thread_id":  threadID,
		"turn_id":    turnID,
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("marshal owner marker: %v", err)
	}
	return string(raw)
}

func assertTerminalFailureCompensation(t *testing.T, got taskdag.EnqueueWakeupInput, dagKey, nodeKey string, runID int64, wantDetails ...string) {
	t.Helper()
	if got.DagKey != dagKey || got.NodeKey != nodeKey || got.RunID != runID {
		t.Fatalf("compensation identity = %+v, want %s/%s run_id=%d", got, dagKey, nodeKey, runID)
	}
	if got.WakeupKind != "terminal_failure_compensation" || got.TargetAgentID != "mcp-orch" {
		t.Fatalf("compensation wakeup kind/target = %q/%q, want terminal_failure_compensation/mcp-orch", got.WakeupKind, got.TargetAgentID)
	}
	for _, want := range wantDetails {
		if !strings.Contains(string(got.PromptPayload), want) {
			t.Fatalf("compensation payload = %s, want detail %q", got.PromptPayload, want)
		}
	}
}

// setupDAGSubscriberDeps 组装 subscriber 依赖；每个用例显式读取指标差值来保持独立计数。
func setupDAGSubscriberDeps(
	lookup *dagSubscriberLookupSpy,
	flow *dagSubscriberFlowSpy,
	threads *dagSubscriberThreadSpy,
	stop *dagSubscriberStopSpy,
) DAGSubscriberDeps {
	return DAGSubscriberDeps{
		LookupStore:  lookup,
		FlowStore:    flow,
		AgentThreads: threads,
		SvcStopper:   stop,
	}
}

func newTurnCompletedEvent(threadID string, success bool, result string) turndto.TurnCompleted {
	return turndto.TurnCompleted{
		TurnHeader: sharedevt.TurnHeader{
			AgentHeader: sharedevt.AgentHeader{
				ThreadHeader: sharedevt.ThreadHeader{ThreadID: threadID},
				AgentID:      "agent-x",
			},
			TurnIDHeader: sharedevt.TurnIDHeader{TurnID: "turn-1"},
		},
		Success: success,
		Result:  result,
	}
}

// metricDelta 计算指标快照差值。
// 顺序执行的子测试共享 singleton 计数器，断言差值可以避免用例之间相互污染。
func metricDelta(before, after DAGSubscriberMetrics) DAGSubscriberMetrics {
	return DAGSubscriberMetrics{
		CompleteDone:            after.CompleteDone - before.CompleteDone,
		CompleteFailed:          after.CompleteFailed - before.CompleteFailed,
		IdempotentSkipped:       after.IdempotentSkipped - before.IdempotentSkipped,
		LookupNoNode:            after.LookupNoNode - before.LookupNoNode,
		LookupDirtyData:         after.LookupDirtyData - before.LookupDirtyData,
		LookupFailed:            after.LookupFailed - before.LookupFailed,
		CompleteSizeCapExceeded: after.CompleteSizeCapExceeded - before.CompleteSizeCapExceeded,
		CompleteResultEmpty:     after.CompleteResultEmpty - before.CompleteResultEmpty,
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nopWriter{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// 以下用例覆盖 DAG turn completed subscriber 的主要状态推进分支。

// TestDAGSubscriber_HappyPath_Done 覆盖成功 turn 将节点推进为 done，并递增 CompleteDone 指标。
func TestDAGSubscriber_HappyPath_Done(t *testing.T) {
	before := DAGSubscriberCounters()
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", Status: "running"}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-1", AgentID: "agent-x"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-1", true, `{"summary":"ok"}`))

	if len(flow.completeCalls) != 1 || flow.completeCalls[0].Status != "done" {
		t.Fatalf("completeCalls = %+v, want 1 done call", flow.completeCalls)
	}
	if len(flow.failCalls) != 0 {
		t.Fatalf("failCalls = %d, want 0", len(flow.failCalls))
	}
	if len(stop.stopped) != 1 || stop.stopped[0] != "agent-x" {
		t.Fatalf("stopped = %v, want [agent-x]", stop.stopped)
	}
	d := metricDelta(before, DAGSubscriberCounters())
	if d.CompleteDone != 1 {
		t.Fatalf("CompleteDone delta = %d, want 1", d.CompleteDone)
	}
}

func TestDAGSubscriber_AgentResultFallsBackToSummary(t *testing.T) {
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-1",
		NodeKey:  "final",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},
			"outputs":{"to_node_result":true}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-summary", AgentID: "agent-summary"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	ev := newTurnCompletedEvent("thr-summary", true, "")
	ev.Summary = "DAG_E2E_OK_20260524"
	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), ev)

	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	if got, want := string(flow.completeCalls[0].Result), `{"text":"DAG_E2E_OK_20260524"}`; got != want {
		t.Fatalf("complete result = %s, want %s", got, want)
	}
	if len(flow.failCalls) != 0 {
		t.Fatalf("failCalls = %d, want 0", len(flow.failCalls))
	}
}

func TestDAGSubscriber_AgentSuccessWithEmptyOutputFailsNode(t *testing.T) {
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-1",
		NodeKey:  "final",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},
			"outputs":{"to_node_result":true}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-empty-agent", AgentID: "agent-empty"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-empty-agent", true, ""))

	if len(flow.completeCalls) != 0 {
		t.Fatalf("completeCalls = %d, want 0 for empty agent output", len(flow.completeCalls))
	}
	if len(flow.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(flow.failCalls))
	}
	if got := flow.failCalls[0].Reason; !strings.Contains(got, "empty agent output") {
		t.Fatalf("failure reason = %q, want empty agent output", got)
	}
	if !flow.failCalls[0].FailFast {
		t.Fatal("FailFast = false, want true so pending downstream is canceled")
	}
}

func TestDAGSubscriber_NonAgentSuccessKeepsLegacyEmptyResult(t *testing.T) {
	before := DAGSubscriberCounters()
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-1",
		NodeKey:  "n1",
		NodeType: "automation",
		Status:   "running",
	}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-non-agent", AgentID: "agent-non-agent"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	ev := newTurnCompletedEvent("thr-non-agent", true, "")
	ev.Summary = "summary must not become non-agent result"
	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), ev)

	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	if got, want := string(flow.completeCalls[0].Result), `{}`; got != want {
		t.Fatalf("complete result = %s, want %s", got, want)
	}
	d := metricDelta(before, DAGSubscriberCounters())
	if d.CompleteResultEmpty != 1 {
		t.Fatalf("CompleteResultEmpty delta = %d, want 1", d.CompleteResultEmpty)
	}
}

func TestDAGSubscriber_DoneInvokesLifecycleHooks(t *testing.T) {
	events := []string{}
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-1",
		NodeKey:  "n1",
		NodeType: "agent",
		Status:   "running",
		Config:   testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},"first_turn":"hi"}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-hooks-done", AgentID: "agent-hooks"}}
	stop := &dagSubscriberStopSpy{}
	agentExec := newTestAgentExecutor(&stubAgentLauncher{}, nodeexec.WithHooks(recordingLifecycleHooks(&events)))
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)
	deps.NodeRouter = NewNodeExecutorRouter(&stubRouterStore{}, agentExec, nil, nil, nil, nil)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-hooks-done", true, `{"summary":"ok"}`))

	want := []string{"on_state_change:n1:done"}
	if got := strings.Join(events, "|"); got != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

// TestDAGSubscriber_HappyPath_Failed 覆盖失败 turn 将节点标记 failed，并递增 CompleteFailed 指标。
func TestDAGSubscriber_HappyPath_Failed(t *testing.T) {
	before := DAGSubscriberCounters()
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", Status: "running"}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-2", AgentID: "agent-y"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	ev := newTurnCompletedEvent("thr-2", false, "")
	ev.Error = "explicit failure"
	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), ev)

	if len(flow.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(flow.failCalls))
	}
	if flow.failCalls[0].Reason != "explicit failure" {
		t.Fatalf("failCalls[0].Reason = %q, want %q", flow.failCalls[0].Reason, "explicit failure")
	}
	if flow.failCalls[0].FailFast {
		t.Fatal("failCalls[0].FailFast = true, want false (A1 不级�?")
	}
	d := metricDelta(before, DAGSubscriberCounters())
	if d.CompleteFailed != 1 {
		t.Fatalf("CompleteFailed delta = %d, want 1", d.CompleteFailed)
	}
}

func TestDAGSubscriber_FailedStoreErrorRecordsCompensation(t *testing.T) {
	runID := int64(1620)
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:  "dag-1",
		NodeKey: "n1",
		Status:  "running",
		RunID:   &runID,
	}}}
	flow := &dagSubscriberFlowSpy{failErr: errors.New("db unavailable")}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-compensate-fail", AgentID: "agent-compensate"}}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, &dagSubscriberStopSpy{})
	ev := newTurnCompletedEvent("thr-compensate-fail", false, "")
	ev.Error = "agent failed"
	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), ev)
	if len(flow.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(flow.failCalls))
	}
	if len(flow.enqueueCalls) != 1 {
		t.Fatalf("enqueueCalls = %d, want 1 durable compensation record", len(flow.enqueueCalls))
	}
	assertTerminalFailureCompensation(t, flow.enqueueCalls[0], "dag-1", "n1", runID, "agent failed", "db unavailable")
}

func TestDAGSubscriber_FailedInvokesLifecycleHooks(t *testing.T) {
	events := []string{}
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-1",
		NodeKey:  "n1",
		NodeType: "agent",
		Status:   "running",
		Config:   testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},"first_turn":"hi"}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-hooks", AgentID: "agent-hooks"}}
	stop := &dagSubscriberStopSpy{}
	agentExec := newTestAgentExecutor(&stubAgentLauncher{}, nodeexec.WithHooks(recordingLifecycleHooks(&events)))
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)
	deps.NodeRouter = NewNodeExecutorRouter(&stubRouterStore{}, agentExec, nil, nil, nil, nil)

	ev := newTurnCompletedEvent("thr-hooks", false, "")
	ev.Error = "explicit failure"
	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), ev)

	want := []string{
		"on_state_change:n1:failed",
		"on_failure:n1:failed",
	}
	if got := strings.Join(events, "|"); got != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestDAGSubscriber_MaterializationFailureLifecycleHooksKeepFailureClass(t *testing.T) {
	events := []string{}
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-sharedfile",
		NodeType: "agent",
		Status:   "running",
		RunID:    dagSubscriberTestRunID(1613),
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"implementer","cwd":"/tmp/node-cwd"},
			"outputs":{"to_sharedfile":{"path":"reports/agent.json","lock_mode":"exclusive"}}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-materialize-fail", AgentID: "agent-a2"}}
	stop := &dagSubscriberStopSpy{}
	agentExec := newTestAgentExecutor(&stubAgentLauncher{}, nodeexec.WithHooks(recordingLifecycleOutcomeHooks(&events)))
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)
	deps.NodeRouter = NewNodeExecutorRouter(&stubRouterStore{}, agentExec, nil, nil, nil, nil)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-materialize-fail", true, `{"summary":"done"}`))

	if len(flow.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(flow.failCalls))
	}
	want := []string{
		"on_state_change:agent-sharedfile:failed:infrastructure",
		"on_failure:agent-sharedfile:failed:infrastructure",
	}
	if got := strings.Join(events, "|"); got != strings.Join(want, "|") {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

// TestDAGSubscriber_RaceA_NodeStillReady 覆盖 TurnCompleted 早于 running 状态落库的竞态。
// 节点仍为 ready 时 subscriber 也要尝试推进，避免完成事件丢失。
func TestDAGSubscriber_RaceA_NodeStillReady(t *testing.T) {
	before := DAGSubscriberCounters()
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", Status: "ready"}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-3", AgentID: "agent-z"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-3", true, `{"k":"v"}`))

	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1 (subscriber must call CompleteNode even when status=ready)", len(flow.completeCalls))
	}
	d := metricDelta(before, DAGSubscriberCounters())
	if d.CompleteDone != 1 {
		t.Fatalf("CompleteDone delta = %d, want 1", d.CompleteDone)
	}
}

// TestDAGSubscriber_RaceC_NodeAlreadyFailed_IdempotentSkip 覆盖终态节点的幂等跳过。
// 已 failed 的节点不再调用 flow，只递增 IdempotentSkipped。
func TestDAGSubscriber_RaceC_NodeAlreadyFailed_IdempotentSkip(t *testing.T) {
	before := DAGSubscriberCounters()
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{DagKey: "dag-1", NodeKey: "n1", Status: "failed"}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-4", AgentID: "agent-a"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-4", true, ""))

	if len(flow.completeCalls) != 0 || len(flow.failCalls) != 0 {
		t.Fatalf("flow calls = %d/%d, want 0/0 (node already terminal, must skip)", len(flow.completeCalls), len(flow.failCalls))
	}
	d := metricDelta(before, DAGSubscriberCounters())
	if d.IdempotentSkipped != 1 {
		t.Fatalf("IdempotentSkipped delta = %d, want 1", d.IdempotentSkipped)
	}
}

// TestDAGSubscriber_LookupEmpty_NoNodeDoesNotStopThread 覆盖普通会话 turn 事件。
// 找不到 DAG 节点时只记录 LookupNoNode，不应停止该 thread。
func TestDAGSubscriber_LookupEmpty_NoNodeDoesNotStopThread(t *testing.T) {
	before := DAGSubscriberCounters()
	lookup := &dagSubscriberLookupSpy{nodes: nil}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-5", AgentID: "agent-b"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-5", true, ""))

	if len(flow.completeCalls) != 0 {
		t.Fatalf("completeCalls = %d, want 0 (no node to advance)", len(flow.completeCalls))
	}
	if len(stop.stopped) != 0 {
		t.Fatalf("stopped = %d, want 0 (no DAG node owns this thread)", len(stop.stopped))
	}
	d := metricDelta(before, DAGSubscriberCounters())
	if d.LookupNoNode != 1 {
		t.Fatalf("LookupNoNode delta = %d, want 1", d.LookupNoNode)
	}
}

// TestDAGSubscriber_LookupDirtyData_AdvanceEveryRow 覆盖同一 thread 反查出多条节点的脏数据。
// subscriber 会记录 LookupDirtyData，并逐条尝试推进，避免遗漏可恢复节点。
func TestDAGSubscriber_LookupDirtyData_AdvanceEveryRow(t *testing.T) {
	before := DAGSubscriberCounters()
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{
		{DagKey: "dag-1", NodeKey: "a", Status: "running"},
		{DagKey: "dag-1", NodeKey: "b", Status: "running"},
	}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-6", AgentID: "agent-c"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-6", true, ""))

	if len(flow.completeCalls) != 2 {
		t.Fatalf("completeCalls = %d, want 2 (must advance every dirty-data row)", len(flow.completeCalls))
	}
	d := metricDelta(before, DAGSubscriberCounters())
	if d.LookupDirtyData != 1 {
		t.Fatalf("LookupDirtyData delta = %d, want 1", d.LookupDirtyData)
	}
	if d.CompleteDone != 2 {
		t.Fatalf("CompleteDone delta = %d, want 2", d.CompleteDone)
	}
}
