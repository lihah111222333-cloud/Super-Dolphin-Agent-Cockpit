package unified

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type generationTestSession struct {
	contract.Session
	threadID string
	stopped  bool
}

func (s *generationTestSession) ThreadID() string { return s.threadID }

func (s *generationTestSession) ForceStop() error {
	s.stopped = true
	return nil
}

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

func TestSessionManagerRemoveStaleGeneration(t *testing.T) {
	manager := NewSessionManager(nil)
	s := &generationTestSession{threadID: "active"}

	generation := manager.Register("agent-1", s)

	// 使用不匹配的陈旧代际试图移除
	manager.Remove("agent-1", generation+99)

	// 验证 session 依然安全存在
	session, err := manager.Get("agent-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got, ok := session.(*generationTestSession); !ok || got != s {
		t.Fatalf("Get() = %#v, want %#v", session, s)
	}
	if manager.SessionGeneration("agent-1") != generation {
		t.Fatalf("SessionGeneration() = %d, want %d", manager.SessionGeneration("agent-1"), generation)
	}
}

func TestSessionManagerRegisterConflict(t *testing.T) {
	manager := NewSessionManager(nil)
	oldSession := &generationTestSession{threadID: "old"}
	newSession := &generationTestSession{threadID: "new"}

	manager.Register("agent-1", oldSession)
	newGeneration := manager.Register("agent-1", newSession)

	// 确认旧 session 在注册冲突时已被强制停止
	if !oldSession.stopped {
		t.Fatalf("expected old session to be stopped on register conflict")
	}

	// 确认新 session 正式就绪
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
