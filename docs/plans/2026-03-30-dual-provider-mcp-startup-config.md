# Claude/Codex 统一 MCP 启动配置 实现计划

> **给 Claude:** 必须使用 @执行计划 逐任务实现此计划。

**目标:** 保持 `dto.MCPManifest` 作为 Claude/Codex 唯一的上层 MCP 启动配置模型；本次只实现 **start 路径**：Claude 继续使用 `--mcp-config`，Codex 在 `thread/start` 前通过 App Server 官方 `config/*` RPC 注入同一份 manifest 并校验 managed server 状态。

**架构:** 共享层只做 `MCPManifest -> provider-specific projection` 的轻量投影，不承载 provider RPC 合约。Claude adapter 继续把 manifest 渲染成 CLI `mcpServers` JSON；Codex adapter 把同一份 manifest 转换成 `config/batchWrite -> config/mcpServer/reload -> mcpServerStatus/list` 的调用参数。**本期不做 `resume` 注入**，因为当前 `ResumeSessionRequest` 没有 `Config` 来源，无法安全重建同一份 manifest。

**技术栈:** Go、JSON-RPC over WebSocket、Claude CLI `--mcp-config`、Codex App Server `config/*` RPC、Go testing

---

## 范围 / 非目标

### 本期范围（必须完成）
- `start` 路径共享同一份 `dto.MCPManifest`
- Codex 在 `thread/start` 前注入 managed MCP servers
- 保持 Claude 现有 manifest 路径不倒退
- 增加最小测试闭环和日志/观测护栏

### 明确非目标（本期禁止）
- **不做 `resume` 注入**
- **不**在 `thread/start` 里传 undocumented `mcp` / `mcpServers`
- **不**回退到 V2 `dynamicTools` 方案
- **不**手工改磁盘配置文件
- **不**覆盖用户现有外部 MCP key（`exa`、`postgres` 等）
- **不**把 Codex 的 `config/*` RPC 细节塞进 `internal/dto/provider/manifest.go`
- **不**把启动配置注入逻辑塞进 `internal/provider/codexapp/transport.go`

### 延后事项（单独计划）
- `ResumeSessionRequest` 增加 `Config` / manifest 来源
- Claude `ResumeSession` 带 manifest
- Codex `thread/resume` 注入同一份 manifest

---

## 只读参考（实现前先看）

### V2 参考
- Claude：
  - `/Users/mima0000/Desktop/wj/go-agent-v2/legacy-agentsdk/claude/client.go:176-239`
  - `/Users/mima0000/Desktop/wj/go-agent-v2/legacy-agentsdk/claude/client_cli_transport.go:50-102`
  - `/Users/mima0000/Desktop/wj/go-agent-v2/legacy-agentsdk/claude/client_cli_transport.go:201-322`
- Codex：
  - `/Users/mima0000/Desktop/wj/go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go:38-50`
  - `/Users/mima0000/Desktop/wj/go-agent-v2/legacy-agentsdk/codex/client_appserver_protocol.go:102-149`
  - `/Users/mima0000/Desktop/wj/go-agent-v2/legacy-agentsdk/codex/client_appserver_runtime.go:129-156`
  - `/Users/mima0000/Desktop/wj/go-agent-v2/internal/runner/manager_launch.go:119-198`
  - `/Users/mima0000/Desktop/wj/go-agent-v2/legacy-agentsdk/agentcore/types.go:83-104`

### 当前仓库
- 共享上层模型：
  - `internal/dto/provider/manifest.go:17-60`
- Claude 当前路径：
  - `internal/provider/claudecli/driver.go:61-84`
  - `internal/provider/claudecli/transport_config.go:25-224`
  - `internal/provider/claudecli/transport_config_test.go:11-48`
- Codex 当前路径：
  - `internal/provider/codexapp/driver.go:90-130`
  - `internal/provider/codexapp/driver.go:249-367`
  - `internal/provider/codexapp/event_map.go:25-77`
  - `internal/provider/codexapp/driver_session_test.go:29-192`
  - `internal/provider/codexapp/transport_local_test.go:30-85`
