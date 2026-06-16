# MCP LSP Error Transparency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make built-in MCP LSP tools fail closed when peer launch, workspace roots, language server lifecycle, search, edit, diagnostics, or sandbox execution fails, so users never receive a false successful empty/no-op result when the requested operation failed.

**Architecture:** Carry one error truth across all layers: Go tool handlers return explicit errors for failed requested work, common MCP responses set the outer `isError` bit whenever the structured payload is unsuccessful, toolbridge preserves that bit into Codex/Claude results, and the Vue activity UI treats parsed tool payload failures as failed even if the transport status says completed. Best-effort enrichment remains allowed only when the response exposes a warning/error count in machine-readable metadata.

**Tech Stack:** Go, MCP JSON-RPC, `internal/mcpserver/common`, `internal/platform/toolbridge`, Codex app peer supervision, `cmd/mcp-lsp` tools/search/exec/manager/multilsp, Vue frontend activity rendering, guarded package tests via `./scripts/test_with_guard.sh`, frontend checks in `cmd/agent-terminal/frontend`.

---

## Root Cause Summary

Explorers found no accepted permanent P0 in committed architecture, but the current dirty `peer_supervisor` change creates a P0 until corrected: it requires global `GO_AGENT_LSP_ROOT(S)` before launching the singleton `mcp-lsp` peer, while the supervisor has no reliable global root source and then logs launch failure as degraded. That can make all LSP tools disappear or become `toolbridge: no active peer`.

The repeated P1 pattern is the same across layers:

| Severity | Area | Root cause | User-visible false success |
| --- | --- | --- | --- |
| P0 | Codex peer launch | `mcp-lsp` workspace roots required at process launch but not injected by `PeerSupervisor`; launch failure is warn+skip | LSP peer never registers and tool table can still be partially returned |
| P1 | MCP/toolbridge error bits | `ToolErrorEnvelope{success:false}` is only payload data; common server omits `isError`, toolbridge ignores structured success | Claude/Codex see a successful tool result containing JSON error text |
| P1 | Tool list aggregation | `ListToolsForCodex` warns and continues when LSP peer listing fails | UI/model sees a partial tool set with LSP missing |
| P1 | Diagnostics/bootstrap | diagnostic wait timeout returns nil; zero diagnostics deletes snapshots; partial bootstrap errors are dropped | `diagnostics` returns `success:true` and "no diagnostics" while LSP never answered |
| P1 | Bootstrap state | wait channels close without carrying success/error; stale/delete can wake waiters as success | later LSP requests run on incomplete bootstrap |
| P1 | Persistent cache | explicit persistent cache setup/read/write failure falls back to memory | configured persistence silently disappears |
| P1 | Go workspace root | broken `go.work` parse returns a go_work root with nil error | gopls starts against a corrupted workspace |
| P1 | LSP lifecycle/env | language bootstrap `DidOpen` errors are logged only; non-env factory drops `cfg.env` | workspace symbols/completions use missing seed docs or wrong `GOWORK` |
| P1 | File/read/edit tools | `open_file` ignores `DidOpen`; batch read and edit failures return unsuccessful payloads with nil tool error | requested file/LSP sync/edit work appears handled |
| P1 | Search tools | invalid regex/glob inputs, walk errors, `sg` exit/JSON errors are converted to empty/partial results | search returns no matches instead of reporting failed search |
| P1 | Sandbox | infrastructure errors become ordinary `CodeRunFailure`; output buffer drops tail errors; Windows guard fail-open | code execution failure details are hidden or sandbox guard failure is invisible |
| P1 | Installer/language routing | installed binary absolute path discarded; `.tsx/.jsx` map to non-React language IDs | auto-install says success but later launch fails; TSX/JSX parse context is degraded |
| P1 | Frontend activity UI | timeline top-level status can be `completed` while `preview` JSON contains `success:false` | UI shows "已完成" or green/success state for failed tools |

P2 items to track after P0/P1: optional symbol enrichment warnings, stderr preservation on transport exit, root fallback cleanup, RSS probe config errors, initialize capability validation, and codemap drift.

---

## File Map

| Task | Files |
| --- | --- |
| 1. Peer launch and LSP availability | `internal/provider/codexapp/peer_supervisor.go`, `internal/provider/codexapp/peer_supervisor_test.go`, `internal/provider/codexapp/module.go`, `internal/contract/config.go`, `cmd/mcp-lsp/runtime.go`, `cmd/mcp-lsp/runtime_test.go` |
| 2. MCP/toolbridge error semantics | `internal/mcpserver/common/server.go`, `internal/mcpserver/common/http_transport.go`, `internal/mcpserver/common/server_test.go`, `cmd/mcp-lsp/fx.go`, `internal/sidecar/lsp/tools_test.go`, `internal/platform/toolbridge/types.go`, `internal/platform/toolbridge/handler.go`, `internal/platform/toolbridge/handler_peer_decode.go`, `internal/platform/toolbridge/handler_host_tools.go`, `internal/platform/toolbridge/stdio_mcp_client.go`, `internal/platform/toolbridge/proxy.go`, same-package tests |
| 3. Diagnostics and bootstrap state | `internal/sidecar/lsp/tools/tool_diagnostics.go`, `internal/sidecar/lsp/tools/*diagnostics*_test.go`, `internal/sidecar/lsp/multilsp/manager_diagnostics.go`, `internal/sidecar/lsp/multilsp/state.go`, `internal/sidecar/lsp/multilsp/bootstrap_doc.go`, same-package tests |
| 4. Multilsp lifecycle, cache, Go roots, installer | `internal/sidecar/lsp/multilsp/cache.go`, `internal/sidecar/lsp/multilsp/factory.go`, `internal/sidecar/lsp/multilsp/go_root_resolver.go`, `internal/sidecar/lsp/multilsp/manager_lifecycle.go`, `internal/sidecar/lsp/installer/installer.go`, `internal/sidecar/lsp/manager/registry.go`, `internal/sidecar/lsp/search/fileutil.go`, `internal/sidecar/lsp/search/language_inference_test.go`, `cmd/mcp-lsp/runtime.go`, same-package tests |
| 5. File/read/edit/code-run result failures | `internal/sidecar/lsp/tools/tool_file.go`, `internal/sidecar/lsp/tools/tool_edit_replace.go`, `internal/sidecar/lsp/tools/factory.go`, `internal/sidecar/lsp/middleware/budget.go`, same-package tests |
| 6. Search and sandbox fail-fast | `internal/sidecar/lsp/tools/tool_grep.go`, `internal/sidecar/lsp/search/searchutil.go`, `internal/sidecar/lsp/search/fileutil.go`, `cmd/mcp-lsp/exec/sandbox.go`, `cmd/mcp-lsp/exec/sandbox_windows.go`, same-package tests |
| 7. Frontend failure rendering | `cmd/agent-terminal/frontend/vue-app/utils/format-utils.js`, `cmd/agent-terminal/frontend/vue-app/format-utils.test.js`, `cmd/agent-terminal/frontend/vue-app/composables/useThreadCards.js`, `cmd/agent-terminal/frontend/vue-app/use-thread-cards.test.js` |
| 8. Verification and review | affected package tests, frontend checks, `make guard`, review agents |

---

### Task 1: Fix Codex mcp-lsp Peer Startup Roots

**Files:**
- Modify: `internal/provider/codexapp/module.go:43-47`
- Modify: `internal/provider/codexapp/peer_supervisor.go:181-610`
- Modify: `internal/provider/codexapp/peer_supervisor_test.go:242-292`
- Modify: `cmd/mcp-lsp/runtime.go:69-92`
- Test: `internal/provider/codexapp/peer_supervisor_test.go`
- Test: `cmd/mcp-lsp/runtime_test.go`

- [ ] **Step 1: Add failing peer env tests**

Replace the current "reject without roots" expectation with tests that distinguish production injection from explicit bad env:

```go
func TestPeerProcessEnvInjectsConfiguredMcpLSPWorkspaceRoots(t *testing.T) {
	root := t.TempDir()
	rawRoots, err := json.Marshal([]string{root})
	if err != nil {
		t.Fatalf("marshal roots: %v", err)
	}
	env, err := peerProcessEnv("mcp-lsp", []string{"PATH=/bin"}, []string{root})
	if err != nil {
		t.Fatalf("peerProcessEnv() error = %v", err)
	}
	requireEnvValue(t, env, "GO_AGENT_LSP_ROOT", root)
	requireEnvValue(t, env, "GO_AGENT_LSP_ROOTS", string(rawRoots))
}

func TestPeerProcessEnvRejectsExplicitInvalidMcpLSPRoots(t *testing.T) {
	_, err := peerProcessEnv("mcp-lsp", []string{"GO_AGENT_LSP_ROOTS=not-json"}, nil)
	if err == nil || !strings.Contains(err.Error(), "GO_AGENT_LSP_ROOTS") {
		t.Fatalf("peerProcessEnv() error = %v, want explicit roots parse failure", err)
	}
}

func TestExecPeerLauncherFailsWhenNoRootSourceExists(t *testing.T) {
	launcher := newExecPeerLauncher(nil)
	launcher.workspaceRoots = func() []string { return nil }
	_, err := launcher.peerEnvForTest("mcp-lsp", []string{"PATH=/bin"})
	if err == nil || !strings.Contains(err.Error(), "workspace root") {
		t.Fatalf("peer env error = %v, want visible missing root failure", err)
	}
}
```

