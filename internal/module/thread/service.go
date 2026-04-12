package thread

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

const (
	statusArchived = "archived"
	statusCreated  = "created"
	statusStopped  = "stopped"
)

// SessionProvider narrows session lookup to keep thread module provider-neutral.
type SessionProvider interface {
	GetSession(agentID string) (contract.Session, error)
	RemoveSession(agentID string)
}

type providerThreadNameSetter interface {
	SetThreadName(ctx context.Context, threadID, name string) error
}

type service struct {
	logger        *slog.Logger
	threadStore   threadstore.Store
	bindingStore  bindingstore.Store
	sessions      SessionProvider
	starter       SessionStarter
	turns         turn.Service
	orchestration OrchestrationFacade
	bus           *event.Dispatcher

	emitStarted      func(threaddto.Started)
	emitStopped      func(threaddto.Stopped)
	emitUpdated      func(threaddto.Updated)
	emitMessagesPage func(threaddto.MessagesPage)
	emitCompacted    func(threaddto.Compacted)

	threadAgentsMu sync.RWMutex
	threadAgents   map[string]string

	// resumeInFlight tracks background resume attempts in progress.
	// Prevents stampede when multiple ReadMessages calls trigger
	// concurrent resume for the same agent.
	resumeInFlight sync.Map // agentID → struct{}
}

var _ Service = (*service)(nil)

func NewService(
	logger *slog.Logger,
	threadStore threadstore.Store,
	bindingStore bindingstore.Store,
	sessions SessionProvider,
	starter SessionStarter,
	turns turn.Service,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
) Service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	var dispatcher *event.Dispatcher
	if threadEvents != nil {
		dispatcher = threadEvents.Dispatcher()
	}
	return &service{
		logger:           logger,
		threadStore:      threadStore,
		bindingStore:     bindingStore,
		sessions:         sessions,
		starter:          starter,
		turns:            turns,
		orchestration:    orchestration,
		bus:              dispatcher,
		emitStarted:      bus.NewEmitter[threaddto.Started](dispatcher),
		emitStopped:      bus.NewEmitter[threaddto.Stopped](dispatcher),
		emitUpdated:      bus.NewEmitter[threaddto.Updated](dispatcher),
		emitMessagesPage: bus.NewEmitter[threaddto.MessagesPage](dispatcher),
		emitCompacted:    bus.NewEmitter[threaddto.Compacted](dispatcher),
		threadAgents:     make(map[string]string),
	}
}

func (s *service) List(ctx context.Context) ([]Ref, error) {
	return s.listThreads(ctx, nil)
}

func (s *service) Get(ctx context.Context, id string) (*Ref, error) {
	thread, err := s.getThread(ctx, id)
	if err != nil {
		return nil, err
	}
	ref := toRef(*thread)
	return &ref, nil
}

func (s *service) ListByStatus(ctx context.Context, status string) ([]Ref, error) {
	want := strings.TrimSpace(status)
	if want == "" {
		return s.List(ctx)
	}
	return s.listThreads(ctx, func(thread threadstore.Thread) bool {
		return strings.EqualFold(strings.TrimSpace(thread.Status), want)
	})
}

func (s *service) ListByCWD(ctx context.Context, cwdPrefix string) ([]Ref, error) {
	prefix := strings.TrimSpace(cwdPrefix)
	return s.listThreads(ctx, func(thread threadstore.Thread) bool {
		return prefix == "" || strings.HasPrefix(strings.TrimSpace(thread.Cwd), prefix)
	})
}

