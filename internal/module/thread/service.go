package thread

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
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

	emitStarted      func(threaddto.Started)
	emitStopped      func(threaddto.Stopped)
	emitMessagesPage func(threaddto.MessagesPage)
	emitCompacted    func(threaddto.Compacted)

	threadAgentsMu sync.RWMutex
	threadAgents   map[string]string
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
		logger = slog.Default()
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
		emitStarted:      bus.NewEmitter[threaddto.Started](dispatcher),
		emitStopped:      bus.NewEmitter[threaddto.Stopped](dispatcher),
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
	return nil
}

func (s *service) Delete(ctx context.Context, threadID string) error {
	id, err := normalizeThreadID(threadID)
	if err != nil {
		return err
	}
	binding, _ := s.resolveBinding(ctx, id)
	_ = s.closeSessionIfActive(ctx, id)
	if s.bindingStore != nil && binding != nil {
		if err := s.bindingStore.DeleteByAgentID(ctx, strings.TrimSpace(binding.AgentID)); err != nil {
			return err
		}
	}
	s.forgetThreadAgent(id)
	if s.threadStore == nil {
		return errors.New("thread store is not configured")
	}
	if err := s.threadStore.DeleteByThreadID(ctx, id); err != nil {
		return err
	}
	s.publishThreadStopped(id, agentIDFromBinding(binding, id), "deleted", "deleted")
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
	return s.threadStore.Upsert(ctx, threadstore.UpsertParams{
		ThreadID:      thread.ThreadID,
		Prompt:        thread.Prompt,
		Model:         thread.Model,
		Cwd:           thread.Cwd,
		Status:        thread.Status,
		Port:          thread.Port,
		PID:           thread.PID,
		CreatedAt:     thread.CreatedAt,
		UpdatedAt:     thread.UpdatedAt,
		OwnerThreadID: thread.OwnerThreadID,
	})
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
	binding, err := s.bindingByAgentID(ctx, id)
	if binding != nil || err == nil {
		return binding, err
	}
	for _, lookup := range []func(context.Context, string) *bindingstore.Binding{
		s.bindingByPersistedThreadAgent,
		s.bindingByRememberedThreadAgent,
		s.bindingByProviderThreadID,
	} {
		if binding := lookup(ctx, id); binding != nil {
			return binding, nil
		}
	}
	return nil, err
}

func (s *service) bindingByAgentID(ctx context.Context, agentID string) (*bindingstore.Binding, error) {
	binding, err := s.bindingStore.GetByAgentID(ctx, agentID)
	if err == nil {
		s.rememberBinding(binding)
	}
	return binding, err
}

func (s *service) bindingByPersistedThreadAgent(ctx context.Context, threadID string) *bindingstore.Binding {
	agentID := s.lookupPersistedAgentID(ctx, threadID)
	return s.bindingByResolvedAgentID(ctx, threadID, agentID)
}

func (s *service) bindingByRememberedThreadAgent(ctx context.Context, threadID string) *bindingstore.Binding {
	agentID := s.lookupThreadAgent(threadID)
	return s.bindingByResolvedAgentID(ctx, threadID, agentID)
}

func (s *service) bindingByResolvedAgentID(ctx context.Context, threadID, agentID string) *bindingstore.Binding {
	if agentID == "" || agentID == threadID {
		return nil
	}
	binding, err := s.bindingByAgentID(ctx, agentID)
	if err != nil {
		return nil
	}
	return binding
}

func (s *service) bindingByProviderThreadID(ctx context.Context, threadID string) *bindingstore.Binding {
	for _, provider := range []string{"codex", "claude"} {
		binding, err := s.bindingStore.GetByProviderThread(ctx, provider, threadID)
		if err == nil {
			s.rememberBinding(binding)
			return binding
		}
	}
	return nil
}

