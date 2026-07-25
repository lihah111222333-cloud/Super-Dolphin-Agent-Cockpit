package main

import (
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/bootstrap"
	"go.uber.org/fx"
)

// TestNewStdioServerFailsFastWhenMcpStdoutNil 锁定 mcpStdout 未初始化时必须返回 error，
// 不得 fallback 到 os.Stdout，否则会把普通 stdout 当作 MCP JSON-RPC 通道继续使用。
func TestNewStdioServerFailsFastWhenMcpStdoutNil(t *testing.T) {
	prev := mcpStdout.Swap(nil)
	t.Cleanup(func() {
		if prev != nil {
			mcpStdout.Store(prev)
		}
	})

	_, err := newServer(ToolHandlers{})
	if err == nil {
		t.Fatal("newServer() error = nil, want error when mcpStdout is nil")
	}
	if !strings.Contains(err.Error(), "mcpStdout") {
		t.Fatalf("newServer() error = %q, want mention of mcpStdout", err.Error())
	}
}

func TestMcpLSPProcessIdleTimeoutValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{name: "default", want: defaultMcpLSPProcessIdleTimeout},
		{name: "disabled", raw: "0"},
		{name: "custom", raw: "250ms", want: 250 * time.Millisecond},
		{name: "negative", raw: "-1s", wantErr: true},
		{name: "invalid", raw: "not-a-duration", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(mcpLSPProcessIdleTimeoutEnv, test.raw)
			got, err := mcpLSPProcessIdleTimeout()
			if (err != nil) != test.wantErr {
				t.Fatalf("mcpLSPProcessIdleTimeout() error = %v, wantErr=%v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("mcpLSPProcessIdleTimeout() = %s, want %s", got, test.want)
			}
		})
	}
}

func TestBootstrapProviderRejectsInvalidSnapshotDuringGraphConstruction(t *testing.T) {
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func() bootstrap.Config {
				return bootstrap.Config{BootSnapshot: []byte(`{"unknown":true}`)}
			},
			bootstrap.New,
		),
		fx.Invoke(func(*bootstrap.Client) {}),
	)
	if err := app.Err(); err == nil {
		t.Fatal("app.Err() = nil, want invalid bootstrap snapshot error")
	} else if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("app.Err() = %v, want unknown field error", err)
	}
}
