package app

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type fakeSessionLifecyclePort struct{}

func (fakeSessionLifecyclePort) StartSession(context.Context, contract.SessionStartRequest) (contract.SessionStartResult, error) {
	return contract.SessionStartResult{}, nil
}

func (fakeSessionLifecyclePort) ResumeSession(context.Context, string) (contract.SessionStartResult, error) {
	return contract.SessionStartResult{}, nil
}

func (fakeSessionLifecyclePort) ForkSession(context.Context, string) (contract.SessionStartResult, error) {
	return contract.SessionStartResult{}, nil
}

func (fakeSessionLifecyclePort) ArchiveSession(context.Context, string) error {
	return nil
}

type fakeSessionStatusPort struct{}

func (fakeSessionStatusPort) ListSessions(context.Context) ([]contract.SessionThreadSummary, error) {
	return nil, nil
}

func (fakeSessionStatusPort) ReadMessages(context.Context, string, int, string) (dto.ThreadMessagesResult, error) {
	return dto.ThreadMessagesResult{}, nil
}

func TestNewSessionPortsImplementsContract(t *testing.T) {
	var _ contract.SessionPorts = newSessionPorts(fakeSessionLifecyclePort{}, fakeSessionStatusPort{})
}
