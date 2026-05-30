package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/jackc/pgx/v5"
)

func TestDAGSubscriber_A2_DefaultAgentResultMaterializesTurnResult(t *testing.T) {
	payload := `{"summary":"real agent output"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-default",
		NodeType: "agent",
		Status:   "running",
		Config:   testRawConfig(t, `{"exec":{"agent_key":"implementer","cwd":"/tmp/node-cwd"}}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-default", AgentID: "agent-a2-default"}},
		&dagSubscriberStopSpy{},
	)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-default", true, payload))

	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	if got := string(flow.completeCalls[0].Result); got != payload {
		t.Fatalf("CompleteNodeInput.Result = %s, want real TurnCompleted result %s", got, payload)
	}
}

func TestDAGSubscriber_A2_SharedfileOnlyDoesNotStoreLargeTurnResult(t *testing.T) {
	hugePayload := `{"data":"` + strings.Repeat("x", completeNodeResultCap+1) + `"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-sharedfile",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"implementer","cwd":"/tmp/node-cwd"},
			"outputs":{
				"to_sharedfile":{"path":"reports/agent.json","lock_mode":"exclusive"},
				"to_node_result":false
			}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-sharedfile", AgentID: "agent-a2"}}
	stop := &dagSubscriberStopSpy{}
	writer := &dagSubscriberSharedFileWriterSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)
	deps.SharedFileReader = &dagSubscriberSharedFileReaderSpy{}
	deps.SharedFileWriter = writer

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-sharedfile", true, hugePayload))

	if len(writer.writes) != 1 {
		t.Fatalf("sharedfile writes = %d, want 1", len(writer.writes))
	}
	if writer.writes[0].Path != "reports/agent.json" {
		t.Fatalf("sharedfile path = %q, want reports/agent.json", writer.writes[0].Path)
	}
	if writer.writes[0].Content != hugePayload {
		t.Fatalf("sharedfile content size = %d, want exact TurnCompleted result size %d", len(writer.writes[0].Content), len(hugePayload))
	}
	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	got := string(flow.completeCalls[0].Result)
	if len(got) > completeNodeResultCap || strings.Contains(got, strings.Repeat("x", 128)) {
		t.Fatalf("CompleteNodeInput.Result stored large TurnCompleted payload; size=%d", len(got))
	}
	if got != `{"sharedfile":{"path":"reports/agent.json"}}` {
		t.Fatalf("CompleteNodeInput.Result = %s, want sharedfile reference envelope", got)
	}
}

func TestDAGSubscriber_A2_SharedfileOnlyEmptyTurnUsesExistingSharedfile(t *testing.T) {
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-sharedfile-existing",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"implementer","cwd":"/tmp/node-cwd"},
			"outputs":{
				"to_sharedfile":{"path":"reports/final.pptx","lock_mode":"exclusive"},
				"to_node_result":false
			}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	reader := &dagSubscriberSharedFileReaderSpy{contents: map[string]string{"reports/final.pptx": "existing final file"}}
	writer := &dagSubscriberSharedFileWriterSpy{}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-sharedfile-existing", AgentID: "agent-a2-existing"}},
		&dagSubscriberStopSpy{},
	)
	deps.SharedFileReader = reader
	deps.SharedFileWriter = writer

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-sharedfile-existing", true, ""))

	if len(flow.failCalls) != 0 {
		t.Fatalf("failCalls = %d, want 0 when configured sharedfile exists", len(flow.failCalls))
	}
	if len(flow.claimCalls) != 1 {
		t.Fatalf("claimCalls = %d, want 1 for existing sharedfile promotion", len(flow.claimCalls))
	}
	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	if got := string(flow.completeCalls[0].Result); got != `{"sharedfile":{"path":"reports/final.pptx"}}` {
		t.Fatalf("CompleteNodeInput.Result = %s, want sharedfile reference", got)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("sharedfile writes = %d, want 0 when preserving existing file", len(writer.writes))
	}
}

func TestDAGSubscriber_A2_SharedfileOnlyEmptyTurnFailsWhenFileMissing(t *testing.T) {
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-sharedfile-missing",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"implementer","cwd":"/tmp/node-cwd"},
			"outputs":{
				"to_sharedfile":{"path":"reports/missing.pptx","lock_mode":"exclusive"},
				"to_node_result":false
			}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	writer := &dagSubscriberSharedFileWriterSpy{}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-sharedfile-missing", AgentID: "agent-a2-missing"}},
		&dagSubscriberStopSpy{},
	)
	deps.SharedFileReader = &dagSubscriberSharedFileReaderSpy{}
	deps.SharedFileWriter = writer

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-sharedfile-missing", true, ""))

	if len(flow.completeCalls) != 0 || len(flow.claimCalls) != 0 {
		t.Fatalf("complete/claim calls = %d/%d, want 0/0 for missing sharedfile with empty output",
			len(flow.completeCalls), len(flow.claimCalls))
	}
	if len(flow.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(flow.failCalls))
	}
	if !flow.failCalls[0].FailFast {
		t.Fatal("FailFast = false, want true so pending downstream is canceled")
	}
	if got := flow.failCalls[0].Reason; !strings.Contains(got, "configured sharedfile is missing") {
		t.Fatalf("failure reason = %q, want configured sharedfile is missing", got)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("sharedfile writes = %d, want 0 when output is empty and file is missing", len(writer.writes))
	}
}

func TestDAGSubscriber_A2_SharedfileSkippedWhenCompleteFenceRejects(t *testing.T) {
	payload := `{"summary":"late duplicate output"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-fence",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"implementer","cwd":"/tmp/node-cwd"},
			"outputs":{"to_sharedfile":{"path":"reports/agent-fence.json","lock_mode":"exclusive"}}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{claimErr: pgx.ErrNoRows}
	writer := &dagSubscriberSharedFileWriterSpy{}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-fence", AgentID: "agent-a2-fence"}},
		&dagSubscriberStopSpy{},
	)
	deps.SharedFileReader = &dagSubscriberSharedFileReaderSpy{}
	deps.SharedFileWriter = writer

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-fence", true, payload))

	if len(flow.claimCalls) != 1 {
		t.Fatalf("claimCalls = %d, want 1", len(flow.claimCalls))
	}
	if len(flow.completeCalls) != 0 {
		t.Fatalf("completeCalls = %d, want 0 when materialization claim rejects", len(flow.completeCalls))
	}
	if len(writer.writes) != 0 {
		t.Fatalf("sharedfile writes = %d, want 0 when materialization claim rejects", len(writer.writes))
	}
}