- 共享 env 规范化守护：
  - `internal/provider/unified/manifest_test.go:11-155`
  - `internal/mcpserver/common/bootstrap/env_test.go:73-104`

---

## 代码预算（必须遵守）

| 文件 | 目标新增 LOC | 硬上限 LOC | 备注 |
|---|---:|---:|---|
| `internal/provider/mcpstartup/adapter.go` | 40 | 80 | 只放共享投影 helper |
| `internal/provider/mcpstartup/adapter_test.go` | 60 | 100 | 最多 4 个测试 |
| `internal/provider/codexapp/mcp_startup.go` | 60 | 100 | Codex 注入编排逻辑 |
| `internal/provider/codexapp/mcp_startup_test.go` | 80 | 140 | 最多 4 个测试 |
| `internal/provider/codexapp/driver.go` | 12 | 30 | 只接调用顺序 |
| `internal/provider/codexapp/driver_session_test.go` | 35 | 70 | 最多 2 个测试 |
| `internal/provider/claudecli/transport_config_test.go` | 10 | 25 | 只补 guard test |
| `internal/provider/codexapp/event_map_test.go` | 10 | 25 | 只补 guard test |

**其余生产文件目标新增 LOC = 0。**

---

### 任务 1: 建立共享 MCP 启动投影层（只放共享 helper）

**文件:**
- 创建: `internal/provider/mcpstartup/adapter.go`
- 创建: `internal/provider/mcpstartup/adapter_test.go`
- 测试: `internal/provider/unified/manifest_test.go:11-155`

**步骤 1: 写失败的测试**

```go
package mcpstartup

import (
    "testing"

    dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestBuildCodexConfigWritesUsesManagedBinaryNames(t *testing.T) {
    manifest := dto.MCPManifest{Binaries: []dto.MCPBinary{
        {
            Name:        "go-agent-mcp-lsp",
            Command:     []string{"/tmp/bin/go-agent-mcp-lsp", "bridge"},
            Env:         map[string]string{"GO_AGENT_CTL_RPC_ADDR": "127.0.0.1:9000"},
            AutoApprove: []string{"lsp_file", "lsp_grep"},
        },
        {
            Name:    "go-agent-mcp-orch",
            Command: []string{"/tmp/bin/go-agent-mcp-orch"},
        },
    }}

    writes := BuildCodexConfigWrites(manifest, "/tmp/work")

    assertWrite(t, writes, "mcp_servers.go-agent-mcp-lsp.command", "/tmp/bin/go-agent-mcp-lsp")
    assertWrite(t, writes, "mcp_servers.go-agent-mcp-lsp.args", []string{"bridge"})
    assertWrite(t, writes, "mcp_servers.go-agent-mcp-lsp.cwd", "/tmp/work")
    assertWrite(t, writes, "mcp_servers.go-agent-mcp-lsp.required", true)
    assertWrite(t, writes, "mcp_servers.go-agent-mcp-orch.command", "/tmp/bin/go-agent-mcp-orch")
}

func TestValidateManagedStatusesRequiresReadyManagedServers(t *testing.T) {
    manifest := dto.MCPManifest{Binaries: []dto.MCPBinary{{Name: "go-agent-mcp-lsp", Command: []string{"/tmp/bin/go-agent-mcp-lsp"}}}}

    err := ValidateManagedStatuses(manifest, []ServerStatus{{Name: "go-agent-mcp-lsp", Status: "starting"}})
    if err == nil {
        t.Fatal("ValidateManagedStatuses() error = nil, want not-ready failure")
    }
}
```

**步骤 2: 运行测试确认失败**

运行:
```bash
go test ./internal/provider/mcpstartup -run 'TestBuildCodexConfigWritesUsesManagedBinaryNames|TestValidateManagedStatusesRequiresReadyManagedServers' -v
```

预期:
```text
FAIL ... package ./internal/provider/mcpstartup: cannot find package
```

**步骤 3: 写最小实现**

