package main

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	orchcron "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/cron"
	taskdagstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	orchtools "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// stubRunStore / stubDAGStore / stubAgentThreadStore / stubAgentBindingStore 是 fx wiring 测试专用空实现。
// 它们只用于满足依赖图，不会被测试实际调用。
type stubRunStore struct{ taskdagstore.RunStore }
type stubScheduledStartStore struct {
	taskdagstore.ScheduledStartStore
}
type stubDAGStore struct {
	taskdagstore.OrchestrationStore
}
type stubAgentThreadStore struct{ orchestration.AgentThreadStore }
type stubAgentBindingStore struct {
	orchestration.AgentBindingStore
}
type stubDAGScheduleStore struct{}
type stubRuntimeLocker struct{}
type stubRuntimeLockHandle struct{}

func (stubDAGScheduleStore) DueDAGs(context.Context, time.Time) ([]orchcron.DueDAG, error) {
	return nil, nil
}

func (stubRuntimeLocker) TryLock(context.Context) (orchcron.RuntimeLockHandle, bool, error) {
	return stubRuntimeLockHandle{}, true, nil
}

func (stubRuntimeLockHandle) Renew(context.Context) error { return nil }

func (stubRuntimeLockHandle) Unlock(context.Context) error { return nil }

func TestParentFxStartup(t *testing.T) {
	// lifecycle group 必须由生产装配提供，测试不能镜像 fx.As 绑定。
	orchAssembly := orchestrationLifecycleOptions()
	app := fx.New(
		fx.NopLogger,
		orchAssembly,
		fx.Supply(slog.New(slog.NewTextHandler(io.Discard, nil))),
		fx.Supply(event.NewDispatcher()),
		fx.Provide(
			newNoopSessionCleaner,
			newNoopTurnStarter,
			func(lc fx.Lifecycle, turnStarter contract.OrchestrationTurnStarter, logger *slog.Logger) orchestration.AgentLauncher {
				return orchestration.NewLocalLauncher(turnStarter, logger)
			},
			// service 强依赖 RunStore，父级启动测试也必须补齐 stub provider。
			func() taskdagstore.RunStore { return &stubRunStore{} },
			func() taskdagstore.ScheduledStartStore { return &stubScheduledStartStore{} },
		),
	)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}
	t.Cleanup(func() { _ = app.Stop(context.Background()) })
}

func TestNewMCPOrchAppBuildsCompleteGraph(t *testing.T) {
	t.Setenv("PROJECT_ROOT", t.TempDir())
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_BOOTSTRAP", "test")
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", "")
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "127.0.0.1:0")
	previousStdout := mcpStdout.Swap(os.Stdout)
	t.Cleanup(func() { mcpStdout.Store(previousStdout) })

	for _, remoteAddr := range []string{"", "127.0.0.1:65535"} {
		t.Run(remoteAddr, func(t *testing.T) {
			t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", filepath.Join(t.TempDir(), "mcp-orch.db"))
			app := newMCPOrchApp(remoteAddr)
			if err := app.Err(); err != nil {
				t.Fatalf("newMCPOrchApp(%q).Err() = %v", remoteAddr, err)
			}
		})
	}
}

func TestBuildOrchestrationOptionsIncludesScheduledDAGCronRunner(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "fx.go", nil, 0)
	if err != nil {
		t.Fatalf("parse fx.go: %v", err)
	}
	fn := findFuncDecl(file, "buildOrchestrationOptions")
	if fn == nil {
		t.Fatal("buildOrchestrationOptions not found")
	}
	if !funcBodyContainsIdent(fn, "orchestrationDAGOptions") {
		t.Fatal("buildOrchestrationOptions must include orchestrationDAGOptions")
	}
	dagFile, err := parser.ParseFile(token.NewFileSet(), "fx_orchestration_dag.go", nil, 0)
	if err != nil {
		t.Fatalf("parse fx_orchestration_dag.go: %v", err)
	}
	dagOptions := findFuncDecl(dagFile, "orchestrationDAGOptions")
	if dagOptions == nil {
		t.Fatal("orchestrationDAGOptions not found")
	}
	if !funcBodyContainsIdent(dagOptions, "provideSQLDAGScheduleStore") {
		t.Fatal("orchestrationDAGOptions must provide scheduled DAG SQL schedule store")
	}
	if !funcBodyContainsIdent(dagOptions, "provideSQLiteRuntimeLocker") {
		t.Fatal("orchestrationDAGOptions must provide scheduled DAG SQLite runtime locker")
	}
	if !funcBodyAnnotatesRunner(dagOptions, "provideScheduledDAGCronRunner") {
		t.Fatal("orchestrationDAGOptions must annotate provideScheduledDAGCronRunner into group:\"runners\"")
	}
}