func TestDAGSubscriber_A2_AwaitingVerifyReplayCompletesAfterPriorCompleteError(t *testing.T) {
	payload := `{"summary":"retry after complete error"}`
	node := taskdag.Node{
		DagKey:   "dag-a2",
		NodeKey:  "agent-replay",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"implementer","cwd":"/tmp/node-cwd"},
			"outputs":{"to_sharedfile":{"path":"reports/agent-replay.json","lock_mode":"exclusive"}}
		}`),
	}
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{node}}
	flow := &dagSubscriberFlowSpy{completeErr: errors.New("temporary complete failure")}
	reader := &dagSubscriberSharedFileReaderSpy{}
	writer := &dagSubscriberSharedFileWriterSpy{}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-replay", AgentID: "agent-a2-replay"}},
		&dagSubscriberStopSpy{},
	)
	deps.SharedFileReader = reader
	deps.SharedFileWriter = writer

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-replay", true, payload))

	if len(flow.claimCalls) != 1 || len(writer.writes) != 1 || len(flow.completeCalls) != 1 {
		t.Fatalf("first attempt claim/write/complete = %d/%d/%d, want 1/1/1",
			len(flow.claimCalls), len(writer.writes), len(flow.completeCalls))
	}

	lookup.nodes[0].Status = "awaiting_verify"
	flow.completeErr = nil
	reader.contents = map[string]string{"reports/agent-replay.json": payload}
	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-replay", true, payload))

	if len(flow.claimCalls) != 2 {
		t.Fatalf("claimCalls = %d, want 2 with awaiting_verify replay claim", len(flow.claimCalls))
	}
	if len(flow.completeCalls) != 2 {
		t.Fatalf("completeCalls = %d, want 2 with replay completing node", len(flow.completeCalls))
	}
	if flow.completeCalls[1].Status != "done" {
		t.Fatalf("replay complete status = %q, want done", flow.completeCalls[1].Status)
	}
	if got := string(flow.completeCalls[1].Result); got != `{"sharedfile":{"path":"reports/agent-replay.json"}}` {
		t.Fatalf("replay CompleteNodeInput.Result = %s, want sharedfile reference", got)
	}
	if len(writer.writes) != 1 {
		t.Fatalf("sharedfile writes = %d, want 1 because awaiting_verify replay preserves existing file", len(writer.writes))
	}
}

func TestDAGSubscriber_A2_SharedfileAndNodeResultWritesBoth(t *testing.T) {
	payload := `{"summary":"real agent output"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-both",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"implementer","cwd":"/tmp/node-cwd"},
			"outputs":{
				"to_sharedfile":{"path":"reports/agent-both.json","lock_mode":"exclusive"},
				"to_node_result":true
			}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	writer := &dagSubscriberSharedFileWriterSpy{}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-both", AgentID: "agent-a2-both"}},
		&dagSubscriberStopSpy{},
	)
	deps.SharedFileReader = &dagSubscriberSharedFileReaderSpy{}
	deps.SharedFileWriter = writer

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-both", true, payload))

	if len(writer.writes) != 1 {
		t.Fatalf("sharedfile writes = %d, want 1", len(writer.writes))
	}
	if writer.writes[0].Path != "reports/agent-both.json" || writer.writes[0].Content != payload {
		t.Fatalf("sharedfile write = %+v, want reports/agent-both.json with real payload", writer.writes[0])
	}
	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	if got := string(flow.completeCalls[0].Result); got != payload {
		t.Fatalf("CompleteNodeInput.Result = %s, want real TurnCompleted result %s", got, payload)
	}
}

