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
	s.skillsChangedNext = mergeSkillsChanged(s.skillsChangedNext, next)
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
	next.Action = skillChangedActionSummary(next.Actions)
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
	if shouldReplaceSkillsChanged(current, next) {
		return next
	}
	current = mergeSkillsChangedMetadata(current, next)
	current.Name = mergeSkillsChangedName(current.Name, next.Name)
	current.Actions = appendUniqueSkillsChangedActions(current.Actions, next.Actions...)
	current.Action = skillChangedActionSummary(current.Actions)
	current.Count = len(current.Actions)
	return current
}

func shouldReplaceSkillsChanged(current, next uidto.SkillsChanged) bool {
	if current.Count == 0 {
		return true
	}
	return current.Scope != next.Scope || current.Cwd != next.Cwd
}

func mergeSkillsChangedMetadata(current, next uidto.SkillsChanged) uidto.SkillsChanged {
	if next.Timestamp.After(current.Timestamp) {
		current.EventHeader = next.EventHeader
	}
	if next.SkillsDir != "" {
		current.SkillsDir = next.SkillsDir
	}
	return current
}

func mergeSkillsChangedName(currentName, nextName string) string {
	if currentName == "" || nextName == "" || currentName != nextName {
		return ""
	}
	return currentName
}

func skillChangedActionSummary(actions []string) string {
	if len(actions) == 1 {
		return actions[0]
	}
	return ""
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
