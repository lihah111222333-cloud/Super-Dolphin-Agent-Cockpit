package rpc

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/creachadair/jrpc2"
)

func TestOnConnectReplaysActiveServers(t *testing.T) {
	server := &Server{active: make(map[*jrpc2.Server]string)}
	active := new(jrpc2.Server)
	server.addActive(active, dto.PeerKindTool)

	got := make(chan *jrpc2.Server, 1)
	server.OnConnect(func(current *jrpc2.Server) {
		got <- current
	})

	select {
	case current := <-got:
		if current != active {
			t.Fatalf("OnConnect replayed %p, want %p", current, active)
		}
	default:
		t.Fatal("OnConnect did not replay active server")
	}
}

func TestOnConnectUIReplaysOnlyUIActiveServers(t *testing.T) {
	server := &Server{active: make(map[*jrpc2.Server]string)}
	server.addActive(new(jrpc2.Server), dto.PeerKindTool)
	active := new(jrpc2.Server)
	server.addActive(active, dto.PeerKindUI)

	got := make(chan *jrpc2.Server, 2)
	server.OnConnectUI(func(current *jrpc2.Server) {
		got <- current
	})

	select {
	case current := <-got:
		if current != active {
			t.Fatalf("OnConnectUI replayed %p, want %p", current, active)
		}
	default:
		t.Fatal("OnConnectUI did not replay active UI server")
	}

	select {
	case current := <-got:
		t.Fatalf("OnConnectUI replayed unexpected server %p", current)
	default:
	}
}

func TestNotifyConnectedInvokesOnConnectHooks(t *testing.T) {
	server := &Server{active: make(map[*jrpc2.Server]string)}
	connected := new(jrpc2.Server)

	got := make(chan *jrpc2.Server, 1)
	server.OnConnect(func(current *jrpc2.Server) {
		got <- current
	})
	server.notifyConnected(connected)

	select {
	case current := <-got:
		if current != connected {
			t.Fatalf("notifyConnected passed %p, want %p", current, connected)
		}
	default:
		t.Fatal("notifyConnected did not invoke hook")
	}
}

func TestRPCFailureLogDoesNotIncludeRawParamsPreview(t *testing.T) {
	meta := rpcPendingRequest{
		ID:            "req-1",
		Method:        "thread/start",
		ThreadID:      "thread-1",
		ParamsSummary: SafeRPCLogSummary("thread/start", `{"threadId":"thread-1","prompt":"secret prompt from user","baseInstructions":"do not leak"}`),
		StartedAt:     time.Now(),
	}

	got := fmt.Sprint(rpcFailureLogArgs(meta, jrpc2.Errorf(jrpc2.InvalidParams, "bad params")))
	for _, forbidden := range []string{"params_preview", "secret prompt from user", "do not leak"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("failure log args leaked %q: %s", forbidden, got)
		}
	}
}

func TestRPCFailureLoggingRejectsRawParamsPreviewAndParamStringLogging(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read rpc package: %v", err)
	}
	allowedParamStringFiles := map[string]bool{
		"transport_ws.go": true,
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		source := string(raw)
		if strings.Contains(source, `"params_preview"`) {
			t.Fatalf("%s still registers raw params_preview log field", name)
		}
		if strings.Contains(source, "ParamString()") && !allowedParamStringFiles[name] {
			t.Fatalf("%s calls ParamString() outside the non-logging transport path", name)
		}
	}
}

func TestRPCSafeLogSummaryKeepsIDsAndLengthsOnly(t *testing.T) {
	raw := `{"threadId":"thread-1","agentId":"agent-1","prompt":"secret prompt from user","baseInstructions":"do not leak","developerInstructions":"dev secret","input":{"content":"user content"},"headers":{"Authorization":"Bearer secret"},"apiKey":"api-key-value"}`

	summary := SafeRPCLogSummary("thread/start", raw)
	for _, want := range []string{
		`"method":"thread/start"`,
		`"threadId":"thread-1"`,
		`"agentId":"agent-1"`,
		`"raw_bytes":`,
		`"raw_sha256":`,
		`"fields":`,
		`"prompt"`,
		`"prompt_bytes":`,
		`"baseInstructions_bytes":`,
		`"developerInstructions_bytes":`,
		`"headers_bytes":`,
		`"apiKey_bytes":`,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
	for _, forbidden := range []string{
		"secret prompt from user",
		"do not leak",
		"dev secret",
		"user content",
		"Bearer secret",
		"api-key-value",
	} {
		if strings.Contains(summary, forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, summary)
		}
	}
}