Add `requireEnvValue` locally if no equivalent helper exists:

```go
func requireEnvValue(t *testing.T, env []string, key, want string) {
	t.Helper()
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			if got := strings.TrimPrefix(item, prefix); got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
			return
		}
	}
	t.Fatalf("%s missing from env %#v", key, env)
}
```

Add a production wiring test that exercises the actual fx provider path, not just pure helpers:

```go
func TestProvideDefaultPeerSupervisorWiresProjectRootIntoMcpLSPLauncher(t *testing.T) {
	root := t.TempDir()
	runner := provideDefaultPeerSupervisor(nil, nil, &contract.Config{ProjectRoot: root})
	supervisor, ok := runner.(*PeerSupervisor)
	if !ok {
		t.Fatalf("runner type = %T, want *PeerSupervisor", runner)
	}
	launcher, ok := supervisor.launcher.(*execPeerLauncher)
	if !ok {
		t.Fatalf("launcher type = %T, want *execPeerLauncher", supervisor.launcher)
	}
	env, err := launcher.peerEnvForTest("mcp-lsp", []string{"PATH=/bin"})
	if err != nil {
		t.Fatalf("peer env error = %v", err)
	}
	requireEnvValue(t, env, "GO_AGENT_LSP_ROOT", root)
	var roots []string
	if err := json.Unmarshal([]byte(requireEnvString(t, env, "GO_AGENT_LSP_ROOTS")), &roots); err != nil {
		t.Fatalf("decode GO_AGENT_LSP_ROOTS: %v", err)
	}
	if !reflect.DeepEqual(roots, []string{root}) {
		t.Fatalf("GO_AGENT_LSP_ROOTS = %#v, want [%q]", roots, root)
	}
}

func TestProvideDefaultPeerSupervisorRejectsMissingProjectRootForMcpLSP(t *testing.T) {
	runner := provideDefaultPeerSupervisor(nil, nil, &contract.Config{ProjectRoot: "relative"})
	supervisor := runner.(*PeerSupervisor)
	launcher := supervisor.launcher.(*execPeerLauncher)
	_, err := launcher.peerEnvForTest("mcp-lsp", []string{"PATH=/bin"})
	if err == nil || !strings.Contains(err.Error(), "workspace root") {
		t.Fatalf("peer env error = %v, want visible workspace-root failure", err)
	}
}
```

`requireEnvString` can share the same scan logic as `requireEnvValue` and return the raw value.

- [ ] **Step 2: Run tests and confirm failure**

Run: `./scripts/test_with_guard.sh ./internal/provider/codexapp -run 'TestPeerProcessEnv(Injects|Rejects)|TestExecPeerLauncherFailsWhenNoRootSourceExists|TestProvideDefaultPeerSupervisor' -count=1`

Expected: FAIL because `peerProcessEnv` has no configured-root injection and currently rejects absent parent env.

- [ ] **Step 3: Inject roots from app config into the launcher**

Change `provideDefaultPeerSupervisor` to accept `*contract.Config` and pass an absolute root provider into `NewPeerSupervisor`. Keep app startup alive if a peer is degraded, but make the degradation observable through Task 2 tool-list failure.

Implementation shape:

```go
func provideDefaultPeerSupervisor(mgr *ServerManager, logger *slog.Logger, cfg *contract.Config) platformrunner.Runner {
	return NewPeerSupervisor(mgr, logger, WithPeerWorkspaceRoots(configuredPeerWorkspaceRoots(cfg)))
}

func configuredPeerWorkspaceRoots(cfg *contract.Config) func() []string {
	return func() []string {
		if cfg == nil {
			return nil
		}
		root := strings.TrimSpace(cfg.ProjectRoot)
		if root == "" || !filepath.IsAbs(root) {
			return nil
		}
		return []string{filepath.Clean(root)}
	}
}
```

Add a `workspaceRoots func() []string` field to `execPeerLauncher`, set it from a new `WithPeerWorkspaceRoots` option, and call:

```go
env, err := peerProcessEnv(name, os.Environ(), l.workspaceRoots())
```

Add a test-only helper so production wiring can be inspected without starting a process:

```go
func (l *execPeerLauncher) peerEnvForTest(name string, parent []string) ([]string, error) {
	return peerProcessEnv(name, parent, l.workspaceRoots())
}
```

Update `peerProcessEnv`:

```go
func peerProcessEnv(name string, parent []string, configuredRoots []string) ([]string, error) {
	env := append([]string(nil), parent...)
	env = append(env, peerModeEnv+"=1")
	if strings.TrimSpace(name) != "mcp-lsp" {
		return env, nil
	}
	if raw, ok := lookupEnvValue(env, "GO_AGENT_LSP_ROOTS"); ok {
		return env, validateMcpLSPPeerWorkspaceRoots(raw)
	}
	if root, ok := lookupEnvValue(env, "GO_AGENT_LSP_ROOT"); ok {
		return env, validateMcpLSPPeerWorkspaceRoot(root)
	}
	roots, err := normalizePeerWorkspaceRoots(configuredRoots)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(roots)
	if err != nil {
		return nil, err
	}
	env = append(env, "GO_AGENT_LSP_ROOT="+roots[0], "GO_AGENT_LSP_ROOTS="+string(raw))
	return env, nil
}
```

Explicit env remains fail-fast; absence is only accepted when the production config root supplies an absolute root.

- [ ] **Step 4: Keep runtime strict and update runtime tests only if needed**

`cmd/mcp-lsp/runtime.go` should continue to reject missing runtime roots. Add or keep:

```go
func TestRuntimeWorkspaceRootsRequiresEnv(t *testing.T) {
	unsetEnvForTest(t, "GO_AGENT_LSP_ROOT")
	unsetEnvForTest(t, "GO_AGENT_LSP_ROOTS")
	_, err := runtimeWorkspaceRoots()
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("runtimeWorkspaceRoots() error = %v, want required roots", err)
	}
}
```

The invariant is: supervisor injects roots before process launch; runtime refuses to guess if launched incorrectly.

- [ ] **Step 5: Verify**

Run: `./scripts/test_with_guard.sh ./internal/provider/codexapp ./cmd/mcp-lsp -run 'TestPeerProcessEnv|TestExecPeerLauncher|TestRuntimeWorkspaceRoots' -count=1`

Expected: PASS.

---

### Task 2: Preserve Tool Error Bits Through MCP and Toolbridge

**Files:**
- Modify: `internal/mcpserver/common/server.go:352-437`
- Modify: `internal/mcpserver/common/http_transport.go:185-192`
- Modify: `internal/mcpserver/common/server_test.go`
- Modify: `cmd/mcp-lsp/fx.go:211-241`
- Test: `internal/sidecar/lsp/tools_test.go`
- Modify: `internal/platform/toolbridge/types.go:123-139`
- Modify: `internal/platform/toolbridge/handler.go:183-198`
- Modify: `internal/platform/toolbridge/handler_peer_decode.go:352-360`
- Modify: `internal/platform/toolbridge/handler_host_tools.go:68-98`
- Modify: `internal/platform/toolbridge/stdio_mcp_client.go:96-114`
- Modify: `internal/platform/toolbridge/proxy.go:248-251`
- Test: `internal/platform/toolbridge/stdio_mcp_client_test.go`
- Test: `internal/platform/toolbridge/handler_shard13_routing_test.go`

- [ ] **Step 1: Add failing common MCP tests for `isError`**

In `internal/mcpserver/common/server_test.go`, add:

```go
func TestToolsCallErrorEnvelopeSetsMCPIsError(t *testing.T) {
	output := runTestServerToolCall(t, testToolProvider{
		call: func(context.Context, string, json.RawMessage) (any, error) {
			return nil, NewCodedToolError("path_outside_workspace", errors.New("outside"), false, "stay inside roots")
		},
	})
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	decodeJSONRPCOutput(t, output.Bytes(), &resp)
	if !resp.Result.IsError {
		t.Fatalf("isError = false, want true")
	}
}

func TestToolsCallSuccessFalsePayloadSetsMCPIsError(t *testing.T) {
	output := runTestServerToolCall(t, testToolProvider{
		call: func(context.Context, string, json.RawMessage) (any, error) {
			return map[string]any{"success": false, "error": "edit failed"}, nil
		},
	})
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	decodeJSONRPCOutput(t, output.Bytes(), &resp)
	if !resp.Result.IsError {
		t.Fatalf("isError = false, want true")
	}
}
```

Use existing decode helpers if they already cover raw output; do not create duplicate helpers if equivalent helpers exist.

- [ ] **Step 2: Add failing toolbridge tests**

In `internal/platform/toolbridge/stdio_mcp_client_test.go`, add a fake MCP response with `isError:true`:

```go
func TestStdioMCPClientCallToolPreservesMCPIsError(t *testing.T) {
	client := newFakeStdioMCPClient(t, map[string]any{
		"content": []map[string]any{{"type": "text", "text": `{"success":false,"code":"path_outside_workspace"}`}},
		"isError": true,
		"structuredContent": map[string]any{"success": false, "code": "path_outside_workspace"},
	})
	got, err := client.CallTool(context.Background(), "file", json.RawMessage(`{}`), ToolCallRequest{})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("CallTool() success = %#v, want false", got)
	}
}
```