```go
package mcpstartup

import (
    "fmt"
    "maps"
    "strings"

    dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type ConfigWrite struct {
    Key   string
    Value any
}

type ServerStatus struct {
    Name   string
    Status string
    Error  string
}

func BuildCodexConfigWrites(manifest dto.MCPManifest, cwd string) []ConfigWrite {
    writes := make([]ConfigWrite, 0, len(manifest.Binaries)*6)
    for _, bin := range manifest.Binaries {
        prefix, ok := ManagedConfigPrefix(bin.Name)
        if !ok || len(bin.Command) == 0 {
            continue
        }
        writes = append(writes,
            ConfigWrite{Key: prefix + ".command", Value: strings.TrimSpace(bin.Command[0])},
            ConfigWrite{Key: prefix + ".args", Value: append([]string(nil), bin.Command[1:]...)},
            ConfigWrite{Key: prefix + ".env", Value: cloneEnv(bin.Env)},
            ConfigWrite{Key: prefix + ".cwd", Value: strings.TrimSpace(cwd)},
            ConfigWrite{Key: prefix + ".required", Value: true},
            ConfigWrite{Key: prefix + ".enabled", Value: true},
        )
    }
    return writes
}

func ValidateManagedStatuses(manifest dto.MCPManifest, statuses []ServerStatus) error {
    for _, bin := range manifest.Binaries {
        want, ok := ManagedServerName(bin.Name)
        if !ok {
            continue
        }
        found := false
        for _, status := range statuses {
            if strings.EqualFold(strings.TrimSpace(status.Name), want) {
                found = true
                if !strings.EqualFold(strings.TrimSpace(status.Status), "ready") {
                    return fmt.Errorf("managed mcp server %s status=%s error=%s", want, status.Status, status.Error)
                }
            }
        }
        if !found {
            return fmt.Errorf("managed mcp server %s missing from status list", want)
        }
    }
    return nil
}

func ManagedConfigPrefix(binaryName string) (string, bool) {
    if name, ok := ManagedServerName(binaryName); ok {
        return "mcp_servers." + name, true
    }
    return "", false
}

func ManagedServerName(binaryName string) (string, bool) {
    switch strings.TrimSpace(binaryName) {
    case "go-agent-mcp-lsp", "go-agent-mcp-orch", "go-agent-mcp-ida":
        return strings.TrimSpace(binaryName), true
    default:
        return "", false
    }
}

func cloneEnv(in map[string]string) map[string]string {
    if len(in) == 0 {
        return map[string]string{}
    }
    out := make(map[string]string, len(in))
    maps.Copy(out, in)
    return out
}
```

**步骤 4: 运行测试确认通过**

运行:
```bash
go test ./internal/provider/mcpstartup -run 'TestBuildCodexConfigWritesUsesManagedBinaryNames|TestValidateManagedStatusesRequiresReadyManagedServers' -v
go test ./internal/provider/unified -run 'TestBuildManifest_DefaultFamilies|TestBuildManifest_NormalizesControlEnvNames' -v
```

预期:
```text
PASS
PASS
```

**步骤 5: 提交**

```bash
git add internal/provider/mcpstartup/adapter.go internal/provider/mcpstartup/adapter_test.go
git commit -m "feat: add shared mcp startup projection helpers"
```

---

### 任务 2: 用测试锁死 Claude 继续依赖共享 manifest（start 路径）

**文件:**
- 修改: `internal/provider/claudecli/transport_config_test.go:11-48`
- 测试: `internal/provider/unified/manifest_test.go:11-155`

**步骤 1: 写失败的测试**

```go
func TestWriteManifestConfigUsesCommandArgsEnvAndAutoApprove(t *testing.T) {
    manifest := dto.MCPManifest{Binaries: []dto.MCPBinary{
        {
            Name:        "go-agent-mcp-lsp",
            Command:     []string{"/tmp/bin/go-agent-mcp-lsp", "bridge"},
            Env:         map[string]string{"GO_AGENT_CTL_RPC_ADDR": "127.0.0.1:9000"},
            AutoApprove: []string{"lsp_file"},
        },
    }}

    path, cleanup, err := writeManifestConfig(manifest, "/tmp/work")
    if err != nil {
        t.Fatalf("writeManifestConfig() error = %v", err)
    }
    defer cleanup()

    doc := readJSON(t, path)
    server := doc["mcpServers"].(map[string]any)["go-agent-mcp-lsp"].(map[string]any)
    if got := server["command"]; got != "/tmp/bin/go-agent-mcp-lsp" {
        t.Fatalf("command = %#v", got)
    }
    if got := server["args"]; !reflect.DeepEqual(got, []any{"bridge"}) {
        t.Fatalf("args = %#v", got)
    }
}
```

