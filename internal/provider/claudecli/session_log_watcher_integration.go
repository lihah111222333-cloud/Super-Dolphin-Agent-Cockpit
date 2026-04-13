package claudecli

import (
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type logWatcherIdentity struct {
	sessionID string
	threadID  string
}

func (s *session) detachLogWatcherLocked() *sessionLogWatcher {
	if s == nil {
		return nil
	}
	watcher := s.logWatcher
	s.logWatcher = nil
	s.logWatcherGen++
	return watcher
}

func (s *session) setContextWindowForTransport(tr *transport, contextWindow int) {
	if s == nil || contextWindow <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.transport != tr {
		return
	}
	s.sessionContextWindow = contextWindow
}

func (s *session) startLogWatcherIfCurrent(tr *transport) {
	if s == nil || tr == nil || s.history == nil {
		return
	}
	s.mu.Lock()
	if s.transport != tr {
		s.mu.Unlock()
		return
	}
	identity := logWatcherIdentity{
		sessionID: strings.TrimSpace(s.sessionID),
		threadID:  strings.TrimSpace(s.threadID),
	}
	if identity.sessionID == "" || requiresResolvedThreadID(identity.sessionID) {
		s.mu.Unlock()
		return
	}
	oldWatcher := s.logWatcher
	nextGeneration := s.logWatcherGen + 1
	history := s.history
	logger := s.logger
	s.logWatcher = nil
	s.logWatcherGen = nextGeneration
	s.mu.Unlock()

	if oldWatcher != nil {
		oldWatcher.stopAndWait()
	}
	watcher := newSessionLogWatcher(sessionLogWatcherConfig{
		Logger:       logger,
		PollInterval: defaultSessionLogWatcherPollInterval,
		ResolvePath: func() (string, error) {
			return history.sessionPath(identity.sessionID)
		},
		OnUsage: func(usage sessionLogUsage) {
			s.dispatchTokenUsageIfCurrent(tr, identity, nextGeneration, usage)
		},
	})
	watcher.start()

	s.mu.Lock()
	if s.transport != tr ||
		s.logWatcherGen != nextGeneration ||
		strings.TrimSpace(s.sessionID) != identity.sessionID ||
		strings.TrimSpace(s.threadID) != identity.threadID {
		s.mu.Unlock()
		watcher.stopAndWait()
		return
	}
	s.logWatcher = watcher
	s.mu.Unlock()
}

func (s *session) dispatchTokenUsageIfCurrent(tr *transport, identity logWatcherIdentity, generation uint64, usage sessionLogUsage) {
	usageSessionID := strings.TrimSpace(usage.SessionID)
	if usageSessionID != "" && !strings.EqualFold(usageSessionID, identity.sessionID) {
		return
	}
	timestamp := strings.TrimSpace(usage.Timestamp)
	if timestamp == "" {
		timestamp = time.Now().Format(time.RFC3339Nano)
	}
	s.mu.Lock()
	if s.transport != tr ||
		s.logWatcherGen != generation ||
		strings.TrimSpace(s.sessionID) != identity.sessionID ||
		strings.TrimSpace(s.threadID) != identity.threadID {
		s.mu.Unlock()
		return
	}
	threadID := s.eventThreadIDLocked()
	sessionID := s.sessionID
	contextWindow := claudeContextWindow(s.sessionContextWindow, s.currentTransportModelLocked(), s.history)
	s.mu.Unlock()

	s.dispatch(dto.RawProviderEvent{
		EventType: "tokens:log_watcher",
		Data: map[string]any{
			"thread_id":      threadID,
			"session_id":     sessionID,
			"timestamp":      timestamp,
			"input_tokens":   usage.InputTokens,
			"output_tokens":  usage.OutputTokens,
			"total_tokens":   usage.TotalTokens,
			"context_window": contextWindow,
		},
	})
}
