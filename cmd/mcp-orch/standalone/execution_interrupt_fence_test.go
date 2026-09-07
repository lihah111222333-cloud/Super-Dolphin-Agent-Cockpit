package standalone

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/codexwire"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// TestExecutionInterruptFenceContract 聚合身份拒绝、当前轮语义和替换后的固定进程目标。
func TestExecutionInterruptFenceContract(t *testing.T) {
	t.Run("mismatches", testInterruptIdentityRejection)
	t.Run("fixed-process", testInterruptCapturedProcess)
	t.Run("current-turn", testInterruptCurrentTurn)
}

// testInterruptIdentityRejection 在进程调用前拒绝缺失或过期身份。
func testInterruptIdentityRejection(t *testing.T) {
	process, calls := newInterruptRPCProcess(t)
	e := NewExecution(Config{})
	e.sessions["agent"] = &cliSession{provider: Provider{Name: "codex"}, process: process, threadID: "thread", turnID: "turn", launchSeq: 2}
	for _, tc := range []struct {
		name, thread, turn string
		seq                uint64
	}{
		{"old-launch", "thread", "turn", 1},
		{"old-thread", "other", "turn", 2},
		{"old-turn", "thread", "other", 2},
		{"missing-thread", "", "turn", 2},
		{"missing-turn", "thread", "", 2},
		{"missing-launch", "thread", "turn", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := "interrupt turn or launch identity mismatch"
			if tc.thread == "" || tc.turn == "" || tc.seq == 0 {
				want = "interrupt requires complete turn and launch identity"
			}
			err := e.InterruptTurn(context.Background(), "agent", turndto.TurnRefV1{ThreadID: tc.thread, TurnID: tc.turn}, tc.seq, "cleanup")
			if err == nil || err.Error() != want {
				t.Fatalf("identity rejection: got %v, want %s", err, want)
			}
			assertNoInterruptRPC(t, calls)
		})
	}
	ref := turndto.TurnRefV1{ThreadID: "thread", TurnID: "turn"}
	if err := e.InterruptTurn(context.Background(), "agent", ref, 2, "positive-control"); err != nil {
		t.Fatalf("valid fixture cannot interrupt: %v", err)
	}
	if got := <-calls; got != ref {
		t.Fatalf("positive control target: got %+v, want %+v", got, ref)
	}
	e.sessions["agent"].provider.Name = "claude"
	err := e.InterruptTurn(context.Background(), "agent", turndto.TurnRefV1{ThreadID: "thread", TurnID: "turn"}, 2, "cleanup")
	if !errors.Is(err, ErrExecutionNotReady) {
		t.Fatalf("Claude must return unsupported: %v", err)
	}
	assertNoInterruptRPC(t, calls)
}

// assertNoInterruptRPC 验证身份拒绝没有向实际协议进程发送中断。
func assertNoInterruptRPC(t *testing.T, calls <-chan turndto.TurnRefV1) {
	t.Helper()
	select {
	case got := <-calls:
		t.Fatalf("rejected identity reached RPC: %+v", got)
	default:
	}
}

// newInterruptRPCProcess 使用真实 jrpc2 内存通道捕获中断协议身份。
func newInterruptRPCProcess(t *testing.T) (*codexwire.Process, <-chan turndto.TurnRefV1) {
	t.Helper()
	calls := make(chan turndto.TurnRefV1, 2)
	local := jrpcserver.NewLocal(handler.Map{
		"turn/interrupt": handler.New(func(_ context.Context, ref turndto.TurnRefV1) (map[string]any, error) {
			calls <- ref
			return map[string]any{}, nil
		}),
	}, nil)
	t.Cleanup(func() {
		if err := local.Close(); err != nil {
			t.Errorf("close RPC: %v", err)
		}
	})
	return &codexwire.Process{Client: local.Client}, calls
}

// testInterruptCapturedProcess 校验后替换 AgentID 映射，RPC 仍只能触及旧进程。
func testInterruptCapturedProcess(t *testing.T) {
	oldProcess, oldCalls := newInterruptRPCProcess(t)
	newProcess, newCalls := newInterruptRPCProcess(t)
	old := &cliSession{provider: Provider{Name: "codex"}, process: oldProcess, threadID: "old-thread", turnID: "old-turn", launchSeq: 1}
	e := NewExecution(Config{})
	e.sessions["agent"] = old
	ref := turndto.TurnRefV1{ThreadID: "old-thread", TurnID: "old-turn"}
	process, target, err := old.captureInterrupt(&ref, 1)
	assertInterruptRefMapping(t, target, map[string]string{"ThreadID": "old-thread", "TurnID": "old-turn"})
	if err != nil {
		t.Fatal(err)
	}
	e.sessions["agent"] = &cliSession{provider: Provider{Name: "codex"}, process: newProcess, threadID: "new-thread", turnID: "new-turn", launchSeq: 2}
	if err := interruptCapturedTurn(context.Background(), process, target); err != nil {
		t.Fatal(err)
	}
	if got := <-oldCalls; got != ref {
		t.Fatalf("old target changed: %+v", got)
	}
	select {
	case got := <-newCalls:
		t.Fatalf("new generation interrupted: %+v", got)
	default:
	}
	if err := e.InterruptTurn(context.Background(), "agent", ref, 1, "cleanup"); err == nil {
		t.Fatal("replaced generation accepted stale cleanup")
	}
}

// testInterruptCurrentTurn 保留公开按 AgentID 中断当前轮的语义。
func testInterruptCurrentTurn(t *testing.T) {
	process, calls := newInterruptRPCProcess(t)
	e := NewExecution(Config{})
	e.sessions["agent"] = &cliSession{provider: Provider{Name: "codex"}, process: process, threadID: "thread", turnID: "turn", launchSeq: 3}
	if err := e.Interrupt(context.Background(), "agent", "user"); err != nil {
		t.Fatal(err)
	}
	if got := <-calls; got != (turndto.TurnRefV1{ThreadID: "thread", TurnID: "turn"}) {
		t.Fatalf("wrong current target: %+v", got)
	}
	if err := e.InterruptTurn(context.Background(), "agent", turndto.TurnRefV1{ThreadID: "thread", TurnID: "turn"}, 3, "cleanup"); err != nil {
		t.Fatal(err)
	}
	if got := <-calls; got.TurnID != "turn" {
		t.Fatalf("wrong fenced target: %+v", got)
	}
}

// assertInterruptRefMapping 动态枚举生产引用字段，新增字段不能从中断映射静默丢失。
func assertInterruptRefMapping(t *testing.T, ref turndto.TurnRefV1, expected map[string]string) {
	t.Helper()
	value := reflect.ValueOf(ref)
	for i := 0; i < value.NumField(); i++ {
		name := value.Type().Field(i).Name
		want, ok := expected[name]
		if !ok {
			t.Errorf("missing interrupt field coverage: %s", name)
			continue
		}
		if value.Field(i).Interface() != want {
			t.Errorf("interrupt field %s mapping mismatch", name)
		}
		delete(expected, name)
	}
	if len(expected) != 0 {
		t.Errorf("stale interrupt fields: %v", expected)
	}
}