**步骤 2: 运行测试确认失败**

运行:
```bash
go test ./internal/provider/claudecli -run 'TestWriteManifestConfigIncludesEnvAndAutoApprove|TestWriteManifestConfigUsesCommandArgsEnvAndAutoApprove' -v
```

预期:
```text
FAIL ... missing helper readJSON or assertion mismatch
```

**步骤 3: 写最小实现**

```go
func readJSON(t *testing.T, path string) map[string]any {
    t.Helper()
    raw, err := os.ReadFile(path)
    if err != nil {
        t.Fatalf("ReadFile(%q) error = %v", path, err)
    }
    var doc map[string]any
    if err := json.Unmarshal(raw, &doc); err != nil {
        t.Fatalf("Unmarshal(%q) error = %v", path, err)
    }
    return doc
}
```

**步骤 4: 运行测试确认通过**

运行:
```bash
go test ./internal/provider/claudecli -run 'TestWriteManifestConfigIncludesEnvAndAutoApprove|TestWriteManifestConfigUsesCommandArgsEnvAndAutoApprove' -v
go test ./internal/provider/unified -run 'TestBuildManifest_DefaultFamilies|TestBuildManifest_NormalizesControlEnvNames' -v
```

预期:
```text
PASS
PASS
```

**步骤 5: 提交**

```bash
git add internal/provider/claudecli/transport_config_test.go
git commit -m "test: guard claude manifest projection"
```

---

### 任务 3: 给 Codex 新增 start-only MCP 注入编排 seam

**文件:**
- 创建: `internal/provider/codexapp/mcp_startup.go`
- 创建: `internal/provider/codexapp/mcp_startup_test.go`
- 修改: `internal/provider/codexapp/driver.go:90-130`
- 修改: `internal/provider/codexapp/driver_session_test.go:29-192`
- 测试: `internal/provider/codexapp/driver_session_test.go:29-192`

**步骤 1: 写失败的测试**

```go
func TestInjectManagedMCPServersBatchWritesReloadsAndValidates(t *testing.T) {
    transport := newStubTransport([]stubCall{
        {Method: "config/batchWrite", Result: []byte(`{"ok":true}`)},
        {Method: "config/mcpServer/reload", Result: []byte(`{"ok":true}`)},
        {Method: "mcpServerStatus/list", Result: []byte(`{"servers":[{"name":"go-agent-mcp-lsp","status":"ready"},{"name":"go-agent-mcp-orch","status":"ready"}]}`)},
    })
    manifest := dto.MCPManifest{Binaries: []dto.MCPBinary{
        {Name: "go-agent-mcp-lsp", Command: []string{"/tmp/bin/go-agent-mcp-lsp"}},
        {Name: "go-agent-mcp-orch", Command: []string{"/tmp/bin/go-agent-mcp-orch"}},
    }}

    if err := injectManagedMCPServers(context.Background(), transport, manifest, "/tmp/work"); err != nil {
        t.Fatalf("injectManagedMCPServers() error = %v", err)
    }
    assertMethods(t, transport.calls,
        "config/batchWrite",
        "config/mcpServer/reload",
        "mcpServerStatus/list",
    )
}

func TestDriverStartSessionInjectsManagedMCPServersBeforeThreadStart(t *testing.T) {
    // expected call order:
    // config/batchWrite -> config/mcpServer/reload -> mcpServerStatus/list -> thread/start
}
```

**步骤 2: 运行测试确认失败**

运行:
```bash
go test ./internal/provider/codexapp -run 'TestInjectManagedMCPServersBatchWritesReloadsAndValidates|TestDriverStartSessionInjectsManagedMCPServersBeforeThreadStart' -v
```

