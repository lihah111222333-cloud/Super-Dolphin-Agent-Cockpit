package directpeer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestDirectPeerProjectorBatchRecoveryMatrix 覆盖批次首中尾的sink失败、ACK事务失败与确认前取消。
func TestDirectPeerProjectorBatchRecoveryMatrix(t *testing.T) {
	for _, fault := range []string{"sink", "ack", "cancel"} {
		for position := range 3 {
			t.Run(fmt.Sprintf("%s/position-%d", fault, position), func(t *testing.T) {
				runPeerProjectorBatchRecovery(t, fault, position)
			})
		}
	}
}

// peerProjectorBatchFixture 创建三个有确定持久顺序的独立事件。
func peerProjectorBatchFixture(t *testing.T) (*sql.DB, *Store, []string) {
	t.Helper()
	db, store := newDirectPeerOutboxFixture(t, t.Context())
	for i := range 2 {
		if _, err := store.GrantPeer(t.Context(), t.TempDir(), "sender", "receiver", fmt.Sprintf("grant-%d", i), true, true); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.Query("SELECT event_id FROM orchestration_direct_peer_event_outbox ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Fatalf("events=%d", len(ids))
	}
	return db, store, ids
}

// runPeerProjectorBatchRecovery 断言已ACK前缀不重放，失败点重放同ID，尚未调用后缀继续投影。
func runPeerProjectorBatchRecovery(t *testing.T, fault string, position int) {
	t.Helper()
	db, store, ids := peerProjectorBatchFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := map[string]int{}
	injected := errors.New("injected sink failure")
	installPeerProjectorACKFault(t, db, fault, ids[position])
	sink := peerProjectorFaultSink(fault, ids[position], calls, injected, cancel)
	projector, err := NewEventProjector(store, sink, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = projector.Process(ctx, time.Minute, 3)
	assertPeerProjectorFault(t, fault, err, injected)
	assertPeerProjectorBatchState(t, db, ids, position)
	for i, id := range ids {
		want := 0
		if i <= position {
			want = 1
		}
		if calls[id] != want {
			t.Fatalf("before recovery index=%d calls=%d want=%d", i, calls[id], want)
		}
	}
	recoverPeerProjectorBatch(t, db, fault, ids, position, calls)
}

// recoverPeerProjectorBatch 清除注入故障后重建owner，验证只有未确认事件重放。
func recoverPeerProjectorBatch(t *testing.T, db *sql.DB, fault string, ids []string, position int, calls map[string]int) {
	t.Helper()
	if fault == "ack" {
		if _, err := db.Exec("DROP TRIGGER fail_projector_ack"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("UPDATE orchestration_direct_peer_event_outbox SET lease_expires_at=1 WHERE status='claimed'"); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	successor, err := NewEventProjector(reopened, func(_ context.Context, event Event) error { calls[event.EventID]++; return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := successor.Process(t.Context(), time.Minute, 3); err != nil {
			t.Fatal(err)
		}
	}
	assertPeerProjectorBatchState(t, db, ids, 3)
	assertPeerProjectorRecoveredCalls(t, ids, calls, position)
}

// assertPeerProjectorRecoveredCalls 只允许未确认故障点重复通知，其余事件恰好一次。
func assertPeerProjectorRecoveredCalls(t *testing.T, ids []string, calls map[string]int, position int) {
	t.Helper()
	for i, id := range ids {
		want := 1
		if i == position {
			want = 2
		}
		if calls[id] != want {
			t.Fatalf("after recovery index=%d calls=%d want=%d", i, calls[id], want)
		}
	}
}

// installPeerProjectorACKFault 用真实SQLite触发器阻断指定事件ACK，不影响领取与其他事件。
func installPeerProjectorACKFault(t *testing.T, db *sql.DB, fault, eventID string) {
	t.Helper()
	if fault != "ack" {
		return
	}
	// eventID来自生产生成器；仍按SQL字符串规则转义，避免测试依赖隐式字符假设。
	quoted := "'" + strings.ReplaceAll(eventID, "'", "''") + "'"
	statement := "CREATE TRIGGER fail_projector_ack BEFORE UPDATE ON orchestration_direct_peer_event_outbox WHEN NEW.status='projected' AND NEW.event_id=" + quoted + " BEGIN SELECT RAISE(ABORT, 'injected ack failure'); END"
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

// assertPeerProjectorFault 区分实际注入错误，避免无关数据库失败假阳性。
func assertPeerProjectorFault(t *testing.T, fault string, err, injected error) {
	t.Helper()
	if err == nil {
		t.Fatal("fault was swallowed")
	}
	switch fault {
	case "sink":
		if !errors.Is(err, injected) {
			t.Fatalf("sink error=%v", err)
		}
	case "cancel":
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	case "ack":
		if !strings.Contains(err.Error(), "injected ack failure") {
			t.Fatalf("ack error=%v", err)
		}
	}
}

// assertPeerProjectorBatchState 精确核对每个事件的持久状态，而不是仅比较总数。
func assertPeerProjectorBatchState(t *testing.T, db *sql.DB, ids []string, projected int) {
	t.Helper()
	for i, id := range ids {
		var status string
		if err := db.QueryRow("SELECT status FROM orchestration_direct_peer_event_outbox WHERE event_id=?", id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		want := "claimed"
		if i < projected {
			want = "projected"
		}
		if status != want {
			t.Fatalf("event index=%d status=%s want=%s", i, status, want)
		}
	}
}

// peerProjectorFaultSink 在指定事件的副作用之后注入失败或取消，其他事件正常返回。
func peerProjectorFaultSink(fault, target string, calls map[string]int, injected error, cancel context.CancelFunc) EventSink {
	return func(_ context.Context, event Event) error {
		calls[event.EventID]++
		if event.EventID != target {
			return nil
		}
		if fault == "sink" {
			return injected
		}
		if fault == "cancel" {
			cancel()
		}
		return nil
	}
}
