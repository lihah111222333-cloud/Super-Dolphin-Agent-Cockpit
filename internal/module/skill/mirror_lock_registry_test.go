package skill

import (
	"context"
	"testing"
	"time"
)

// TestMirrorRootLockRegistrySerializesSameRootAndReclaims 验证同 root 串行并在最后释放后回收 entry。
func TestMirrorRootLockRegistrySerializesSameRootAndReclaims(t *testing.T) {
	locks := NewMirrorRootLockRegistry()
	firstUnlock := locks.lock("/tmp/skill-mirror-root")

	acquired := make(chan struct{})
	released := make(chan struct{})
	go func() {
		unlock := locks.lock("/tmp/skill-mirror-root")
		close(acquired)
		unlock()
		close(released)
	}()

	deadline := time.After(time.Second)
	for {
		locks.mu.Lock()
		entry := locks.roots["/tmp/skill-mirror-root"]
		refs := 0
		if entry != nil {
			refs = entry.refs
		}
		locks.mu.Unlock()
		if refs == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("second same-root lock did not register as waiting")
		case <-time.After(time.Millisecond):
		}
	}

	select {
	case <-acquired:
		t.Fatal("same-root lock acquired before first holder released")
	default:
	}

	firstUnlock()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("waiting same-root lock did not finish after release")
	}
	if got := locks.entryCount(); got != 0 {
		t.Fatalf("root entries after final unlock = %d, want 0", got)
	}
}

// TestPublishSkillMirrorsRequiresExplicitLockOwner 验证发布入口缺 owner 时立即阻断。
func TestPublishSkillMirrorsRequiresExplicitLockOwner(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("PublishSkillMirrors without lock owner must panic")
		}
	}()
	PublishSkillMirrors(nil, context.Background(), nil, nil)
}

// TestCleanupSuppressedPersonalMirrorRequiresExplicitLockOwner 验证 cleanup 路径同样不接受隐式 owner。
func TestCleanupSuppressedPersonalMirrorRequiresExplicitLockOwner(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("cleanup without lock owner must panic")
		}
	}()
	_, _, _ = cleanupSuppressedPersonalMirrorRecord(nil, SkillMirrorTarget{Root: t.TempDir()}, canonicalSkillRecord{})
}

// TestNewServiceRequiresExplicitMirrorLockOwner 验证 service 构造期不接受缺失的锁 owner。
func TestNewServiceRequiresExplicitMirrorLockOwner(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewService without lock owner must panic")
		}
	}()
	NewService(t.TempDir(), testSkillMetrics(t), nil)
}