In `internal/platform/toolbridge/handler_host_tools.go` tests, add:

```go
func TestListToolsForCodexFailsWhenLSPPeerMissing(t *testing.T) {
	h := newHandlerWithPeerListFailures(t, map[string]error{
		dto.ClientKindOrch: nil,
		dto.ClientKindLSP:  ErrNoPeerAvailable,
	})
	_, err := h.ListToolsForCodex(context.Background())
	if err == nil || !strings.Contains(err.Error(), dto.ClientKindLSP) {
		t.Fatalf("ListToolsForCodex() error = %v, want lsp peer failure", err)
	}
}
```

Add a registered peer callback routing test, because normal toolbridge calls do not go through `stdioMCPClient`:

```go
func TestHandleToolCallPreservesPeerCallbackIsError(t *testing.T) {
	h := newHandlerWithPeerCallback(t, dto.ClientKindLSP, func(_ context.Context, method string, _ any, out any) error {
		if method != ProxyMethodToolsCall {
			t.Fatalf("method = %q, want %q", method, ProxyMethodToolsCall)
		}
		resp := out.(*peerToolCallResponse)
		resp.IsError = true
		resp.Content = []peerToolCallContent{{Type: "text", Text: `{"success":false,"code":"path_outside_workspace"}`}}
		resp.StructuredContent = json.RawMessage(`{"success":false,"code":"path_outside_workspace"}`)
		return nil
	})
	got, err := h.HandleToolCall(context.Background(), ToolCallRequest{Name: "file", ClientKind: dto.ClientKindLSP})
	if err != nil {
		t.Fatalf("HandleToolCall() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("HandleToolCall() = %#v, want Success false", got)
	}
}
```

Add a direct `cmd/mcp-lsp` scoped response test so the built-in peer path cannot bypass common MCP:

```go
func TestHandleScopedToolsCallSetsIsErrorForToolEnvelope(t *testing.T) {
	tp := registryToolProvider{defs: testToolDefinitions(map[string]func(context.Context, json.RawMessage) (any, error){
		"file": func(context.Context, json.RawMessage) (any, error) {
			return nil, common.NewCodedToolError("path_outside_workspace", errors.New("outside"), false, "stay inside roots")
		},
	})}
	raw := json.RawMessage(`{"name":"file","arguments":{}}`)
	got, err := handleScopedToolsCall(context.Background(), tp, mcpdto.ClientKindLSP, raw)
	if err != nil {
		t.Fatalf("handleScopedToolsCall() error = %v", err)
	}
	result := got.(map[string]any)
	if result["isError"] != true {
		t.Fatalf("isError = %#v, want true; result=%#v", result["isError"], result)
	}
}
```

- [ ] **Step 3: Run tests and confirm failure**

Run: `./scripts/test_with_guard.sh ./internal/mcpserver/common ./internal/platform/toolbridge ./cmd/mcp-lsp -run 'TestToolsCall.*IsError|TestStdioMCPClientCallToolPreservesMCPIsError|TestHandleToolCallPreservesPeerCallbackIsError|TestHandleScopedToolsCallSetsIsError|TestListToolsForCodexFailsWhenLSPPeerMissing' -count=1`

Expected: FAIL because common omits `isError`, toolbridge response types omit it, and list aggregation currently degrades.

- [ ] **Step 4: Implement one error predicate in common server**

Add an exported helper in `internal/mcpserver/common/server.go` or `tool_error_envelope.go` so both common MCP and `cmd/mcp-lsp/fx.go` use the same rule:

```go
func ToolResultIsError(value any) bool {
	if envelope, ok := value.(ToolErrorEnvelope); ok {
		return !envelope.Success
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return true
	}
	var marker struct {
		Success *bool `json:"success"`
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		return true
	}
	return marker.Success != nil && !*marker.Success
}
```

Update `toolCallResultResponse` to include `isError`:

```go
return maybeResult(id, map[string]any{
	"content":           []textContent{{Type: "text", Text: string(raw)}},
	"structuredContent": json.RawMessage(raw),
	"isError":           ToolResultIsError(value),
}), raw, nil
```

Apply the same result shape in HTTP transport if it has its own duplicated response builder.

Update `cmd/mcp-lsp/fx.go` `wrapScopedToolResult` to include the same outer bit:

```go
return map[string]any{
	"content":           []map[string]string{{"type": "text", "text": string(text)}},
	"structuredContent": json.RawMessage(text),
	"isError":           common.ToolResultIsError(result),
}, nil
```

- [ ] **Step 5: Preserve `isError` in toolbridge**

Extend response DTOs:

```go
type peerToolCallResponse struct {
	Content           []peerToolCallContent `json:"content"`
	IsError           bool                  `json:"isError,omitempty"`
	StructuredContent json.RawMessage       `json:"structuredContent,omitempty"`
}
```

Change `adaptMCPResponse`:

```go
func adaptMCPResponse(resp peerToolCallResponse) *ToolCallResult {
	items := make([]ToolCallContentItem, 0, len(resp.Content))
	for _, item := range resp.Content {
		items = append(items, ToolCallContentItem{Type: "inputText", Text: strings.TrimSpace(item.Text)})
	}
	return &ToolCallResult{ContentItems: items, Success: !resp.IsError}
}
```

Change `ListToolsForCodex` so configured peers are fail-fast:

```go
for _, outcome := range outcomes {
	if outcome.err != nil {
		return nil, fmt.Errorf("toolbridge dynamic tools peer %s unavailable: %w", outcome.clientKind, outcome.err)
	}
	merged = h.appendDynamicToolsWithShadowWarning(merged, seenToolSources, outcome.clientKind, outcome.tools)
}
```

If a future mode wants degraded peer lists, add an explicit config flag. Do not silently continue in the default path.

- [ ] **Step 6: Verify**

Run: `./scripts/test_with_guard.sh ./internal/mcpserver/common ./internal/platform/toolbridge -count=1`

Expected: PASS.

---

### Task 3: Make Diagnostics and Bootstrap State Observable

**Files:**
- Modify: `internal/sidecar/lsp/tools/tool_diagnostics.go:30-75,155-193`
- Modify: `internal/sidecar/lsp/multilsp/manager_diagnostics.go:114-190`
- Modify: `internal/sidecar/lsp/multilsp/state.go:29-156`
- Modify: `internal/sidecar/lsp/multilsp/bootstrap_doc.go:212-285`
- Test: `internal/sidecar/lsp/tools/tool_diagnostics_test.go`
- Test: `internal/sidecar/lsp/multilsp/manager_diagnostics_scoped_test.go`
- Test: `internal/sidecar/lsp/multilsp/bootstrap_state_test.go`

- [ ] **Step 1: Add failing diagnostics wait tests**

In `internal/sidecar/lsp/multilsp/manager_diagnostics_scoped_test.go`, add:

```go
func TestWaitDiagnosticsStableFailsWhenTargetNeverPublishes(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsTestFile(t, root, "package.json", `{"name":"no-publish"}`)
	mgr := newDiagnosticsTestManager(t, root, &diagnosticsRefreshClientFactory{publishOnChange: false})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})
	err := mgr.WaitDiagnosticsStable(ctx, []string{fileURIFromPath(target)})
	if err == nil || !strings.Contains(err.Error(), "diagnostics") {
		t.Fatalf("WaitDiagnosticsStable() error = %v, want no publish failure", err)
	}
}

func TestWaitDiagnosticsStableFailsWhenAnyRequestedTargetNeverPublishes(t *testing.T) {
	root := t.TempDir()
	first := writeDiagnosticsTestFile(t, root, "one.json", `{"name":"one"}`)
	second := writeDiagnosticsTestFile(t, root, "two.json", `{"name":"two"}`)
	mgr := newDiagnosticsTestManager(t, root, nil)
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})
	firstURI := fileURIFromPath(first)
	secondURI := fileURIFromPath(second)
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: firstURI}); err != nil {
		t.Fatalf("PublishDiagnostics(first) error = %v", err)
	}
	err := mgr.WaitDiagnosticsStable(ctx, []string{firstURI, secondURI})
	if err == nil || !strings.Contains(err.Error(), secondURI) {
		t.Fatalf("WaitDiagnosticsStable() error = %v, want missing second URI", err)
	}
}

func TestPublishEmptyDiagnosticsCountsAsObservedReadySnapshot(t *testing.T) {
	root := t.TempDir()
	target := writeDiagnosticsTestFile(t, root, "package.json", `{"name":"empty"}`)
	mgr := newDiagnosticsTestManager(t, root, nil)
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})
	uri := fileURIFromPath(target)
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri}); err != nil {
		t.Fatalf("PublishDiagnostics() error = %v", err)
	}
	if err := mgr.WaitDiagnosticsStable(ctx, []string{uri}); err != nil {
		t.Fatalf("WaitDiagnosticsStable() error = %v, want observed empty diagnostics success", err)
	}
	items, err := mgr.Diagnostics(ctx, []string{uri})
	if err != nil {
		t.Fatalf("Diagnostics() error = %v", err)
	}
	if len(items) != 1 || len(items[0].Diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %#v, want one empty ready item", items)
	}
}
```

