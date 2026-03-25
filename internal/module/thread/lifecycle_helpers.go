package thread

import (
	"context"
	"errors"
	"strings"
	"time"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func resolveProviderThreadID(threadID, fallback string) string {
	return firstNonEmpty(threadID, fallback)
}

func bindingPublicThreadID(binding *bindingstore.Binding, fallback string) string {
	if binding == nil {
		return strings.TrimSpace(fallback)
	}
	return firstNonEmpty(binding.CodexThreadID, fallback)
}

func normalizeThreadContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return time.Now().Unix()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (s *service) maybeRegisterThreadBinding(
	ctx context.Context,
	state threadState,
	updateBinding bool,
) (bindingWriteOutcome, error) {
	if !updateBinding || s.bindingStore == nil {
		return bindingWriteOutcome{}, nil
	}
	return s.registerThreadBinding(ctx, state)
}

func (s *service) persistStartedThread(
	ctx context.Context,
	state threadState,
	bindingOutcome bindingWriteOutcome,
) error {
	if err := s.upsertPublicThread(ctx, state, bindingOutcome); err != nil {
		return err
	}
	s.rememberStartedThread(state)
	s.publishThreadStarted(state)
	return nil
}

func (s *service) upsertPublicThread(
	ctx context.Context,
	state threadState,
	bindingOutcome bindingWriteOutcome,
) error {
	if s.threadStore == nil {
		return nil
	}
	err := s.threadStore.Upsert(ctx, threadstore.UpsertParams{
		ThreadID:      state.PublicThreadID,
		Prompt:        state.Prompt,
		Model:         state.Model,
		Cwd:           state.CWD,
		Status:        statusCreated,
		CreatedAt:     state.CreatedAt,
		UpdatedAt:     time.Now().Unix(),
		OwnerThreadID: state.OwnerThreadID,
	})
	if err == nil {
		return nil
	}
	if rollbackErr := s.rollbackThreadBinding(ctx, bindingOutcome); rollbackErr != nil {
		return errors.Join(err, rollbackErr)
	}
	return err
}

func (s *service) rememberStartedThread(state threadState) {
	s.rememberThreadAgent(state.PublicThreadID, state.AgentID)
	s.rememberThreadAgent(state.ProviderThreadID, state.AgentID)
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
	return Ref{ID: strings.TrimSpace(thread.ThreadID), Name: name, AgentID: strings.TrimSpace(thread.AgentID)}
}

func normalizeThreadID(threadID string) (string, error) {
	id := strings.TrimSpace(threadID)
	if id == "" {
		return "", errors.New("thread id is required")
	}
	return id, nil
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
