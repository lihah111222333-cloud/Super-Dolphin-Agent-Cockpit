package memory

import (
	"fmt"
	"strings"
	"sync"
)

type ExtractionState struct {
	cursor         int64
	inProgress     bool
	pendingLatest  bool
	pendingHandled bool
	lastError      string
	mu             sync.Mutex
}

type toolCallScope struct {
	threadID string
	turnID   string
}

func turnTrackingKey(threadID, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}

func turnWriteFiles(files map[string]struct{}) []string {
	if len(files) == 0 {
		return nil
	}
	out := make([]string, 0, len(files))
	for file := range files {
		out = append(out, file)
	}
	return uniqueNonEmptyStrings(out)
}

func extractDiffFiles(diffText string) []string {
	lines := strings.Split(strings.ReplaceAll(diffText, "\r\n", "\n"), "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			files = append(files, strings.TrimSpace(strings.TrimPrefix(line, "+++ b/")))
		case strings.HasPrefix(line, "diff --git "):
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				files = append(files, strings.TrimPrefix(parts[3], "b/"))
			}
		}
	}
	return uniqueNonEmptyStrings(files)
}

func (s *ExtractionState) markPending(handled bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingLatest = true
	s.pendingHandled = s.pendingHandled || handled
	if s.inProgress {
		return false
	}
	s.inProgress = true
	s.lastError = ""
	return true
}

func (s *ExtractionState) beginCycle() (int64, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pendingLatest {
		return s.cursor, false, false
	}
	cursor := s.cursor
	handled := s.pendingHandled
	s.pendingLatest = false
	s.pendingHandled = false
	return cursor, handled, true
}

func (s *ExtractionState) commit(cursor int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursor = cursor
	s.lastError = ""
	if !s.pendingLatest {
		s.inProgress = false
		return false
	}
	return true
}

func (s *ExtractionState) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = strings.TrimSpace(err.Error())
	s.inProgress = false
}

func (s *ExtractionState) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inProgress = false
}

func (h *MemoryLifecycleHooks) debugExtractionState(threadID string) string {
	state := h.extractionState(threadID)
	state.mu.Lock()
	defer state.mu.Unlock()
	return fmt.Sprintf("cursor=%d in_progress=%t pending=%t handled=%t error=%q",
		state.cursor,
		state.inProgress,
		state.pendingLatest,
		state.pendingHandled,
		state.lastError,
	)
}
