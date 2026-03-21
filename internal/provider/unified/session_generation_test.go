package unified

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type generationTestSession struct {
	threadID string
	closed   bool
}

func (s *generationTestSession) ThreadID() string                               { return s.threadID }
func (s *generationTestSession) Capabilities() dto.CapabilitySet                { return nil }
func (s *generationTestSession) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, nil
}
func (s *generationTestSession) Interrupt(context.Context, dto.InterruptRequest) error { return nil }
func (s *generationTestSession) ForceComplete(context.Context, dto.ForceCompleteRequest) error {
	return nil
}
func (s *generationTestSession) ListThreads(context.Context) ([]dto.ThreadRef, error)   { return nil, nil }
func (s *generationTestSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}
func (s *generationTestSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}
func (s *generationTestSession) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }
func (s *generationTestSession) Close(context.Context) error {
	s.closed = true
	return nil
}
func (s *generationTestSession) ForceStop() error { return nil }

func TestSessionManagerRemoveMatchesGeneration(t *testing.T) {
	manager := NewSessionManager(nil)
	oldSession := &generationTestSession{threadID: "old"}
	newSession := &generationTestSession{threadID: "new"}

	oldGeneration := manager.Register("agent-1", oldSession)
	newGeneration := manager.Register("agent-1", newSession)
	manager.Remove("agent-1", oldGeneration)

	session, err := manager.Get("agent-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, ok := session.(*generationTestSession); !ok || got != newSession {
		t.Fatalf("Get() = %#v, want %#v", session, newSession)
	}
	if manager.SessionGeneration("agent-1") != newGeneration {
		t.Fatalf("SessionGeneration() = %d, want %d", manager.SessionGeneration("agent-1"), newGeneration)
	}
}
