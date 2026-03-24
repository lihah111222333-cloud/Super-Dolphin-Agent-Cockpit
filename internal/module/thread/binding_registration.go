package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

type bindingRegistration struct {
	AgentID          string
	Provider         string
	ProviderThreadID string
	PublicThreadID   string
	CWD              string
	CreatedAt        int64
}

type bindingWriteOutcome struct {
	AgentID   string
	Persisted bool
	Previous  *bindingstore.Binding
}

func normalizeThreadState(state threadState) (threadState, error) {
	state.PublicThreadID = strings.TrimSpace(state.PublicThreadID)
	state.ProviderThreadID = strings.TrimSpace(state.ProviderThreadID)
	state.OwnerThreadID = strings.TrimSpace(state.OwnerThreadID)
	state.AgentID = strings.TrimSpace(state.AgentID)
	state.Provider = strings.TrimSpace(state.Provider)
	state.CWD = strings.TrimSpace(state.CWD)
	state.Model = strings.TrimSpace(state.Model)
	state.Prompt = strings.TrimSpace(state.Prompt)
	if state.PublicThreadID == "" || state.AgentID == "" {
		return threadState{}, errors.New("thread and agent ids are required")
	}
	state.ProviderThreadID = resolveProviderThreadID(state.ProviderThreadID, state.PublicThreadID)
	if state.CreatedAt == 0 {
		state.CreatedAt = time.Now().Unix()
	}
	return state, nil
}

func normalizeBindingRegistration(state threadState) (bindingRegistration, error) {
	if state.AgentID == "" || state.Provider == "" || state.PublicThreadID == "" {
		return bindingRegistration{}, errors.New("binding requires agent, provider, and public thread ids")
	}
	return bindingRegistration{
		AgentID:          state.AgentID,
		Provider:         state.Provider,
		ProviderThreadID: resolveProviderThreadID(state.ProviderThreadID, state.PublicThreadID),
		PublicThreadID:   state.PublicThreadID,
		CWD:              state.CWD,
		CreatedAt:        state.CreatedAt,
	}, nil
}

func (s *service) ensurePublicThreadAvailable(ctx context.Context, state threadState) error {
	if s == nil || s.threadStore == nil {
		return nil
	}
	existing, err := s.threadStore.GetByThreadID(ctx, state.PublicThreadID)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return nil
		}
		return err
	}
	if existing == nil {
		return nil
	}
	existingAgentID := strings.TrimSpace(existing.AgentID)
	if existingAgentID == "" {
		return fmt.Errorf("public thread id %q already exists without a binding owner", state.PublicThreadID)
	}
	if existingAgentID != state.AgentID {
		return fmt.Errorf("public thread id %q is already bound to agent %q", state.PublicThreadID, existingAgentID)
	}
	return nil
}

func (s *service) registerThreadBinding(ctx context.Context, state threadState) (bindingWriteOutcome, error) {
	if s == nil || s.bindingStore == nil {
		return bindingWriteOutcome{}, nil
	}
	registration, err := normalizeBindingRegistration(state)
	if err != nil {
		return bindingWriteOutcome{}, err
	}
	if err := s.ensureProviderThreadAvailable(ctx, registration); err != nil {
		return bindingWriteOutcome{}, err
	}
	existing, err := s.bindingStore.GetByAgentID(ctx, registration.AgentID)
	if err != nil && !platformdb.IsNotFound(err) {
		return bindingWriteOutcome{}, err
	}
	outcome := bindingWriteOutcome{
		AgentID:  registration.AgentID,
		Previous: cloneBinding(existing),
	}
	if err := validateBindingRegistration(existing, registration); err != nil {
		return outcome, err
	}
	if !shouldPersistBinding(existing, registration) {
		return outcome, nil
	}
	if err := s.bindingStore.Upsert(ctx, bindingstore.UpsertParams{
		AgentID:          registration.AgentID,
		Provider:         registration.Provider,
		ProviderThreadID: registration.ProviderThreadID,
		CodexThreadID:    registration.PublicThreadID,
		Cwd:              registration.CWD,
		CreatedAt:        registration.CreatedAt,
		UpdatedAt:        time.Now().Unix(),
	}); err != nil {
		return outcome, err
	}
	outcome.Persisted = true
	if err := s.verifyThreadBinding(ctx, registration); err != nil {
		if rollbackErr := s.rollbackThreadBinding(ctx, outcome); rollbackErr != nil {
			return outcome, errors.Join(err, rollbackErr)
		}
		return outcome, err
	}
	return outcome, nil
}