- [ ] **Step 2: Add failing partial bootstrap test**

In `internal/sidecar/lsp/tools/tool_diagnostics_test.go`, add a fake registry where one URI bootstrap fails and one succeeds:

```go
func TestDiagnosticsReportsPartialBootstrapFailure(t *testing.T) {
	reg := &fakeDiagnosticsRegistry{
		bootstrapErrByURI: map[string]error{
			"file:///repo/bad.ts": errors.New("bootstrap boom"),
		},
	}
	h := handlerBase{registry: reg}
	_, _, err := h.fetchDiagnosticsWithRetry(context.Background(), []string{"file:///repo/bad.ts", "file:///repo/good.ts"})
	if err == nil || !strings.Contains(err.Error(), "bootstrap boom") || !strings.Contains(err.Error(), "bad.ts") {
		t.Fatalf("fetchDiagnosticsWithRetry() error = %v, want partial bootstrap failure", err)
	}
}
```

- [ ] **Step 3: Add failing bootstrap wait-state tests**

Create `internal/sidecar/lsp/multilsp/bootstrap_state_test.go`:

```go
func TestBootstrapWaitReturnsErrorWhenInflightEntryExpires(t *testing.T) {
	store := newBootstrapStateStore()
	decision := store.prepare("/repo", "file:///repo/a.go", "fp1")
	entry := store.entries[bootstrapKey{workspace: "/repo", uri: "file:///repo/a.go"}]
	entry.updatedAt = time.Now().Add(-bootstrapInFlightTTL - time.Second)
	next := store.prepare("/repo", "file:///repo/a.go", "fp2")
	if next.action != bootstrapActionRun {
		t.Fatalf("next action = %v, want run", next.action)
	}
	err := store.waitFor(context.Background(), "/repo", "file:///repo/a.go", decision.wait)
	if err == nil || !strings.Contains(err.Error(), "stale bootstrap") {
		t.Fatalf("waitFor() error = %v, want stale bootstrap error", err)
	}
}

func TestBootstrapWaitReturnsErrorWhenInflightEntryIsDeleted(t *testing.T) {
	store := newBootstrapStateStore()
	decision := store.prepare("/repo", "file:///repo/a.go", "fp1")
	store.delete("/repo", "file:///repo/a.go")
	err := store.waitFor(context.Background(), "/repo", "file:///repo/a.go", decision.wait)
	if err == nil || !strings.Contains(err.Error(), "deleted bootstrap") {
		t.Fatalf("waitFor() error = %v, want deleted bootstrap error", err)
	}
}
```

- [ ] **Step 4: Run tests and confirm failure**

Run: `./scripts/test_with_guard.sh ./internal/sidecar/lsp/tools ./internal/sidecar/lsp/multilsp -run 'Test.*Diagnostics|TestBootstrapWait' -count=1`

Expected: FAIL under current nil-timeout/delete-snapshot behavior.

- [ ] **Step 5: Preserve observed empty diagnostics**

Change `publishDiagnosticsForGeneration` so empty diagnostics store a ready snapshot instead of deleting the snapshot. Deleted files should be cleaned by `cleanupDeletedDiagnostics`, not by a zero diagnostics publish.

Implementation shape:

```go
metadata := diagnosticMetadataForURI(params.URI)
m.diagnostics[key.String()] = diagnosticSnapshot{
	scopeKey: scope.ScopeKey, workspaceKey: scope.WorkspaceKey, language: scope.LanguageID,
	uri: params.URI, generation: capturedGen, fingerprint: metadata.fingerprint,
	mtimeNS: metadata.mtimeNS, size: metadata.size, updatedAt: time.Now(),
	source: "publish", state: diagnosticStateReady, params: params,
}
return nil
```

- [ ] **Step 6: Make wait timeout an error**

Change `WaitDiagnosticsStable`:

```go
readiness := m.diagnosticReadiness(filter, uris)
if missing := readiness.missingURIs(); len(missing) > 0 {
	return fmt.Errorf("diagnostics did not publish for requested targets before %s: %s", m.diagMaxWait, strings.Join(missing, ", "))
}
...
if time.Now().After(deadline) {
	return fmt.Errorf("diagnostics did not stabilize before %s", m.diagMaxWait)
}
```

Implement readiness per requested URI, not with one aggregate latest timestamp. For explicit URI filters, every requested URI that resolves to an LSP-managed existing file must have an observed ready snapshot, including a zero-diagnostic snapshot. For all-scope diagnostics, the aggregate latest timestamp is acceptable because there is no explicit requested URI set. The exact error text must include "diagnostics", the timeout, and the missing URI(s); tests should assert substrings, not full strings.

- [ ] **Step 7: Return joined partial bootstrap errors**

Change `reactiveBootstrap` to accumulate URI-qualified errors and return them even when some targets succeeded:

```go
var errs []error
...
if err := h.registry.BootstrapDocument(ctx, uri); err != nil {
	errs = append(errs, fmt.Errorf("%s: %w", uri, err))
	continue
}
...
if len(errs) > 0 {
	return count, errors.Join(errs...)
}
return count, nil
```

Then `fetchDiagnosticsWithRetry` should fail closed when any requested target could not be bootstrapped.

- [ ] **Step 8: Carry bootstrap wait results**

Replace `wait <-chan struct{}` with a result channel:

```go
type bootstrapResult struct { err error }
type bootstrapDecision struct {
	action bootstrapAction
	previous bootstrapStatus
	wait <-chan bootstrapResult
}
type bootstrapEntry struct {
	...
	wait chan bootstrapResult
}
```

On complete: `finish(..., nil)`. On fail: `finish(..., err)`. On stale expiry: send a `stale bootstrap` error to the old waiter before replacing it. On delete, send a `deleted bootstrap` error to any waiter before removing the entry. `waitFor` must return the result error and must not inspect missing entries as success.

- [ ] **Step 9: Verify**

Run: `./scripts/test_with_guard.sh ./internal/sidecar/lsp/tools ./internal/sidecar/lsp/multilsp -count=1`

Expected: PASS.

---

### Task 4: Fail Fast in Multilsp Lifecycle, Cache, Go Roots, Installer, and Language Routing

**Files:**
- Modify: `internal/sidecar/lsp/multilsp/cache.go:100-123,353-369,523-532`
- Modify: `internal/sidecar/lsp/multilsp/factory.go:260-275`
- Modify: `internal/sidecar/lsp/multilsp/go_root_resolver.go:184-199`
- Modify: `internal/sidecar/lsp/multilsp/manager_lifecycle.go:169-185,311-315`
- Modify: `internal/sidecar/lsp/multilsp/bootstrap_doc.go:212-285`
- Modify: `internal/sidecar/lsp/installer/installer.go:83-115`
- Modify: `internal/sidecar/lsp/manager/registry.go:24-32,168-173`
- Modify: `cmd/mcp-lsp/runtime.go:303-334`
- Test: same-package tests under `internal/sidecar/lsp/multilsp`, `internal/sidecar/lsp/installer`, `internal/sidecar/lsp/manager`, `internal/sidecar/lsp/tools`

- [ ] **Step 1: Add failing tests**

Persistent cache:

