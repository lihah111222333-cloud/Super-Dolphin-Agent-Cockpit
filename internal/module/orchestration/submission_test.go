package orchestration

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"

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
