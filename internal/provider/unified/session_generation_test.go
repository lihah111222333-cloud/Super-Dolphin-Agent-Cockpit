package unified

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type generationTestSession struct {
	contract.Session
	threadID string
}

func (s *generationTestSession) ThreadID() string { return s.threadID }

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
