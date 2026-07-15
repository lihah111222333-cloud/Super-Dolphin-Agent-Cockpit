package orchestration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
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
		RunID:    dagSubscriberTestRunID(1601),
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

	if len(writer.writes) != 2 {
		t.Fatalf("sharedfile writes = %d, want content + owner marker", len(writer.writes))
	}
	if got := findSharedFileWrite(t, writer, "reports/agent.json"); got != hugePayload {
		t.Fatalf("sharedfile content size = %d, want exact TurnCompleted result size %d", len(got), len(hugePayload))
	}
	assertSharedFileOwnerMarker(t,
		findSharedFileWrite(t, writer, expectedDAGSubscriberMarkerPath("reports/agent.json")),
		"dag-a2", "agent-sharedfile", "thr-a2-sharedfile", "turn-1", 1601)
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

func TestDAGSubscriber_A2_SharedfileOnlyEmptyTurnFailsWhenExistingFileHasNoCurrentRunMarker(t *testing.T) {
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-sharedfile-existing",
		NodeType: "agent",
		Status:   "running",
		RunID:    dagSubscriberTestRunID(1602),
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

	if len(flow.completeCalls) != 0 || len(flow.claimCalls) != 0 {
		t.Fatalf("complete/claim calls = %d/%d, want 0/0 without current-run marker",
			len(flow.completeCalls), len(flow.claimCalls))
	}
	if len(flow.failCalls) != 1 {
		t.Fatalf("failCalls = %d, want 1 when existing sharedfile has no current-run marker", len(flow.failCalls))
	}
	if !flow.failCalls[0].FailFast {
		t.Fatal("FailFast = false, want true so pending downstream is canceled")
	}
	if got := flow.failCalls[0].Reason; !strings.Contains(got, "current-run ownership marker") {
		t.Fatalf("failure reason = %q, want current-run ownership marker", got)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("sharedfile writes = %d, want 0 when output is empty and marker is missing", len(writer.writes))
	}
}

func TestDAGSubscriber_A2_SharedfileOnlyEmptyTurnUsesExistingSharedfileWithCurrentRunMarker(t *testing.T) {
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-sharedfile-existing",
		NodeType: "agent",
		Status:   "running",
		RunID:    dagSubscriberTestRunID(1603),
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"implementer","cwd":"/tmp/node-cwd"},
			"outputs":{
				"to_sharedfile":{"path":"reports/final.pptx","lock_mode":"exclusive"},
				"to_node_result":false
			}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	reader := &dagSubscriberSharedFileReaderSpy{contents: map[string]string{
		"reports/final.pptx": "existing final file",
		expectedDAGSubscriberMarkerPath("reports/final.pptx"): dagSubscriberOwnerMarkerJSON(
			t, "dag-a2", "agent-sharedfile-existing", "thr-a2-sharedfile-existing", "turn-1", 1603),
	}}
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
		t.Fatalf("failCalls = %d, want 0 when current-run marker exists", len(flow.failCalls))
	}
	if len(flow.claimCalls) != 1 {
		t.Fatalf("claimCalls = %d, want 1 for current-run sharedfile promotion", len(flow.claimCalls))
	}
	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	if got := string(flow.completeCalls[0].Result); got != `{"sharedfile":{"path":"reports/final.pptx"}}` {
		t.Fatalf("CompleteNodeInput.Result = %s, want sharedfile reference", got)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("sharedfile writes = %d, want 0 when preserving current-run file", len(writer.writes))
	}
}

func TestDAGSubscriber_A2_SharedfileOnlyEmptyTurnFailsWhenFileMissing(t *testing.T) {
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-sharedfile-missing",
		NodeType: "agent",
		Status:   "running",
		RunID:    dagSubscriberTestRunID(1604),
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
	if got := flow.failCalls[0].Reason; !strings.Contains(got, "current-run ownership marker") {
		t.Fatalf("failure reason = %q, want current-run ownership marker", got)
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
		RunID:    dagSubscriberTestRunID(1605),
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"implementer","cwd":"/tmp/node-cwd"},
			"outputs":{"to_sharedfile":{"path":"reports/agent-fence.json","lock_mode":"exclusive"}}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{claimErr: sql.ErrNoRows}
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
		RunID:    dagSubscriberTestRunID(1606),
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

	if len(flow.claimCalls) != 1 || len(writer.writes) != 2 || len(flow.completeCalls) != 1 {
		t.Fatalf("first attempt claim/write/complete = %d/%d/%d, want 1/2/1",
			len(flow.claimCalls), len(writer.writes), len(flow.completeCalls))
	}

	lookup.nodes[0].Status = "awaiting_verify"
	flow.completeErr = nil
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
	if len(writer.writes) != 4 {
		t.Fatalf("sharedfile writes = %d, want 4 because replay refreshes content and owner marker", len(writer.writes))
	}
}