func TestDAGSubscriber_A2_PreservesAgentWrittenSharedfileWithNodeResultEnabled(t *testing.T) {
	payload := `{"summary":"short turn summary"}`
	existing := strings.Repeat("agent-authored report\n", 300)
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-preserve-both",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"paper_summarizer","cwd":"/tmp/node-cwd"},
			"outputs":{
				"to_sharedfile":{"path":"reports/agent-preserve-both.md","lock_mode":"exclusive"},
				"to_node_result":true
			}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	reader := &dagSubscriberSharedFileReaderSpy{contents: map[string]string{
		"reports/agent-preserve-both.md": existing,
	}}
	writer := &dagSubscriberSharedFileWriterSpy{}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-preserve-both", AgentID: "agent-a2-preserve-both"}},
		&dagSubscriberStopSpy{},
	)
	deps.SharedFileReader = reader
	deps.SharedFileWriter = writer

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-preserve-both", true, payload))

	if len(writer.writes) != 0 {
		t.Fatalf("sharedfile writes = %d, want 0 to preserve agent-authored content", len(writer.writes))
	}
	if len(flow.claimCalls) != 1 {
		t.Fatalf("claimCalls = %d, want 1", len(flow.claimCalls))
	}
	if got := string(flow.claimCalls[0].Result); got != payload {
		t.Fatalf("ClaimNodeOutputMaterialization.Result = %s, want real TurnCompleted result %s", got, payload)
	}
	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	if got := string(flow.completeCalls[0].Result); got != payload {
		t.Fatalf("CompleteNodeInput.Result = %s, want real TurnCompleted result %s", got, payload)
	}
}

func TestDAGSubscriber_A2_PreservesAgentWrittenSharedfile(t *testing.T) {
	payload := `{"summary":"short turn summary"}`
	existing := strings.Repeat("agent-authored report\n", 300)
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-preserve",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"paper_summarizer","cwd":"/tmp/node-cwd"},
			"outputs":{"to_sharedfile":{"path":"reports/agent-preserve.md","lock_mode":"exclusive"}}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	reader := &dagSubscriberSharedFileReaderSpy{contents: map[string]string{
		"reports/agent-preserve.md": existing,
	}}
	writer := &dagSubscriberSharedFileWriterSpy{}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-preserve", AgentID: "agent-a2-preserve"}},
		&dagSubscriberStopSpy{},
	)
	deps.SharedFileReader = reader
	deps.SharedFileWriter = writer

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-preserve", true, payload))

	if len(reader.reads) != 1 || reader.reads[0] != "reports/agent-preserve.md" {
		t.Fatalf("reader reads = %+v, want configured sharedfile path", reader.reads)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("sharedfile writes = %d, want 0 to preserve agent-authored content", len(writer.writes))
	}
	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	if got := string(flow.completeCalls[0].Result); got != `{"sharedfile":{"path":"reports/agent-preserve.md"}}` {
		t.Fatalf("CompleteNodeInput.Result = %s, want sharedfile reference", got)
	}
}

