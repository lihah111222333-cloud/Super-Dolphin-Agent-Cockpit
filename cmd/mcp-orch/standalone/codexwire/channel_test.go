package codexwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type testWriteCloser struct{ bytes.Buffer }

func (*testWriteCloser) Close() error { return nil }

// TestCodexNotificationRoutingPreservesReverseRequests 防止通知快路径吞掉带 ID 的反向请求。
func TestCodexNotificationRoutingPreservesReverseRequests(t *testing.T) {
	for _, id := range []string{`1`, `0`, `"approval-1"`, `""`, `null`, `{}`, `true`} {
		t.Run(id, func(t *testing.T) {
			raw := `{"id":` + id + `,"method":"item/commandExecution/requestApproval","params":{"command":"pwd"},"extension":{"keep":true}}`
			channel := NewChannel(io.NopCloser(strings.NewReader(raw+"\n")), &testWriteCloser{})
			t.Cleanup(func() {
				if err := channel.Close(); err != nil {
					t.Error(err)
				}
			})
			notifications := 0
			channel.onNotify = func(*jrpc2.Request) { notifications++ }
			frame, err := channel.Recv()
			if err != nil || notifications != 0 {
				t.Fatalf("reverse request consumed: notifications=%d err=%v", notifications, err)
			}
			var want, got map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(frame, &got); err != nil {
				t.Fatal(err)
			}
			want["jsonrpc"] = json.RawMessage(`"2.0"`)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("request fields changed before jrpc2 validation: got=%s want=%s", frame, raw)
			}
		})
	}
	// The existing real jrpc2 client fixture also proves notification ordering.
	t.Run("notification-order", TestCodexNotificationsKeepWireOrder)
}

func TestCodexJSONLChannelContract(t *testing.T) {
	writer := &testWriteCloser{}
	channel := NewChannel(io.NopCloser(strings.NewReader(`{"id":1,"result":{"thread":{"id":"thread-1"}}}`+"\n")), writer)
	if err := channel.Send([]byte(`{"jsonrpc":"2.0","id":1,"method":"thread/start","params":{"cwd":"/tmp/work"}}`)); err != nil {
		t.Fatal(err)
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(writer.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if _, exists := sent["jsonrpc"]; exists {
		t.Fatal("Codex wire must omit jsonrpc")
	}
	if string(sent["method"]) != `"thread/start"` || string(sent["id"]) != "1" {
		t.Fatal("request identity changed")
	}
	received, err := channel.Recv()
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(received, &envelope); err != nil || string(envelope["jsonrpc"]) != `"2.0"` {
		t.Fatalf("jrpc2 envelope = %s, %v", received, err)
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCodexProcessExitBindingEmitsExactlyOnceAndClassifiesClose(t *testing.T) {
	var events []contract.CLIProcessExit
	p := &Process{done: make(chan struct{}), exitBind: contract.CLIProcessExitBinding{
		AgentID: "agent", SessionID: "session", LaunchSeq: 7,
		OnExit: func(event contract.CLIProcessExit) { events = append(events, event) },
	}, exitErr: errors.New("provider crashed")}
	p.emitExit()
	p.emitExit()
	if len(events) != 1 || !events[0].Unexpected || !errors.Is(events[0].Err, p.exitErr) {
		t.Fatalf("unexpected exit events = %#v", events)
	}
	p2 := &Process{done: make(chan struct{}), exitBind: p.exitBind, exitErr: nil}
	p2.exitBind.OnExit = func(event contract.CLIProcessExit) { events = append(events, event) }
	p2.exitMu.Lock()
	p2.closeReq = true
	p2.exitMu.Unlock()
	p2.emitExit()
	if len(events) != 2 || events[1].Unexpected {
		t.Fatalf("explicit close event = %#v", events)
	}
}

func TestCodexJSONLRejectsInvalidFrames(t *testing.T) {
	for _, input := range []string{"log output\n", "null\n", "[]\n", strings.Repeat("x", maxFrameBytes+1) + "\n"} {
		channel := NewChannel(io.NopCloser(strings.NewReader(input)), &testWriteCloser{})
		if _, err := channel.Recv(); err == nil {
			t.Fatal("invalid frame accepted")
		}
		if err := channel.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
