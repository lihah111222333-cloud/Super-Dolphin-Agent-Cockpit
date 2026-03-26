package skill

import (
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/kelindar/event"
)

type skillsChangedEmitter func(uidto.SkillsChanged)

const skillsChangedDebounceWindow = 100 * time.Millisecond

func (s *service) bindDispatcher(dispatcher *event.Dispatcher) {
	if s == nil {
		return
	}
	s.emitSkillsChanged = bus.NewEmitter[uidto.SkillsChanged](dispatcher)
}

func (s *service) publishSkillsChanged(action, name string) {
	if s == nil || s.emitSkillsChanged == nil {
		return
	}
	s.scheduleSkillsChanged(uidto.SkillsChanged{
		EventHeader: shared.EventHeader{Timestamp: time.Now()},
		SkillsDir:   strings.TrimSpace(s.root),
		Name:        strings.TrimSpace(name),
		Action:      normalizeSkillsChangedAction(action),
		Count:       1,
	})
}

func (s *service) scheduleSkillsChanged(next uidto.SkillsChanged) {
	next = normalizeSkillsChanged(next)

	s.skillsChangedMu.Lock()
	s.skillsChangedNext = mergeSkillsChanged(s.skillsChangedNext, next)
	s.skillsChangedSeq++
	seq := s.skillsChangedSeq
	s.skillsChangedMu.Unlock()

	platformshared.SafeGo(slog.Default(), func() {
		time.Sleep(skillsChangedDebounceWindow)
		s.flushSkillsChanged(seq)
	})
}

func (s *service) flushSkillsChanged(seq uint64) {
	s.skillsChangedMu.Lock()
	if seq != s.skillsChangedSeq {
		s.skillsChangedMu.Unlock()
		return
	}
	next := s.skillsChangedNext
	s.skillsChangedNext = uidto.SkillsChanged{}
	emit := s.emitSkillsChanged
	s.skillsChangedMu.Unlock()

	if emit == nil {
		return
	}
	emit(next)
}

func normalizeSkillsChanged(next uidto.SkillsChanged) uidto.SkillsChanged {
	next.SkillsDir = strings.TrimSpace(next.SkillsDir)
	next.Name = strings.TrimSpace(next.Name)
	next.Action = normalizeSkillsChangedAction(next.Action)
	next.Actions = appendUniqueSkillsChangedActions(nil, next.Actions...)
	if next.Action != "" {
		next.Actions = appendUniqueSkillsChangedActions(next.Actions, next.Action)
	}
	switch len(next.Actions) {
	case 0:
		next.Action = ""
	case 1:
		next.Action = next.Actions[0]
	default:
		next.Action = ""
	}
	next.Count = len(next.Actions)
	return next
}

func normalizeSkillsChangedAction(action string) string {
	action = strings.TrimSpace(action)
	switch {
	case action == "":
		return ""
	case strings.Contains(action, "import"):
		return "import"
	case strings.Contains(action, "delete"):
		return "delete"
	case strings.Contains(action, "write"):
		return "write"
	default:
		return action
	}
}

func mergeSkillsChanged(current, next uidto.SkillsChanged) uidto.SkillsChanged {
	if current.Count == 0 {
		return next
	}
	if next.Timestamp.After(current.Timestamp) {
		current.EventHeader = next.EventHeader
	}
	if next.SkillsDir != "" {
		current.SkillsDir = next.SkillsDir
	}
	if current.Name == "" || next.Name == "" || current.Name != next.Name {
		current.Name = ""
	}
	current.Actions = appendUniqueSkillsChangedActions(current.Actions, next.Actions...)
	switch len(current.Actions) {
	case 0:
		current.Action = ""
	case 1:
		current.Action = current.Actions[0]
	default:
		current.Action = ""
	}
	current.Count = len(current.Actions)
	return current
}

func appendUniqueSkillsChangedActions(dst []string, actions ...string) []string {
	for _, action := range actions {
		action = normalizeSkillsChangedAction(action)
		if action == "" || containsSkillsChangedAction(dst, action) {
			continue
		}
		dst = append(dst, action)
	}
	return dst
}

func containsSkillsChangedAction(actions []string, target string) bool {
	for _, action := range actions {
		if action == target {
			return true
		}
	}
	return false
}