func TestDAGSubscriber_A2_SharedfileAndNodeResultWritesBoth(t *testing.T) {
	payload := `{"summary":"real agent output"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-both",
		NodeType: "agent",
		Status:   "running",
		RunID:    dagSubscriberTestRunID(1607),
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

	if len(writer.writes) != 2 {
		t.Fatalf("sharedfile writes = %d, want content + owner marker", len(writer.writes))
	}
	if got := findSharedFileWrite(t, writer, "reports/agent-both.json"); got != payload {
		t.Fatalf("sharedfile content = %s, want %s", got, payload)
	}
	assertSharedFileOwnerMarker(t,
		findSharedFileWrite(t, writer, expectedDAGSubscriberMarkerPath("reports/agent-both.json")),
		"dag-a2", "agent-both", "thr-a2-both", "turn-1", 1607)
	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	if got := string(flow.completeCalls[0].Result); got != payload {
		t.Fatalf("CompleteNodeInput.Result = %s, want real TurnCompleted result %s", got, payload)
	}
}

func TestDAGSubscriber_A2_ExistingSharedfileWithNodeResultEnabledRefreshesContentAndMarker(t *testing.T) {
	payload := `{"summary":"short turn summary"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-preserve-both",
		NodeType: "agent",
		Status:   "running",
		RunID:    dagSubscriberTestRunID(1608),
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
		"reports/agent-preserve-both.md": strings.Repeat("old report from a previous run\n", 300),
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

	if len(writer.writes) != 2 {
		t.Fatalf("sharedfile writes = %d, want content + owner marker", len(writer.writes))
	}
	if got := findSharedFileWrite(t, writer, "reports/agent-preserve-both.md"); got != payload {
		t.Fatalf("sharedfile content = %s, want current TurnCompleted payload %s", got, payload)
	}
	assertSharedFileOwnerMarker(t,
		findSharedFileWrite(t, writer, expectedDAGSubscriberMarkerPath("reports/agent-preserve-both.md")),
		"dag-a2", "agent-preserve-both", "thr-a2-preserve-both", "turn-1", 1608)
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

func TestDAGSubscriber_A2_ExistingSharedfileRefreshesContentAndMarker(t *testing.T) {
	payload := `{"summary":"short turn summary"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-preserve",
		NodeType: "agent",
		Status:   "running",
		RunID:    dagSubscriberTestRunID(1609),
		Config: testRawConfig(t, `{
			"exec":{"agent_key":"paper_summarizer","cwd":"/tmp/node-cwd"},
			"outputs":{"to_sharedfile":{"path":"reports/agent-preserve.md","lock_mode":"exclusive"}}
		}`),
	}}}
	flow := &dagSubscriberFlowSpy{}
	reader := &dagSubscriberSharedFileReaderSpy{contents: map[string]string{
		"reports/agent-preserve.md": strings.Repeat("old report from a previous run\n", 300),
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

	if len(writer.writes) != 2 {
		t.Fatalf("sharedfile writes = %d, want content + owner marker", len(writer.writes))
	}
	if got := findSharedFileWrite(t, writer, "reports/agent-preserve.md"); got != payload {
		t.Fatalf("sharedfile content = %s, want current TurnCompleted payload %s", got, payload)
	}
	assertSharedFileOwnerMarker(t,
		findSharedFileWrite(t, writer, expectedDAGSubscriberMarkerPath("reports/agent-preserve.md")),
		"dag-a2", "agent-preserve", "thr-a2-preserve", "turn-1", 1609)
	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	if got := string(flow.completeCalls[0].Result); got != `{"sharedfile":{"path":"reports/agent-preserve.md"}}` {
		t.Fatalf("CompleteNodeInput.Result = %s, want sharedfile reference", got)
	}
}

func TestDAGSubscriber_A2_SharedfilePresentTurnOutputWritesWithoutReader(t *testing.T) {
	payload := `{"summary":"short turn summary"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-no-reader",
		NodeType: "agent",
		Status:   "running",
		RunID:    dagSubscriberTestRunID(1610),
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

	if len(writer.writes) != 2 {
		t.Fatalf("sharedfile writes = %d, want content + owner marker without reader preflight", len(writer.writes))
	}
	if got := findSharedFileWrite(t, writer, "reports/agent-no-reader.md"); got != payload {
		t.Fatalf("sharedfile content = %s, want %s", got, payload)
	}
	assertSharedFileOwnerMarker(t,
		findSharedFileWrite(t, writer, expectedDAGSubscriberMarkerPath("reports/agent-no-reader.md")),
		"dag-a2", "agent-no-reader", "thr-a2-no-reader", "turn-1", 1610)
	if len(flow.claimCalls) != 1 {
		t.Fatalf("claimCalls = %d, want 1", len(flow.claimCalls))
	}
	if len(flow.completeCalls) != 1 {
		t.Fatalf("completeCalls = %d, want 1", len(flow.completeCalls))
	}
	if len(flow.failCalls) != 0 {
		t.Fatalf("failCalls = %d, want 0", len(flow.failCalls))
	}
}

func TestDAGSubscriber_A2_SharedfileMissingWriterFails(t *testing.T) {
	payload := `{"summary":"real agent output"}`
	lookup := &dagSubscriberLookupSpy{nodes: []taskdag.Node{{
		DagKey:   "dag-a2",
		NodeKey:  "agent-no-writer",
		NodeType: "agent",
		Status:   "running",
		RunID:    dagSubscriberTestRunID(1611),
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
		RunID:    dagSubscriberTestRunID(1612),
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