func (s *service) ensureProviderThreadAvailable(ctx context.Context, registration bindingRegistration) error {
	if s == nil || s.bindingStore == nil {
		return nil
	}
	existing, err := s.bindingStore.GetByProviderThread(ctx, registration.Provider, registration.ProviderThreadID)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return nil
		}
		return err
	}
	if existing == nil {
		return nil
	}
	if existingAgentID := strings.TrimSpace(existing.AgentID); existingAgentID != registration.AgentID {
		return fmt.Errorf("provider thread %q is already bound to agent %q", registration.ProviderThreadID, existingAgentID)
	}
	return nil
}

func validateBindingRegistration(existing *bindingstore.Binding, registration bindingRegistration) error {
	if existing == nil {
		return nil
	}
	if provider := strings.TrimSpace(existing.Provider); provider != "" && provider != registration.Provider {
		return fmt.Errorf("agent %q is already bound to provider %q", registration.AgentID, provider)
	}
	if providerThreadID := strings.TrimSpace(existing.ProviderThreadID); providerThreadID != "" && providerThreadID != registration.ProviderThreadID {
		return fmt.Errorf("agent %q is already bound to provider thread %q", registration.AgentID, providerThreadID)
	}
	if publicThreadID := strings.TrimSpace(existing.CodexThreadID); publicThreadID != "" && publicThreadID != registration.PublicThreadID {
		return fmt.Errorf("agent %q is already bound to public thread %q", registration.AgentID, publicThreadID)
	}
	if cwd := strings.TrimSpace(existing.Cwd); cwd != "" && registration.CWD != "" && cwd != registration.CWD {
		return fmt.Errorf("agent %q binding cwd mismatch", registration.AgentID)
	}
	return nil
}

func shouldPersistBinding(existing *bindingstore.Binding, registration bindingRegistration) bool {
	if existing == nil {
		return true
	}
	if strings.TrimSpace(existing.CodexThreadID) == "" && registration.PublicThreadID != "" {
		return true
	}
	if strings.TrimSpace(existing.Cwd) == "" && registration.CWD != "" {
		return true
	}
	return false
}

func (s *service) verifyThreadBinding(ctx context.Context, registration bindingRegistration) error {
	if s == nil || s.bindingStore == nil {
		return nil
	}
	binding, err := s.bindingStore.GetByAgentID(ctx, registration.AgentID)
	if err != nil {
		return err
	}
	if binding == nil {
		return fmt.Errorf("binding verification failed for agent %q", registration.AgentID)
	}
	if strings.TrimSpace(binding.Provider) != registration.Provider {
		return fmt.Errorf("binding verification failed: provider mismatch for agent %q", registration.AgentID)
	}
	if strings.TrimSpace(binding.ProviderThreadID) != registration.ProviderThreadID {
		return fmt.Errorf("binding verification failed: provider thread mismatch for agent %q", registration.AgentID)
	}
	if strings.TrimSpace(binding.CodexThreadID) != registration.PublicThreadID {
		return fmt.Errorf("binding verification failed: public thread mismatch for agent %q", registration.AgentID)
	}
	if registration.CWD != "" && strings.TrimSpace(binding.Cwd) != registration.CWD {
		return fmt.Errorf("binding verification failed: cwd mismatch for agent %q", registration.AgentID)
	}
	return nil
}

func (s *service) rollbackThreadBinding(ctx context.Context, outcome bindingWriteOutcome) error {
	if s == nil || s.bindingStore == nil || !outcome.Persisted {
		return nil
	}
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	if outcome.Previous == nil {
		return s.bindingStore.DeleteByAgentID(ctx, outcome.AgentID)
	}
	return s.bindingStore.Upsert(ctx, bindingstore.UpsertParams{
		AgentID:          strings.TrimSpace(outcome.Previous.AgentID),
		Provider:         strings.TrimSpace(outcome.Previous.Provider),
		ProviderThreadID: strings.TrimSpace(outcome.Previous.ProviderThreadID),
		CodexThreadID:    strings.TrimSpace(outcome.Previous.CodexThreadID),
		RolloutPath:      strings.TrimSpace(outcome.Previous.RolloutPath),
		Cwd:              strings.TrimSpace(outcome.Previous.Cwd),
		CreatedAt:        outcome.Previous.CreatedAt,
		UpdatedAt:        time.Now().Unix(),
	})
}

func cloneBinding(binding *bindingstore.Binding) *bindingstore.Binding {
	if binding == nil {
		return nil
	}
	copy := *binding
	return &copy
}

func (s *service) lookupPersistedAgentID(ctx context.Context, threadID string) string {
	if s == nil || s.threadStore == nil {
		return ""
	}
	thread, err := s.threadStore.GetByThreadID(ctx, threadID)
	if err != nil || thread == nil {
		return ""
	}
	return strings.TrimSpace(thread.AgentID)
}