func TestRunIncludesDBMigrationLifecycleModule(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "fx.go", nil, 0)
	if err != nil {
		t.Fatalf("parse fx.go: %v", err)
	}
	fn := findFuncDecl(file, "run")
	if fn == nil {
		t.Fatal("run not found")
	}
	if !funcBodyContainsIdent(fn, "newMCPOrchApp") {
		t.Fatal("run must construct the application through newMCPOrchApp")
	}
	appBuilder := findFuncDecl(file, "newMCPOrchApp")
	if appBuilder == nil {
		t.Fatal("newMCPOrchApp not found")
	}
	if !funcBodyContainsSelector(appBuilder, "platformdb", "Module") {
		t.Fatal("newMCPOrchApp must include platformdb.Module so standalone mcp-orch creates and migrates a fresh database")
	}
	if funcBodyContainsIdent(appBuilder, "registerPoolLifecycle") {
		t.Fatal("newMCPOrchApp must not use the close-only pool lifecycle instead of platformdb.Module")
	}
}

func findFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

func funcBodyContainsSelector(fn *ast.FuncDecl, xName, selName string) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != selName {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if ok && x.Name == xName {
			found = true
			return false
		}
		return true
	})
	return found
}

func funcBodyContainsIdent(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if ok && ident.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

func funcBodyAnnotatesRunner(fn *ast.FuncDecl, provider string) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isSelector(call.Fun, "Annotate") || len(call.Args) == 0 {
			return true
		}
		ident, ok := call.Args[0].(*ast.Ident)
		if !ok || ident.Name != provider {
			return true
		}
		if callHasRunnerGroupTag(call.Args[1:]) {
			found = true
			return false
		}
		return true
	})
	return found
}

func callHasRunnerGroupTag(args []ast.Expr) bool {
	for _, arg := range args {
		call, ok := arg.(*ast.CallExpr)
		if !ok || !isSelector(call.Fun, "ResultTags") || len(call.Args) == 0 {
			continue
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok {
			continue
		}
		value, err := strconv.Unquote(lit.Value)
		if err == nil && value == `group:"runners"` {
			return true
		}
	}
	return false
}

func isSelector(expr ast.Expr, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name
}

// TestFxStoresAllProvided 是干式装配断言：service 依赖的四个 Store 字段
// (DAGStore / RunStore / AgentThreads / AgentBindings) 必须能被 fx 解出。
// 任一字段未注册 provider 时，optional 字段会静默变零值，required 字段会报 missing dependency；
// 该测试只验证逐字段 resolve，不拉起 lifecycle，也不连接数据库。
func TestFxStoresAllProvided(t *testing.T) {
	type consumeStores struct {
		fx.In
		DAGStore      taskdagstore.OrchestrationStore
		RunStore      taskdagstore.RunStore
		AgentThreads  orchestration.AgentThreadStore
		AgentBindings orchestration.AgentBindingStore
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func() taskdagstore.OrchestrationStore { return &stubDAGStore{} },
			func() taskdagstore.RunStore { return &stubRunStore{} },
			func() orchestration.AgentThreadStore { return &stubAgentThreadStore{} },
			func() orchestration.AgentBindingStore { return &stubAgentBindingStore{} },
		),
		fx.Invoke(func(s consumeStores) error {
			if s.DAGStore == nil {
				return errors.New("DAGStore not wired")
			}
			if s.RunStore == nil {
				return errors.New("RunStore not wired")
			}
			if s.AgentThreads == nil {
				return errors.New("AgentThreads not wired")
			}
			if s.AgentBindings == nil {
				return errors.New("AgentBindings not wired")
			}
			return nil
		}),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
	}
}

