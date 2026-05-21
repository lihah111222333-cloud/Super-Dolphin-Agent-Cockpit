# mcp-ida P0/P1 Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the current P0/P1 failure modes in `cmd/mcp-ida` without shipping fake IDA tool stubs.

**Architecture:** Treat `mcp-ida` as an honest placeholder until a real IDA tool registry exists: it must not advertise `tools/ida` or appear in generated manifests as a callable tool family. It still must keep stdio framing clean, fail fast on missing required control-plane config, and return protocol-valid empty tool lists where an empty provider is exercised.

**Tech Stack:** Go 1.25.7, Fx, `internal/mcpserver/common`, bootstrap control-plane client, `internal/provider/manifestbuilder`, `internal/platform/toolbridge`.

---

## Review Findings

### P0: stdout MCP framing pollution

Evidence:
- `pkg/logger` initializes before `main()` and binds production logs to the original `os.Stdout`: `pkg/logger/logger.go:44`, `pkg/logger/logger.go:108`.
- `cmd/mcp-ida/main.go:25` saves original stdout for MCP protocol writes and `cmd/mcp-ida/main.go:26` redirects `os.Stdout`, but does not reinitialize `pkglogger`.
- `internal/mcpserver/common/server.go:195` logs as soon as the stdio server starts, so normal startup can write non-MCP log JSON to the same original stdout used by `common.NewStdioTransport` at `cmd/mcp-ida/fx.go:140`.

Impact: stdio clients can fail handshake or JSON-RPC framing before any IDA tool call.

### P1: `tools/ida` advertised without a callable tool surface

Evidence:
- `cmd/mcp-ida/fx.go:45` sets `cfg.Capabilities = []string{"tools/ida"}`.
- `cmd/mcp-ida` never sets `bootstrap.Config.OnToolsList` or `bootstrap.Config.OnToolsCall`.
- `internal/mcpserver/common/bootstrap/lifecycle.go:131` and `:138` fail closed when those callbacks are nil.
- Direct stdio uses `emptyToolProvider`; `ListTools` returns nil at `cmd/mcp-ida/fx.go:127` and `CallTool` always returns unknown tool at `cmd/mcp-ida/fx.go:131`.

Impact: the control plane can discover an active `ida` service while every real tools/list or tools/call path is unavailable or empty.

### P1: missing `RPCAddr` becomes quiet long-running success

Evidence:
- `bootstrap.ReadBootConfig()` leaves `RPCAddr` empty when `GO_AGENT_CTL_RPC_ADDR` / `RPC_ADDR` is absent: `internal/mcpserver/common/bootstrap/env.go:39`.
- `cmd/mcp-ida/fx.go:111` blocks until cancellation and returns nil instead of surfacing the config error.
- The shared bootstrap client would fail fast on the same missing address at `internal/mcpserver/common/bootstrap/client.go:107`.

Impact: a production bootstrap misconfiguration can look healthy while no `ctl/register`, heartbeat, shutdown callback, config callback, or final report exists.

### P1: `/mcp/ida/...` accepts the family but rejects IDA tool names

Evidence:
- `internal/platform/toolbridge/proxy.go:328` maps path family `ida` to `ClientKindIDA`.
- `internal/platform/toolbridge/types.go:161` classifies LSP tools specially and defaults every non-LSP name to `orch`.
- `internal/platform/toolbridge/proxy.go:225` rejects tool calls when classified kind does not match the URL family.

Impact: once real `ida_*` tools are restored, the proxy path is already wired in a way that rejects every IDA tool call.

---

## File Structure

Modify:
- `cmd/mcp-ida/main.go`: rebind logger after stdout redirection.
- `cmd/mcp-ida/fx.go`: remove false `tools/ida` advertisement, return empty arrays instead of nil lists, fail fast on missing RPC address.
- `cmd/mcp-ida/fx_test.go`: add regression tests for empty provider and missing RPC address.
- `cmd/mcp-ida/main_test.go`: add regression test proving startup logging does not use protocol stdout.
- `internal/contract/manifest.go`: stop emitting `mcp-ida` manifest entries for the current `ida` capability until real tools exist.
- `internal/provider/unified/manifest_test.go`: update manifest expectations so `ida` no longer emits a callable binary/proxy endpoint.
- `internal/platform/toolbridge/types.go`: classify `ida_` tool names as `ClientKindIDA` for the future real tool surface.
- `internal/platform/toolbridge/types_test.go`: add classifier coverage for `ida_` names.

Do not modify:
- `internal/mcpserver/common/bootstrap/*`: the shared client already fails fast when called with an empty RPC address.
- `internal/mcpserver/common/server.go`: the server behavior is correct; `mcp-ida` must stop feeding it an invalid provider/transport setup.
- V2 IDA migration docs: this plan fixes current P0/P1 only; real IDA migration needs a separate implementation plan.

---

## Task 1: Keep MCP stdout protocol-clean

**Files:**
- Modify: `cmd/mcp-ida/main.go`
- Test: `cmd/mcp-ida/main_test.go`