func (s *service) SetName(ctx context.Context, threadID, name string) error {
	thread, err := s.getThread(ctx, threadID)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	thread.Prompt = name
	thread.UpdatedAt = time.Now().Unix()
	if err := s.upsertThread(ctx, *thread); err != nil {
		return err
	}
	session, binding, err := s.resolveSession(ctx, threadID)
	if err != nil {
		return nil
	}
	syncer, ok := session.(providerThreadNameSetter)
	if !ok {
		return nil
	}
	// TODO(P8): promote provider-backed thread rename into the unified Session
	// contract once at least one provider exposes a stable rename surface.
	if err := syncer.SetThreadName(ctx, historyTargetID(binding, threadID), name); err != nil && s.logger != nil {
		s.logger.Warn("thread/name/set: provider sync failed", "thread_id", threadID, "error", err)
	}

	if s.emitUpdated != nil {
		s.emitUpdated(threaddto.Updated{
			EventHeader: shareddto.EventHeader{Timestamp: time.Now()},
			ThreadID:    threadID,
			Name:        name,
		})
	}

	return nil
}

func (s *service) Delete(ctx context.Context, threadID string) error {
	ctx = shared.NonNilContext(ctx)
	id, err := normalizeThreadID(threadID)
	if err != nil {
		return err
	}
	binding, _ := s.resolveBinding(ctx, id)
	stopState := newThreadStopState(binding, id)
	if binding != nil {
		if err := s.stopThreadRuntime(ctx, stopState, "thread_deleted", true); err != nil {
			return err
		}
	}
	if s.bindingStore != nil && binding != nil {
		if err := s.bindingStore.DeleteByAgentID(ctx, strings.TrimSpace(binding.AgentID)); err != nil {
			return err
		}
	}
	for _, targetID := range stopState.targets {
		s.forgetThreadAgent(targetID)
	}
	if s.threadStore == nil {
		return errors.New("thread store is not configured")
	}
	if err := s.threadStore.DeleteByThreadID(ctx, stopState.stoppedID); err != nil {
		return err
	}
	s.cleanupThreadTurns(ctx, "thread_deleted", stopState.targets...)
	s.publishThreadStopped(
		stopState.stoppedID,
		agentIDFromBinding(binding, stopState.stoppedID),
		"deleted",
		"deleted",
	)
	return nil
}

func (s *service) listThreads(ctx context.Context, filter func(threadstore.Thread) bool) ([]Ref, error) {
	if s.threadStore == nil {
		return nil, errors.New("thread store is not configured")
	}
	threads, err := s.threadStore.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Ref, 0, len(threads))
	for _, thread := range threads {
		if filter != nil && !filter(thread) {
			continue
		}
		result = append(result, toRef(thread))
	}
	return result, nil
}

func (s *service) getThread(ctx context.Context, threadID string) (*threadstore.Thread, error) {
	id, err := normalizeThreadID(threadID)
	if err != nil {
		return nil, err
	}
	if s.threadStore == nil {
		return nil, errors.New("thread store is not configured")
	}
	return s.threadStore.GetByThreadID(ctx, id)
}

func (s *service) upsertThread(ctx context.Context, thread threadstore.Thread) error {
	if s.threadStore == nil {
		return errors.New("thread store is not configured")
	}
	return s.threadStore.Upsert(ctx, newThreadUpsertParams(thread))
}

func (s *service) updateThreadStatus(ctx context.Context, threadID, status string) error {
	id, err := normalizeThreadID(threadID)
	if err != nil {
		return err
	}
	if s.threadStore == nil {
		return errors.New("thread store is not configured")
	}
	return s.threadStore.UpdateStatus(ctx, threadstore.UpdateStatusParams{
		ThreadID:  id,
		Status:    strings.TrimSpace(status),
		UpdatedAt: time.Now().Unix(),
	})
}

func (s *service) resolveBinding(ctx context.Context, threadID string) (*bindingstore.Binding, error) {
	id, err := normalizeThreadID(threadID)
	if err != nil {
		return nil, err
	}
	if s.bindingStore == nil {
		return nil, errors.New("binding store is not configured")
	}
	return s.resolveBindingChain(ctx, id)
}