预期:
```text
FAIL ... undefined: injectManagedMCPServers
FAIL ... expected call order [config/batchWrite config/mcpServer/reload mcpServerStatus/list thread/start]
```

**步骤 3: 写最小实现**

```go
package codexapp

import (
    "context"
    "encoding/json"
    "fmt"

    dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
    "github.com/anthropic-ai/super-agent-v3/internal/provider/mcpstartup"
)

type rpcCaller interface {
    Call(ctx context.Context, method string, params any) (json.RawMessage, error)
}

type mcpStatusList struct {
    Servers []struct {
        Name   string `json:"name"`
        Status string `json:"status"`
        Error  string `json:"error"`
    } `json:"servers"`
}

func startManifest(req dto.StartSessionRequest) dto.MCPManifest {
    return dto.BuildManifest(dto.ManifestContext{
        AgentID:     strings.TrimSpace(req.AgentID),
        CWD:         strings.TrimSpace(req.CWD),
        ThreadCaps:  copyCapabilities(codexCapabilities),
        BinaryDir:   resolveBinaryDir(req.CWD, req.Config),
        Env:         stringMap(req.Config["env"]),
        AutoApprove: configStringSlice(req.Config, "auto_approve", "autoApprove"),
    })
}

func injectManagedMCPServers(ctx context.Context, t rpcCaller, manifest dto.MCPManifest, cwd string) error {
    writes := mcpstartup.BuildCodexConfigWrites(manifest, cwd)
    if len(writes) == 0 {
        return nil
    }
    if _, err := t.Call(ctx, "config/batchWrite", map[string]any{"writes": writes}); err != nil {
        return err
    }
    if _, err := t.Call(ctx, "config/mcpServer/reload", map[string]any{}); err != nil {
        return err
    }
    raw, err := t.Call(ctx, "mcpServerStatus/list", map[string]any{})
    if err != nil {
        return err
    }
    statuses, err := decodeMCPStatuses(raw)
    if err != nil {
        return err
    }
    return mcpstartup.ValidateManagedStatuses(manifest, statuses)
}
```

**步骤 4: 运行测试确认通过**

运行:
```bash
go test ./internal/provider/codexapp -run 'TestInjectManagedMCPServersBatchWritesReloadsAndValidates|TestDriverStartSessionInjectsManagedMCPServersBeforeThreadStart' -v
```

预期:
```text
PASS
```

**步骤 5: 提交**

```bash
git add internal/provider/codexapp/mcp_startup.go internal/provider/codexapp/mcp_startup_test.go internal/provider/codexapp/driver.go internal/provider/codexapp/driver_session_test.go
git commit -m "feat: inject managed mcp servers before codex thread start"
```

---

### 任务 4: 用最小改动把 Codex start 路径接到注入逻辑

**文件:**
- 修改: `internal/provider/codexapp/driver.go:90-130`
- 测试: `internal/provider/codexapp/driver_session_test.go:29-192`

**步骤 1: 写失败的测试**

```go
func TestDriverStartSessionStopsSessionWhenMCPInjectionFails(t *testing.T) {
    // stub transport returns error from config/batchWrite
    // expect StartSession to return that error and force-stop the session
}
```

**步骤 2: 运行测试确认失败**

运行:
```bash
go test ./internal/provider/codexapp -run 'TestDriverStartSessionStopsSessionWhenMCPInjectionFails' -v
```

预期:
```text
FAIL ... StartSession() error = nil, want batchWrite failure
```

**步骤 3: 写最小实现**

```go
func (d *driver) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
    s, err := newSession(ctx, d.logger, d.serverURL, req.AgentID, d.eventDispatcher, d.approvals)
    if err != nil {
        return nil, err
    }
    manifest := startManifest(req)
    if err := injectManagedMCPServers(ctx, s.transport, manifest, req.CWD); err != nil {
        shared.LogIgnoredError(d.logger, "force stop failed on mcp inject error", s.ForceStop())
        return nil, err
    }
    // existing thread/start flow remains unchanged
}
```

