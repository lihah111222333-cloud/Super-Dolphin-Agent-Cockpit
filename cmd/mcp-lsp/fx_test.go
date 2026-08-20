package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/bootstrap"
	platformmetrics "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/metrics"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"go.uber.org/fx"
)

// TestRegistryToolProviderBlocksToolUntilReadinessRecovered 锁定启动期安全初始化失败时
// 不得执行工具；下一次宿主批准后的同一调用重试可以重新初始化，但成功后不再重复初始化。
func TestRegistryToolProviderBlocksToolUntilReadinessRecovered(t *testing.T) {
	wantErr := errors.New("private logger ACL requires authorization")
	attempts := 0
	calls := 0
	provider := registryToolProvider{
		defs: []toolDefinition{{
			Manifest: ToolManifest{Name: "probe"},
			Handler: func(context.Context, json.RawMessage) (any, error) {
				calls++
				return map[string]any{"ok": true}, nil
			},
		}},
		ensureReady: func() error {
			attempts++
			if attempts == 1 {
				return wantErr
			}
			return nil
		},
	}
	root := t.TempDir()
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})

	if _, err := provider.CallTool(ctx, "probe", json.RawMessage(`{}`)); !errors.Is(err, wantErr) {
		t.Fatalf("first CallTool() error = %v, want %v", err, wantErr)
	}
	if calls != 0 {
		t.Fatalf("handler calls after readiness failure = %d, want 0", calls)
	}
	if _, err := provider.CallTool(ctx, "probe", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("retry CallTool() error = %v", err)
	}
	if attempts != 2 || calls != 1 {
		t.Fatalf("after retry attempts/calls = %d/%d, want 2/1", attempts, calls)
	}
}

// TestNewStdioServerFailsFastWhenMcpStdoutNil 锁定 stdout 未注入时必须返回 error，
// 不得 fallback 到 os.Stdout，否则会把普通 stdout 当作 MCP JSON-RPC 通道继续使用。
func TestNewStdioServerFailsFastWhenMcpStdoutNil(t *testing.T) {
	gate, gateErr := newSidecarFileLoggerGate(func() error { return nil })
	if gateErr != nil {
		t.Fatalf("newSidecarFileLoggerGate() error = %v", gateErr)
	}
	_, err := newServer(nil, ToolHandlers{}, pkglogger.NewRuntime(pkglogger.RuntimeConfig{}), gate)
	if err == nil {
		t.Fatal("newServer() error = nil, want error when stdout is nil")
	}
	if !strings.Contains(err.Error(), "stdout") {
		t.Fatalf("newServer() error = %q, want mention of stdout", err.Error())
	}
}

func TestNewBootstrapRunnerRequiresLoggerRuntime(t *testing.T) {
	if _, err := newBootstrapRunner(bootstrap.Config{}, nil, nil, nil); err == nil {
		t.Fatal("newBootstrapRunner() error = nil, want missing logger runtime")
	}
}

func TestBootstrapProviderRejectsInvalidSnapshotDuringGraphConstruction(t *testing.T) {
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func() bootstrap.Config {
				return bootstrap.Config{BootSnapshot: []byte(`{"unknown":true}`), Metrics: platformmetrics.NewBootstrapMetrics()}
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