func (s *service) resolveSession(ctx context.Context, threadID string) (contract.Session, *bindingstore.Binding, error) {
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return nil, nil, err
	}
	if s.sessions == nil {
		return nil, nil, errors.New("session provider is not configured")
	}
	session, err := s.sessions.GetSession(strings.TrimSpace(binding.AgentID))
	if err != nil {
		return nil, nil, err
	}
	return session, binding, nil
}

func (s *service) closeSessionIfActive(ctx context.Context, threadID string) error {
	if s.bindingStore == nil || s.sessions == nil {
		return nil
	}
	binding, err := s.resolveBinding(ctx, threadID)
	if err != nil {
		return nil
	}
	session, err := s.sessions.GetSession(strings.TrimSpace(binding.AgentID))
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

func historyTargetID(binding *bindingstore.Binding, threadID string) string {
	requestedID := strings.TrimSpace(threadID)
	if binding == nil {
		return requestedID
	}
	publicThreadID := strings.TrimSpace(binding.CodexThreadID)
	agentID := strings.TrimSpace(binding.AgentID)
	if requestedID != "" && requestedID != publicThreadID && requestedID != agentID {
		return requestedID
	}
	return firstNonEmpty(binding.ProviderThreadID, publicThreadID, agentID, requestedID)
}

func toRef(thread threadstore.Thread) Ref {
	name := strings.TrimSpace(thread.Prompt)
	if name == "" {
		name = strings.TrimSpace(thread.ThreadID)
	}
	return Ref{
		ID:      strings.TrimSpace(thread.ThreadID),
		Name:    name,
		AgentID: strings.TrimSpace(thread.AgentID),
	}
}

func normalizeThreadID(threadID string) (string, error) {
	id := strings.TrimSpace(threadID)
	if id == "" {
		return "", errors.New("thread id is required")
	}
	return id, nil
}

func (s *service) publishThreadStarted(state threadState) {
	if s == nil || s.emitStarted == nil {
		return
	}
	s.emitStarted(threaddto.Started{
		EventHeader:      shared.EventHeader{Timestamp: time.Now()},
		ThreadID:         strings.TrimSpace(state.PublicThreadID),
		AgentID:          strings.TrimSpace(state.AgentID),
		Provider:         strings.TrimSpace(state.Provider),
		ProviderThreadID: strings.TrimSpace(resolveProviderThreadID(state.ProviderThreadID, state.PublicThreadID)),
		CWD:              strings.TrimSpace(state.CWD),
		Model:            strings.TrimSpace(state.Model),
	})
}

func (s *service) publishThreadStopped(threadID, agentID, status, reason string) {
	if s == nil || s.emitStopped == nil {
		return
	}
	s.emitStopped(threaddto.Stopped{
		EventHeader: shared.EventHeader{Timestamp: time.Now()},
		ThreadID:    strings.TrimSpace(threadID),
		AgentID:     strings.TrimSpace(agentID),
		Status:      strings.TrimSpace(status),
		Reason:      strings.TrimSpace(reason),
	})
}

func (s *service) publishMessagesPage(threadID string, totalCount, pages int) {
	if s == nil || s.emitMessagesPage == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	s.emitMessagesPage(threaddto.MessagesPage{
		EventHeader: shared.EventHeader{Timestamp: time.Now()},
		ThreadID:    strings.TrimSpace(threadID),
		TotalCount:  totalCount,
		Pages:       pages,
	})
}

func agentIDFromBinding(binding *bindingstore.Binding, fallback string) string {
	if binding == nil {
		return strings.TrimSpace(fallback)
	}
	if agentID := strings.TrimSpace(binding.AgentID); agentID != "" {
		return agentID
	}
	return strings.TrimSpace(fallback)
}

func pageCount(totalCount, limit int) int {
	if totalCount <= 0 {
		return 0
	}
	if limit <= 0 || totalCount <= limit {
		return 1
	}
	pages := totalCount / limit
	if totalCount%limit != 0 {
		pages++
	}
	return pages
}
