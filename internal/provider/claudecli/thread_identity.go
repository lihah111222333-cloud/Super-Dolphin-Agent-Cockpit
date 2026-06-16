package claudecli

import (
	"context"
	"errors"
	"strings"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
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
	return strings.HasPrefix(strings.ToLower(threadID), "agent_")
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

// markThreadReadyLocked closes threadReady at most once using an idempotent
// select/default pattern. Caller must hold s.mu, which serializes the check
// and the close so the double-close panic is impossible.
//
// This replaces the previous sync.Once-per-session pattern whose reset
// path (s.threadReadyOnce = sync.Once{}) was a reassignment of a struct
// containing a Mutex — an idiom that, while safe under s.mu here, is
// flagged by race detectors and by govet in stricter settings.
func (s *session) markThreadReadyLocked() {
	if s == nil || s.threadReady == nil {
		return
	}
	select {
	case <-s.threadReady:
		// Already closed; nothing to do.
	default:
		close(s.threadReady)
	}
}

// resetThreadReadyLocked replaces the threadReady channel with a fresh
// unclosed one so the next markThreadReadyLocked call can close it again.
// Caller must hold s.mu.
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
