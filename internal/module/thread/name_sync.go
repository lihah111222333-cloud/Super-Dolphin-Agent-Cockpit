package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

type providerThreadNameSetter interface {
	SetThreadName(ctx context.Context, threadID, name string) error
}

func (s *service) syncProviderThreadName(ctx context.Context, threadID, agentID, name string) error {
	session, active, err := s.activeProviderThreadNameSession(agentID)
	if err != nil || !active {
		return err
	}
	syncer, ok := session.(providerThreadNameSetter)
	if !ok {
		return nil
	}
	targetID, err := s.providerThreadNameTargetID(ctx, threadID, agentID)
	if err != nil {
		return err
	}
	if err := syncer.SetThreadName(ctx, targetID, name); err != nil {
		return fmt.Errorf("thread/name/set: provider rename failed: %w", err)
	}
	return nil
}

func (s *service) activeProviderThreadNameSession(agentID string) (contract.Session, bool, error) {
	agentID = strings.TrimSpace(agentID)
	if s == nil || s.sessions == nil || agentID == "" {
		return nil, false, nil
	}
	session, err := s.sessions.GetSession(agentID)
	switch {
	case err == nil && session != nil:
		return session, true, nil
	case err == nil:
		return nil, false, nil
	case errors.Is(err, contract.ErrSessionNotFound):
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("thread/name/set: provider session lookup failed: %w", err)
	}
}

func (s *service) providerThreadNameTargetID(ctx context.Context, threadID, agentID string) (string, error) {
	binding, err := s.providerThreadNameBinding(ctx, agentID)
	if err != nil {
		return "", err
	}
	return historyTargetID(binding, threadID), nil
}

func (s *service) providerThreadNameBinding(ctx context.Context, agentID string) (*bindingstore.Binding, error) {
	if s == nil || s.bindingStore == nil {
		return nil, errors.New("thread/name/set: binding store is not configured")
	}
	binding, err := s.bindingStore.GetByAgentID(ctx, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("thread/name/set: provider binding lookup failed: %w", err)
	}
	return binding, nil
}
