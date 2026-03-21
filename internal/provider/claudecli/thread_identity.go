package claudecli

import (
	"context"
	"errors"
	"fmt"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"strings"
)

func requiresResolvedThreadID(threadID string) bool {
	return isPlaceholderThreadID(threadID)
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
	threadID = strings.TrimSpace(threadID)
	if isPlaceholderThreadID(threadID) {
		return
	}
	s.mu.Lock()
	s.threadID = threadID
	s.sessionID = threadID
	s.mu.Unlock()
	s.markThreadReady()
}

func (s *session) markThreadReady() {
	if s == nil || s.threadReady == nil {
		return
	}
	s.threadReadyOnce.Do(func() {
		close(s.threadReady)
	})
}

func (s *session) awaitResolvedThreadID(ctx context.Context) error {
	ready, tr := s.threadReadyState()
	if ready == nil {
		return nil
	}
	select {
	case <-ready:
		return nil
	default:
	}
	waitCtx, cancel := withThreadIDTimeout(ctx)
	defer cancel()
	if tr == nil {
		return waitForThreadReady(waitCtx, ready)
	}
	return waitForThreadReadyOrExit(waitCtx, ready, tr)
}

func (s *session) threadReadyState() (chan struct{}, *transport) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadReady, s.transport
}

func withThreadIDTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return platformconfig.WithInitialThreadIDTimeout(ctx)
}

func waitForThreadReady(ctx context.Context, ready <-chan struct{}) error {
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("claudecli: waiting for real thread id: %w", ctx.Err())
	}
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
		return fmt.Errorf("claudecli: waiting for real thread id: %w", ctx.Err())
	}
}
