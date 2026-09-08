package directpeer

import (
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"testing"
)

// TestNativeReceiptConcurrentCommit 竞争同一accepted消息只能提交一次，失败方不能增添事件或改写回执。
func TestNativeReceiptConcurrentCommit(t *testing.T) {
	store, db, message, capability, receipt := nativeReceiptFixture(t)
	start := make(chan struct{})
	errorsByWorker := make([]error, 2)
	messages := make([]Message, 2)
	var workers sync.WaitGroup
	for i := range 2 {
		workers.Go(func() {
			<-start
			messages[i], errorsByWorker[i] = store.RecordNativeReceipt(t.Context(), capability, message.MessageID, receipt)
		})
	}
	close(start)
	workers.Wait()
	winner := requireSingleNativeReceiptCommit(t, messages, errorsByWorker)
	got, err := store.ReadNativeReceipt(t.Context(), winner)
	if err != nil || !reflect.DeepEqual(got, receipt) {
		t.Fatalf("concurrent receipt changed: %+v %v", got, err)
	}
	assertPendingDirectPeerEvents(t, db, 3)
	if _, err := store.RecordNativeReceipt(t.Context(), capability, message.MessageID, receipt); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("already committed receipt was writable again: %v", err)
	}
	assertPendingDirectPeerEvents(t, db, 3)
}

// requireSingleNativeReceiptCommit 只接受一个原子提交和一个已不再accepted的拒绝。
func requireSingleNativeReceiptCommit(t *testing.T, messages []Message, failures []error) Message {
	t.Helper()
	successes := 0
	var winner Message
	for i, err := range failures {
		if err == nil {
			successes++
			winner = messages[i]
		} else if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("unexpected competing receipt error: %v", err)
		}
	}
	if successes != 1 || winner.Status != "submitted" {
		t.Fatalf("atomic receipt winners=%d status=%s", successes, winner.Status)
	}
	return winner
}
