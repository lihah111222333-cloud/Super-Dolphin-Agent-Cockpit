package directpeer

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

// TestDirectPeerOutboxRenewalBoundaries 验证真实SQLite租约续期的worker/token/到期围栏。
func TestDirectPeerOutboxRenewalBoundaries(t *testing.T) {
	ctx := t.Context()
	db, store := newDirectPeerOutboxFixture(t, ctx)
	item := claimDirectPeerOutbox(t, ctx, store, "owner")
	for _, tc := range []struct{ worker, token string }{{"other", item.ClaimToken}, {"owner", "wrong-token"}} {
		if _, err := store.RenewDirectPeerEventOutbox(ctx, item.ID, tc.worker, tc.token, time.Minute); !errors.Is(err, ErrEventOutboxFence) {
			t.Fatalf("invalid renewal fence: %v", err)
		}
	}
	renewed, err := store.RenewDirectPeerEventOutbox(ctx, item.ID, "owner", item.ClaimToken, 2*time.Minute)
	if err != nil || !renewed.After(item.LeaseExpiresAt) {
		t.Fatalf("legal renewal did not extend lease: %v", err)
	}
	var persisted int64
	if err := db.QueryRowContext(ctx, "SELECT lease_expires_at FROM orchestration_direct_peer_event_outbox WHERE id=?", item.ID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != renewed.UnixMilli() {
		t.Fatal("renewal result differs from durable lease")
	}
	expireDirectPeerOutbox(t, ctx, db, item.ID)
	if _, err := store.RenewDirectPeerEventOutbox(ctx, item.ID, "owner", item.ClaimToken, time.Minute); !errors.Is(err, ErrEventOutboxFence) {
		t.Fatalf("expired renewal accepted: %v", err)
	}
	if err := store.MarkDirectPeerEventOutboxProjected(ctx, item.ID, "owner", item.ClaimToken); !errors.Is(err, ErrEventOutboxFence) {
		t.Fatalf("expired ACK accepted: %v", err)
	}
}

// TestDirectPeerOutboxPoisonContinuesHealthyEvents 真实损坏持久事件被隔离，后续正常事件仍可领取与确认。
func TestDirectPeerOutboxPoisonContinuesHealthyEvents(t *testing.T) {
	ctx := t.Context()
	db, store := newDirectPeerOutboxFixture(t, ctx)
	corruptDirectPeerOutboxJSON(t, db)
	good, err := store.GrantPeer(ctx, t.TempDir(), "sender", "receiver", "healthy", true, true)
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.ClaimDirectPeerEventOutbox(ctx, "worker", time.Minute, 200)
	if err != nil || len(items) != 1 {
		t.Fatalf("poison blocked healthy event: count=%d err=%v", len(items), err)
	}
	if items[0].Event.GrantID != good.GrantID {
		t.Fatal("claimed corrupt or unrelated event")
	}
	assertDirectPeerPoisonRecorded(t, db)
	if err := store.MarkDirectPeerEventOutboxProjected(ctx, items[0].ID, "worker", items[0].ClaimToken); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.ClaimDirectPeerEventOutbox(ctx, "next", time.Minute, 200)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("poison/projected event reclaimed: %v %v", remaining, err)
	}
}

// corruptDirectPeerOutboxJSON 仅在测试独占连接临时关闭CHECK以模拟磁盘损坏，恢复约束后交回生产owner。
func corruptDirectPeerOutboxJSON(t *testing.T, db *sql.DB) {
	t.Helper()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := conn.ExecContext(t.Context(), "PRAGMA ignore_check_constraints=ON"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.ExecContext(t.Context(), "PRAGMA ignore_check_constraints=OFF"); err != nil {
			t.Error(err)
		}
	}()
	if _, err := conn.ExecContext(t.Context(), "UPDATE orchestration_direct_peer_events SET payload_json='{'"); err != nil {
		t.Fatal(err)
	}
}

// assertDirectPeerPoisonRecorded 核对损坏记录被带诊断地隔离而非静默丢弃。
func assertDirectPeerPoisonRecorded(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := t.Context()
	var poisoned int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM orchestration_direct_peer_event_outbox WHERE status='poisoned' AND last_error<>''").Scan(&poisoned); err != nil {
		t.Fatal(err)
	}
	if poisoned != 1 {
		t.Fatalf("poison record count=%d", poisoned)
	}
}
