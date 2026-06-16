package skill

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

type skillsChangedEmitter func(uidto.SkillsChanged)

const skillsChangedDebounceWindow = 100 * time.Millisecond

func (s *service) bindDispatcher(dispatcher *event.Dispatcher) {
	if s == nil {
		return
	}
	s.emitSkillsChanged = contract.NewEmitter[uidto.SkillsChanged](dispatcher)
}

func (s *service) publishSkillsChanged(ctx context.Context, action, name, scope string) {
	s.publishSkillsChangedForPersonalType(ctx, action, name, scope, "")
}

func (s *service) publishSkillsChangedForPersonalType(ctx context.Context, action, name, scope, personalType string) {
	if s == nil || s.emitSkillsChanged == nil {
		return
	}
	normalizedScope := strings.TrimSpace(scope)
	normalizedPersonalType := strings.TrimSpace(personalType)
	repoFingerprint, relativePath := s.skillsChangedLocation(ctx, normalizedScope)
	s.scheduleSkillsChanged(uidto.SkillsChanged{
		EventHeader:     shared.EventHeader{Timestamp: time.Now()},
		Name:            strings.TrimSpace(name),
		Action:          normalizeSkillsChangedAction(action),
		Count:           1,
		Scope:           normalizedScope,
		PersonalType:    normalizedPersonalType,
		RepoFingerprint: repoFingerprint,
		RelativePath:    relativePath,
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

	// Bounded lifetime: goroutine sleeps for skillsChangedDebounceWindow (100ms)
	// then performs a non-blocking flush. Total duration ~100ms; no lifecycle ctx needed.
	safego.Go(context.Background(), pkglogger.Get(), "skill.scheduleSkillsChangedFlush", func(context.Context) {
		s.waitSkillsChangedDebounce()
		s.flushSkillsChanged(seq)
	})
}

func (s *service) waitSkillsChangedDebounce() {
	if s != nil && s.skillsChangedDelay != nil {
		s.skillsChangedDelay()
		return
	}
	time.Sleep(skillsChangedDebounceWindow)
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

// skillsChangedLocation 处理skillschanged位置。
func (s *service) skillsChangedLocation(ctx context.Context, scope string) (string, string) {
	if scope != skillScopeProject {
		return "", ""
	}
	cwd := cwdFromContext(ctx)
	projectRoot := s.projectRootForCWD(cwd)
	fp := RepoFingerprint(projectRoot)
	if fp == "" {
		return "", ""
	}
	canonicalRoot, rootErr := canonicalProjectPath(projectRoot)
	canonicalCWD, cwdErr := canonicalProjectPath(cwd)
	if rootErr != nil || cwdErr != nil {
		return fp, "."
	}
	rel, err := filepath.Rel(canonicalRoot, canonicalCWD)
	if err != nil || rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return fp, "."
	}
	return fp, rel
}

func normalizeSkillsChanged(next uidto.SkillsChanged) uidto.SkillsChanged {
	next.SkillsDir = strings.TrimSpace(next.SkillsDir)
	next.Name = strings.TrimSpace(next.Name)
	next.Scope = strings.TrimSpace(next.Scope)
	next.PersonalType = strings.TrimSpace(next.PersonalType)
	next.RepoFingerprint = strings.TrimSpace(next.RepoFingerprint)
	next.RelativePath = strings.TrimSpace(next.RelativePath)
	next.Cwd = ""
	if next.Scope != skillScopeProject {
		next.RepoFingerprint = ""
		next.RelativePath = ""
	}
	if next.Scope != skillScopePersonal {
		next.PersonalType = ""
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
	return current.Scope == next.Scope &&
		current.PersonalType == next.PersonalType &&
		current.RepoFingerprint == next.RepoFingerprint &&
		current.RelativePath == next.RelativePath
}

// mergeSkillsChangedMetadata 合并skillschanged元数据。
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