```go
func TestPersistentCacheConfiguredPathFailureReturnsError(t *testing.T) {
	cfg := lspCacheConfig{Persistent: true, Path: filepath.Join(t.TempDir(), "missing", "cache.json")}
	blocker := filepath.Dir(cfg.Path)
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	_, err := newLSPCacheStore(cfg)
	if err == nil || !strings.Contains(err.Error(), "persistent cache") {
		t.Fatalf("newLSPCacheStore() error = %v, want persistent cache failure", err)
	}
}

func TestPersistentCacheWriteFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	store, err := newLSPCacheStore(lspCacheConfig{Persistent: true, Dir: dir})
	if err != nil {
		t.Fatalf("newLSPCacheStore() error = %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod cache dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	err = store.Upsert(lspCacheValue{Key: lspCacheKey{URI: "file:///repo/a.go"}})
	if err == nil || !strings.Contains(err.Error(), "persistent cache") {
		t.Fatalf("Upsert() error = %v, want persistent write failure", err)
	}
	if !store.persistent || !store.persistentReady {
		t.Fatalf("store disabled persistence after write failure; persistent=%v ready=%v", store.persistent, store.persistentReady)
	}
}

func TestPersistentCacheLoadFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, lspCacheFileName)
	if err := os.WriteFile(cachePath, []byte("{bad-json"), 0o644); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}
	_, err := newLSPCacheStore(lspCacheConfig{Persistent: true, Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "persistent cache") || !strings.Contains(err.Error(), "load") {
		t.Fatalf("newLSPCacheStore() error = %v, want persistent cache load failure", err)
	}
}

func TestBootstrapCoordinatorForReturnsPersistentCacheLoadError(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, lspCacheFileName)
	if err := os.WriteFile(cachePath, []byte("{bad-json"), 0o644); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}
	t.Setenv("AGENT_LSP_CACHE_PERSISTENT", "1")
	t.Setenv("AGENT_LSP_CACHE_DIR", dir)
	mgr := &manager{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	_, err := bootstrapCoordinatorFor(mgr)
	if err == nil || !strings.Contains(err.Error(), "persistent cache") || !strings.Contains(err.Error(), "load") {
		t.Fatalf("bootstrapCoordinatorFor() error = %v, want persistent cache load failure", err)
	}
}

func TestBootstrapCoordinatorForReturnsPersistentCacheSetupError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "cache")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	t.Setenv("AGENT_LSP_CACHE_PERSISTENT", "1")
	t.Setenv("AGENT_LSP_CACHE_DIR", blocker)
	mgr := &manager{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	_, err := bootstrapCoordinatorFor(mgr)
	if err == nil || !strings.Contains(err.Error(), "persistent cache") {
		t.Fatalf("bootstrapCoordinatorFor() error = %v, want persistent cache setup failure", err)
	}
}

func TestBootstrapDocumentPropagatesPersistentCacheWriteError(t *testing.T) {
	root := t.TempDir()
	target := writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"cache-write"}`)
	mgr := newManagerWithPersistentCacheDir(t, root, unwritableCacheDir(t))
	err := mgr.BootstrapDocument(common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}}), fileURIFromPath(target))
	if err == nil || !strings.Contains(err.Error(), "persistent cache") {
		t.Fatalf("BootstrapDocument() error = %v, want persistent cache write failure", err)
	}
}
```

Broken `go.work`:

```go
func TestResolveGoWorkRootRejectsBrokenGoWork(t *testing.T) {
	root := t.TempDir()
	goWork := filepath.Join(root, "go.work")
	if err := os.WriteFile(goWork, []byte("go 1.22\nuse (\n"), 0o644); err != nil {
		t.Fatalf("write go.work: %v", err)
	}
	_, err := resolveGoWorkRoot(filepath.Join(root, "main.go"), root, goWork, "auto")
	if err == nil || !strings.Contains(err.Error(), "go.work") {
		t.Fatalf("resolveGoWorkRoot() error = %v, want parse failure", err)
	}
}
```

Env-aware factory:

```go
func TestNewClientFromFactoryRejectsEnvWithLegacyFactory(t *testing.T) {
	_, err := newClientFromFactory(legacyClientFactory{}, workspaceConfig{rootPath: t.TempDir(), env: []string{"GOWORK=/repo/go.work"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "env") {
		t.Fatalf("newClientFromFactory() error = %v, want env unsupported", err)
	}
}
```

Installer path propagation:

```go
func TestRegistryUsesInstallerResolvedBinaryPath(t *testing.T) {
	resolved := filepath.Join(t.TempDir(), "gopls")
	reg := NewRegistryWithInstaller(fakeInstaller{path: resolved})
	mgr := &capturingManager{}
	reg.Register("go", mgr, fakeResolver{})
	if _, err := reg.ResolveManagerForLanguage(context.Background(), "go"); err != nil {
		t.Fatalf("ResolveManagerForLanguage() error = %v", err)
	}
	if got := mgr.binaryOverride(); got != resolved {
		t.Fatalf("binary override = %q, want %q", got, resolved)
	}
}

func TestRuntimeGenericManagerUsesInstalledBinaryOverride(t *testing.T) {
	root := t.TempDir()
	installed := filepath.Join(t.TempDir(), "gopls")
	adapter := fakeLanguageAdapter{command: multilsp.ServerCommand{Executable: "gopls"}}
	mgr, err := createGenericManagerWithBinary(adapter, multilsp.NewDefaultLanguageAdapterRegistry(), root, nil, installed)
	if err != nil {
		t.Fatalf("createGenericManagerWithBinary() error = %v", err)
	}
	client := mustCreateClientFromManager(t, mgr, root)
	if got := clientBinary(client); got != installed {
		t.Fatalf("client binary = %q, want installed override %q", got, installed)
	}
}
```

TSX/JSX language IDs:

```go
func TestDetectLanguageIDReactExtensions(t *testing.T) {
	cases := map[string]string{
		"component.jsx": "javascriptreact",
		"component.tsx": "typescriptreact",
	}
	for file, want := range cases {
		if got := DetectLanguageID(file); got != want {
			t.Fatalf("DetectLanguageID(%q) = %q, want %q", file, got, want)
		}
	}
}

func TestInferASTLanguageReactExtensions(t *testing.T) {
	cases := map[string]string{
		"/tmp/component.jsx": "javascriptreact",
		"/tmp/component.tsx": "typescriptreact",
	}
	for target, want := range cases {
		got, err := normalizeASTLanguage("", target, false, "")
		if err != nil {
			t.Fatalf("normalizeASTLanguage(%q) error = %v", target, err)
		}
		if got != want {
			t.Fatalf("normalizeASTLanguage(%q) = %q, want %q", target, got, want)
		}
	}
}
```

- [ ] **Step 2: Run tests and confirm failure**

Run: `./scripts/test_with_guard.sh ./internal/sidecar/lsp/multilsp ./internal/sidecar/lsp/installer ./internal/sidecar/lsp/manager ./internal/sidecar/lsp/search -run 'TestPersistentCache|TestResolveGoWorkRootRejects|TestNewClientFromFactoryRejects|TestRegistryUsesInstaller|TestDetectLanguageIDReactExtensions|TestInferASTLanguageReactExtensions' -count=1`

Expected: FAIL on current fallback/discard behavior.

- [ ] **Step 3: Make persistent cache constructors return errors**

Change signatures:

```go
func newLSPCacheStore(cfg lspCacheConfig) (*lspCacheStore, error)
func newLSPCacheStoreFromEnv(logger *slog.Logger) (*lspCacheStore, error)
func newLSPCacheCoordinator(cfg lspCacheConfig) (*lspCacheCoordinator, error)
func bootstrapCoordinatorFor(m *manager) (*bootstrapCoordinator, error)
func restoreBootstrappedWorkspace(ctx context.Context, m *manager, cfg workspaceConfig) error
func (s *lspCacheStore) Upsert(value lspCacheValue) error
func (s *lspCacheStore) Delete(key lspCacheKey) error
func (s *lspCacheStore) Tombstone(key lspCacheKey) error
func (s *lspCacheStore) RememberDocumentScope(uri string, scope ResolvedLSPToolScope, fingerprint string) error
```

For `cfg.Persistent == true`, `ensurePersistentReady` and `loadPersistent` must return wrapped errors. Remove `fallbackToMemory` for explicitly configured persistence. Soft memory mode remains only when `Persistent` is false.

Update production call sites, not just tests. `bootstrapCoordinatorFor`, `restoreBootstrappedWorkspace`, `bootstrapDocument`, `cleanupDeletedDiagnostics`, `cleanupDocumentForRef`, `cleanupDocumentForScopes`, and any caller of `coordinator.cache.Upsert/Delete/Tombstone/RememberDocumentScope` must return or propagate cache setup/write failures. Change `persistOnMutation` to return an error:

```go
func (s *lspCacheStore) persistOnMutation(changed bool) error {
	if !changed {
		return nil
	}
	if err := s.persistLocked(); err != nil {
		if s.config.Persistent {
			return fmt.Errorf("persistent cache write: %w", err)
		}
		s.fallbackToMemory(err)
	}
	return nil
}
```

`maybeCleanup` can return/log a cleanup error, but requested cache writes (`Upsert`, `Delete`, `Tombstone`) must return errors to their callers.

- [ ] **Step 4: Reject broken go.work**

Change `resolveGoWorkRoot`:

```go
moduleRoots, err := parseGoWorkModuleRoots(goWorkPath)
if err != nil {
	return GoRootInfo{}, fmt.Errorf("parse go.work %s: %w", goWorkPath, err)
}
```

Only the documented "go binary missing" path may use text fallback parser; corrupt go.work content is not a fallback condition.

- [ ] **Step 5: Make bootstrap and env lifecycle errors explicit**

Change `bootstrapLanguageClient` to return `error`, and propagate from callers that depend on seeded workspace state:

```go
func (m *manager) bootstrapLanguageClient(ctx context.Context, client Client, root, languageID string) error {
	...
	if err := client.DidOpen(ctx, fileURIFromPath(target), languageID, 0, string(content)); err != nil {
		return fmt.Errorf("bootstrap %s DidOpen %s: %w", languageID, target, err)
	}
	return nil
}
```

Change `newClientFromFactory`:

```go
if len(cfg.env) > 0 {
	envFactory, ok := factory.(ClientFactoryWithEnv)
	if !ok {
		return nil, fmt.Errorf("client factory does not support environment overrides for %s", cfg.key)
	}
	return envFactory.NewClientWithEnv(cfg.rootPath, append([]string(nil), cfg.env...), handler)
}
return factory.NewClient(cfg.rootPath, handler)
```

- [ ] **Step 6: Propagate installer resolved binary**

Introduce a small installer interface so registry tests can inject resolved binary paths without depending on the concrete provider:

```go
type Installer interface {
	EnsureInstalled(ctx context.Context, languageID string) (string, error)
}
```

Change `dynamicRegistry.installer` and `NewRegistry` to accept that interface while preserving the current concrete provider call sites.

Change registry language config to store resolved binary override after install:

```go
type languageConfig struct {
	...
	binaryPath string
}
```

Change `ensureInstalled`:

```go
path, err := r.installer.EnsureInstalled(ctx, lang)
if err != nil {
	return err
}
config.binaryPath = path
return nil
```

Thread `binaryPath` into runtime manager/client creation. The generic runtime manager should prefer the installed absolute binary over `adapter.ServerCommand(...).Executable` when non-empty:

```go
binary := firstNonEmpty(config.binaryPath, command.Executable)
```

Because `runtime.go` currently captures `command.Executable` inside `createGenericManager` before any registry install call, also add one of these concrete implementation routes and test it:

1. Install before constructing each runtime manager, then pass the installed binary into a new `createGenericManagerWithBinary(..., binaryOverride string)` used by the client factory.
2. Or make the manager/client factory ask the registry language config for the current binary override at client creation time.

Do not only store `binaryPath` in registry config; the runtime client factory must use it. If this is too invasive for current interfaces, choose the stricter alternative: `EnsureInstalled` must only return success when `exec.LookPath` finds the binary on PATH, and tests must assert fail-fast when the post-install path is outside PATH. Do not keep "install successful" while later launch still uses a missing bare binary.

- [ ] **Step 7: Fix React language IDs**

Update:

```go
".jsx": "javascriptreact",
".tsx": "typescriptreact",
```

Add an `open_file` test that `DidOpen` receives `typescriptreact` for `.tsx`.

Also update `internal/sidecar/lsp/search/fileutil.go` language inference so `.jsx` and `.tsx` resolve to `javascriptreact` and `typescriptreact`, and update `internal/sidecar/lsp/search/language_inference_test.go` expectations. If ast-grep itself only accepts canonical `javascript`/`typescript`, add an explicit adapter at the `sg` command boundary, not by losing React identity in the public LSP/search inference layer.

- [ ] **Step 8: Verify**

Run: `./scripts/test_with_guard.sh ./internal/sidecar/lsp/multilsp ./internal/sidecar/lsp/installer ./internal/sidecar/lsp/manager ./cmd/mcp-lsp -count=1`

Expected: PASS.

---

### Task 5: Make File, Read, Edit, Budget, and Code Run Failures Non-Successful

**Files:**
- Modify: `internal/sidecar/lsp/tools/tool_file.go:144-239,264-305`
- Modify: `internal/sidecar/lsp/tools/tool_edit_replace.go:95-119`
- Modify: `internal/sidecar/lsp/tools/factory.go:346-356`
- Modify: `internal/sidecar/lsp/middleware/budget.go:45-104`
- Test: `internal/sidecar/lsp/tools/tool_file_scope_test.go`
- Test: `internal/sidecar/lsp/tools/tool_language_override_test.go`
- Test: `internal/sidecar/lsp/tools/tool_edit_support_test.go`
- Test: `internal/sidecar/lsp/tools/factory_test.go`
- Test: `internal/sidecar/lsp/middleware/budget_test.go`

- [ ] **Step 1: Add failing tests**

`open_file`:

```go
func TestOpenFileReturnsErrorWhenDidOpenFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package main\n"), 0o644))
	manager := &languageOverrideManager{didOpenErr: errors.New("did open boom")}
	registry := &languageOverrideRegistry{manager: manager}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})
	_, err := handlerBase{registry: registry}.openFile(ctx, path, "")
	if err == nil || !strings.Contains(err.Error(), "did open boom") {
		t.Fatalf("openFile() error = %v, want DidOpen failure", err)
	}
}
```

Batch read:

```go
func TestReadBatchReturnsPartialFailureWhenAnyItemFails(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "ok.txt"), []byte("ok"), 0o644))
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})
	resp, err := handlerBase{}.readBatch(ctx, []string{"ok.txt", "missing.txt"}, 0, 0)
	if err == nil {
		t.Fatalf("readBatch() err = nil, response = %#v; want partial failure error", resp)
	}
	if !strings.Contains(err.Error(), "missing.txt") {
		t.Fatalf("readBatch() error = %v, want missing path", err)
	}
}
```

Edit failure:

```go
func TestReplaceRangeReturnsErrorForInvalidPatch(t *testing.T) {
	_, err := testReplaceRange(t, replaceRangeRequest{FilePath: "a.go", StartLine: 10, EndLine: 2})
	if err == nil {
		t.Fatalf("replace_range error = nil, want invalid range failure")
	}
}
```

Budget overflow:

```go
func TestBudgetOverflowSetsSuccessFalse(t *testing.T) {
	handler := WithOutputBudget("edit", func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"success": true, "data": strings.Repeat("x", 1024)}, nil
	}, Budget{MaxBytes: 64})
	got, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("budget middleware error = %v", err)
	}
	payload := got.(map[string]any)
	if payload["success"] != false || payload["error_code"] != "result_too_large" {
		t.Fatalf("overflow payload = %#v, want success false result_too_large", payload)
	}
}

