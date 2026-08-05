package acpnode

import (
	"errors"
	"fmt"
	"sync"
)

// ErrOwnerPending identifies an owner whose underlying operation has not
// completed yet. Callers that need the final operation error should use Join.
var ErrOwnerPending = errors.New("acp: owner remains pending")

// ownerState is the shared completion state for bounded lifecycle owners.
// The operation-specific result channel remains separate because Write also
// needs to retain its byte count.
type ownerState struct {
	done chan struct{}

	mu       sync.Mutex
	err      error
	finished bool
	onDone   func()
}

func newOwnerState() ownerState {
	return ownerState{done: make(chan struct{})}
}

func (s *ownerState) finish(err error) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.err = err
	s.finished = true
	onDone := s.onDone
	close(s.done)
	s.mu.Unlock()
	if onDone != nil {
		onDone()
	}
}

func (s *ownerState) setOnDone(onDone func()) {
	s.mu.Lock()
	finished := s.finished
	if !finished {
		s.onDone = onDone
	}
	s.mu.Unlock()
	if finished && onDone != nil {
		onDone()
	}
}

// Done 返回 owner 底层操作完成时关闭的信号。
func (s *ownerState) Done() <-chan struct{} {
	if s == nil || s.done == nil {
		return closedChannel()
	}
	return s.done
}

// Err 返回 owner 的完成错误，操作尚未结束时返回 ErrOwnerPending。
func (s *ownerState) Err() error {
	if s == nil {
		return ErrOwnerPending
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.finished {
		return ErrOwnerPending
	}
	return s.err
}

// Join 等待 owner 完成并返回其底层错误。
func (s *ownerState) Join() error {
	if s == nil {
		return ErrOwnerPending
	}
	<-s.done
	return s.Err()
}

type trackedOwner interface {
	Done() <-chan struct{}
	Err() error
	Join() error
	setOnDone(func())
	pendingError() error
}

// streamOwner retains a stdout/stderr read until the non-cooperative reader
// actually returns, so Close can report the owner instead of leaking a read
// goroutine behind a completed client lifecycle.
type streamOwner struct {
	ownerState
	result chan error
	kind   string
}

func newStreamOwner(kind string) *streamOwner {
	return &streamOwner{ownerState: newOwnerState(), result: make(chan error, 1), kind: kind}
}

type streamOwnerPendingError struct {
	owner *streamOwner
}

// Error 返回 stdout/stderr owner 仍未完成的稳定错误分类。
func (e *streamOwnerPendingError) Error() string {
	if e == nil || e.owner == nil {
		return "acp: stream owner remains pending"
	}
	return fmt.Sprintf("acp: %s stream owner remains pending", e.owner.kind)
}

// Done 暴露 stdout/stderr owner 的最终完成信号。
func (e *streamOwnerPendingError) Done() <-chan struct{} {
	if e == nil || e.owner == nil {
		return closedChannel()
	}
	return e.owner.Done()
}

// Err 返回 stdout/stderr owner 完成后的底层错误。
func (e *streamOwnerPendingError) Err() error {
	if e == nil || e.owner == nil {
		return ErrOwnerPending
	}
	return e.owner.Err()
}

// Join 等待 stdout/stderr owner 完成并返回底层错误。
func (e *streamOwnerPendingError) Join() error {
	if e == nil || e.owner == nil {
		return ErrOwnerPending
	}
	return e.owner.Join()
}

// Unwrap 在 stream owner 完成后暴露其底层错误。
func (e *streamOwnerPendingError) Unwrap() error {
	err := e.Err()
	if errors.Is(err, ErrOwnerPending) {
		return nil
	}
	return err
}

func (o *streamOwner) pendingError() error {
	if o == nil {
		return nil
	}
	select {
	case <-o.Done():
		return nil
	default:
		return &streamOwnerPendingError{owner: o}
	}
}
