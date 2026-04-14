package memory

import (
	"context"
	"errors"
	"strings"
)

type dreamTaskState struct {
	threadID string
	phase    string
	cancel   context.CancelFunc
	done     chan struct{}
}

type DreamTaskSnapshot struct {
	Running  bool
	ThreadID string
	Phase    string
}

var ErrDreamTaskNotRunning = errors.New("dream task is not running")

func (h *MemoryLifecycleHooks) GetDreamTaskStatus() DreamTaskSnapshot {
	return h.dreamTaskSnapshot()
}

func (h *MemoryLifecycleHooks) KillDreamTask() error {
	if !h.killDreamTask() {
		return ErrDreamTaskNotRunning
	}
	return nil
}

func (h *MemoryLifecycleHooks) startDreamTask(threadID string) (context.Context, bool) {
	if h == nil {
		return nil, false
	}
	h.dreamMu.Lock()
	defer h.dreamMu.Unlock()
	if h.dreamTask != nil {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.dreamTask = &dreamTaskState{
		threadID: threadID,
		phase:    dreamTaskPhaseStarting,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	return ctx, true
}

func (h *MemoryLifecycleHooks) setDreamTaskPhase(phase string) {
	if h == nil || strings.TrimSpace(phase) == "" {
		return
	}
	h.dreamMu.Lock()
	if h.dreamTask != nil {
		h.dreamTask.phase = strings.TrimSpace(phase)
	}
	h.dreamMu.Unlock()
}

func (h *MemoryLifecycleHooks) finishDreamTask() {
	if h == nil {
		return
	}
	h.dreamMu.Lock()
	task := h.dreamTask
	h.dreamTask = nil
	h.dreamMu.Unlock()
	if task != nil && task.done != nil {
		close(task.done)
	}
}

func (h *MemoryLifecycleHooks) dreamTaskSnapshot() DreamTaskSnapshot {
	if h == nil {
		return DreamTaskSnapshot{}
	}
	h.dreamMu.Lock()
	defer h.dreamMu.Unlock()
	if h.dreamTask == nil {
		return DreamTaskSnapshot{}
	}
	return DreamTaskSnapshot{
		Running:  true,
		ThreadID: h.dreamTask.threadID,
		Phase:    h.dreamTask.phase,
	}
}

func (h *MemoryLifecycleHooks) killDreamTask() bool {
	if h == nil {
		return false
	}
	h.dreamMu.Lock()
	cancel := context.CancelFunc(nil)
	if h.dreamTask != nil {
		cancel = h.dreamTask.cancel
	}
	h.dreamMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (h *MemoryLifecycleHooks) waitDreamTask(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.dreamMu.Lock()
	done := (<-chan struct{})(nil)
	if h.dreamTask != nil {
		done = h.dreamTask.done
	}
	h.dreamMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