func TestGenericBudgetOverflowSetsSuccessFalse(t *testing.T) {
	handler := WithOutputBudget("grep", func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"success": true, "data": strings.Repeat("x", 1024)}, nil
	}, Budget{MaxBytes: 64})
	got, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("budget middleware error = %v", err)
	}
	payload := got.(map[string]any)
	if payload["success"] != false || payload["error_code"] != "result_too_large" {
		t.Fatalf("overflow payload = %#v, want success false result_too_large", payload)
	}
	if payload["original_success"] != true {
		t.Fatalf("original_success = %#v, want true", payload["original_success"])
	}
}
```

Code run infrastructure:

```go
func TestExecuteSandboxInfrastructureErrorReturnsToolError(t *testing.T) {
	_, err := executeSandbox(context.Background(), failingSandbox{err: context.DeadlineExceeded}, lspexec.Request{}, "go", "test")
	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("executeSandbox() error = %v, want infrastructure error", err)
	}
}
```

- [ ] **Step 2: Run tests and confirm failure**

Run: `./scripts/test_with_guard.sh ./internal/sidecar/lsp/tools ./internal/sidecar/lsp/middleware -run 'Test(OpenFile|ReadBatch|ReplaceRange|BudgetOverflow|ExecuteSandbox)' -count=1`

Expected: FAIL under current nil-error wrappers.

- [ ] **Step 3: Return errors for requested file/LSP sync failures**

Change `openFile`:

```go
manager, err := managerForFile(...)
if err != nil {
	return openFileResult{}, err
}
...
if err := manager.DidOpen(ctx, uri, openLanguageID, 1, file.Content); err != nil {
	return openFileResult{}, fmt.Errorf("open_file DidOpen %s: %w", file.Path.DisplayPath, err)
}
```

If a future mode wants "read without LSP open", add an explicit action; do not report `opened` when no manager exists.

- [ ] **Step 4: Return joined batch read errors**

Keep per-item data for callers that inspect it, but return `errors.Join` when any item failed:

```go
var errs []error
...
if indexed.Item.Error != "" {
	errs = append(errs, fmt.Errorf("%s: %s", indexed.Item.FilePath, indexed.Item.Error))
}
...
resp.Success = len(errs) == 0
if len(errs) > 0 {
	return encodeBatchReadPayload(resp), errors.Join(errs...)
}
return encodeBatchReadPayload(resp), nil
```

When payload truncation drops items, add `DroppedErrorCount` and `DroppedErrors []string` to `batchReadMeta` before slicing.

- [ ] **Step 5: Return edit failures as errors**

Change both failure returns:

```go
failure := h.replaceFailure(...)
return failure, err
```

For rollback failures, wrap both sync and rollback causes with `errors.Join` in `replaceFailure` or its caller so the common envelope includes the original cause.

- [ ] **Step 6: Make budget overflow a structured failure**

Set `"success": false` on all overflow envelopes, including edit. Preserve the original success under a different field:

```go
"success": false,
"original_success": payload["success"],
"error": "tool result exceeded output budget",
```

The middleware can still return nil error because common MCP `isError` from Task 2 will detect `success:false`.

- [ ] **Step 7: Return sandbox infrastructure errors**

Change `executeSandbox`:

```go
if err != nil {
	return nil, common.NewCodedToolError("sandbox_exec_failed", err, true, "Inspect sandbox command setup and retry after fixing the environment.")
}
```

Program exit code failures remain ordinary `CodeRunResult{Success:false, ExitCode:n}` because the program did run. Infrastructure failures return tool errors.

- [ ] **Step 8: Verify**

Run: `./scripts/test_with_guard.sh ./internal/sidecar/lsp/tools ./internal/sidecar/lsp/middleware -count=1`

Expected: PASS.

---

### Task 6: Make Search and Sandbox Fail Closed

**Files:**
- Modify: `internal/sidecar/lsp/tools/tool_grep.go:82-100`
- Modify: `internal/sidecar/lsp/search/searchutil.go:117-126,190-205,312-320,323-355`
- Modify: `internal/sidecar/lsp/search/fileutil.go:270-286`
- Modify: `cmd/mcp-lsp/exec/sandbox.go:291-320`
- Modify: `cmd/mcp-lsp/exec/sandbox_windows.go:29-71`
- Test: `internal/sidecar/lsp/tools/tool_grep_test.go`
- Test: `internal/sidecar/lsp/search/searchutil_test.go`
- Test: `internal/sidecar/lsp/search/fileutil_test.go`
- Test: `cmd/mcp-lsp/exec/sandbox_test.go`
- Test: `cmd/mcp-lsp/exec/sandbox_windows_test.go`

- [ ] **Step 1: Add failing search tests**

Invalid regex:

```go
func TestGrepInvalidRegexReturnsErrorWithoutLiteralFallback(t *testing.T) {
	_, err := callGrepTool(t, grepToolInput{Action: "text_search", Query: "[", Regex: true})
	if err == nil || !strings.Contains(err.Error(), "regex") {
		t.Fatalf("grep invalid regex error = %v, want regex syntax error", err)
	}
}

