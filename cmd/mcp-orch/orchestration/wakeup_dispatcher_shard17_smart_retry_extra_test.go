package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	orchmetrics "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/metrics"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/wakeuptext"
	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

func TestDispatcherSmartRetryEscalationExhaustionUsesDAGFailFast(t *testing.T) {
	now := time.Date(2026, 5, 14, 11, 0, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "escalate-exhausted", 1, now)
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"cwd":"/tmp/node-cwd",
			"model":"opus",
			"on_failure":{
				"by_class":{"capability":"escalate_model"},
				"max_attempts":2,
				"escalation_chain":["sonnet","opus"]
			}
		},
		"first_turn":"go"
	}`)
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	w := store.claimReply[0]

	d.handleFailedRouterOutcome(context.Background(), &w, extractFence(&w), nodeexec.NodeOutcome{
		Status:       nodeexec.NodeStatusFailed,
		FailureClass: nodeexec.FailureClassCapability,
		ErrorSummary: "model lacks capability",
	})

	if len(store.retryCalls) != 0 {
		t.Fatalf("retryCalls = %d, want 0 when escalation chain is exhausted", len(store.retryCalls))
	}
	if len(store.failCalls) != 1 || len(store.failNodeCalls) != 1 {
		t.Fatalf("fail calls = wakeup %d node %d, want 1/1", len(store.failCalls), len(store.failNodeCalls))
	}
	if !store.failNodeCalls[0].FailFast {
		t.Fatalf("FailFast = false, want DAG policy fail_fast when escalation is exhausted")
	}
}

func TestDispatcherSmartRetryDoesNotPatchConfigWhenRetryFenceMisses(t *testing.T) {
	now := time.Date(2026, 5, 14, 11, 5, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "retry-fence-miss", 1, now)
	store.retryRows = -1
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"cwd":"/tmp/node-cwd",
			"model":"sonnet",
			"on_failure":{
				"by_class":{"capability":"escalate_model"},
				"max_attempts":2,
				"escalation_chain":["sonnet","opus"]
			}
		},
		"first_turn":"go"
	}`)
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	w := store.claimReply[0]

	d.handleFailedRouterOutcome(context.Background(), &w, extractFence(&w), nodeexec.NodeOutcome{
		Status:       nodeexec.NodeStatusFailed,
		FailureClass: nodeexec.FailureClassCapability,
		ErrorSummary: "model lacks capability",
	})

	if len(store.retryCalls) != 1 {
		t.Fatalf("retryCalls = %d, want 1", len(store.retryCalls))
	}
	if len(store.patchConfigCalls) != 0 {
		t.Fatalf("patchConfigCalls = %d, want 0 when RetryWakeup writes 0 rows", len(store.patchConfigCalls))
	}
}

func TestDispatcherSmartRetryConfigPatchFailureFailsClosed(t *testing.T) {
	now := time.Date(2026, 5, 14, 11, 10, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "patch-failure", 1, now)
	store.patchConfigErr = errors.New("stale node config")
	store.nodesReply[0].Config = testRawConfig(t, `{
		"exec":{
			"agent_key":"alpha",
			"cwd":"/tmp/node-cwd",
			"model":"sonnet",
			"on_failure":{
				"by_class":{"capability":"escalate_model"},
				"max_attempts":2,
				"escalation_chain":["sonnet","opus"]
			}
		},
		"first_turn":"go"
	}`)
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	w := store.claimReply[0]

	d.handleFailedRouterOutcome(context.Background(), &w, extractFence(&w), nodeexec.NodeOutcome{
		Status:       nodeexec.NodeStatusFailed,
		FailureClass: nodeexec.FailureClassCapability,
		ErrorSummary: "model lacks capability",
	})

	if len(store.failCalls) != 1 || len(store.failNodeCalls) != 1 {
		t.Fatalf("fail calls = wakeup %d node %d, want 1/1 on config patch failure", len(store.failCalls), len(store.failNodeCalls))
	}
	if !strings.Contains(store.failCalls[0].LastError, "smart retry prepare failed") ||
		!strings.Contains(store.failCalls[0].LastError, "stale node config") {
		t.Fatalf("FailWakeup LastError = %q, want smart retry prepare failure with patch error", store.failCalls[0].LastError)
	}
	if len(store.patchConfigCalls) != 1 {
		t.Fatalf("patchConfigCalls = %d, want 1", len(store.patchConfigCalls))
	}
}

