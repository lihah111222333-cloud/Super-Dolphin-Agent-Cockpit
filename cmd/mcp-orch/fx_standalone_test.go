package main

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// TestStandaloneGraphStartsWithoutHost 校验生产 Fx 图，不能用手写假 registry 替代。
func TestStandaloneGraphStartsWithoutHost(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(state, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: t.TempDir(), StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: executable, Home: t.TempDir(), Models: []string{"test-model"}, Sandbox: "read-only"}}}
	db, err := newStandaloneDB(newStandalonePlatformConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	readyPath := filepath.Join(state, "ready.json")
	app := newStandaloneTestApp(t, cfg, "test-only-operator-token-value", readyPath, db)
	if err := app.Err(); err != nil {
		t.Fatalf("standalone Fx graph: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("standalone start: %v", err)
	}
	t.Cleanup(func() { stopStandaloneTestApp(t, app) })
	ready := awaitStandaloneTestReady(t, ctx, app, readyPath)
	assertStandaloneTestHandshake(t, ctx, ready.Endpoint)
	stopStandaloneTestApp(t, app)
	if _, err := os.Stat(readyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stopped daemon retained ready file: %v", err)
	}
}

func TestStandalonePlatformConfigUsesInstanceCanonicalStateDir(t *testing.T) {
	state := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(state, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasParent := t.TempDir()
	alias := filepath.Join(aliasParent, "state-alias")
	if err := os.Symlink(state, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	lock, err := standalone.AcquireInstance(alias)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}()

	cfg := standalone.Config{WorkspaceRoot: t.TempDir(), StateDir: lock.CanonicalStateDir()}
	platform := newStandalonePlatformConfig(cfg)
	wantRoot, err := filepath.EvalSymlinks(state)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(wantRoot, "orch.sqlite")
	if platform.SQLitePath != want {
		t.Fatalf("SQLitePath = %q, want canonical state path %q", platform.SQLitePath, want)
	}
}

// stopStandaloneTestApp 使用独立关闭预算，即使启动断言失败也回收生产图。
func stopStandaloneTestApp(t *testing.T, app *fx.App) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.Stop(ctx); err != nil {
		t.Errorf("standalone stop: %v", err)
	}
}

// awaitStandaloneTestReady 等待实际 runner 发布发现信息，同时观察提前退出。
func awaitStandaloneTestReady(t *testing.T, ctx context.Context, app *fx.App, path string) standalone.Ready {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := os.ReadFile(path)
		if err == nil {
			var ready standalone.Ready
			if err := json.Unmarshal(raw, &ready); err != nil || ready.Endpoint == "" || ready.InstanceID == "" {
				t.Fatalf("invalid readiness: %v", err)
			}
			return ready
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("readiness timeout: %v", ctx.Err())
		case signal := <-app.Wait():
			t.Fatalf("runtime exited before readiness: %v", signal)
		case <-ticker.C:
		}
	}
}

// assertStandaloneTestHandshake 通过真实 TCP 监听验证 MCP，不以文件存在替代可服务证据。
func assertStandaloneTestHandshake(t *testing.T, ctx context.Context, endpoint string) {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"graph-test","version":"1"}}}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-only-operator-token-value")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 65536))
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Mcp-Session-Id") == "" {
		t.Fatalf("initialize status=%d body=%s", response.StatusCode, raw)
	}
	assertStandaloneInitializeReply(t, raw)
}

// assertStandaloneInitializeReply 校验响应身份和协议结果，防止 HTTP 200 错误响应假绿。
func assertStandaloneInitializeReply(t *testing.T, raw []byte) {
	t.Helper()
	var reply struct {
		ID     int             `json:"id"`
		Error  json.RawMessage `json:"error"`
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.ID != 1 || len(reply.Error) != 0 || reply.Result.ProtocolVersion != "2025-11-25" || reply.Result.ServerInfo.Name != "mcp-orch" {
		t.Fatalf("unexpected initialize result: %s", raw)
	}
}

// TestStandaloneGraphIncludesDAGExecution 防止注册 DAG 工具后遗漏调度或误接宿主 launcher。
func TestStandaloneGraphIncludesDAGExecution(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "fx_standalone.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	builder := requireStandaloneCompositionBuilder(t, file)
	for _, required := range []string{"orchestrationDAGOptions", "orchestrationNodeExecutionOptions", "newStandaloneLauncher", "newStandaloneDirectPeerEventProjector"} {
		if !funcBodyContainsIdent(builder, required) {
			t.Fatalf("standalone graph missing %s", required)
		}
	}
	if funcBodyContainsIdent(builder, "orchestrationExecutionOptions") {
		t.Fatal("standalone graph must not install the sidecar launcher")
	}
	executionFile, err := parser.ParseFile(token.NewFileSet(), "fx_orchestration_execution.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	nodeOptions := findFuncDecl(executionFile, "orchestrationNodeExecutionOptions")
	if nodeOptions == nil || !funcBodyContainsSelector(nodeOptions, "orchestration", "NewNodeExecutorRouter") {
		t.Fatal("shared node execution must provide the real router")
	}
}

// requireStandaloneCompositionBuilder 直接定位真实装配入口并禁止无主体的生产包装。
func requireStandaloneCompositionBuilder(t *testing.T, file *ast.File) *ast.FuncDecl {
	t.Helper()
	if findFuncDecl(file, "newStandaloneApp") != nil {
		t.Fatal("production standalone nil-issuer wrapper must not exist")
	}
	builder := findFuncDecl(file, "newStandaloneAppWithPrincipalIssuer")
	if builder == nil {
		t.Fatal("principal-aware standalone composition missing")
	}
	return builder
}

func TestStandaloneModelsExposeConfiguredProviders(t *testing.T) {
	cfg := standalone.Config{Providers: []standalone.Provider{
		{Name: "codex", Models: []string{"gpt-test"}, Sandbox: "workspace-write"},
		{Name: "claude", Models: []string{"sonnet-test"}},
	}}
	providers, err := newStandaloneModels(cfg).ListProviders()
	if err != nil || len(providers) != 2 {
		t.Fatalf("standalone providers = %#v err=%v", providers, err)
	}
	for _, provider := range providers {
		if provider.Available == nil || !*provider.Available {
			t.Fatalf("standalone provider %q availability = %#v", provider.Provider, provider.Available)
		}
	}
}
