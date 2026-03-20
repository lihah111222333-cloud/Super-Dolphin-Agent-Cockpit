package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
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
	producerWG.Add(producers)
	for producer := 0; producer < producers; producer++ {
		go enqueueSubmissions(start, &producerWG, &q, producer, perProducer)
	}
	go closeWhenDone(&producerWG, producerDone)

	var consumerWG sync.WaitGroup
	consumerWG.Add(consumers)
	for consumer := 0; consumer < consumers; consumer++ {
		go dequeueSubmissions(start, producerDone, &consumerWG, &q, results)
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

func makeSubmission(id int) turn.TurnSubmission {
	return turn.TurnSubmission{
		ThreadID:             fmt.Sprintf("thread-%d", id),
		ExpectedTurnID:       fmt.Sprintf("turn-%d", id),
		Inputs:               []turn.InputItem{{Type: "text", Content: fmt.Sprintf("text-%d", id)}},
		SelectedSkills:       []string{fmt.Sprintf("skill-%d", id)},
		ManualSkillSelection: true,
		OutputSchema:         []byte(fmt.Sprintf(`{"id":%d}`, id)),
	}
}

func enqueueSubmissions(start <-chan struct{}, wg *sync.WaitGroup, q *SubmissionQueue, producerID, perProducer int) {
	defer wg.Done()
	<-start
	for i := 0; i < perProducer; i++ {
		id := producerID*perProducer + i
		q.Enqueue(makeSubmission(id))
	}
}

func closeWhenDone(wg *sync.WaitGroup, done chan<- struct{}) {
	wg.Wait()
	close(done)
}

func dequeueSubmissions(start <-chan struct{}, producerDone <-chan struct{}, wg *sync.WaitGroup, q *SubmissionQueue, results chan<- string) {
	defer wg.Done()
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

	svc := &service{agents: map[string]*agentRuntime{"agent-1": {id: "agent-1"}}}
	_, err := svc.HandleReportEvent(context.Background(), ReportEvent{AgentID: "agent-1"})
	if err == nil || !strings.Contains(err.Error(), "event type is required") {
		t.Fatalf("HandleReportEvent() error = %v, want event type validation", err)
	}
}

func TestHandleReportEventTreatsCompletionAsTerminal(t *testing.T) {
	t.Parallel()

	svc := &service{agents: map[string]*agentRuntime{"agent-1": {
		id:               "agent-1",
		lastReport:       "done",
		reportRequesters: []string{"req-1"},
	}}}
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

func TestReportParamCompatibility(t *testing.T) {
	t.Parallel()

	var report reportParams
	if err := json.Unmarshal([]byte(`{"agent_id":"agent-1"}`), &report); err != nil || report.AgentID != "agent-1" {
		t.Fatalf("reportParams snake_case = %#v, err=%v", report, err)
	}
	if err := json.Unmarshal([]byte(`{"agentId":"agent-2"}`), &report); err != nil || report.AgentID != "agent-2" {
		t.Fatalf("reportParams camelCase = %#v, err=%v", report, err)
	}

	var remember rememberReportRequestParams
	if err := json.Unmarshal([]byte(`{"sender_id":"sender","worker_id":"worker"}`), &remember); err != nil || remember.RequesterID != "sender" || remember.AgentID != "worker" {
		t.Fatalf("rememberReportRequestParams V2 = %#v, err=%v", remember, err)
	}
	if err := json.Unmarshal([]byte(`{"requesterId":"sender-2","agentId":"worker-2"}`), &remember); err != nil || remember.RequesterID != "sender-2" || remember.AgentID != "worker-2" {
		t.Fatalf("rememberReportRequestParams camelCase = %#v, err=%v", remember, err)
	}

	var event reportEventParams
	if err := json.Unmarshal([]byte(`{"agent_id":"agent-3","event_type":"error","event_data":{"message":"x"}}`), &event); err != nil || event.AgentID != "agent-3" || event.EventType != "error" {
		t.Fatalf("reportEventParams snake_case = %#v, err=%v", event, err)
	}
	if err := json.Unmarshal([]byte(`{"agentId":"agent-4","eventType":"completion","eventData":{"message":"y"}}`), &event); err != nil || event.AgentID != "agent-4" || event.EventType != "completion" {
		t.Fatalf("reportEventParams camelCase = %#v, err=%v", event, err)
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
