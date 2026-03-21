package codexapp

import "strings"

func (s *session) ThreadID() string {
	if s == nil {
		return ""
	}
	threadID, _ := s.threadID.Load().(string)
	return strings.TrimSpace(threadID)
}

func (s *session) setThreadID(threadID string) {
	if s == nil {
		return
	}
	s.threadID.Store(strings.TrimSpace(threadID))
}

func (s *session) resolveThreadID(explicit string) string {
	return strings.TrimSpace(firstNonEmpty(explicit, s.ThreadID()))
}
