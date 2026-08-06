package toolbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
)

func TestManagedAdmissionFailureStopsRealDriverBeforeRemoteThreadStart(t *testing.T) {
	var remoteStarts atomic.Int32
	serverURL := startRecoveryDriverRPCServer(t, &remoteStarts)
	pool := codexapp.NewServerPool(nil, func(context.Context, string, string) (codexapp.SpawnedServer, error) {
		return recoveryDriverSpawnedServer{url: serverURL}, nil
	}, codexapp.PoolConfig{})
	t.Cleanup(func() { _ = pool.Close(context.Background()) })
	executor := &task4BSchemaExecutor{
		failCodes: map[string]schema.Code{"unsafe": schema.CodeReapFailed},
		failText:  task4BRecoverySecret(),
	}
	handler := task4BHandler(
		newTask4BAuthorityOwner(),
		executor,
		&task4BMCPClient{tools: []mcpdto.MCPTool{task4BTool("unsafe", `{"type":"object"}`)}},
	)
	factory := codexapp.NewDriverFactory(nil, nil, platformrpc.NewApprovalManager(nil, nil), nil, &codexapp.ServerManager{}, pool, recoveryDriverSkillMirror{}, nil)
	factory.SetLogRuntime(pkglogger.NewRuntime(pkglogger.RuntimeConfig{}))
	factory.SetSkillMetrics(skillmetrics.NewRegistry())
	factory.SetPrepareTools(func(ctx context.Context, scope contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error) {
		tools, err := handler.PrepareCodexToolSurface(ctx, scope)
		if err != nil {
			return nil, err
		}
		out := make([]codexprotocol.DynamicToolSchema, len(tools))
		for index, tool := range tools {
			out[index] = codexprotocol.DynamicToolSchema(tool)
		}
		return out, nil
	})
	factory.SetReleaseTools(handler.ReleaseCodexToolSurface)

	session, err := factory.Create().StartSession(context.Background(), providerdto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "managed-admission-agent",
		CWD:           t.TempDir(),
		StartAssembly: providerdto.StartAssembly{BaseInstructions: "test base instructions"},
		Config: map[string]any{"mcpConfig": map[string]any{"mcpServers": map[string]any{
			"external": map[string]any{
				"trustedServerId": "external",
				"transport":       "stdio",
				"command":         "npx",
				"args":            []any{"-y", "@bytebase/dbhub@0.23.0", "--dsn=sqlite:///tmp/test.db"},
			},
		}}},
	})
	if session != nil {
		t.Fatalf("StartSession() session = %#v, want nil", session)
	}
	failure, ok := contract.RecoveryFailureFromError(err)
	if !ok || failure.Code != string(schema.CodeReapFailed) {
		t.Fatalf("StartSession() error = %v; recovery failure = %#v, %v", err, failure, ok)
	}
	if got := remoteStarts.Load(); got != 0 {
		t.Fatalf("remote thread/start calls = %d, want 0", got)
	}
}

type recoveryDriverSkillMirror struct{}

func (recoveryDriverSkillMirror) ReconcileProviderMirrors(context.Context, string, []contract.SkillProviderMirrorTarget) (contract.SkillMirrorReport, error) {
	return contract.SkillMirrorReport{}, nil
}

type recoveryDriverSpawnedServer struct{ url string }

func (server recoveryDriverSpawnedServer) ServerURL() string { return server.url }

func (recoveryDriverSpawnedServer) Close(context.Context) error { return nil }

func (recoveryDriverSpawnedServer) Alive() bool { return true }

func startRecoveryDriverRPCServer(t *testing.T, remoteStarts *atomic.Int32) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		serveRecoveryDriverRPC(conn, remoteStarts)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func serveRecoveryDriverRPC(conn *websocket.Conn, remoteStarts *atomic.Int32) {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(raw, &request) != nil || len(request.ID) == 0 {
			continue
		}
		result := map[string]any{"ok": true}
		if request.Method == "initialize" {
			result = map[string]any{"codexHome": "/tmp/codex-home"}
		}
		if request.Method == "thread/start" {
			remoteStarts.Add(1)
			result = map[string]any{"thread": map[string]any{"id": "unexpected-thread"}}
		}
		response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		if conn.WriteMessage(websocket.TextMessage, response) != nil {
			return
		}
	}
}
