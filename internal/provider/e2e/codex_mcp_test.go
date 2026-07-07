package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	"github.com/gorilla/websocket"
	"go.uber.org/fx"
)

func TestCodexStartSession_InjectsDynamicTools_E2E(t *testing.T) {
	recorder := &codexRPCRecorder{}
	serverURL := startCodexRPCServer(t, recorder)

	factory := newCodexE2EDriverFactory(t, serverURL)
	factory.SetListTools(func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		return []codexprotocol.DynamicToolSchema{{
			Name:        "tool.echo",
			Description: "echo payload",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}}, nil
	})

	workDir := t.TempDir()
	session, err := factory.Create().StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-1",
		CWD:     workDir,
		StartAssembly: dto.StartAssembly{
			BaseInstructions:      "base prompt",
			DeveloperInstructions: "developer prompt",
		},
		Config: map[string]any{
			"mcp": map[string]any{"legacy": true},
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	if session.ThreadID() != "provider-thread-1" {
		t.Fatalf("ThreadID() = %q, want provider-thread-1", session.ThreadID())
	}
	if calls := recorder.calls("thread/start"); calls != 1 {
		t.Fatalf("thread/start calls = %d, want 1", calls)
	}

	params := recorder.threadStartParamsSnapshot()
	assertDynamicToolNames(t, params, []string{"tool.echo"})
	assertNoLegacyMCPKeys(t, params)
	if params["cwd"] != workDir {
		t.Fatalf("cwd = %#v, want %q", params["cwd"], workDir)
	}
	if params["approvalPolicy"] != "never" {
		t.Fatalf("approvalPolicy = %#v, want never", params["approvalPolicy"])
	}
	if params["baseInstructions"] != "base prompt" {
		t.Fatalf("baseInstructions = %#v, want base prompt", params["baseInstructions"])
	}
	if params["developerInstructions"] != "developer prompt" {
		t.Fatalf("developerInstructions = %#v, want developer prompt", params["developerInstructions"])
	}
}

func TestCodexStartSession_InjectsHostMemoryReadAndFiltersPeerMemoryRead_E2E(t *testing.T) {
	recorder := &codexRPCRecorder{}
	serverURL := startCodexRPCServer(t, recorder)

	handler := newCodexMemoryReadToolBridgeHandler(t)
	factory := newCodexE2EDriverFactory(t, serverURL)
	factory.SetListTools(handler.ListToolsForCodex)

	session, err := factory.Create().StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-memory-read",
		CWD:     t.TempDir(),
		StartAssembly: dto.StartAssembly{
			BaseInstructions: "base prompt",
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	params := recorder.threadStartParamsSnapshot()
	assertDynamicToolNames(t, params, []string{toolbridge.ToolNameMemoryRead, "orchestration_launch_agent", "lsp_hover"})
	tools := dynamicToolsFromParams(t, params)
	if countDynamicToolName(tools, toolbridge.ToolNameMemoryRead) != 1 {
		t.Fatalf("dynamicTools = %#v, want exactly one memory_read", tools)
	}
	memoryRead := dynamicToolByName(t, tools, toolbridge.ToolNameMemoryRead)
	if memoryRead["description"] == "peer memory read must be filtered" {
		t.Fatalf("memory_read came from peer list, want host-direct schema: %#v", memoryRead)
	}
	inputSchema, ok := memoryRead["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("memory_read inputSchema = %#v, want object", memoryRead["inputSchema"])
	}
	properties, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("memory_read schema properties = %#v, want object", inputSchema["properties"])
	}
	for _, key := range []string{"name", "path", "scope", "type"} {
		if _, ok := properties[key]; !ok {
			t.Fatalf("memory_read schema properties missing %q: %#v", key, properties)
		}
	}
}