- [ ] **Step 1: Write the failing regression test**

Create `cmd/mcp-ida/main_test.go`:

```go
package main

import (
	"os"
	"testing"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestProtectMCPStdoutRebindsLoggerOffProtocolStdout(t *testing.T) {
	originalStdout := os.Stdout
	originalStderr := os.Stderr
	t.Cleanup(func() {
		os.Stdout = originalStdout
		os.Stderr = originalStderr
		mcpStdout = nil
		pkglogger.InitWithConsoleWriter(os.Stderr)
	})

	protocolRead, protocolWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("protocol pipe: %v", err)
	}
	defer protocolRead.Close()
	defer protocolWrite.Close()

	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	defer stderrRead.Close()
	defer stderrWrite.Close()

	os.Stdout = protocolWrite
	os.Stderr = stderrWrite
	pkglogger.InitWithConsoleWriter(os.Stdout)

	protectMCPStdout()
	pkglogger.Info("mcp-ida logger routing test")

	if mcpStdout != protocolWrite {
		t.Fatalf("mcpStdout = %v, want original protocol stdout", mcpStdout)
	}
	if os.Stdout != os.Stderr {
		t.Fatalf("os.Stdout was not redirected to stderr")
	}

	if err := protocolWrite.Close(); err != nil {
		t.Fatalf("close protocol writer: %v", err)
	}
	buf := make([]byte, 1)
	n, _ := protocolRead.Read(buf)
	if n != 0 {
		t.Fatalf("logger wrote %q to protocol stdout", string(buf[:n]))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-ida -run TestProtectMCPStdoutRebindsLoggerOffProtocolStdout -count=1
```

Expected: fail because `protectMCPStdout` does not exist.

- [ ] **Step 3: Implement the minimal fix**

Change `cmd/mcp-ida/main.go`:

```go
func protectMCPStdout() {
	mcpStdout = os.Stdout
	os.Stdout = os.Stderr
	pkglogger.InitWithConsoleWriter(os.Stderr)
}
```

Then replace the direct stdout setup in `main()` with:

```go
protectMCPStdout()
```

- [ ] **Step 4: Verify**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-ida -run TestProtectMCPStdoutRebindsLoggerOffProtocolStdout -count=1
```

Expected: pass.

---

## Task 2: Make the placeholder provider protocol-valid and fail-fast

**Files:**
- Modify: `cmd/mcp-ida/fx.go`
- Test: `cmd/mcp-ida/fx_test.go`

- [ ] **Step 1: Write failing tests**

Create or extend `cmd/mcp-ida/fx_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
)

func TestEmptyToolProviderListToolsReturnsEmptyArray(t *testing.T) {
	tools, err := emptyToolProvider{}.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if tools == nil {
		t.Fatalf("ListTools() returned nil; want empty non-nil slice")
	}
	if len(tools) != 0 {
		t.Fatalf("ListTools() len = %d, want 0", len(tools))
	}
}

func TestEmptyToolProviderCallToolFailsClosed(t *testing.T) {
	_, err := emptyToolProvider{}.CallTool(context.Background(), "ida_ping", json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("CallTool() error = nil, want unknown tool error")
	}
}

func TestBootstrapRunnerRequiresRPCAddr(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	runner := bootstrapRunner{
		cfg:        bootstrap.Config{BinaryName: "mcp-ida"},
		client:     bootstrap.New(bootstrap.Config{BinaryName: "mcp-ida"}),
		stdioReady: ready,
	}
	err := runner.Run(ctx)
	if err == nil {
		t.Fatalf("Run() error = nil, want missing RPC address error")
	}
}
```

- [ ] **Step 2: Run tests to verify failures**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-ida -run 'TestEmptyToolProvider|TestBootstrapRunnerRequiresRPCAddr' -count=1
```

Expected: provider nil-slice test fails and missing-RPC test blocks until context timeout or returns nil.

- [ ] **Step 3: Implement provider and RPC fixes**

Change `cmd/mcp-ida/fx.go`:

```go
func (emptyToolProvider) ListTools(context.Context) ([]mcp.MCPTool, error) {
	return []mcp.MCPTool{}, nil
}
```

Change `bootstrapRunner.Run` so missing `RPCAddr` returns an error:

```go
if strings.TrimSpace(r.cfg.RPCAddr) == "" {
	return errors.New("mcp-ida: GO_AGENT_CTL_RPC_ADDR is required")
}
```

- [ ] **Step 4: Remove false capability advertisement**

Delete this line from `cmd/mcp-ida/fx.go`:

```go
cfg.Capabilities = []string{"tools/ida"}
```

Add a short comment near the config construction:

```go
// Do not advertise tools/ida until a real IDA provider registers
// concrete schemas and handlers. An empty provider is a placeholder,
// not a tool capability.
```

