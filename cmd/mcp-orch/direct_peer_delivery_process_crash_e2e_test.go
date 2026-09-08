//go:build e2e

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	directpeer "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

type peerCrashEvidence struct {
	MessageID string
	Receipt   contract.ProviderSubmissionReceipt
}

// TestStandaloneDirectPeerAcceptedProcessCrash 杀死已持久accepted但阻塞在模拟原生确认前的独占测试子进程。
func TestStandaloneDirectPeerAcceptedProcessCrash(t *testing.T) { runPeerProcessCrash(t, "accepted") }

// TestStandaloneDirectPeerSubmittedProcessCrash 杀死已提交receipt事务的测试子进程，验证SQLite恢复和原确认重放。
func TestStandaloneDirectPeerSubmittedProcessCrash(t *testing.T) { runPeerProcessCrash(t, "submitted") }

// runPeerProcessCrash 只启动并强制终止本测试进程；Process.Kill在Unix发送SIGKILL，其他平台保持同一强制终止契约。
func runPeerProcessCrash(t *testing.T, window string) {
	t.Helper()
	if os.Getenv("ORCH_PEER_CRASH_CHILD") == window {
		runPeerCrashChild(t, window, os.Getenv("ORCH_PEER_CRASH_DIR"))
		return
	}
	state := t.TempDir()
	cmd := exec.Command(requireExecutable(t), "-test.run=^"+t.Name()+"$", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), "ORCH_PEER_CRASH_CHILD="+window, "ORCH_PEER_CRASH_DIR="+state)
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
	evidence := waitPeerCrashEvidence(t, state)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	waited = true
	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		t.Fatalf("child was not killed: %v output=%s", err, output.String())
	}
	if exited.ProcessState.Success() {
		t.Fatal("forcibly terminated child reported successful exit")
	}
	assertPeerCrashRecovery(t, state, window, evidence)
}

// newPeerCrashFixture 重开同一路径的真实迁移数据库，原生端口明确为gated测试替身。
func newPeerCrashFixture(t *testing.T, state string, submitter *gatedPeerNativeSubmitter) peerFaultFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	f := peerFaultFixture{db: directPeerStandaloneDatabase(t, state), ctx: ctx, workspace: state}
	return rebuildPeerFaultRegistry(t, f, submitter)
}

