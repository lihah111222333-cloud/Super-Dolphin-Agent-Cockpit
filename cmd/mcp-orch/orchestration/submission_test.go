package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

func TestEnqueueDequeue(t *testing.T) {
	t.Parallel()

	var q SubmissionQueue
	want := makeSubmission(1)
	q.Enqueue(want)
	if got := q.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
	if got, ok := q.Peek(); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("Peek() = (%#v, %t), want (%#v, true)", got, ok, want)
	}

	got, ok := q.Dequeue()
	if !ok {
		t.Fatal("Dequeue() ok = false, want true")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Dequeue() = %#v, want %#v", got, want)
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("Len() after drain = %d, want 0", got)
	}
}

func TestQueueOrdering(t *testing.T) {
	t.Parallel()

	var q SubmissionQueue
	for i := 1; i <= 3; i++ {
		q.Enqueue(makeSubmission(i))
	}

	for i := 1; i <= 3; i++ {
		got, ok := q.Dequeue()
		if !ok {
			t.Fatalf("Dequeue(%d) ok = false, want true", i)
		}
		wantID := fmt.Sprintf("thread-%d", i)
		if got.ThreadID != wantID {
			t.Fatalf("Dequeue(%d) thread ID = %q, want %q", i, got.ThreadID, wantID)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()

	const producers = 8
	const perProducer = 25
	const consumers = 4
	const total = producers * perProducer

	var q SubmissionQueue
	results := make(chan string, total)
	start := make(chan struct{})
	producerDone := make(chan struct{})

	var producerWG sync.WaitGroup
	for producer := range producers {
		producerID := producer
		producerWG.Go(func() { enqueueSubmissions(start, &q, producerID, perProducer) })
	}
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() { closeWhenDone(&producerWG, producerDone) })

	var consumerWG sync.WaitGroup
	for range consumers {
		consumerWG.Go(func() { dequeueSubmissions(start, producerDone, &q, results) })
	}

	close(start)
	consumerWG.Wait()
	close(results)
	assertUniqueResults(t, results, total)
	if got := q.Len(); got != 0 {
		t.Fatalf("Len() after concurrent drain = %d, want 0", got)
	}
}

func TestClearQueue(t *testing.T) {
	t.Parallel()

	var q SubmissionQueue
	q.Enqueue(makeSubmission(1))
	q.Enqueue(makeSubmission(2))
	q.Clear()

	if got := q.Len(); got != 0 {
		t.Fatalf("Len() after Clear = %d, want 0", got)
	}
	if got, ok := q.Peek(); ok || !reflect.DeepEqual(got, turn.TurnSubmission{}) {
		t.Fatalf("Peek() after Clear = (%#v, %t), want (zero, false)", got, ok)
	}
}

func TestQueueClonesSubmissionOnEnqueue(t *testing.T) {
	t.Parallel()

	var q SubmissionQueue
	original := makeSubmission(1)
	q.Enqueue(original)

	original.Inputs[0].Content = "mutated"
	original.SelectedSkills[0] = "mutated"
	original.OutputSchema[0] = '['

	got, ok := q.Peek()
	if !ok {
		t.Fatal("Peek() ok = false, want true")
	}
	if got.Inputs[0].Content != "text-1" {
		t.Fatalf("peeked input content = %q, want original text", got.Inputs[0].Content)
	}
	if got.SelectedSkills[0] != "skill-1" {
		t.Fatalf("peeked selected skill = %q, want original skill", got.SelectedSkills[0])
	}
	if string(got.OutputSchema) != `{"id":1}` {
		t.Fatalf("peeked output schema = %s, want original schema", string(got.OutputSchema))
	}
}

func TestPeekReturnsSubmissionClone(t *testing.T) {
	t.Parallel()

	var q SubmissionQueue
	q.Enqueue(makeSubmission(2))

	first, ok := q.Peek()
	if !ok {
		t.Fatal("first Peek() ok = false, want true")
	}
	first.Inputs[0].Content = "mutated"
	first.SelectedSkills[0] = "mutated"
	first.OutputSchema[0] = '['

	second, ok := q.Peek()
	if !ok {
		t.Fatal("second Peek() ok = false, want true")
	}
	if second.Inputs[0].Content != "text-2" {
		t.Fatalf("second peek input content = %q, want original text", second.Inputs[0].Content)
	}
	if second.SelectedSkills[0] != "skill-2" {
		t.Fatalf("second peek selected skill = %q, want original skill", second.SelectedSkills[0])
	}
	if string(second.OutputSchema) != `{"id":2}` {
		t.Fatalf("second peek output schema = %s, want original schema", string(second.OutputSchema))
	}
}

func makeSubmission(id int) turn.TurnSubmission {
	return turn.TurnSubmission{
		ThreadID:             fmt.Sprintf("thread-%d", id),
		ExpectedTurnID:       fmt.Sprintf("turn-%d", id),
		Inputs:               []turn.InputItem{{Type: "text", Content: fmt.Sprintf("text-%d", id)}},
		SelectedSkills:       []string{fmt.Sprintf("skill-%d", id)},
		ManualSkillSelection: true,
		OutputSchema:         fmt.Appendf(nil, `{"id":%d}`, id),
	}
}

func enqueueSubmissions(start <-chan struct{}, q *SubmissionQueue, producerID, perProducer int) {
	<-start
	for i := range perProducer {
		id := producerID*perProducer + i
		q.Enqueue(makeSubmission(id))
	}
}

func closeWhenDone(wg *sync.WaitGroup, done chan<- struct{}) {
	wg.Wait()
	close(done)
}

func dequeueSubmissions(start <-chan struct{}, producerDone <-chan struct{}, q *SubmissionQueue, results chan<- string) {
	<-start
	for {
		sub, ok := q.Dequeue()
		if ok {
			results <- sub.ThreadID
			continue
		}
		select {
		case <-producerDone:
			return
		default:
			runtime.Gosched()
		}
	}
}

func assertUniqueResults(t *testing.T, results <-chan string, want int) {
	t.Helper()

	seen := make(map[string]struct{}, want)
	count := 0
	for threadID := range results {
		if _, ok := seen[threadID]; ok {
			t.Fatalf("duplicate dequeue for %q", threadID)
		}
		seen[threadID] = struct{}{}
		count++
	}
	if count != want {
		t.Fatalf("dequeue count = %d, want %d", count, want)
	}
}

func TestHandleReportEventRequiresEventType(t *testing.T) {
	t.Parallel()

	svc := newTestFacadeServiceWithAgents(&agentRuntime{id: "agent-1"})
	_, err := svc.HandleReportEvent(context.Background(), ReportEvent{AgentID: "agent-1"})
	if err == nil || !strings.Contains(err.Error(), "event type is required") {
		t.Fatalf("HandleReportEvent() error = %v, want event type validation", err)
	}
}

func TestHandleReportEventTreatsCompletionAsTerminal(t *testing.T) {
	t.Parallel()

	svc := newTestFacadeServiceWithAgents(&agentRuntime{
		id:               "agent-1",
		lastReport:       "done",
		reportRequesters: []string{"req-1"},
	})
	got, err := svc.HandleReportEvent(context.Background(), ReportEvent{AgentID: "agent-1", EventType: "completion"})
	if err != nil {
		t.Fatalf("HandleReportEvent() error = %v", err)
	}
	if len(got.NotifiedRequesterIDs) != 1 || got.NotifiedRequesterIDs[0] != "req-1" {
		t.Fatalf("NotifiedRequesterIDs = %#v, want [req-1]", got.NotifiedRequesterIDs)
	}
	if got.Report != "done" {
		t.Fatalf("Report = %q, want done", got.Report)
	}
}

func TestHandleReportEventExtractsNestedItemText(t *testing.T) {
	t.Parallel()

	svc := newTestFacadeServiceWithAgents(&agentRuntime{id: "agent-1"})
	got, err := svc.HandleReportEvent(context.Background(), ReportEvent{
		AgentID:   "agent-1",
		EventType: "item/completed",
		EventData: json.RawMessage(`{"item":{"type":"agentMessage","phase":"final_answer","text":"ORCH_OK"}}`),
	})
	if err != nil {
		t.Fatalf("HandleReportEvent() error = %v", err)
	}
	if got.Report != "ORCH_OK" {
		t.Fatalf("Report = %q, want ORCH_OK", got.Report)
	}
}

func TestHandleReportEventPersistsReportToAgentCWD(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	svc := newTestFacadeServiceWithAgents(&agentRuntime{
		id:   "agent-1",
		name: "display one",
		cwd:  cwd,
	})
	got, err := svc.HandleReportEvent(context.Background(), ReportEvent{
		AgentID:   "agent-1",
		EventType: "item/completed",
		Report:    " final body \n",
	})
	if err != nil {
		t.Fatalf("HandleReportEvent() error = %v", err)
	}
	if got.Report != "final body" {
		t.Fatalf("Report = %q, want final body", got.Report)
	}
	raw, err := os.ReadFile(filepath.Join(cwd, ".agnet", "report", "agent-1+display one"))
	if err != nil {
		t.Fatalf("ReadFile(persisted report) error = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "report_seq: 1") || !strings.Contains(text, "final body") {
		t.Fatalf("persisted report = %q, want front matter and final body", text)
	}
}

func TestGetReportNormalizesSimpleMultiLineDisplay(t *testing.T) {
	t.Parallel()

	svc := newTestFacadeServiceWithAgents(&agentRuntime{
		id:         "agent-1",
		lastReport: "1\n2\n3\n4\n5\n6\n7\n8\n9\n10",
	})
	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if got.Report != "1 2 3 4 5 6 7 8 9 10" {
		t.Fatalf("GetReport().Report = %q, want single-line normalized display", got.Report)
	}
}

func TestGetReportPreservesStructuredParagraphs(t *testing.T) {
	t.Parallel()

	svc := newTestFacadeServiceWithAgents(&agentRuntime{
		id:         "agent-1",
		lastReport: "结论：配置缺失\n\n修复：补齐 FOO=bar",
	})
	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if got.Report != "结论：配置缺失\n\n修复：补齐 FOO=bar" {
		t.Fatalf("GetReport().Report = %q, want paragraph breaks preserved", got.Report)
	}
}

func TestReportParamCompatibility(t *testing.T) {
	t.Parallel()

	assertReportParamsAlias(t, `{"agent_id":"agent-1"}`, "agent-1")
	assertReportParamsAlias(t, `{"agentId":"agent-2"}`, "agent-2")
	assertRememberReportParamsAlias(t, `{"sender_id":"sender","worker_id":"worker"}`, "sender", "worker")
	assertRememberReportParamsAlias(t, `{"requesterId":"sender-2","agentId":"worker-2"}`, "sender-2", "worker-2")
	assertReportEventParamsAlias(t, `{"agent_id":"agent-3","event_type":"error","event_data":{"message":"x"}}`, "agent-3", "error")
	assertReportEventParamsAlias(t, `{"agentId":"agent-4","eventType":"completion","eventData":{"message":"y"}}`, "agent-4", "completion")
}

func TestLaunchParamsCompatibility(t *testing.T) {
	t.Parallel()

	assertLegacyLaunchParams(t)
	assertCurrentLaunchParams(t)
}

func assertReportParamsAlias(t *testing.T, input, wantAgentID string) {
	t.Helper()

	var report reportParams
	if err := json.Unmarshal([]byte(input), &report); err != nil || report.AgentID != wantAgentID {
		t.Fatalf("reportParams alias = %#v, err=%v, want agent %q", report, err, wantAgentID)
	}
}

func assertRememberReportParamsAlias(t *testing.T, input, wantRequesterID, wantAgentID string) {
	t.Helper()

	var remember rememberReportRequestParams
	if err := json.Unmarshal([]byte(input), &remember); err != nil ||
		remember.RequesterID != wantRequesterID || remember.AgentID != wantAgentID {
		t.Fatalf("rememberReportRequestParams alias = %#v, err=%v", remember, err)
	}
}

func assertReportEventParamsAlias(t *testing.T, input, wantAgentID, wantEventType string) {
	t.Helper()

	var event reportEventParams
	if err := json.Unmarshal([]byte(input), &event); err != nil ||
		event.AgentID != wantAgentID || event.EventType != wantEventType {
		t.Fatalf("reportEventParams alias = %#v, err=%v", event, err)
	}
}

func assertLegacyLaunchParams(t *testing.T) {
	t.Helper()

	var legacy launchParams
	input := []byte(`{"id":"agent-1","name":"demo","prompt":"hello","cwd":"/tmp","instructions":"follow","config":{"parentID":"parent-1","agentType":"worker","memoryScope":"local"}}`)
	if err := json.Unmarshal(input, &legacy); err != nil {
		t.Fatalf("legacy launchParams err = %v", err)
	}
	if legacy.AgentID != "agent-1" || legacy.Prompt != "hello" || legacy.Instructions != "follow" {
		t.Fatalf("legacy launchParams = %#v", legacy)
	}
	if legacy.ParentID != "parent-1" {
		t.Fatalf("legacy ParentID = %q, want parent-1", legacy.ParentID)
	}
	if legacy.AgentType != "worker" || legacy.MemoryScope != "local" {
		t.Fatalf("legacy metadata = %#v", legacy)
	}
	assertLaunchPromptKeyAlias(t, `{"id":"agent-1","promptKey":"main/sql"}`, "main/sql")
	assertLaunchPromptKeyAlias(t, `{"id":"agent-1","config":{"prompt_key":"main/review"}}`, "main/review")
}

func assertCurrentLaunchParams(t *testing.T) {
	t.Helper()

	var current launchParams
	input := []byte(`{"agentId":"agent-2","parentId":"parent-2","agentType":"reviewer","memoryScope":"user","prompt":"hi","instructions":"careful"}`)
	if err := json.Unmarshal(input, &current); err != nil {
		t.Fatalf("current launchParams err = %v", err)
	}
	if current.AgentID != "agent-2" || current.ParentID != "parent-2" {
		t.Fatalf("current launchParams = %#v", current)
	}
	if current.AgentType != "reviewer" || current.MemoryScope != "user" {
		t.Fatalf("current metadata = %#v", current)
	}
}

func assertLaunchPromptKeyAlias(t *testing.T, input, want string) {
	t.Helper()

	var params launchParams
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		t.Fatalf("launchParams prompt_key alias err = %v", err)
	}
	if params.PromptKey != want {
		t.Fatalf("launchParams PromptKey = %q, want %q", params.PromptKey, want)
	}
}

func TestLaunchRequestFromParamsCarriesPromptAndInstructions(t *testing.T) {
	t.Parallel()

	req := launchRequestFromParams(launchParams{
		AgentID:      "agent-1",
		Prompt:       "hello",
		Instructions: "follow",
		ParentID:     "agent-root",
		AgentType:    "worker",
		MemoryScope:  "project",
		Env: map[string]string{
			"B": "2",
			"A": "1",
		},
	})
	if req.Prompt != "hello" || req.Instructions != "follow" {
		t.Fatalf("launchRequestFromParams() = %#v", req)
	}
	if req.ParentID != "agent-root" || req.AgentType != "worker" || req.MemoryScope != "project" {
		t.Fatalf("launchRequestFromParams() metadata = %#v", req)
	}
	if !reflect.DeepEqual(req.Env, []string{"A=1", "B=2"}) {
		t.Fatalf("launchRequestFromParams() env = %#v, want sorted env", req.Env)
	}
}

func TestSubmitParamsCarryOptionalFields(t *testing.T) {
	t.Parallel()

	assertSubmitParamsSnakeCase(t)
	assertSubmitParamsCamelCase(t)
}

func assertSubmitParamsSnakeCase(t *testing.T) {
	t.Helper()

	var snake submitParams
	if err := json.Unmarshal([]byte(`{"agent_id":"agent-1","selected_skills":["debug"],"manual_skill_selection":true,"output_schema":{"type":"object"}}`), &snake); err != nil {
		t.Fatalf("submitParams snake_case err = %v", err)
	}
	if len(snake.SelectedSkills) != 1 || snake.SelectedSkills[0] != "debug" {
		t.Fatalf("snake SelectedSkills = %#v, want [debug]", snake.SelectedSkills)
	}
	if !snake.ManualSkillSelection {
		t.Fatal("snake ManualSkillSelection = false, want true")
	}
	if string(snake.OutputSchema) != `{"type":"object"}` {
		t.Fatalf("snake OutputSchema = %s, want object schema", string(snake.OutputSchema))
	}
}

func assertSubmitParamsCamelCase(t *testing.T) {
	t.Helper()

	var camel submitParams
	if err := json.Unmarshal([]byte(`{"agentId":"agent-2","selectedSkills":["review"],"manualSkillSelection":true,"outputSchema":{"type":"array"}}`), &camel); err != nil {
		t.Fatalf("submitParams camelCase err = %v", err)
	}
	if camel.AgentID != "agent-2" {
		t.Fatalf("camel AgentID = %q, want agent-2", camel.AgentID)
	}
	if len(camel.SelectedSkills) != 1 || camel.SelectedSkills[0] != "review" {
		t.Fatalf("camel SelectedSkills = %#v, want [review]", camel.SelectedSkills)
	}
	if !camel.ManualSkillSelection {
		t.Fatal("camel ManualSkillSelection = false, want true")
	}
	if string(camel.OutputSchema) != `{"type":"array"}` {
		t.Fatalf("camel OutputSchema = %s, want array schema", string(camel.OutputSchema))
	}
}

func TestDAGDetailJSONUsesSnakeCase(t *testing.T) {
	t.Parallel()

	now := time.Unix(1700000000, 0).UTC()
	payload, err := json.Marshal(DAGDetail{
		DAG: DAGSummary{DagKey: "dag-1", CreatedBy: "tester", CreatedAt: now, UpdatedAt: now},
		Nodes: []DAGNode{{
			DagKey:         "dag-1",
			NodeKey:        "node-1",
			CommandRef:     "cmd.demo",
			ActiveTurnID:   stringPtr("turn-1"),
			ActiveWakeupID: int64Ptr(7),
			CreatedAt:      now,
			UpdatedAt:      now,
		}},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	text := string(payload)
	for _, forbidden := range []string{"dagKey", "nodeKey", "commandRef", "createdBy", "activeTurnId", "activeWakeupId"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("payload contains camelCase key %q: %s", forbidden, text)
		}
	}
	for _, required := range []string{"dag_key", "node_key", "command_ref", "created_by", "active_turn_id", "active_wakeup_id"} {
		if !strings.Contains(text, required) {
			t.Fatalf("payload missing snake_case key %q: %s", required, text)
		}
	}
}

func stringPtr(v string) *string { return &v }

func int64Ptr(v int64) *int64 { return &v }