- [ ] **Step 5: Verify**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-ida -count=1
```

Expected: pass.

---

## Task 3: Stop manifest/proxy exposure for unfinished IDA tools

**Files:**
- Modify: `internal/contract/manifest.go`
- Modify: `internal/provider/unified/manifest_test.go`

- [ ] **Step 1: Update manifest tests first**

Change `TestBuildManifest_WithIDA` so `ida` thread capability does not emit an IDA binary:

```go
func TestBuildManifest_WithIDADoesNotExposeUnimplementedIDATools(t *testing.T) {
	got := manifestbuilder.BuildManifest(dto.ManifestContext{ThreadCaps: dto.CapabilitySet{"ida": true}})
	if len(got.Binaries) != 2 {
		t.Fatalf("unexpected ida manifest: %+v", got.Binaries)
	}
	for _, binary := range got.Binaries {
		if binary.Name == "ida" {
			t.Fatalf("manifest exposed unimplemented ida binary: %+v", got.Binaries)
		}
	}
}
```

Change `TestBuildManifest_UsesProxyHTTPAddr` expected URLs to remove `/mcp/ida/...` and expect exactly two binaries:

```go
want := []string{
	"http://127.0.0.1:39001/mcp/lsp/agent-1",
	"http://127.0.0.1:39001/mcp/orch/agent-1",
}
```

- [ ] **Step 2: Run tests to verify failures**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/unified ./internal/contract -run 'TestBuildManifest_WithIDA|TestBuildManifest_UsesProxyHTTPAddr' -count=1
```

Expected: fail because `BuildManifest` still appends `dto.FamilyIDA`.

- [ ] **Step 3: Implement the manifest gate**

Change `internal/contract/manifest.go` so the current `ida` capability no longer appends `dto.FamilyIDA`:

```go
families := []dto.ToolFamily{dto.FamilyLSP, dto.FamilyOrch}
```

Do not add a replacement hidden capability in this fix. Re-enabling IDA should happen only in the future real-tool migration, together with concrete schemas, handlers, and proxy tests.

- [ ] **Step 4: Verify**

Run:

```bash
./scripts/test_with_guard.sh ./internal/provider/unified ./internal/contract -count=1
```

Expected: pass.

---

## Task 4: Make future IDA proxy routing correct

**Files:**
- Modify: `internal/platform/toolbridge/types.go`
- Test: `internal/platform/toolbridge/types_test.go`

- [ ] **Step 1: Write classifier regression test**

Extend `internal/platform/toolbridge/types_test.go`:

```go
func TestClassifyToolIDANames(t *testing.T) {
	for _, name := range []string{"ida_ping", "ida_decompile", "ida_frida_attach"} {
		if got := classifyTool(name); got != dto.ClientKindIDA {
			t.Fatalf("classifyTool(%q) = %q, want %q", name, got, dto.ClientKindIDA)
		}
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/toolbridge -run TestClassifyToolIDANames -count=1
```

Expected: fail because `ida_*` currently defaults to `orch`.

- [ ] **Step 3: Implement classifier**

Change `internal/platform/toolbridge/types.go`:

```go
if strings.HasPrefix(trimmed, "ida_") {
	return dto.ClientKindIDA
}
```

Place it before the default `return dto.ClientKindOrch`.

- [ ] **Step 4: Verify**

Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/toolbridge -count=1
```

Expected: pass.

---

## Task 5: Final verification

**Files:**
- Verify only.

- [ ] **Step 1: Run affected package tests**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-ida ./internal/contract ./internal/provider/unified ./internal/platform/toolbridge -count=1
```

Expected: pass.

- [ ] **Step 2: Run guard**

Run:

```bash
make guard
```

Expected: pass with no new architecture or baseline failure.

- [ ] **Step 3: Inspect git diff**

Run:

```bash
git status --short
git diff -- cmd/mcp-ida/main.go cmd/mcp-ida/fx.go cmd/mcp-ida/main_test.go cmd/mcp-ida/fx_test.go internal/contract/manifest.go internal/provider/unified/manifest_test.go internal/platform/toolbridge/types.go internal/platform/toolbridge/types_test.go
```

Expected: only the planned files changed; no unrelated codemap or pre-existing plan edits are staged or modified by this work.

---

## Exit Criteria

- P0 stdout pollution is closed: `pkglogger` writes to stderr after `mcp-ida` protects protocol stdout.
- P1 false capability is closed: current `mcp-ida` no longer advertises `tools/ida`.
- P1 quiet config success is closed: missing `GO_AGENT_CTL_RPC_ADDR` returns an error in `mcp-ida`.
- P1 manifest/proxy exposure is closed: generated manifests no longer expose `/mcp/ida/...` for the current placeholder, and future `ida_*` tools classify to `ClientKindIDA`.
- Direct `tools/list` on the empty provider serializes as `[]`, not `null`.
- No fake IDA stubs are introduced.

## Deferred Work

Re-enable IDA only with a separate migration plan that adds:
- concrete IDA tool manifests and JSON schemas,
- real handlers backed by the IDA gateway/provider,
- `bootstrap.Config.OnToolsList` and `OnToolsCall` wired to that provider,
- manifest tests that expose IDA only when the provider is complete,
- proxy tests proving `/mcp/ida/<agent>` accepts `ida_*` tool calls and rejects non-IDA names.