**步骤 4: 运行测试确认通过**

运行:
```bash
go test ./internal/provider/codexapp -run 'TestDriverStartSessionStopsSessionWhenMCPInjectionFails|TestDriverStartSessionInjectsManagedMCPServersBeforeThreadStart' -v
```

预期:
```text
PASS
```

**步骤 5: 提交**

```bash
git add internal/provider/codexapp/driver.go internal/provider/codexapp/driver_session_test.go
git commit -m "refactor: connect codex start session to shared mcp startup injection"
```

---

### 任务 5: 补最小观测护栏（测试优先，尽量不动生产代码）

**文件:**
- 修改: `internal/provider/codexapp/event_map_test.go:12-57`
- 测试: `internal/provider/codexapp/transport_local_test.go:30-85`
- 测试: `internal/provider/claudecli/transport_config_test.go:11-48`

**步骤 1: 写失败的测试**

```go
func TestTranslateCodexEventLogsMCPStartupStatusWithoutUnknownWarning(t *testing.T) {
    raw := dto.RawProviderEvent{
        EventType: "mcpServer/startupStatus/updated",
        Data: map[string]any{"agentId": "agent-1", "name": "go-agent-mcp-lsp", "status": "ready"},
    }

    output := captureLogs(t, func() {
        translateCodexEvent(raw, func(any) {})
    })

    if !strings.Contains(output, "mcp server startup status") {
        t.Fatalf("output = %q, want startup status log", output)
    }
    if strings.Contains(output, "unknown raw event") {
        t.Fatalf("output = %q, want no unknown warning", output)
    }
}
```

**步骤 2: 运行测试确认失败**

运行:
```bash
go test ./internal/provider/codexapp -run 'TestTranslateCodexEventLogsMCPStartupStatusWithoutUnknownWarning|TestTransportSpawnLocalWaitsForReady' -v
```

预期:
```text
FAIL ... want startup status log
```

**步骤 3: 写最小实现**

```go
// Prefer reusing existing startup-status logging in event_map.go.
// Only add production code if the new test proves current behavior is insufficient.
```

**步骤 4: 运行测试确认通过**

运行:
```bash
go test ./internal/provider/codexapp -run 'TestTranslateCodexEventLogsMCPStartupStatusWithoutUnknownWarning|TestTransportSpawnLocalWaitsForReady|TestTransportCloseGracefullyStopsLocalProcess' -v
go test ./internal/provider/claudecli -run 'TestWriteManifestConfigIncludesEnvAndAutoApprove|TestWriteManifestConfigUsesCommandArgsEnvAndAutoApprove' -v
```

预期:
```text
PASS
PASS
```

**步骤 5: 提交**

```bash
git add internal/provider/codexapp/event_map_test.go internal/provider/codexapp/transport_local_test.go internal/provider/claudecli/transport_config_test.go
git commit -m "test: guard mcp startup observability on codex and claude"
```

---

## 最终验证（本期 start-only）

```bash
go test ./internal/provider/mcpstartup -count=1
go test ./internal/provider/claudecli -count=1
go test ./internal/provider/codexapp -count=1
go test ./internal/provider/unified -run 'TestBuildManifest_DefaultFamilies|TestBuildManifest_NormalizesControlEnvNames' -count=1
```

预期:
```text
ok  ./internal/provider/mcpstartup
ok  ./internal/provider/claudecli
ok  ./internal/provider/codexapp
ok  ./internal/provider/unified
```

## 交付检查单

- Claude 启动日志仍能看到 `claudecli: launch mcp manifest`
- Codex 启动路径能观察到 `config/batchWrite -> config/mcpServer/reload -> mcpServerStatus/list -> thread/start`
- `mcpServerStatus/list` 至少包含 `go-agent-mcp-lsp`、`go-agent-mcp-orch`；若线程具备 ida capability，则包含 `go-agent-mcp-ida`
- 用户原有 `exa`、`postgres` 不受影响
- 新实现不依赖 undocumented `thread/start.mcp*` 字段
- 计划未触碰 `resume`；resume 需求需另起计划