func newCodexMemoryReadToolBridgeHandler(t *testing.T) *toolbridge.Handler {
	t.Helper()
	var handler *toolbridge.Handler
	app := fx.New(
		fx.NopLogger,
		fx.Supply(newCodexToolBridgeRegistry()),
		fx.Provide(func(reg codexToolBridgeRegistry) *toolbridge.Handler {
			return toolbridge.NewHandlerForTesting(reg, toolbridge.NewCompositeHostToolRegistry(
				toolbridge.NewMemoryReadHostToolRegistry(
					&codexMemoryReaderStub{enabled: true, toolsEnabled: true},
					toolbridge.MemoryReadHostToolOptions{Enabled: true, ToolsEnabled: true},
				),
			))
		}),
		fx.Populate(&handler),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New(toolbridge handler) error = %v", err)
	}
	t.Cleanup(func() { _ = app.Stop(context.Background()) })
	return handler
}

type codexToolBridgeRegistry struct {
	peers map[string][]*mcpcontrol.ToolInstance
}

func (r codexToolBridgeRegistry) FindActiveByKind(clientKind string) []*mcpcontrol.ToolInstance {
	return r.peers[clientKind]
}

func newCodexToolBridgeRegistry() codexToolBridgeRegistry {
	return codexToolBridgeRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
		mcpdto.ClientKindOrch: {
			codexToolInstance(mcpdto.ClientKindOrch, codexListToolsPeer([]mcpdto.MCPTool{
				{Name: toolbridge.ToolNameMemoryRead, Description: "peer memory read must be filtered", InputSchema: json.RawMessage(`{"type":"object","properties":{"peer":{"type":"boolean"}}}`)},
				{Name: "orchestration_launch_agent", Description: "peer orch", InputSchema: json.RawMessage(`{"type":"object"}`)},
			})),
		},
		mcpdto.ClientKindLSP: {
			codexToolInstance(mcpdto.ClientKindLSP, codexListToolsPeer([]mcpdto.MCPTool{
				{Name: "lsp_hover", Description: "peer lsp", InputSchema: json.RawMessage(`{"type":"object"}`)},
			})),
		},
	}}
}

func codexToolInstance(clientKind string, peer *codexToolBridgePeer) *mcpcontrol.ToolInstance {
	return &mcpcontrol.ToolInstance{ClientKind: clientKind, Status: mcpdto.StatusActive, Peer: peer}
}

type codexMemoryReaderStub struct {
	enabled      bool
	toolsEnabled bool
}

func (s *codexMemoryReaderStub) ReadAgentMemory(_ context.Context, req contract.MemoryReadRequest) (contract.MemoryReadResult, error) {
	return contract.MemoryReadResult{Entry: &contract.MemoryEntry{Name: req.Name, Type: req.Type, Content: "memory content"}, SourcePath: "feedback/read.md", IndexHit: true}, nil
}

func (s *codexMemoryReaderStub) MemoryReadEnabled() bool {
	return s == nil || s.enabled
}

func (s *codexMemoryReaderStub) MemoryReadToolsEnabled() bool {
	return s == nil || s.toolsEnabled
}

func codexListToolsPeer(tools []mcpdto.MCPTool) *codexToolBridgePeer {
	return &codexToolBridgePeer{tools: tools}
}

type codexToolBridgePeer struct {
	tools []mcpdto.MCPTool
}

func (p *codexToolBridgePeer) Notify(context.Context, string, any) error { return nil }