func TestHandleScopedToolsCallWithCallerUsesTrustedScope(t *testing.T) {
	params := json.RawMessage(`{"name":"list_agents","arguments":{"agent_id":"evil","cwd":"/evil"},"_agentId":"agent-1","_threadId":"thread-1","_callId":"call-1","_cwd":"/trusted/orch"}`)
	called := false

	result, err := handleScopedToolsCallWithCaller(context.Background(), "orch", params, scopedToolsCallVerifier(t, &called))
	if err != nil {
		t.Fatalf("handleScopedToolsCallWithCaller() error = %v", err)
	}
	if !called {
		t.Fatal("caller was not invoked")
	}
	if payload, ok := result.(map[string]any); !ok || payload["structuredContent"] == nil {
		t.Fatalf("result = %#v, want structured content", result)
	}
}

func TestHandleScopedToolsCallRejectsLegacyAliasThroughRegistryProvider(t *testing.T) {
	registry := orchtools.NewRegistry(orchtools.Dependencies{})
	provider := registryToolProvider{registry: registry}
	params := json.RawMessage(`{"name":"orchestration_list_agents","arguments":{},"_agentId":"agent-1"}`)

	result, err := handleScopedToolsCall(context.Background(), provider, "orch", params)
	if err != nil {
		t.Fatalf("handleScopedToolsCall() error = %v, want structured unknown tool error", err)
	}
	envelope := decodeScopedToolEnvelope(t, result)
	if envelope.Success {
		t.Fatalf("Success = true, want false for legacy alias")
	}
	if !strings.Contains(envelope.Error, "unknown tool: orchestration_list_agents") {
		t.Fatalf("Error = %q, want unknown legacy alias", envelope.Error)
	}
}

func TestHandleScopedToolsCallWithCallerWrapsSelectedToolErrors(t *testing.T) {
	params := json.RawMessage(`{"name":"list_agents","arguments":{},"_agentId":"agent-1"}`)
	toolErr := errors.New("store unavailable")

	result, err := handleScopedToolsCallWithCaller(context.Background(), "orch", params, func(context.Context, string, json.RawMessage) (any, error) {
		return nil, toolErr
	})
	if err != nil {
		t.Fatalf("handleScopedToolsCallWithCaller() error = %v, want structured tool error", err)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want map", result)
	}
	raw, ok := payload["structuredContent"].(json.RawMessage)
	if !ok {
		t.Fatalf("structuredContent = %T, want json.RawMessage", payload["structuredContent"])
	}
	if isError, _ := payload["isError"].(bool); !isError {
		t.Fatalf("isError = %v, want true", payload["isError"])
	}
	var envelope common.ToolErrorEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if envelope.Success || envelope.Error != toolErr.Error() {
		t.Fatalf("envelope = %+v, want failed tool envelope", envelope)
	}
}

func TestHandleScopedToolsCallWithCallerUsesOrchestrationClassifier(t *testing.T) {
	params := json.RawMessage(`{"name":"task_create_dag","arguments":{},"_agentId":"agent-1"}`)

	result, err := handleScopedToolsCallWithCaller(context.Background(), "orch", params, func(context.Context, string, json.RawMessage) (any, error) {
		return nil, platformdb.ErrConflict
	})
	if err != nil {
		t.Fatalf("handleScopedToolsCallWithCaller() error = %v", err)
	}
	envelope := decodeScopedToolEnvelope(t, result)
	if envelope.Code != "invalid_input" {
		t.Fatalf("Code = %q, want invalid_input", envelope.Code)
	}
	if envelope.Hint != "next: choose a new dag_key or update the existing DAG with task_dag_apply_ops" {
		t.Fatalf("Hint = %q", envelope.Hint)
	}
}

