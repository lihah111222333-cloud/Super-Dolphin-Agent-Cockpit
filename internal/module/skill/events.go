package skill

import (
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
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
		Action:      strings.TrimSpace(action),
	})
}

func (s *service) scheduleSkillsChanged(next uidto.SkillsChanged) {
	s.skillsChangedMu.Lock()
	s.skillsChangedNext = next
	s.skillsChangedSeq++
	seq := s.skillsChangedSeq
	s.skillsChangedMu.Unlock()

	go func() {
		time.Sleep(skillsChangedDebounceWindow)
		s.flushSkillsChanged(seq)
	}()
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
