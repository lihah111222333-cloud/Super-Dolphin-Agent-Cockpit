//go:build e2e

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
)

// TestStandaloneDirectPeerOutboxSinkProcessCrash 验证sink副作用后ACK前强杀允许同EventID重投，幂等消费只产生一次业务效果。
func TestStandaloneDirectPeerOutboxSinkProcessCrash(t *testing.T) {
	if os.Getenv("ORCH_OUTBOX_CRASH_CHILD") == "1" {
		runOutboxCrashChild(t, os.Getenv("ORCH_OUTBOX_CRASH_DIR"))
		return
	}
	state := t.TempDir()
	eventID := killOutboxChildAtSink(t, state)
	recoverOutboxCrash(t, state, eventID)
}

// killOutboxChildAtSink 只强制终止本测试启动的进程，握手在文件Sync完成之后。
func killOutboxChildAtSink(t *testing.T, state string) string {
	t.Helper()
	cmd := exec.Command(requireExecutable(t), "-test.run=^"+t.Name()+"$", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), "ORCH_OUTBOX_CRASH_CHILD=1", "ORCH_OUTBOX_CRASH_DIR="+state)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	eventID := waitOutboxSinkReady(t, state)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	waited = true
	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		t.Fatalf("child not killed: %v output=%s", err, output.String())
	}
	if exited.ProcessState.Success() {
		t.Fatal("killed child returned success")
	}
	return eventID
}

// runOutboxCrashChild 用真实projector领取唯一grant事件，sink落盘后阻塞且绝不返回成功。
func runOutboxCrashChild(t *testing.T, state string) {
	t.Helper()
	if !filepath.IsAbs(state) {
		t.Fatal("child state must be absolute")
	}
	f := newPeerCrashFixture(t, state, &gatedPeerNativeSubmitter{})
	if _, err := f.store.GrantPeer(f.ctx, state, "sender", "receiver", "grant", true, true); err != nil {
		t.Fatal(err)
	}
	sink := func(ctx context.Context, event directpeer.Event) error {
		if err := persistOutboxConsumerEffect(state, event.EventID); err != nil {
			return err
		}
		if err := writeSyncedOutboxFile(filepath.Join(state, "sink-ready.tmp"), []byte(event.EventID)); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(state, "sink-ready.tmp"), filepath.Join(state, "sink-ready")); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	processOutboxCrash(t, f, sink)
	t.Fatal("parent failed to kill child before sink returned")
}

// writeSyncedOutboxFile 将测试消费者证据写入并Sync，错误不能作为成功握手。
func writeSyncedOutboxFile(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(body)
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

// persistOutboxConsumerEffect 消费者按EventID幂等；通知日志记录每次投递，业务文件只创建一次。
func persistOutboxConsumerEffect(state, eventID string) error {
	if eventID == "" || strings.ContainsAny(eventID, "/\\") {
		return errors.New("unsafe event identity")
	}
	path := filepath.Join(state, "effect-"+eventID)
	err := writeSyncedOutboxFile(path, []byte(eventID))
	if err != nil && !os.IsExist(err) {
		return err
	}
	if os.IsExist(err) {
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if string(existing) != eventID {
			return errors.New("idempotency record differs")
		}
	}
	file, err := os.OpenFile(filepath.Join(state, "notifications"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(eventID + "\n")
	return errors.Join(writeErr, file.Sync(), file.Close())
}

// waitOutboxSinkReady 有界等待已Sync的sink握手，不用sleep推测事务位置。
func waitOutboxSinkReady(t *testing.T, state string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := os.ReadFile(filepath.Join(state, "sink-ready"))
		if err == nil && len(raw) > 0 {
			return string(raw)
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("sink handshake missing")
		}
	}
}

// processOutboxCrash 执行生产projector单批领取与ACK，sink是明确测试消费者。
func processOutboxCrash(t *testing.T, f peerFaultFixture, sink directpeer.EventSink) {
	t.Helper()
	projector, err := directpeer.NewEventProjector(f.store, sink, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := projector.Process(f.ctx, time.Minute, 1); err != nil {
		t.Fatal(err)
	}
}

// recoverOutboxCrash 明确用测试SQL使原租约到期，恢复消费者再收同EventID并成功ACK。
func recoverOutboxCrash(t *testing.T, state, eventID string) {
	t.Helper()
	f := newPeerCrashFixture(t, state, &gatedPeerNativeSubmitter{})
	assertOutboxCrashPriorEffect(t, state, eventID)
	var status string
	if err := f.db.QueryRowContext(f.ctx, "SELECT status FROM orchestration_direct_peer_event_outbox WHERE event_id=?", eventID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "claimed" {
		t.Fatalf("sink-before-ACK status=%s", status)
	}
	result, err := f.db.ExecContext(f.ctx, "UPDATE orchestration_direct_peer_event_outbox SET lease_expires_at=1 WHERE event_id=? AND status='claimed'", eventID)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		t.Fatalf("lease fault changed=%d err=%v", changed, err)
	}
	calls := 0
	sink := func(_ context.Context, event directpeer.Event) error {
		calls++
		if event.EventID != eventID {
			return errors.New("reclaimed a different event")
		}
		return persistOutboxConsumerEffect(state, event.EventID)
	}
	processOutboxCrash(t, f, sink)
	processOutboxCrash(t, f, sink)
	if calls != 1 {
		t.Fatalf("recovered sink calls=%d", calls)
	}
	if err := f.db.QueryRowContext(f.ctx, "SELECT status FROM orchestration_direct_peer_event_outbox WHERE event_id=?", eventID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "projected" {
		t.Fatalf("successful sink not ACKed: %s", status)
	}
	assertOutboxConsumerEvidence(t, state, eventID)
}

// assertOutboxConsumerEvidence 重复通知同ID但业务文件一次，证明消费者幂等而非产品exactly-once。
func assertOutboxConsumerEvidence(t *testing.T, state, eventID string) {
	t.Helper()
	notifications, err := os.ReadFile(filepath.Join(state, "notifications"))
	if err != nil {
		t.Fatal(err)
	}
	if string(notifications) != eventID+"\n"+eventID+"\n" {
		t.Fatal("expected two notifications with the identical EventID")
	}
	effects, err := filepath.Glob(filepath.Join(state, "effect-*"))
	if err != nil || len(effects) != 1 {
		t.Fatalf("business effects=%d err=%v", len(effects), err)
	}
	effect, err := os.ReadFile(effects[0])
	if err != nil || string(effect) != eventID {
		t.Fatalf("business effect differs: %v", err)
	}
}

// assertOutboxCrashPriorEffect 确认强杀前一次通知和业务效果已经持久化，恢复不能伪造首投。
func assertOutboxCrashPriorEffect(t *testing.T, state, eventID string) {
	t.Helper()
	notifications, err := os.ReadFile(filepath.Join(state, "notifications"))
	if err != nil || string(notifications) != eventID+"\n" {
		t.Fatalf("initial durable notification differs: %v", err)
	}
	effect, err := os.ReadFile(filepath.Join(state, "effect-"+eventID))
	if err != nil || string(effect) != eventID {
		t.Fatalf("initial durable effect differs: %v", err)
	}
}