func TestGrepInvalidGlobReturnsError(t *testing.T) {
	_, err := callGrepTool(t, grepToolInput{Action: "text_search", Query: "needle", Glob: "["})
	if err == nil || !strings.Contains(err.Error(), "glob") {
		t.Fatalf("grep invalid glob error = %v, want glob syntax error", err)
	}
}
```

Walk/search errors:

```go
func TestWalkSearchEntryPropagatesWalkErr(t *testing.T) {
	var results []SearchMatch
	err := walkSearchEntry(context.Background(), "/repo", "/repo/a.go", "", 1024, literalMatcher("x"), &results, nil, errors.New("walk boom"))
	if err == nil || !strings.Contains(err.Error(), "walk boom") {
		t.Fatalf("walkSearchEntry() error = %v, want walk boom", err)
	}
}

func TestIsSearchCandidatePropagatesInfoError(t *testing.T) {
	entry := fakeDirEntry{
		name: "a.go",
		mode: 0,
		infoErr: errors.New("info boom"),
	}
	_, err := isSearchCandidate("/repo/a.go", entry, 1024)
	if err == nil || !strings.Contains(err.Error(), "info boom") {
		t.Fatalf("isSearchCandidate() error = %v, want info boom", err)
	}
}

func TestIsBinaryFilePropagatesOpenError(t *testing.T) {
	_, err := isBinaryFile(filepath.Join(t.TempDir(), "missing.go"))
	if err == nil || !strings.Contains(err.Error(), "open") {
		t.Fatalf("isBinaryFile() error = %v, want open failure", err)
	}
}

type fakeDirEntry struct {
	name string
	mode fs.FileMode
	infoErr error
}

func (e fakeDirEntry) Name() string { return e.name }
func (e fakeDirEntry) IsDir() bool { return e.mode.IsDir() }
func (e fakeDirEntry) Type() fs.FileMode { return e.mode.Type() }
func (e fakeDirEntry) Info() (fs.FileInfo, error) {
	if e.infoErr != nil {
		return nil, e.infoErr
	}
	return fakeFileInfo{name: e.name, mode: e.mode, size: 1}, nil
}

type fakeFileInfo struct {
	name string
	mode fs.FileMode
	size int64
}

