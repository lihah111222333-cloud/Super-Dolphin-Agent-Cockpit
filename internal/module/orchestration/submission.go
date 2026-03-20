package orchestration

import (
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

type SubmissionQueue struct {
	mu    sync.Mutex
	items []turn.TurnSubmission
}

func (q *SubmissionQueue) Enqueue(s turn.TurnSubmission) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = append(q.items, s)
}

func (q *SubmissionQueue) Dequeue() (turn.TurnSubmission, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return turn.TurnSubmission{}, false
	}
	s := q.items[0]
	q.items = q.items[1:]
	return s, true
}

func (q *SubmissionQueue) Peek() (turn.TurnSubmission, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return turn.TurnSubmission{}, false
	}
	return q.items[0], true
}

func (q *SubmissionQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *SubmissionQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = nil
}