// runPeerCrashChild 在目标事务边界写出证据后保持进程存活，必须由父进程SIGKILL结束。
func runPeerCrashChild(t *testing.T, window, state string) {
	t.Helper()
	if !filepath.IsAbs(state) {
		t.Fatal("child state must be absolute")
	}
	submitter := &gatedPeerNativeSubmitter{}
	f := newPeerCrashFixture(t, state, submitter)
	grant, ok := f.registry.Lookup("manage_direct_peer")
	if !ok {
		t.Fatal("grant handler missing")
	}
	_, err := grant.Handler(mcpcommon.WithToolScope(f.ctx, mcpcommon.ToolScope{CWD: state}), json.RawMessage(`{"action":"grant","sender_agent_id":"sender","receiver_agent_id":"receiver","idempotency_key":"grant","can_send":true,"can_read":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if window == "accepted" {
		holdPeerAcceptedCrash(t, f, submitter)
	} else {
		holdPeerSubmittedCrash(t, f, submitter)
	}
	t.Fatal("crash child returned without SIGKILL")
}

// holdPeerAcceptedCrash 原生调用已进入闸门但不能返回receipt，确认accepted事务已提交。
func holdPeerAcceptedCrash(t *testing.T, f peerFaultFixture, submitter *gatedPeerNativeSubmitter) {
	t.Helper()
	submitter.entered = make(chan struct{}, 1)
	submitter.release = make(chan struct{})
	var workers sync.WaitGroup
	workers.Go(func() { _, _ = sendPeerFaultMessage(f) })
	t.Cleanup(workers.Wait)
	select {
	case <-submitter.entered:
	case <-f.ctx.Done():
		t.Fatal("native gate not entered")
	}
	assertPeerFaultRows(t, f, "accepted", 0)
	evidence := peerCrashEvidence{}
	if err := f.db.QueryRowContext(f.ctx, "SELECT message_id FROM orchestration_direct_peer_messages").Scan(&evidence.MessageID); err != nil {
		t.Fatal(err)
	}
	publishPeerCrashEvidence(t, f, evidence)
}

// holdPeerSubmittedCrash 真实Registry提交receipt/event/outbox后才发布握手，原生确认本身是替身。
func holdPeerSubmittedCrash(t *testing.T, f peerFaultFixture, submitter *gatedPeerNativeSubmitter) {
	t.Helper()
	value, err := sendPeerFaultMessage(f)
	if err != nil {
		t.Fatal(err)
	}
	var message directpeer.Message
	if err := json.Unmarshal(mustJSON(t, value), &message); err != nil {
		t.Fatal(err)
	}
	receipt, err := f.store.ReadNativeReceipt(f.ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	if submitter.calls.Load() != 1 {
		t.Fatal("expected exactly one stub native ack")
	}
	assertPeerFaultRows(t, f, "submitted", 1)
	publishPeerCrashEvidence(t, f, peerCrashEvidence{MessageID: message.MessageID, Receipt: receipt})
}

// publishPeerCrashEvidence 原子发布边界证据；超时是失败，不能自然退出冒充崩溃。
func publishPeerCrashEvidence(t *testing.T, f peerFaultFixture, evidence peerCrashEvidence) {
	t.Helper()
	pending := filepath.Join(f.workspace, "ready.tmp")
	if err := os.WriteFile(pending, mustJSON(t, evidence), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(pending, filepath.Join(f.workspace, "ready.json")); err != nil {
		t.Fatal(err)
	}
	<-f.ctx.Done()
	t.Fatal("parent did not SIGKILL child at ready boundary")
}

// waitPeerCrashEvidence 只读取本测试目录的完整握手文件，缺失期间有界等待。
func waitPeerCrashEvidence(t *testing.T, state string) peerCrashEvidence {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := os.ReadFile(filepath.Join(state, "ready.json"))
		if err == nil {
			var evidence peerCrashEvidence
			if err := json.Unmarshal(raw, &evidence); err != nil {
				t.Fatal(err)
			}
			if evidence.MessageID == "" {
				t.Fatal("child handshake missing durable message identity")
			}
			return evidence
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("child did not publish crash boundary")
		}
	}
}

// assertPeerCrashRecovery 在SIGKILL并wait后新建连接与owner，原key不再次获得native提交资格。
func assertPeerCrashRecovery(t *testing.T, state, window string, evidence peerCrashEvidence) {
	t.Helper()
	submitter := &gatedPeerNativeSubmitter{}
	f := newPeerCrashFixture(t, state, submitter)
	for range 2 {
		value, err := sendPeerFaultMessage(f)
		if window == "accepted" {
			assertPeerFaultUnknown(t, err)
		} else {
			assertPeerCrashReceipt(t, f, value, err, evidence)
		}
	}
	if submitter.calls.Load() != 0 {
		t.Fatal("crash recovery resubmitted original message")
	}
	submitted := 0
	if window == "submitted" {
		submitted = 1
	}
	assertPeerFaultRows(t, f, window, submitted)
	var messageID string
	if err := f.db.QueryRowContext(f.ctx, "SELECT message_id FROM orchestration_direct_peer_messages").Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	if messageID != evidence.MessageID {
		t.Fatal("crash recovery replaced durable message")
	}
}

// assertPeerCrashReceipt 核对崩溃前后整个typed原生确认相等，不能仅检查submitted状态。
func assertPeerCrashReceipt(t *testing.T, f peerFaultFixture, value any, err error, evidence peerCrashEvidence) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var message directpeer.Message
	if err := json.Unmarshal(mustJSON(t, value), &message); err != nil {
		t.Fatal(err)
	}
	receipt, err := f.store.ReadNativeReceipt(f.ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	if message.MessageID != evidence.MessageID || !reflect.DeepEqual(receipt, evidence.Receipt) {
		t.Fatal("crash recovery changed authoritative receipt")
	}
}