func TestDAGSubscriber_A2_SharedfileMissingReaderFailsWithoutOverwrite(t *testing.T) {
	payload := `{"summary":"short turn summary"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-no-reader",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"paper_summarizer","cwd":"/tmp/node-cwd"},
			"outputs":{"to_sharedfile":{"path":"reports/agent-no-reader.md","lock_mode":"exclusive"}}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	writer := &dagSubscriberSharedFileWriterSpy{}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-no-reader", AgentID: "agent-a2-no-reader"}},
		&dagSubscriberStopSpy{},
	)
	deps.SharedFileWriter = writer

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-no-reader", true, payload))

	if len(writer.writes) != 0 {
		t.Fatalf("sharedfile writes = %d, want 0 when sharedfile reader is missing", len(writer.writes))
	}
	if len(flow.claimCalls) != 0 {
		t.Fatalf("claimCalls = %d, want 0 when sharedfile reader is missing", len(flow.claimCalls))
	}
	if len(flow.completeCalls) != 0 {
		t.Fatalf("completeCalls = %d, want 0 when sharedfile reader is missing", len(flow.completeCalls))
	}
	if len(flow.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(flow.failCalls))
	}
	if !strings.Contains(flow.failCalls[0].Reason, "SharedFileReader not wired") {
		t.Fatalf("fail reason = %q, want SharedFileReader not wired", flow.failCalls[0].Reason)
	}
}

func TestDAGSubscriber_A2_SharedfileMissingWriterFails(t *testing.T) {
	payload := `{"summary":"real agent output"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-no-writer",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"implementer","cwd":"/tmp/node-cwd"},
			"outputs":{"to_sharedfile":{"path":"reports/agent.json","lock_mode":"exclusive"}}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-no-writer", AgentID: "agent-a2-no-writer"}},
		&dagSubscriberStopSpy{},
	)
	deps.SharedFileReader = &dagSubscriberSharedFileReaderSpy{}

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-no-writer", true, payload))

	if len(flow.completeCalls) != 0 {
		t.Fatalf("completeCalls = %d, want 0 when sharedfile writer is missing", len(flow.completeCalls))
	}
	if len(flow.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(flow.failCalls))
	}
	if !strings.Contains(flow.failCalls[0].Reason, "SharedFileWriter not wired") {
		t.Fatalf("fail reason = %q, want SharedFileWriter not wired", flow.failCalls[0].Reason)
	}
}

func TestDAGSubscriber_A2_SharedfileWriteErrorFails(t *testing.T) {
	payload := `{"summary":"real agent output"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-write-error",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"implementer","cwd":"/tmp/node-cwd"},
			"outputs":{"to_sharedfile":{"path":"reports/fail.json","lock_mode":"exclusive"}}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	writer := &dagSubscriberSharedFileWriterSpy{err: errors.New("disk full")}
	deps := setupDAGSubscriberDeps(
		lookup,
		flow,
		&dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-write-error", AgentID: "agent-a2-write-error"}},
		&dagSubscriberStopSpy{},
	)
	deps.SharedFileReader = &dagSubscriberSharedFileReaderSpy{}
	deps.SharedFileWriter = writer

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-write-error", true, payload))

	if len(flow.completeCalls) != 0 {
		t.Fatalf("completeCalls = %d, want 0 when sharedfile write fails", len(flow.completeCalls))
	}
	if len(flow.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1", len(flow.failCalls))
	}
	reason := flow.failCalls[0].Reason
	if !strings.Contains(reason, "reports/fail.json") || !strings.Contains(reason, "disk full") {
		t.Fatalf("fail reason = %q, want path and disk full", reason)
	}
}

func TestDAGSubscriber_A2_ToNodeResultOversizeFailsWithoutFallback(t *testing.T) {
	hugePayload := `{"data":"` + strings.Repeat("x", completeNodeResultCap+1) + `"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-result",
		NodeType: "agent",
		Status:   "running",
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"reviewer","cwd":"/tmp/node-cwd"},
			"outputs":{"to_node_result":true}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	threads := &dagSubscriberThreadSpy{thread: &PersistedThread{ThreadID: "thr-a2-result", AgentID: "agent-a2-result"}}
	stop := &dagSubscriberStopSpy{}
	deps := setupDAGSubscriberDeps(lookup, flow, threads, stop)

	handleDAGTurnCompleted(context.Background(), deps, discardLogger(), newTurnCompletedEvent("thr-a2-result", true, hugePayload))

	if len(flow.completeCalls) != 0 {
		t.Fatalf("completeCalls = %d, want 0 because oversized to_node_result must fail instead of completing", len(flow.completeCalls))
	}
	if len(flow.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1 for ADR-006 size-cap failure", len(flow.failCalls))
	}
	if !strings.Contains(flow.failCalls[0].Reason, "4KB") && !strings.Contains(flow.failCalls[0].Reason, "ADR-006") {
		t.Fatalf("fail reason = %q, want 4KB/ADR-006 context", flow.failCalls[0].Reason)
	}
}