func (p *codexToolBridgePeer) Callback(_ context.Context, method string, _ any, result any) error {
	if method != "tools/list" {
		return nil
	}
	raw, err := json.Marshal(map[string]any{"tools": p.tools})
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

func (p *codexToolBridgePeer) Close() error { return nil }

func TestCodexStartSession_PreservesUserConfigFields_E2E(t *testing.T) {
	recorder := &codexRPCRecorder{}
	serverURL := startCodexRPCServer(t, recorder)

	factory := newCodexE2EDriverFactory(t, serverURL)
	factory.SetListTools(func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		return []codexprotocol.DynamicToolSchema{
			{
				Name:        "tool.echo",
				Description: "echo payload",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
			{
				Name:        "tool.sum",
				Description: "sum payload",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
		}, nil
	})

	session, err := factory.Create().StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-2",
		CWD:     t.TempDir(),
		Model:   "gpt-5-codex",
		StartAssembly: dto.StartAssembly{
			BaseInstructions:      "base prompt",
			DeveloperInstructions: "developer prompt",
		},
		Config: map[string]any{
			"approval_policy": "on-request",
			"modelProvider":   "openai",
			"personality":     "reviewer",
			"summary":         "brief",
			"effort":          "high",
			"sandbox": map[string]any{
				"mode":           "workspace-write",
				"network_access": false,
			},
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	params := recorder.threadStartParamsSnapshot()
	assertDynamicToolNames(t, params, []string{"tool.echo", "tool.sum"})
	assertCodexUserConfigFields(t, params)
}

func TestCodexStartSession_ReconcilesNativeSkillMirrorsBeforeProviderStart_E2E(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	if err := os.MkdirAll(filepath.Join(userHome, ".codex"), 0o700); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	events := []string{}
	recorder := &codexRPCRecorder{events: &events}
	serverURL := startCodexRPCServer(t, recorder)

	workDir := t.TempDir()
	mirror := &recordingCodexE2ESkillMirrorReconciler{
		events:    &events,
		skillName: "provider-native-proof",
	}
	factory := newCodexE2EDriverFactoryWithMirror(t, serverURL, mirror, func() {
		events = append(events, "pool")
	})
	factory.SetListTools(func(context.Context) ([]codexprotocol.DynamicToolSchema, error) {
		return nil, nil
	})

	session, err := factory.Create().StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-provider-native",
		CWD:     workDir,
		StartAssembly: dto.StartAssembly{
			BaseInstructions: "base prompt",
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	params := recorder.threadStartParamsSnapshot()
	if params["cwd"] != workDir {
		t.Fatalf("thread/start cwd = %#v, want %q", params["cwd"], workDir)
	}
	if strings.Join(events, ",") != "reconcile,pool,thread/start" {
		t.Fatalf("events = %v, want reconcile before provider pool and thread/start", events)
	}
	if mirror.cwd != workDir {
		t.Fatalf("mirror cwd = %q, want %q", mirror.cwd, workDir)
	}
	assertCodexE2EMirrorTargets(t, mirror.targets, workDir, userHome)
	projectSkillPath := filepath.Join(codexE2EProjectSkillsRoot(t, mirror.targets), "provider-native-proof", "SKILL.md")
	raw, err := os.ReadFile(projectSkillPath)
	if err != nil {
		t.Fatalf("ReadFile mirrored project skill: %v", err)
	}
	if !strings.Contains(string(raw), "SD_PROJECT_SKILL_OK") {
		t.Fatalf("mirrored project skill = %q, want SD_PROJECT_SKILL_OK sentinel", raw)
	}
}

func assertCodexUserConfigFields(t *testing.T, params map[string]any) {
	t.Helper()
	if params["model"] != "gpt-5-codex" {
		t.Fatalf("model = %#v, want gpt-5-codex", params["model"])
	}
	if params["approvalPolicy"] != "on-request" {
		t.Fatalf("approvalPolicy = %#v, want on-request", params["approvalPolicy"])
	}
	if params["modelProvider"] != "openai" {
		t.Fatalf("modelProvider = %#v, want openai", params["modelProvider"])
	}
	if params["personality"] != "reviewer" {
		t.Fatalf("personality = %#v, want reviewer", params["personality"])
	}
	if params["summary"] != "brief" {
		t.Fatalf("summary = %#v, want brief", params["summary"])
	}
	if params["effort"] != "high" {
		t.Fatalf("effort = %#v, want high", params["effort"])
	}
	if params["sandbox"] != "workspace-write" {
		t.Fatalf("sandbox = %#v, want workspace-write", params["sandbox"])
	}
}

func newCodexE2EDriverFactory(t *testing.T, serverURL string) *codexapp.DriverFactory {
	t.Helper()
	return newCodexE2EDriverFactoryWithMirror(t, serverURL, noopCodexE2ESkillMirrorReconciler{}, nil)
}

func newCodexE2EDriverFactoryWithMirror(t *testing.T, serverURL string, mirror contract.SkillMirrorReconciler, onPoolAcquire func()) *codexapp.DriverFactory {
	t.Helper()
	t.Setenv(providershared.SuperDolphinHomeEnv, t.TempDir())
	pool := codexapp.NewServerPool(nil, func(context.Context, string, string) (codexapp.SpawnedServer, error) {
		if onPoolAcquire != nil {
			onPoolAcquire()
		}
		return newCodexE2EFakeServer(serverURL), nil
	}, codexapp.PoolConfig{})
	t.Cleanup(func() { _ = pool.Close(context.Background()) })
	return codexapp.NewDriverFactory(nil, nil, nil, nil, nil, pool, mirror, nil)
}

type noopCodexE2ESkillMirrorReconciler struct{}

func (noopCodexE2ESkillMirrorReconciler) ReconcileProviderMirrors(context.Context, string, []contract.SkillProviderMirrorTarget) (contract.SkillMirrorReport, error) {
	return contract.SkillMirrorReport{}, nil
}

type recordingCodexE2ESkillMirrorReconciler struct {
	events    *[]string
	skillName string
	cwd       string
	targets   []contract.SkillProviderMirrorTarget
}

func (r *recordingCodexE2ESkillMirrorReconciler) ReconcileProviderMirrors(_ context.Context, cwd string, targets []contract.SkillProviderMirrorTarget) (contract.SkillMirrorReport, error) {
	if r.events != nil {
		*r.events = append(*r.events, "reconcile")
	}
	r.cwd = cwd
	r.targets = append([]contract.SkillProviderMirrorTarget(nil), targets...)
	for _, target := range targets {
		if target.Provider != providershared.ProviderCodex {
			continue
		}
		if err := writeCodexE2ENativeSkillMirror(target.SkillsRoot, r.skillName); err != nil {
			return contract.SkillMirrorReport{}, fmt.Errorf("write provider-native mirror target %#v: %w", target, err)
		}
	}
	return contract.SkillMirrorReport{}, nil
}

func writeCodexE2ENativeSkillMirror(root, name string) error {
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := "---\nname: " + name + "\ndescription: provider native proof\n---\n\nSD_PROJECT_SKILL_OK\n"
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644)
}

func assertCodexE2EMirrorTargets(t *testing.T, targets []contract.SkillProviderMirrorTarget, workDir, userHome string) {
	t.Helper()
	if len(targets) != 2 {
		t.Fatalf("mirror targets = %#v, want personal + project targets", targets)
	}
	realWorkDir, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		t.Fatalf("EvalSymlinks workDir: %v", err)
	}
	wantProjectSkills := filepath.Join(realWorkDir, ".agents", "skills")
	if targets[1].Provider != providershared.ProviderCodex || targets[1].SkillsRoot != wantProjectSkills {
		t.Fatalf("project mirror target = %#v, want codex skills root %q", targets[1], wantProjectSkills)
	}
	wantPersonalSkills := filepath.Join(userHome, ".agents", "skills")
	if targets[0].Provider != providershared.ProviderCodex || targets[0].SkillsRoot != wantPersonalSkills {
		t.Fatalf("personal mirror target = %#v, want user-global codex skills root %q", targets[0], wantPersonalSkills)
	}
}

func codexE2EProjectSkillsRoot(t *testing.T, targets []contract.SkillProviderMirrorTarget) string {
	t.Helper()
	for _, target := range targets {
		if target.Provider == providershared.ProviderCodex && strings.Contains(target.SkillsRoot, string(filepath.Separator)+".agents"+string(filepath.Separator)) {
			return target.SkillsRoot
		}
	}
	t.Fatalf("mirror targets = %#v, want project codex skills root", targets)
	return ""
}

type codexE2EFakeServer struct {
	url    string
	alive  atomic.Bool
	closed atomic.Bool
}

func newCodexE2EFakeServer(url string) *codexE2EFakeServer {
	server := &codexE2EFakeServer{url: url}
	server.alive.Store(true)
	return server
}

func (s *codexE2EFakeServer) ServerURL() string { return s.url }

func (s *codexE2EFakeServer) Close(context.Context) error {
	s.closed.Store(true)
	s.alive.Store(false)
	return nil
}

func (s *codexE2EFakeServer) Alive() bool { return s.alive.Load() }

type codexRPCRecorder struct {
	mu                sync.Mutex
	events            *[]string
	callCount         map[string]int
	methodParams      map[string][]map[string]any
	threadStartParams map[string]any
}

func (r *codexRPCRecorder) record(method string, raw json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.callCount == nil {
		r.callCount = make(map[string]int)
	}
	r.callCount[method]++
	if len(raw) == 0 {
		return
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}
	if r.methodParams == nil {
		r.methodParams = make(map[string][]map[string]any)
	}
	r.methodParams[method] = append(r.methodParams[method], cloneAnyMap(params))
	if method != "thread/start" {
		return
	}
	if r.events != nil {
		*r.events = append(*r.events, "thread/start")
	}
	r.threadStartParams = cloneAnyMap(params)
}

func (r *codexRPCRecorder) calls(method string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callCount[method]
}

func (r *codexRPCRecorder) threadStartParamsSnapshot() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneAnyMap(r.threadStartParams)
}

func startCodexRPCServer(t *testing.T, recorder *codexRPCRecorder) string {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, rawBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(rawBytes, &msg); err != nil {
				continue
			}
			recorder.record(msg.Method, msg.Params)
			if len(msg.ID) == 0 {
				continue
			}
			result := codexRPCResult(msg.Method)
			resp, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
				"result":  result,
			})
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func codexRPCResult(method string) map[string]any {
	switch method {
	case "model/list":
		return map[string]any{"models": []map[string]any{{"id": "gpt-5"}}}
	case "thread/start":
		return map[string]any{"thread": map[string]any{"id": "provider-thread-1"}}
	case "turn/start":
		return map[string]any{"turn": map[string]any{"id": "provider-turn-1"}}
	default:
		return map[string]any{"ok": true}
	}
}