func TestDispatcherAgentHardValidationFailureFailsWithoutRetry(t *testing.T) {
	now := time.Date(2026, 5, 13, 9, 30, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "bad-config", 1, now)
	store.nodesReply[0].Config = testRawConfig(t, `{"exec":`)
	d := newAgentFailureClassDispatcher(t, store, errors.New("launcher should not be called"))

	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.retryCalls) != 0 {
		t.Fatalf("retryCalls = %d, want 0 for non-retryable config validation", len(store.retryCalls))
	}
	if len(store.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1 for non-retryable config validation", len(store.failCalls))
	}
	if len(store.failNodeCalls) != 1 {
		t.Fatalf("failNodeCalls = %d, want 1 for non-retryable config validation", len(store.failNodeCalls))
	}
}

func TestDispatcherAgentPermanentFailureAtThirdAttemptRecordsRetryAlert(t *testing.T) {
	orchmetrics.ResetDispatchRetryForTesting()
	now := time.Date(2026, 5, 13, 9, 45, 0, 0, time.UTC)
	store := newAgentFailureClassStore(t, "bad-config-alert", 3, now)
	store.nodesReply[0].Config = testRawConfig(t, `{"exec":`)
	sink := &recordingDispatchRetryAlertSink{}
	d := newAgentFailureClassDispatcher(t, store, errors.New("launcher should not be called"))
	d.WithDispatchRetryAlertSink(sink)

	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	key := store.claimReply[0].DagKey + "/" + store.claimReply[0].NodeKey
	if got := orchmetrics.DispatchRetryCounters().RetryCountPerNode[key]; got != 3 {
		t.Fatalf("retry_count_per_node[%s] = %d, want 3", key, got)
	}
	calls := sink.waitForCalls(t, 1)
	if calls[0].DagKey != store.claimReply[0].DagKey || calls[0].NodeKey != store.claimReply[0].NodeKey {
		t.Fatalf("alert keys = %+v, want %s/%s", calls[0], store.claimReply[0].DagKey, store.claimReply[0].NodeKey)
	}
}

func TestDispatcherRetryAlertSinkDoesNotBlockBatch(t *testing.T) {
	orchmetrics.ResetDispatchRetryForTesting()
	now := time.Date(2026, 5, 13, 9, 50, 0, 0, time.UTC)
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(260, "dag-alert", "node-hot", "agent-hot", 3, now)},
	}
	launcher := &dispatcherStubLauncher{errs: []error{errors.New("connection refused")}}
	block := make(chan struct{})
	sink := &recordingDispatchRetryAlertSink{block: block}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{})
	d.WithDispatchRetryAlertSink(sink)
	done := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		_, err := d.ProcessBatch(context.Background())
		done <- err
	})

	closed := false
	unblock := func() {
		if !closed {
			closed = true
			close(block)
		}
	}
	defer unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ProcessBatch err = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		unblock()
		<-done
		t.Fatal("ProcessBatch blocked on retry alert sink")
	}
}

