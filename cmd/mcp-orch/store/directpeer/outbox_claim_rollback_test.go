package directpeer

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type peerClaimSnapshot struct {
	EventID, Status string
	Worker, Token   sql.NullString
	Lease           sql.NullInt64
	Attempts        int64
}

// TestDirectPeerClaimBatchRollback 覆盖pending初领与expired接管在首中尾写入失败时的整批事务回滚。
func TestDirectPeerClaimBatchRollback(t *testing.T) {
	for _, expired := range []bool{false, true} {
		for position := range 3 {
			t.Run(fmt.Sprintf("expired-%t/position-%d", expired, position), func(t *testing.T) {
				runPeerClaimRollback(t, expired, position)
			})
		}
	}
}

// runPeerClaimRollback 核对失败后无部分claim或sink调用，移除故障后原事件全部可恢复。
func runPeerClaimRollback(t *testing.T, expired bool, position int) {
	t.Helper()
	db, store, ids := peerProjectorBatchFixture(t)
	attempts := preparePeerClaimState(t, db, store, expired)
	before := readPeerClaimSnapshots(t, db)
	installPeerClaimWriteFailure(t, db, ids[position])
	calls := map[string]int{}
	sink := func(_ context.Context, event Event) error { calls[event.EventID]++; return nil }
	projector, err := NewEventProjector(store, sink, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = projector.Process(t.Context(), time.Minute, 3)
	if err == nil || !strings.Contains(err.Error(), "injected claim failure") {
		t.Fatalf("claim failure=%v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("partial claim reached sink: %v", calls)
	}
	if after := readPeerClaimSnapshots(t, db); !reflect.DeepEqual(before, after) {
		t.Fatalf("claim rollback changed durable rows: before=%+v after=%+v", before, after)
	}
	if _, err := db.Exec("DROP TRIGGER fail_peer_claim"); err != nil {
		t.Fatal(err)
	}
	verifyPeerClaimRecovery(t, db, ids, calls, sink, attempts)
}

// installPeerClaimWriteFailure 仅拦截指定事件的claim更新，其他SQL仍执行生产约束。
func installPeerClaimWriteFailure(t *testing.T, db *sql.DB, eventID string) {
	t.Helper()
	quoted := "'" + strings.ReplaceAll(eventID, "'", "''") + "'"
	statement := "CREATE TRIGGER fail_peer_claim BEFORE UPDATE ON orchestration_direct_peer_event_outbox WHEN NEW.status='claimed' AND NEW.event_id=" + quoted + " BEGIN SELECT RAISE(ABORT, 'injected claim failure'); END"
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

// readPeerClaimSnapshots 读取全部租约围栏字段和尝试次数，避免只看status遗漏部分写入。
func readPeerClaimSnapshots(t *testing.T, db *sql.DB) []peerClaimSnapshot {
	t.Helper()
	rows, err := db.Query("SELECT event_id,status,claimed_by,claim_token,lease_expires_at,attempt_count FROM orchestration_direct_peer_event_outbox ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	var result []peerClaimSnapshot
	for rows.Next() {
		var row peerClaimSnapshot
		if err := rows.Scan(&row.EventID, &row.Status, &row.Worker, &row.Token, &row.Lease, &row.Attempts); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

// verifyPeerClaimRecovery 重建owner后每事件投影一次，失败事务不增加attempt计数。
func verifyPeerClaimRecovery(t *testing.T, db *sql.DB, ids []string, calls map[string]int, sink EventSink, attempts int64) {
	t.Helper()
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	projector, err := NewEventProjector(store, sink, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := projector.Process(t.Context(), time.Minute, 3); err != nil {
			t.Fatal(err)
		}
	}
	after := readPeerClaimSnapshots(t, db)
	if len(after) != len(ids) || len(calls) != len(ids) {
		t.Fatal("recovery lost or created events")
	}
	assertPeerClaimRecoveredRows(t, after, ids, calls, attempts)
}

// preparePeerClaimState 建立pending或已经过期的旧worker租约，返回恢复后的合法尝试次数。
func preparePeerClaimState(t *testing.T, db *sql.DB, store *Store, expired bool) int64 {
	t.Helper()
	attempts := int64(1)
	if expired {
		items, err := store.ClaimDirectPeerEventOutbox(t.Context(), "previous-owner", time.Minute, 3)
		if err != nil || len(items) != 3 {
			t.Fatalf("initial claim count=%d err=%v", len(items), err)
		}
		if _, err := db.Exec("UPDATE orchestration_direct_peer_event_outbox SET lease_expires_at=1 WHERE status='claimed'"); err != nil {
			t.Fatal(err)
		}
		attempts = 2
	}
	return attempts
}

// assertPeerClaimRecoveredRows 逐项核对恢复状态、原事件身份及无回滚残留的尝试次数。
func assertPeerClaimRecoveredRows(t *testing.T, after []peerClaimSnapshot, ids []string, calls map[string]int, attempts int64) {
	t.Helper()
	for i, row := range after {
		if row.EventID != ids[i] || row.Status != "projected" || row.Attempts != attempts || calls[row.EventID] != 1 {
			t.Fatalf("recovery row=%+v calls=%d expected attempts=%d", row, calls[row.EventID], attempts)
		}
	}
}
