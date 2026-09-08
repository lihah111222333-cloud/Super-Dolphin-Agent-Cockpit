package standalone

import (
	"context"
	"errors"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/claudewire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestNativeAckIdentityAndWait 以确定性事件验证早到确认、身份隔离和等待失败。
func TestNativeAckIdentityAndWait(t *testing.T) {
	parent := "child"
	for _, tc := range []struct {
		name     string
		event    claudewire.Event
		accepted bool
	}{
		{"missing-replay", claudewire.Event{Type: "user", SessionID: "thread", UUID: "turn"}, false},
		{"echo-before-wait", claudewire.Event{Type: "user", IsReplay: true, SessionID: "thread", UUID: "turn"}, true},
		{"wrong-session", claudewire.Event{Type: "user", IsReplay: true, SessionID: "other", UUID: "turn"}, false},
		{"wrong-uuid", claudewire.Event{Type: "user", IsReplay: true, SessionID: "thread", UUID: "other"}, false},
		{"child", claudewire.Event{Type: "user", IsReplay: true, SessionID: "thread", UUID: "turn", ParentToolUseID: &parent}, false},
		{"init-not-ack", claudewire.Event{Type: "system", Subtype: "init", SessionID: "thread", UUID: "turn"}, false},
		{"system-metadata-not-ack", claudewire.Event{Type: "system", SystemMessage: "metadata", IsReplay: true, SessionID: "thread", UUID: "turn"}, false},
		{"result-not-ack", claudewire.Event{Type: "result", SessionID: "thread", UUID: "turn"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ack := &nativeSubmissionAck{threadID: "thread", turnID: "turn", done: make(chan struct{})}
			session := &cliSession{threadID: "thread", pendingNativeAck: ack}
			acceptNativeClaudeAck(session, tc.event)
			acceptNativeClaudeAck(session, tc.event)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := waitNativeAck(ctx, session, ack, make(chan struct{}))
			if tc.accepted && err != nil {
				t.Fatal(err)
			}
			if !tc.accepted && !errors.Is(err, context.Canceled) {
				t.Fatalf("missing ack returned %v", err)
			}
		})
	}
	t.Run("exit-without-ack", func(t *testing.T) {
		ack := &nativeSubmissionAck{done: make(chan struct{})}
		done := make(chan struct{})
		close(done)
		if err := waitNativeAck(context.Background(), &cliSession{}, ack, done); err == nil {
			t.Fatal("exit accepted")
		}
	})
	t.Run("terminal-clears-turn-not-ack", func(t *testing.T) {
		ack := &nativeSubmissionAck{threadID: "thread", turnID: "turn", done: make(chan struct{})}
		s := &cliSession{threadID: "thread", pendingNativeAck: ack}
		acceptNativeClaudeAck(s, claudewire.Event{Type: "user", IsReplay: true, SessionID: "thread", UUID: "turn"})
		if !ack.accepted {
			t.Fatal("early terminal lost ack")
		}
		releaseNativeAck(s, &nativeSubmissionAck{})
		if s.pendingNativeAck != ack {
			t.Fatal("foreign waiter released slot")
		}
		releaseNativeAck(s, ack)
		if s.pendingNativeAck != nil {
			t.Fatal("slot leaked")
		}
	})
}

// TestNativeReceiptFieldGuard 动态枚举 DTO 并验证生产 mapper，新增字段必须补消费断言。
func TestNativeReceiptFieldGuard(t *testing.T) {
	receipt, err := nativeReceipt("claude", "agent", "thread", "turn", 7)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{"Provider": "claude", "AgentID": "agent", "ThreadID": "thread", "TurnID": "turn", "ReceiptID": "turn", "LaunchSeq": uint64(7)}
	value := reflect.ValueOf(receipt)
	for i := 0; i < value.NumField(); i++ {
		name := value.Type().Field(i).Name
		want, ok := expected[name]
		if !ok {
			t.Errorf("missing native mapper coverage: %s", name)
			continue
		}
		if !reflect.DeepEqual(value.Field(i).Interface(), want) {
			t.Errorf("%s mapping mismatch", name)
		}
		delete(expected, name)
	}
	if len(expected) != 0 {
		t.Fatalf("stale fields: %v", expected)
	}
	if _, err := nativeReceipt("claude", "agent", "thread", "turn", 0); err == nil {
		t.Fatal("empty generation accepted")
	}
}

// TestNativeReceiptRequiresIdentity 在任何副作用前拒绝缺失代际。
func TestNativeReceiptRequiresIdentity(t *testing.T) {
	e := NewExecution(Config{})
	e.sessions["agent"] = &cliSession{provider: Provider{Name: "claude"}, threadID: "thread"}
	_, err := e.SubmitTurnWithReceipt(context.Background(), contract.TurnSubmission{})
	if err == nil {
		t.Fatal("invalid input accepted")
	}
	e.sessions["agent"].launchSeq = 1
	for _, threadID := range []string{"", "foreign"} {
		_, err = e.SubmitTurnWithReceipt(context.Background(), contract.TurnSubmission{AgentID: "agent", ThreadID: threadID, Inputs: []turndto.InputItem{{Type: "text", Content: "input"}}})
		if err == nil || err.Error() != "native submission thread identity mismatch" {
			t.Fatalf("thread %q must fail before provider access: %v", threadID, err)
		}
	}
}

// TestNativeSubmissionReceiptContract 聚合原生确认契约确定性回归。
func TestNativeSubmissionReceiptContract(t *testing.T) {
	t.Run("identity", TestNativeAckIdentityAndWait)
	t.Run("fields", TestNativeReceiptFieldGuard)
	t.Run("validation", TestNativeReceiptRequiresIdentity)
	t.Run("write-without-ack", TestNativeClaudeWriteIsNotAck)
}

// TestNativeClaudeWriteIsNotAck 真实子进程已读输入仍不得以 stdin 成功确认投递。
func TestNativeClaudeWriteIsNotAck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	f := newClaudeBootstrapFixture(t, ctx)
	f.session.mu.Lock()
	f.session.launchSeq = 1
	f.session.mu.Unlock()
	result := make(chan error, 1)
	var workers sync.WaitGroup
	defer workers.Wait()
	workers.Go(func() {
		_, err := f.execution.SubmitTurnWithReceipt(ctx, contract.TurnSubmission{AgentID: "agent", ThreadID: f.session.threadID, Inputs: []turndto.InputItem{{Type: "text", Content: "first-input"}}})
		result <- err
	})
	awaitClaudeBootstrapEvent(t, ctx, f.events)

	f.session.mu.Lock()
	f.session.turnID = ""
	f.session.mu.Unlock()
	if _, err := f.execution.SubmitTurn(ctx, contract.TurnSubmission{AgentID: "agent", ThreadID: f.session.threadID, Inputs: []turndto.InputItem{{Type: "text", Content: "first-input"}}}); err == nil {
		t.Fatal("next turn overwrote pending ack")
	}
	select {
	case err := <-result:
		t.Fatalf("write returned without ack: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ack wait ignored cancellation")
	}
}
