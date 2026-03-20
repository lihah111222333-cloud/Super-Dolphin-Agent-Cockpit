package thread

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

const (
	statusArchived = "archived"
	statusCreated  = "created"
)

// SessionProvider narrows session lookup to keep thread module provider-neutral.
type SessionProvider interface {
	GetSession(agentID string) (contract.Session, error)
}

type service struct {
	logger        *slog.Logger
	threadStore   threadstore.Store
	bindingStore  bindingstore.Store
	sessions      SessionProvider
	starter       SessionStarter
	orchestration OrchestrationFacade

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
	orchestration OrchestrationFacade,
) Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &service{
		logger:        logger,
		threadStore:   threadStore,
		bindingStore:  bindingStore,
		sessions:      sessions,
		starter:       starter,
		orchestration: orchestration,
		threadAgents:  make(map[string]string),
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
	thread.Prompt = strings.TrimSpace(name)
	thread.UpdatedAt = time.Now().Unix()
	return s.upsertThread(ctx, *thread)
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
	return s.threadStore.DeleteByThreadID(ctx, id)
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
	binding, err := s.bindingStore.GetByAgentID(ctx, id)
	if err == nil {
		s.rememberBinding(binding)
		return binding, nil
	}
	if agentID := s.lookupThreadAgent(id); agentID != "" && agentID != id {
		binding, agentErr := s.bindingStore.GetByAgentID(ctx, agentID)
		if agentErr == nil {
			s.rememberBinding(binding)
			return binding, nil
		}
	}
	for _, provider := range []string{"codex", "claude"} {
		binding, providerErr := s.bindingStore.GetByProviderThread(ctx, provider, id)
		if providerErr == nil {
			s.rememberBinding(binding)
			return binding, nil
		}
	}
	return nil, err
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
	if binding == nil {
		return strings.TrimSpace(threadID)
	}
	for _, candidate := range []string{
		threadID,
		binding.ProviderThreadID,
		binding.CodexThreadID,
		binding.AgentID,
	} {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			return candidate
		}
	}
	return ""
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