func (s *service) resolveSession(ctx context.Context, threadID string) (contract.Session, *bindingstore.Binding, error) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return nil, nil, err
	}
	if s.sessions == nil {
		return nil, binding, errors.New("session provider is not configured")
	}
	session, err := s.sessions.GetSession(strings.TrimSpace(binding.AgentID))
	if err != nil {
		return nil, binding, err
	}
	return session, binding, nil
}

// backgroundResumeIfNeeded checks whether the thread has a stored binding
// (from a previous session) but no active session, and triggers a background
// Resume so the session is ready by the time the user sends a message.
func (s *service) backgroundResumeIfNeeded(ctx context.Context, threadID string) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil || binding == nil {
		return
	}
	agentID := strings.TrimSpace(binding.AgentID)
	if agentID == "" {
		return
	}
	// Check if session already exists.
	if s.sessions != nil {
		if sess, _ := s.sessions.GetSession(agentID); sess != nil {
			return
		}
	}
	// Prevent stampede: skip if a resume was already attempted for this agent.
	// The entry is never deleted — a failed resume stays marked so we don't
	// retry in an infinite loop and exhaust the DB connection pool.
	if _, loaded := s.resumeInFlight.LoadOrStore(agentID, struct{}{}); loaded {
		return
	}
	shared.SafeGo(s.logger, func() {
		if s.logger != nil {
			s.logger.Info("thread: background resume", "thread_id", threadID, "agent_id", agentID)
		}
		if _, err := s.Resume(context.Background(), ResumeRequest{ThreadID: threadID}); err != nil {
			shared.LogIgnoredError(s.logger, "thread: background resume failed", err)
			// Keep resumeInFlight entry to block further retries.
			return
		}
		// Only clear on success so subsequent ReadMessages can detect a live session.
		s.resumeInFlight.Delete(agentID)
	})
}

func (s *service) closeSessionIfActive(ctx context.Context, threadID string) error {
	if s.bindingStore == nil || s.sessions == nil {
		return nil
	}
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return nil
	}
	return s.closeSessionForAgent(ctx, binding.AgentID)
}

func (s *service) closeSessionForAgent(ctx context.Context, agentID string) error {
	if s.sessions == nil {
		return nil
	}
	session, err := s.sessions.GetSession(strings.TrimSpace(agentID))
	if err != nil {
		return nil
	}
	return session.Close(ctx)
}

func (s *service) setBindingArchived(ctx context.Context, threadID string, archived bool) error {
	if s.bindingStore == nil {
		return nil
	}
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return err
	}
	return s.bindingStore.SetArchived(ctx, bindingstore.SetArchivedParams{
		AgentID:   strings.TrimSpace(binding.AgentID),
		Archived:  archived,
		UpdatedAt: time.Now().Unix(),
	})
}

func (s *service) publishThreadStarted(state threadState) {
	if s == nil || s.emitStarted == nil {
		return
	}
	event := newThreadEvent(threadEventStartedKind, state.PublicThreadID, threadEventFields{State: state})
	if event == nil {
		return
	}
	s.emitStarted(event.(threaddto.Started))
}

func (s *service) publishThreadStopped(threadID, agentID, status, reason string) {
	if s == nil || s.emitStopped == nil {
		return
	}
	event := newThreadEvent(threadEventStoppedKind, threadID, threadEventFields{
		AgentID: agentID,
		Status:  status,
		Reason:  reason,
	})
	if event == nil {
		return
	}
	s.emitStopped(event.(threaddto.Stopped))
}

func (s *service) publishMessagesPage(threadID string, totalCount, pages int) {
	if s == nil || s.emitMessagesPage == nil {
		return
	}
	event := newThreadEvent(threadEventMessagesPageKind, threadID, threadEventFields{
		TotalCount: totalCount,
		Pages:      pages,
	})
	if event == nil {
		return
	}
	s.emitMessagesPage(event.(threaddto.MessagesPage))
}
