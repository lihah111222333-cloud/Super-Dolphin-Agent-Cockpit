package main

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/bootstrap"
	"go.uber.org/fx"
)

// TestNewStdioServerFailsFastWhenMcpStdoutNil 锁定 stdout 未注入时必须返回 error，
// 不得 fallback 到 os.Stdout，否则会把普通 stdout 当作 MCP JSON-RPC 通道继续使用。
func TestNewStdioServerFailsFastWhenMcpStdoutNil(t *testing.T) {
	_, err := newServer(nil, ToolHandlers{})
	if err == nil {
		t.Fatal("newServer() error = nil, want error when stdout is nil")
	}
	if !strings.Contains(err.Error(), "stdout") {
		t.Fatalf("newServer() error = %q, want mention of stdout", err.Error())
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