func newAgentFailureClassStore(t *testing.T, suffix string, attempt int32, now time.Time) *dispatcherStubStore {
	t.Helper()
	dagKey := "dag-f14-" + suffix
	nodeKey := "agent-" + suffix
	return &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{makeDAGWakeup(70+int64(attempt), dagKey, nodeKey, "agent-"+suffix, attempt, now)},
		dagReply: &taskdag.DAG{
			DagKey:   dagKey,
			Metadata: dagDefaultRetryMetadata(t, 1, true),
		},
		nodesReply: []taskdag.Node{{
			DagKey:   dagKey,
			NodeKey:  nodeKey,
			NodeType: "agent",
			Title:    nodeKey,
			Config:   testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},"first_turn":"go"}`),
			Status:   string(nodeexec.NodeStatusReady),
		}},
	}
}

func newAgentFailureClassDispatcher(t *testing.T, store *dispatcherStubStore, launchErr error) *WakeupDispatcher {
	t.Helper()
	agentExec := newTestAgentExecutor(&stubAgentLauncher{err: launchErr})
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	return d.WithNodeRouter(router)
}

// TestDispatcherNonDAGWakeupKeepsLegacyRetry 验证：Wakeup 没有 DagKey/NodeKey
// 时（�?DAG 来源），新代码不应该�?DAG 决策；保持旧 RetryWakeup 路径�?
func TestDispatcherNonDAGWakeupKeepsLegacyRetry(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	w := makeClaimedWakeup(23, "agent-D", 5, now) // DagKey/NodeKey 留空
	w.DagKey = ""
	w.NodeKey = ""
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{w},
	}
	launcher := &dispatcherStubLauncher{errs: []error{errors.New("connection refused")}}
	d, _ := NewWakeupDispatcher(store, launcher, nil, WakeupDispatcherConfig{})
	if _, err := d.ProcessBatch(context.Background()); err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if len(store.retryCalls) != 1 {
		t.Fatalf("retryCalls = %d, want 1 (legacy path)", len(store.retryCalls))
	}
	if len(store.failNodeCalls) != 0 {
		t.Fatalf("failNodeCalls = %d, want 0 (no DAG cascade for non-DAG wakeup)", len(store.failNodeCalls))
	}
}

// Phase 3.9 新增：dispatcher 把上游产出路径注入下一节点 prompt�?

// TestBuildLaunchRequestPhase39_InjectsUpstreamOutputsIntoPrompt 验证�?
// payload �?DownstreamWakeupPayload + UpstreamOutputs 非空时，prompt �?
// 路径列表 + Read 提示文案。AgentID �?payload 优先，fallback �?wakeup�?
func TestBuildLaunchRequestPhase39_InjectsUpstreamOutputsIntoPrompt(t *testing.T) {
	payload, err := json.Marshal(taskdag.DownstreamWakeupPayload{
		AgentID: "agent-downstream",
		UpstreamOutputs: []taskdag.DownstreamUpstreamRef{
			{NodeKey: "node-A", Path: "dag/dag-x/node-A/output.json"},
			{NodeKey: "node-B", Path: "dag/dag-x/node-B/output.json"},
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := buildLaunchRequestFromWakeup(taskdag.Wakeup{
		TargetAgentID: "agent-fallback",
		PromptPayload: payload,
	})
	if req.AgentID != "agent-downstream" {
		t.Fatalf("AgentID = %q, want agent-downstream (payload override)", req.AgentID)
	}
	if !strings.Contains(req.Prompt, "node-A: dag/dag-x/node-A/output.json") {
		t.Fatalf("Prompt missing node-A path:\n%s", req.Prompt)
	}
	if !strings.Contains(req.Prompt, "node-B: dag/dag-x/node-B/output.json") {
		t.Fatalf("Prompt missing node-B path:\n%s", req.Prompt)
	}
	if !strings.Contains(req.Prompt, "Read") {
		t.Fatalf("Prompt missing Read hint:\n%s", req.Prompt)
	}
}

// TestBuildLaunchRequestPhase39_FallsBackToWakeupAgentWhenPayloadAgentEmpty:
// payload �?UpstreamOutputs �?AgentID 空时退化用 wakeup.TargetAgentID�?
func TestBuildLaunchRequestPhase39_FallsBackToWakeupAgentWhenPayloadAgentEmpty(t *testing.T) {
	payload, _ := json.Marshal(taskdag.DownstreamWakeupPayload{
		UpstreamOutputs: []taskdag.DownstreamUpstreamRef{
			{NodeKey: "X", Path: "dag/d/X/output.json"},
		},
	})
	req := buildLaunchRequestFromWakeup(taskdag.Wakeup{
		TargetAgentID: "agent-fallback",
		PromptPayload: payload,
	})
	if req.AgentID != "agent-fallback" {
		t.Fatalf("AgentID = %q, want fallback", req.AgentID)
	}
	if req.Prompt == "" {
		t.Fatalf("Prompt empty, want render with X path")
	}
}

// TestBuildLaunchRequestPhase39_LegacyPayloadStillWorks:
// 老式 LaunchRequest payload（无 upstream_outputs）仍然走 fallback 解析路径�?
func TestBuildLaunchRequestPhase39_LegacyPayloadStillWorks(t *testing.T) {
	// 仿照 TestBuildLaunchRequestFromWakeupDecodesJSONPayload 的形状�?
	legacy := `{"agent_id":"agent-legacy","prompt":"hello"}`
	req := buildLaunchRequestFromWakeup(taskdag.Wakeup{
		TargetAgentID: "agent-fallback",
		PromptPayload: json.RawMessage(legacy),
	})
	if req.Prompt != "hello" {
		t.Fatalf("legacy payload prompt = %q, want hello", req.Prompt)
	}
}

// TestRenderUpstreamPromptHint_SkipsEmptyPathRefs 验证渲染�?path 为空�?ref
// 安静跳过（不留下 "- :" 这种空行垃圾）�?
func TestRenderUpstreamPromptHint_SkipsEmptyPathRefs(t *testing.T) {
	prompt := wakeuptext.RenderUpstreamPromptHint([]taskdag.DownstreamUpstreamRef{
		{NodeKey: "A", Path: "dag/d/A/output.json"},
		{NodeKey: "B", Path: ""},
		{NodeKey: "", Path: "dag/d/anon/output.json"},
	})
	if !strings.Contains(prompt, "A: dag/d/A/output.json") {
		t.Fatalf("missing A entry:\n%s", prompt)
	}
	if strings.Contains(prompt, "- B:") || strings.Contains(prompt, "- :") {
		t.Fatalf("empty path ref leaked into prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "- dag/d/anon/output.json") {
		t.Fatalf("missing anon path entry:\n%s", prompt)
	}
}
