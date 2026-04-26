package skill

import (
	"context"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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

func (s *service) publishSkillsChanged(ctx context.Context, action, name, scope string) {
	if s == nil || s.emitSkillsChanged == nil {
		return
	}
	normalizedScope := strings.TrimSpace(scope)
	cwd := ""
	if normalizedScope == skillScopeProject {
		cwd = cwdFromContext(ctx)
	}
	s.scheduleSkillsChanged(uidto.SkillsChanged{
		EventHeader: shared.EventHeader{Timestamp: time.Now()},
		SkillsDir:   strings.TrimSpace(s.root),
		Name:        strings.TrimSpace(name),
		Action:      normalizeSkillsChangedAction(action),
		Count:       1,
		Scope:       normalizedScope,
		Cwd:         cwd,
	})
}

func (s *service) scheduleSkillsChanged(next uidto.SkillsChanged) {
	next = normalizeSkillsChanged(next)

	s.skillsChangedMu.Lock()
	if s.skillsChangedNext.Count == 0 {
		s.skillsChangedNext = next
	} else if skillsChangedMergeable(s.skillsChangedNext, next) {
		s.skillsChangedNext = mergeSkillsChanged(s.skillsChangedNext, next)
	} else {
		// P0b F12: cross-scope or cross-cwd events cannot share one payload.
		// Queue the buffered event for this debounce flush and start a fresh
		// buffer for next so subscribers can attribute both mutations.
		s.skillsChangedQueue = append(s.skillsChangedQueue, s.skillsChangedNext)
		s.skillsChangedNext = next
	}
	s.skillsChangedSeq++
	seq := s.skillsChangedSeq
	s.skillsChangedMu.Unlock()

	runtimesafe.SafeGo(context.Background(), pkglogger.Get(), "skill.scheduleSkillsChangedFlush", func(context.Context) {
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
	queue := s.skillsChangedQueue
	s.skillsChangedQueue = nil
	next := s.skillsChangedNext
	s.skillsChangedNext = uidto.SkillsChanged{}
	emit := s.emitSkillsChanged
	s.skillsChangedMu.Unlock()

	if emit == nil {
		return
	}
	for _, ev := range queue {
		emit(ev)
	}
	if next.Count > 0 {
		emit(next)
	}
}

func normalizeSkillsChanged(next uidto.SkillsChanged) uidto.SkillsChanged {
	next.SkillsDir = strings.TrimSpace(next.SkillsDir)
	next.Name = strings.TrimSpace(next.Name)
	next.Scope = strings.TrimSpace(next.Scope)
	next.Cwd = strings.TrimSpace(next.Cwd)
	// P0b Step 6: system-scope events never carry Cwd; enforce here.
	if next.Scope != skillScopeProject {
		next.Cwd = ""
	}
	next.Action = normalizeSkillsChangedAction(next.Action)
	next.Actions = appendUniqueSkillsChangedActions(nil, next.Actions...)
	if next.Action != "" {
		next.Actions = appendUniqueSkillsChangedActions(next.Actions, next.Action)
	}
	return syncSkillsChangedActionSummary(next)
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
	// P0b F12: scheduleSkillsChanged must handle cross-scope/cwd events by
	// enqueueing current before starting next. Keep this fallback for any legacy
	// direct callers, but new code should only call merge for mergeable events.
	if !skillsChangedMergeable(current, next) {
		return next
	}
	current = mergeSkillsChangedMetadata(current, next)
	current.Actions = appendUniqueSkillsChangedActions(current.Actions, next.Actions...)
	return syncSkillsChangedActionSummary(current)
}

func skillsChangedMergeable(current, next uidto.SkillsChanged) bool {
	return current.Scope == next.Scope && current.Cwd == next.Cwd
}

func mergeSkillsChangedMetadata(current, next uidto.SkillsChanged) uidto.SkillsChanged {
	if next.Timestamp.After(current.Timestamp) {
		current.EventHeader = next.EventHeader
	}
	if next.SkillsDir != "" {
		current.SkillsDir = next.SkillsDir
	}
	if current.Name == "" || next.Name == "" || current.Name != next.Name {
		current.Name = ""
	}
	return current
}

func syncSkillsChangedActionSummary(ev uidto.SkillsChanged) uidto.SkillsChanged {
	switch len(ev.Actions) {
	case 0:
		ev.Action = ""
	case 1:
		ev.Action = ev.Actions[0]
	default:
		ev.Action = ""
	}
	ev.Count = len(ev.Actions)
	return ev
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

// scopeFromTrust maps a SKILL.md trust scope to the SkillsChanged event scope
// string. TrustProject corresponds to the project scope; everything else (TrustUser,
// TrustSigned, TrustUnknown) is reported as system-scope so subscribers receive the
// trust boundary explicitly.
func scopeFromTrust(trust TrustScope) string {
	if trust == TrustProject {
		return skillScopeProject
	}
	return skillScopeSystem
}