func (i fakeFileInfo) Name() string { return i.name }
func (i fakeFileInfo) Size() int64 { return i.size }
func (i fakeFileInfo) Mode() fs.FileMode { return i.mode }
func (i fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (i fakeFileInfo) IsDir() bool { return i.mode.IsDir() }
func (i fakeFileInfo) Sys() any { return nil }
```

Ast-grep bad JSON:

```go
func TestDecodeSGMatchesRejectsInvalidJSON(t *testing.T) {
	_, err := decodeSGMatches([]byte("{bad-json}\n"), "/repo")
	if err == nil || !strings.Contains(err.Error(), "sg json") {
		t.Fatalf("decodeSGMatches() error = %v, want json failure", err)
	}
}

func TestDecodeSGScanMatchesRejectsInvalidJSON(t *testing.T) {
	_, err := decodeSGScanMatches([]byte("{bad-json}"), "/repo")
	if err == nil || !strings.Contains(err.Error(), "sg scan json") {
		t.Fatalf("decodeSGScanMatches() error = %v, want scan json failure", err)
	}
}
```

Ast-grep exit 1 with stderr:

```go
func TestSearchASTExitOneWithStderrReturnsError(t *testing.T) {
	sg := writeFakeSG(t, 1, "", "parse error")
	t.Setenv("PATH", filepath.Dir(sg)+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err := SearchAST(context.Background(), ASTSearchOptions{Root: t.TempDir(), Query: "bad", Language: "go"})
	if err == nil || !strings.Contains(err.Error(), "parse error") {
		t.Fatalf("SearchAST() error = %v, want sg stderr", err)
	}
}

func TestSearchASTScanExitOneWithStderrReturnsError(t *testing.T) {
	sg := writeFakeSG(t, 1, "", "scan parse error")
	t.Setenv("PATH", filepath.Dir(sg)+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	_, err := SearchAST(context.Background(), ASTSearchOptions{Root: root, Path: target, Query: "function_declaration", Language: "go"})
	if err == nil || !strings.Contains(err.Error(), "scan parse error") {
		t.Fatalf("SearchAST scan error = %v, want sg scan stderr", err)
	}
}

func TestSearchTextSingleFilePropagatesCandidateProbeError(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o000); err != nil {
		t.Fatalf("write target: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o600) })
	_, err := SearchText(context.Background(), TextSearchOptions{Root: root, Path: target, Query: "package"})
	if err == nil || !strings.Contains(err.Error(), "binary probe") {
		t.Fatalf("SearchText() error = %v, want single-file probe failure", err)
	}
}

func TestSearchTextInvalidGlobReturnsError(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	_, err := SearchText(context.Background(), TextSearchOptions{Root: root, Path: target, Query: "package", Glob: "["})
	if err == nil || !strings.Contains(err.Error(), "glob") {
		t.Fatalf("SearchText() error = %v, want invalid glob error", err)
	}
}
```

- [ ] **Step 2: Add failing sandbox output tests**

```go
func TestLimitedBufferKeepsTailAfterTruncation(t *testing.T) {
	buf := newLimitedBuffer(16)
	_, _ = buf.Write([]byte("0123456789abcdef"))
	_, _ = buf.Write([]byte("FATAL_SENTINEL"))
	out := buf.String()
	if !strings.Contains(out, "FATAL_SENTINEL") {
		t.Fatalf("limitedBuffer output = %q, want tail sentinel", out)
	}
}
```

For Windows guard, extract syscall hooks so tests can run deterministically:

```go
func TestAttachSandboxGuardReturnsErrorWhenJobCreationFails(t *testing.T) {
	hooks := sandboxWindowsHooks{createJob: func() (windows.Handle, error) { return 0, errors.New("job boom") }}
	_, err := attachSandboxGuardWithHooks(fakeStartedCmd(t), hooks)
	if err == nil || !strings.Contains(err.Error(), "job boom") {
		t.Fatalf("attachSandboxGuardWithHooks() error = %v, want job boom", err)
	}
}

func TestRunKillsStartedProcessWhenSandboxGuardAttachFails(t *testing.T) {
	runner := NewSandbox(SandboxConfig{
		windowsHooks: sandboxWindowsHooks{
			createJob: func() (windows.Handle, error) { return 0, errors.New("job boom") },
		},
	})
	result, err := runner.Run(context.Background(), Request{Command: longRunningTestCommand(t)})
	if err == nil || !strings.Contains(err.Error(), "job boom") {
		t.Fatalf("Run() result=%#v error=%v, want guard attach failure", result, err)
	}
	assertNoLongRunningTestCommand(t)
}
```

- [ ] **Step 3: Run tests and confirm failure**

Run: `./scripts/test_with_guard.sh ./internal/sidecar/lsp/tools ./internal/sidecar/lsp/search ./cmd/mcp-lsp/exec -run 'TestGrepInvalidRegex|TestGrepInvalidGlob|TestWalkSearchEntry|TestIsSearchCandidate|TestIsBinaryFile|TestSearchTextSingleFile|TestSearchTextInvalidGlob|TestDecodeSG|TestSearchAST|TestLimitedBuffer|TestAttachSandboxGuard|TestRunKillsStartedProcess' -count=1`

Expected: FAIL under fallback/ignore/head-only behavior.

- [ ] **Step 4: Remove implicit regex fallback**

Delete the automatic `opts.Regex=false` retry. Invalid regex from `search.SearchText` should return to the handler and become a common tool error envelope. If fallback is required later, add an explicit input field such as `allow_regex_fallback`; do not infer it.

- [ ] **Step 5: Propagate search traversal and ast-grep errors**

Change `walkSearchEntry`:

```go
if walkErr != nil {
	return fmt.Errorf("walk %s: %w", candidate, walkErr)
}
if entry == nil {
	return fmt.Errorf("walk %s: missing dir entry", candidate)
}
...
if err != nil {
	return fmt.Errorf("search %s: %w", candidate, err)
}
```

Validate glob syntax once before search, and propagate matcher errors instead of treating them as no-match:

```go
func validateSearchGlob(glob string) error {
	glob = strings.TrimSpace(glob)
	if glob == "" {
		return nil
	}
	for _, pattern := range expandSearchGlobPatterns(glob) {
		if _, err := path.Match(pattern, "probe"); err != nil {
			return fmt.Errorf("invalid glob %q: %w", glob, err)
		}
	}
	return nil
}
```

Call this from `SearchText` before walking or single-file search, and from `SearchAST` before invoking `sg`. If the implementation keeps per-path glob matching, change `matchesPathGlob` / `matchesCompiledGlob` to return `(bool, error)` and bubble the same invalid-glob error through both directory and single-file paths. `handleGrep` should receive the error and rely on Task 2 to set outer `isError:true`.

Change candidate helpers so probe failures are not converted to "not a candidate":

```go
func isSearchCandidate(path string, entry os.DirEntry, maxBytes int) (bool, error) {
	if entry == nil {
		return false, fmt.Errorf("missing dir entry for %s", path)
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return false, nil
	}
	info, err := entry.Info()
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || (maxBytes > 0 && info.Size() > int64(maxBytes)) {
		return false, nil
	}
	binary, err := isBinaryFile(path)
	if err != nil {
		return false, err
	}
	return !binary, nil
}

func isBinaryFile(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open %s for binary probe: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	...
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read %s for binary probe: %w", path, err)
	}
	return isBinaryContent(buf[:n]), nil
}
```

Change ast-grep command execution to use `CombinedOutput` or capture stderr separately:

```go
output, err := cmd.Output()
if err != nil {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(bytes.TrimSpace(exitErr.Stderr)) == 0 {
		return nil, nil
	}
	return nil, fmt.Errorf("sg run: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
}
```

Apply the same stderr rule in `runSGKindSearch` for `sg scan`: exit code 1 with empty stderr can mean no matches, but exit code 1 with stderr is a tool failure. Change `decodeSGScanMatches` and `decodeSGMatches` to return `([]SearchMatch, error)` and check `scanner.Err()`. Invalid JSON should include the line number or scan mode, and `runSGKindSearch` must propagate `decodeSGScanMatches` errors instead of returning nil matches.

- [ ] **Step 6: Keep sandbox head and tail**

Replace `limitedBuffer` with a head/tail buffer:

```go
type limitedBuffer struct {
	mu sync.Mutex
	limit int
	head bytes.Buffer
	tail []byte
	dropped int
}
```

Write keeps the first half in `head`, always retains the last half in `tail`, and increments `dropped`. `String()` renders:

```text
<head>
... output truncated, dropped N bytes ...
<tail>
```

- [ ] **Step 7: Make Windows sandbox guard fail closed**

Change `attachSandboxGuard` to return `(*sandboxGuard, error)` and propagate errors from job creation, `OpenProcess`, and `AssignProcessToJobObject`. Because the process is already started before Windows job attachment, `Run` must kill and wait for the process before returning an attach error:

```go
guard, err := attachSandboxGuard(command)
if err != nil {
	killErr := command.Process.Kill()
	waitErr := command.Wait()
	return Result{}, errors.Join(fmt.Errorf("attach sandbox guard: %w", err), killErr, waitErr)
}
```

Change timeout kill helpers to return errors:

```go
func killSandboxProcess(process *os.Process, guard *sandboxGuard) error
```

On timeout, `Run` must include guard termination errors in the returned sandbox error. Do not continue without a job object on Windows.

- [ ] **Step 8: Verify**

Run: `./scripts/test_with_guard.sh ./internal/sidecar/lsp/tools ./internal/sidecar/lsp/search ./cmd/mcp-lsp/exec -count=1`

Expected: PASS.

---

### Task 7: Render Parsed Tool Failures as Failed in the Frontend

**Files:**
- Modify: `cmd/agent-terminal/frontend/vue-app/utils/format-utils.js:54-145`
- Modify: `cmd/agent-terminal/frontend/vue-app/format-utils.test.js:136-146`
- Modify: `cmd/agent-terminal/frontend/vue-app/composables/useThreadCards.js:44-62`
- Modify: `cmd/agent-terminal/frontend/vue-app/use-thread-cards.test.js:190-222`

- [ ] **Step 1: Add failing formatter tests**

In `cmd/agent-terminal/frontend/vue-app/format-utils.test.js`, extend the `tool activity formatting` block:

```js
it('marks parsed preview success false as failed even when top-level status completed', () => {
  expect(summarizeToolActivity('mcp__lsp__grep', {
    status: 'completed',
    preview: '{"success":false,"error":"invalid glob ["}',
  })).toEqual({
    name: 'grep',
    summary: '搜索代码失败',
    status: 'failed',
  });

  expect(summarizeToolActivity('future.vendor/scan', {
    status: 'completed',
    preview: '{"success":false,"error":"scan boom"}',
  })).toEqual({
    name: 'future_vendor_scan',
    summary: '执行失败：scan boom',
    status: 'failed',
  });
});

it('marks parsed MCP isError payloads as failed', () => {
  expect(summarizeToolActivity('mcp__lsp__file', {
    status: 'completed',
    preview: '{"isError":true,"error":"outside workspace"}',
  })).toEqual({
    name: 'file',
    summary: '读取文件失败',
    status: 'failed',
  });
});
```

- [ ] **Step 2: Add failing thread card test**

In `cmd/agent-terminal/frontend/vue-app/use-thread-cards.test.js`, add a timeline case where the backend item is transport-completed but the parsed payload is failed:

```js
it('marks tool cards failed when preview JSON reports success false', () => {
  const vm = createThreadCards({
    activeTimeline: [
      {
        id: 'tool-preview-fail',
        kind: 'tool',
        tool: 'mcp__lsp__grep',
        status: 'completed',
        preview: '{"success":false,"error":"invalid glob ["}',
        ts: '2026-03-09T10:00:00Z',
      },
    ],
  });

  expect(vm.activeProcessActivity.value).toEqual([
    expect.objectContaining({
      kind: 'tool',
      status: 'failed',
      message: 'grep · 搜索代码失败',
    }),
  ]);
});
```

- [ ] **Step 3: Run tests and confirm failure**

Run:

```bash
cd cmd/agent-terminal/frontend
npx vitest run vue-app/format-utils.test.js vue-app/use-thread-cards.test.js
```

Expected: FAIL because `summarizeToolActivity` currently computes `failed` before parsing `preview`, so `{"success":false}` inside preview is ignored unless `item.success === false`.

- [ ] **Step 4: Detect failure from parsed preview**

Change `summarizeToolActivity` to parse `preview` before computing the final failed flag:

```js
function parsedToolFailure(result) {
  if (!result || typeof result !== 'object') return false;
  if (result.success === false) return true;
  if (result.isError === true) return true;
  if ((result.error || '').toString().trim()) return true;
  if ((result.error_code || '').toString().trim() && result.error_code !== 'none') return true;
  return false;
}

export function summarizeToolActivity(toolName, item = {}) {
  const name = displayToolName(toolName);
  const status = (item?.status || '').toString().trim().toLowerCase();
  const preview = item?.preview || item?.error || '';
  const result = parsedToolResult(preview);
  const failed = item?.success === false
    || status === 'failed'
    || status === 'error'
    || Boolean((item?.error || '').toString().trim())
    || parsedToolFailure(result);
  if (status === 'running' && !failed) return { name, summary: '执行中', status: 'active' };

  const known = knownToolSummary(name, failed, result, preview);
  if (known) return { name, summary: known, status: failed ? 'failed' : 'done' };
  if (failed) return { name, summary: `执行失败${toolFailureSuffix(result, preview)}`, status: 'failed' };
  return { name, summary: '已完成', status: 'done' };
}
```

Keep `status === 'running' && !failed` semantics so active calls remain active until an actual failure payload arrives.

- [ ] **Step 5: Preserve failure details in summaries**

If a known LSP tool has `result.error`, include it where the existing helper already supports suffixes. At minimum, generic tools must show `执行失败：<error>`. Do not show "已完成", "已搜索代码", or "搜索无结果" when parsed payload says `success:false`.

- [ ] **Step 6: Run focused frontend checks**

Run:

```bash
cd cmd/agent-terminal/frontend
npx vitest run vue-app/format-utils.test.js vue-app/use-thread-cards.test.js
```

Expected: PASS.

- [ ] **Step 7: Run required frontend verification**

Run:

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

Expected: PASS.

---

### Task 8: Whole-Surface Verification and Review Loop

**Files:**
- Review all files changed by Tasks 1-7.

- [ ] **Step 1: Run targeted affected packages**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/codexapp ./internal/mcpserver/common ./internal/platform/toolbridge ./cmd/mcp-lsp/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run frontend verification**

Run:

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

Expected: PASS.

- [ ] **Step 3: Run guard**

Run:

```bash
make guard
```

Expected: PASS.

- [ ] **Step 4: Inspect baseline diff if any**

Run:

```bash
git diff -- internal/archtest/baseline.json
```

Expected: empty diff unless the task explicitly changed code-size baseline. If non-empty, inspect and report every changed file entry. Do not run `go run scripts/code_size_guard.go --freeze` without explicit user approval.

- [ ] **Step 5: Dispatch independent reviewers**

Use two read-only reviewer agents with this prompt:

```text
Review the implementation for docs/superpowers/plans/2026-05-20-lsp-error-transparency.md.
Scope: built-in MCP LSP fail-fast/error transparency only.
Find any remaining P0/P1 where a requested LSP operation, peer launch/list, diagnostics/bootstrap, search, edit, file, installer, cache, sandbox, or frontend activity rendering failure can still return or display a successful tool result.
Do not edit files. Return findings ordered by severity with exact file/line references and tests that prove the issue.
```

- [ ] **Step 6: Iterate until no P0/P1**

If any reviewer reports a plausible P0/P1:

1. Add or update a failing regression test for the reported path.
2. Implement the smallest fix.
3. Re-run the affected package command.
4. Re-dispatch review on the changed diff.

Completion criterion: both reviewers report no P0/P1, and targeted verification plus `make guard` pass.
