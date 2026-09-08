//go:build e2e

package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
)

// TestStandaloneDirectPeerOutboxWorkerTakeover 验证真实SQLite双projector租约接管，不使用provider或OS崩溃。
func TestStandaloneDirectPeerOutboxWorkerTakeover(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "active-lease"
		if expired {
			name = "expired-lease"
		}
		t.Run(name, func(t *testing.T) { assertOutboxWorkerTakeover(t, expired) })
	}
}

type outboxTakeoverNotifications struct {
	mu  sync.Mutex
	ids []string
}

// record 在两个sink可能重叠期间安全记录每次通知，不去重掩盖至少一次投递。
func (n *outboxTakeoverNotifications) record(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ids = append(n.ids, id)
}

// assertIDs 检查所有通知均绑定唯一事件并保持预期次数。
func (n *outboxTakeoverNotifications) assertIDs(t *testing.T, id string, want int) {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.ids) != want {
		t.Fatalf("notifications=%d want=%d", len(n.ids), want)
	}
	for _, got := range n.ids {
		if got != id {
			t.Fatal("worker notified a different EventID")
		}
	}
}

// assertOutboxWorkerTakeover 将旧sink停在返回前，生产projector的ACK必须服从当前租约围栏。
func assertOutboxWorkerTakeover(t *testing.T, expired bool) {
	t.Helper()
	f := newPeerCrashFixture(t, t.TempDir(), &gatedPeerNativeSubmitter{})
	if _, err := f.store.GrantPeer(f.ctx, f.workspace, "sender", "receiver", "grant", true, true); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan string, 1), make(chan struct{})
	var notifications outboxTakeoverNotifications
	old := newTakeoverProjector(t, f, func(ctx context.Context, event directpeer.Event) error {
		notifications.record(event.EventID)
		entered <- event.EventID
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	successor := newTakeoverProjector(t, f, func(_ context.Context, event directpeer.Event) error {
		notifications.record(event.EventID)
		return nil
	})
	completed := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() { completed <- old.Process(f.ctx, time.Minute, 1) })
	t.Cleanup(func() { releaseTakeoverWorker(release, &workers) })
	var eventID string
	select {
	case eventID = <-entered:
	case <-f.ctx.Done():
		t.Fatal("old worker did not reach sink")
	}
	assertTakeoverStatus(t, f, eventID, "claimed")
	expected := runTakeoverSuccessor(t, f, successor, eventID, expired)
	notifications.assertIDs(t, eventID, expected)
	close(release)
	select {
	case err := <-completed:
		assertTakeoverOldResult(t, err, expired)
	case <-f.ctx.Done():
		t.Fatal("old worker did not finish ACK")
	}
	assertTakeoverStatus(t, f, eventID, "projected")
	if err := successor.Process(f.ctx, time.Minute, 1); err != nil {
		t.Fatal(err)
	}
	if err := old.Process(f.ctx, time.Minute, 1); err != nil {
		t.Fatal(err)
	}
	notifications.assertIDs(t, eventID, expected)
}

// newTakeoverProjector 为同一真实store创建独立生产worker身份。
func newTakeoverProjector(t *testing.T, f peerFaultFixture, sink directpeer.EventSink) *directpeer.EventProjector {
	t.Helper()
	projector, err := directpeer.NewEventProjector(f.store, sink, nil)
	if err != nil {
		t.Fatal(err)
	}
	return projector
}

// expireTakeoverLease 仅对闸门内已领取事件注入确定性到期，不等待墙钟或修改生产逻辑。
func expireTakeoverLease(t *testing.T, f peerFaultFixture, eventID string) {
	t.Helper()
	result, err := f.db.ExecContext(f.ctx, "UPDATE orchestration_direct_peer_event_outbox SET lease_expires_at=1 WHERE event_id=? AND status='claimed'", eventID)
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		t.Fatalf("expiry fault changed=%d err=%v", count, err)
	}
}

// assertTakeoverStatus 核对迟到旧ACK不能回滚或替换已完成的projected状态。
func assertTakeoverStatus(t *testing.T, f peerFaultFixture, eventID, want string) {
	t.Helper()
	var status string
	if err := f.db.QueryRowContext(f.ctx, "SELECT status FROM orchestration_direct_peer_event_outbox WHERE event_id=?", eventID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != want {
		t.Fatalf("outbox status=%s want=%s", status, want)
	}
}

// assertTakeoverOldResult 到期接管后旧worker必须显式围栏失败，未到期则正常ACK。
func assertTakeoverOldResult(t *testing.T, err error, expired bool) {
	t.Helper()
	if expired {
		if !errors.Is(err, directpeer.ErrEventOutboxFence) {
			t.Fatalf("old ACK error=%v want fence", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("valid old worker ACK failed: %v", err)
	}
}

// runTakeoverSuccessor 核对新worker在有效租约时不偷领，到期后完成接管。
func runTakeoverSuccessor(t *testing.T, f peerFaultFixture, successor *directpeer.EventProjector, eventID string, expired bool) int {
	t.Helper()
	if expired {
		expireTakeoverLease(t, f, eventID)
	}
	if err := successor.Process(f.ctx, time.Minute, 1); err != nil {
		t.Fatal(err)
	}
	expected := 1
	if expired {
		expected = 2
		assertTakeoverStatus(t, f, eventID, "projected")
	} else {
		assertTakeoverStatus(t, f, eventID, "claimed")
	}
	return expected
}

// releaseTakeoverWorker 保证失败路径释放sink并等待托管worker退出。
func releaseTakeoverWorker(release chan struct{}, workers *sync.WaitGroup) {
	select {
	case <-release:
	default:
		close(release)
	}
	workers.Wait()
}
