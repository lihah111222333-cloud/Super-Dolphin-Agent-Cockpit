package claudecli

import (
	"context"
	"errors"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"strings"
	"sync"
)

func requiresResolvedThreadID(threadID string) bool {
	return isPlaceholderThreadID(threadID)
}

// shouldMarkThreadReady decides whether the session should be marked ready
// immediately (without waiting for the CLI to send a thread ID event).
func shouldMarkThreadReady(specThreadID, publicThreadID string) bool {
	return !requiresResolvedThreadID(specThreadID) || !isPlaceholderThreadID(publicThreadID)
}

func isPlaceholderThreadID(threadID string) bool {
	switch strings.ToLower(strings.TrimSpace(threadID)) {
	case "", "pending", "unknown", "placeholder", "none", "null":
		return true
	default:
		return false
	}
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
	if isPlaceholderThreadID(s.publicThreadID) {
		s.publicThreadID = threadID
	}
	s.sessionID = threadID
	s.markThreadReadyLocked()
}

func (s *session) eventThreadIDLocked() string {
	return firstNonEmpty(s.publicThreadID, s.threadID)
}

func (s *session) markThreadReady() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markThreadReadyLocked()
}

func (s *session) markThreadReadyLocked() {
	if s == nil || s.threadReady == nil {
		return
	}
	s.threadReadyOnce.Do(func() {
		close(s.threadReady)
	})
}

func (s *session) resetThreadReadyLocked() {
	if s == nil {
		return
	}
	s.threadReadyOnce = sync.Once{}
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
	if err := checkCtx(ctx); err != nil {
		return ctx, func() {}
	}
	return platformconfig.WithTimeoutIfNone(ctx, platformconfig.InitialThreadIDTimeout)
}

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
