package codexwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type testWriteCloser struct{ bytes.Buffer }

func (*testWriteCloser) Close() error { return nil }

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