func TestHandleScopedToolsCallWithCallerWrapsLaunchAgentContractErrors(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		err      error
		wantCode string
	}{
		{name: "missing cwd", toolName: "launch_agent", err: contract.ErrLaunchCWDRequired, wantCode: "cwd_required"},
		{name: "invalid cwd", toolName: "launch_agent", err: contract.ErrLaunchCWDInvalid, wantCode: "cwd_invalid"},
		{name: "missing provider", toolName: "launch_agent", err: errors.New("provider is required"), wantCode: "provider_required"},
		{name: "invalid provider", toolName: "launch_agent", err: errors.New(`invalid provider "openai"`), wantCode: "provider_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := json.RawMessage(`{"name":"` + tt.toolName + `","arguments":{},"_agentId":"agent-1"}`)
			result, err := handleScopedToolsCallWithCaller(context.Background(), "orch", params, func(context.Context, string, json.RawMessage) (any, error) {
				return nil, tt.err
			})
			if err != nil {
				t.Fatalf("handleScopedToolsCallWithCaller() error = %v", err)
			}
			envelope := decodeScopedToolEnvelope(t, result)
			if envelope.Code != tt.wantCode {
				t.Fatalf("Code = %q, want %s", envelope.Code, tt.wantCode)
			}
			if envelope.Retryable {
				t.Fatal("Retryable = true, want false")
			}
			assertNoLSPHint(t, envelope.Hint)
		})
	}
}

func decodeScopedToolEnvelope(t *testing.T, result any) common.ToolErrorEnvelope {
	t.Helper()
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result = %#v, want map", result)
	}
	if isError, _ := payload["isError"].(bool); !isError {
		t.Fatalf("isError = %v, want true", payload["isError"])
	}
	raw, ok := payload["structuredContent"].(json.RawMessage)
	if !ok {
		t.Fatalf("structuredContent = %T, want json.RawMessage", payload["structuredContent"])
	}
	var envelope common.ToolErrorEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	return envelope
}

func assertNoLSPHint(t *testing.T, hint string) {
	t.Helper()
	for _, forbidden := range []string{"lsp", "language server"} {
		if containsFold(hint, forbidden) {
			t.Fatalf("Hint = %q, must not mention %s", hint, forbidden)
		}
	}
}

func containsFold(value, substr string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(substr))
}

func scopedToolsCallVerifier(t *testing.T, called *bool) func(context.Context, string, json.RawMessage) (any, error) {
	t.Helper()

	return func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		*called = true
		assertTrustedScopedToolsCall(t, ctx, name, args)
		return map[string]any{"ok": true}, nil
	}
}

func assertTrustedScopedToolsCall(t *testing.T, ctx context.Context, name string, args json.RawMessage) {
	t.Helper()

	if name != "list_agents" {
		t.Fatalf("tool name = %q, want list_agents", name)
	}
	scope, ok := common.ToolScopeFromContext(ctx)
	if !ok {
		t.Fatal("ToolScopeFromContext() missing scope")
	}
	if scope.AgentID != "agent-1" {
		t.Fatalf("scope.AgentID = %q, want agent-1", scope.AgentID)
	}
	if scope.ThreadID != "thread-1" {
		t.Fatalf("scope.ThreadID = %q, want thread-1", scope.ThreadID)
	}
	if scope.CallID != "call-1" {
		t.Fatalf("scope.CallID = %q, want call-1", scope.CallID)
	}
	if got := common.WorkspaceRootFromContext(ctx, "/fallback"); got != "/trusted/orch" {
		t.Fatalf("WorkspaceRootFromContext() = %q, want /trusted/orch", got)
	}
	if !json.Valid(args) {
		t.Fatalf("args is not valid json: %s", args)
	}
}
