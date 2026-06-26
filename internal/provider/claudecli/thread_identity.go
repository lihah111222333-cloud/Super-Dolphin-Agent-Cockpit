package claudecli

import (
	"context"
	"errors"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"strings"
)

func isPlaceholderThreadID(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return true
	}
	switch strings.ToLower(threadID) {
	case "pending", "unknown", "placeholder", "none", "null":
		return true
	}
	if strings.HasPrefix(strings.ToLower(threadID), "agent_") {
		return true
	}
	return false
}

func (s *session) setResolvedThreadID(threadID string) {
	s.setResolvedThreadIDForTransport(nil, threadID)
}

func (s *session) setResolvedThreadIDForTransport(tr *transport, threadID string) {
	threadID = strings.TrimSpace(threadID)
	if isPlaceholderThreadID(threadID) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if tr != nil && s.transport != tr {
		return
	}
	s.threadID = threadID
	// Only promote the resolved UUID to publicThreadID when it is truly
	// unset (empty). When publicThreadID already carries an agentID
	// (e.g. "agent_17782…"), it is the identifier the frontend uses to
	// track this session card. Overwriting it with the provider UUID
	// would cause every subsequent event to carry an unknown thread_id,
	// making the frontend create a duplicate session card.
	if strings.TrimSpace(s.publicThreadID) == "" {
		s.publicThreadID = threadID
	}
	s.sessionID = threadID
	s.markThreadReadyLocked()
}

func (s *session) eventThreadIDLocked() string {
	return shared.FirstNonEmpty(s.publicThreadID, s.threadID)
}

func (s *session) markThreadReady() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markThreadReadyLocked()
}

// markThreadReadyLocked 在持锁状态下至多关闭一次 threadReady。
// select/default 让 ready 信号可重复触发而不 panic，也避免重置 sync.Once 这类容易被 vet 误判的写法。
func (s *session) markThreadReadyLocked() {
	if s == nil || s.threadReady == nil {
		return
	}
	select {
	case <-s.threadReady:
		// 已经关闭，保持幂等。
	default:
		close(s.threadReady)
	}
}

// resetThreadReadyLocked 用新的未关闭 channel 开始下一轮 thread ready 等待。
// 调用方必须持有 s.mu，否则等待方可能看到半更新状态。
func (s *session) resetThreadReadyLocked() {
	if s == nil {
		return
	}
	s.threadReady = make(chan struct{})
}

func (s *session) awaitResolvedThreadID(ctx context.Context) error {
	ready, tr := s.threadReadyState()
	return waitThreadReady(ctx, ready, tr)
}

func (s *session) threadReadyState() (chan struct{}, *transport) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadReady, s.transport
}

func (s *session) pendingThreadReadyLocked() (chan struct{}, *transport) {
	if s == nil || s.threadReady == nil {
		return nil, nil
	}
	select {
	case <-s.threadReady:
		return nil, nil
	default:
		return s.threadReady, s.transport
	}
}

func (s *session) awaitThreadReadyLocked(ctx context.Context) error {
	ready, tr := s.pendingThreadReadyLocked()
	if ready == nil {
		return nil
	}
	s.mu.Unlock()
	err := waitThreadReady(ctx, ready, tr)
	s.mu.Lock()
	return err
}

func withThreadIDTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if err := shared.CheckCtx(ctx); err != nil {
		return ctx, func() {}
	}
	return platformconfig.WithTimeoutIfNone(ctx, platformconfig.InitialThreadIDTimeout)
}

// waitForThreadReadyOrExit 等待 Claude 线程 ready，或在进程退出时返回错误。
func waitForThreadReadyOrExit(ctx context.Context, ready <-chan struct{}, tr *transport) error {
	select {
	case <-ready:
		return nil
	case <-tr.done:
		select {
		case <-ready:
			return nil
		default:
		}
		if err := tr.waitErr(); err != nil {
			return err
		}
		return errors.New("claudecli: session exited before real thread id")
	case <-ctx.Done():
		select {
		case <-ready:
			return nil
		default:
		}
		return threadReadyContextErr(ctx.Err())
	}
}