func assertDynamicToolNames(t *testing.T, params map[string]any, want []string) {
	t.Helper()

	rawTools, ok := params["dynamicTools"].([]any)
	if !ok {
		t.Fatalf("dynamicTools = %#v, want array", params["dynamicTools"])
	}
	got := make([]string, 0, len(rawTools))
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			t.Fatalf("dynamicTools item = %#v, want object", rawTool)
		}
		name, _ := tool["name"].(string)
		got = append(got, name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dynamic tool names = %#v, want %#v", got, want)
	}
}

func dynamicToolsFromParams(t *testing.T, params map[string]any) []any {
	t.Helper()
	rawTools, ok := params["dynamicTools"].([]any)
	if !ok {
		t.Fatalf("dynamicTools = %#v, want array", params["dynamicTools"])
	}
	return rawTools
}

func dynamicToolByName(t *testing.T, tools []any, name string) map[string]any {
	t.Helper()
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("dynamicTools item = %#v, want object", raw)
		}
		if tool["name"] == name {
			return tool
		}
	}
	t.Fatalf("dynamicTools = %#v, missing %q", tools, name)
	return nil
}

func countDynamicToolName(tools []any, name string) int {
	count := 0
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool["name"] == name {
			count++
		}
	}
	return count
}

func assertNoLegacyMCPKeys(t *testing.T, params map[string]any) {
	t.Helper()
	for _, key := range []string{"mcp", "mcpConfig", "mcp_config", "mcpServers", "mcp_servers"} {
		if _, ok := params[key]; ok {
			t.Fatalf("%s = %#v, want omitted from thread/start", key, params[key])
		}
	}
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]any, len(src))
	maps.Copy(out, src)
	return out
}
