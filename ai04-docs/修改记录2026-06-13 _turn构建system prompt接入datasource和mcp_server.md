# 修改记录 2026-06-13 turn 构建 system prompt 接入 datasource 和 mcp_server

## 需求

本次需求是补齐 `internal/module/turn` 构建 turn system prompt 时的运行时上下文：

- `datasource` 要进入 turn prompt 的动态 section。
- MCP 配置要读取 `internal/module/mcp_server` 管理的 server 配置，并进入 turn prompt 与 provider MCP manifest。

相关模块：

- `internal/module/turn`
- `internal/module/prompt`
- `internal/module/datasource`
- `internal/module/mcp_server`

## 行为变化

### datasource 进入 turn prompt

生产装配里已经加载 `datasource.Module` 和 `prompt.Module`，`datasource.Module` 会把 `datasource.PromptProvider` 注册到 prompt 动态 section registry。

本次补了 e2e 回归，确认 `AssembleTurn` 会把当前 workspace 的 datasource 正文注入到 `datasource` section：

```md
## datasource

Uploaded datasource file contents available in this workspace.

### launch-notes.txt
datasource text from FX wiring
```

覆盖点：

- `internal/module/prompt/e2e_test.go`
  - 新增 `TestDatasourceInjectsIntoTurnPrompt`
  - 在 FX harness 中注册 datasource store 和 prompt provider
  - 验证 turn prompt 的 `datasource` section 包含文件名和正文

### mcp_server 配置进入 turn prompt

`turn.Service` 现在新增可选依赖 `contract.MCPServerConfigProvider`，由 `mcp_server.Module` 暴露的 `AsMCPServerConfigProvider` 提供。

`PrepareTurn` 处理顺序变为：

1. 先根据 session 补齐 `PrepareInput`。
2. 读取当前 `GitRoot` 或 `CWD` 对应的 mcp_server 配置。
3. 归一化并合并到 `PrepareInput.MCPSnapshot.ServerConfigs`。
4. 构建 MCP manifest。
5. 将包含配置 server 的 `MCPSnapshot` 传入 prompt assembly。

合并后，turn prompt 看到的 MCP snapshot 会同时包含：

- 内置 managed server：`lsp`、`orch`
- 已在输入 snapshot 中存在的 server
- `mcp_server` 模块配置的 HTTP server

### mcp_server 配置进入 provider manifest

`manifestBuilder.Build` 现在会把 `MCPSnapshot.ServerConfigs` 转为 `dto.MCPBinary`，通过 `ManifestContext.ExtraBinaries` 传给 `contract.BuildManifest`。

生成的额外 MCP binary 形态：

```json
{
  "name": "my-search",
  "type": "http",
  "url": "https://your-domain.com/mcp",
  "headers": {
    "Authorization": "Bearer YOUR_API_KEY"
  }
}
```

`contract.BuildManifest` 已经支持 `ExtraBinaries`，并会跳过与内置 manifest 同名的 server。

## Fail-fast 规则

turn 侧新增的 MCP 配置归一化保持 fail-fast：

- server 名不能为空。
- server 名 trim 后不能重复。
- server 名不能与内置 managed server `lsp`、`orch` 冲突。
- `transport` 必填，目前只支持 `http`。
- `url` 必填。
- header key 和 value 都不能为空。
- 如果配置 server 名与 active server 冲突，且该 active server 不是已有 `ServerConfigs` 中的同一个配置项，会直接报错。

这个冲突策略避免静默覆盖 provider manifest 中已有的 MCP server。

## 主要代码变更

### `internal/module/turn/service.go`

- `service` 新增 `mcpServers contract.MCPServerConfigProvider`。
- `NewServiceWithPromptAssemblyAndTurnContext` 新增 optional `mcpServers` 参数。
- `PrepareTurn` 在构建 manifest 前调用 `hydrateMCPServerConfigs`。

### `internal/module/turn/module.go`

- `fx.ParamTags` 增加一个 optional 参数标签，让 `mcp_server.Module` 提供的 `contract.MCPServerConfigProvider` 能被注入。

### `internal/module/turn/service_helpers.go`

- 承接 turn service 的辅助逻辑，避免 `service.go` 超过 code size guard。
- 新增 MCP snapshot/config 归一化与合并 helper：
  - `hydrateMCPServerConfigs`
  - `normalizeTurnMCPSnapshot`
  - `mergeTurnConfiguredMCPServers`
  - `normalizeTurnMCPServerConfigs`
  - `normalizeTurnMCPServerConfig`
  - `normalizeTurnMCPServerHeaders`
  - `firstTurnMCPServerNameConflict`
  - `turnMCPServerConfigLookupRoot`
  - `uniqueTurnMCPServerNames`
  - `mergeTurnMCPServerConfigMaps`

### `internal/module/turn/factory.go`

- `mergeMCPSnapshot` 现在保留并合并 `ServerConfigs`。
- `Servers` 会包含 `ServerConfigs` 中的 server 名，避免 prompt 侧只看到配置内容却没有 server 列表。

### `internal/module/turn/manifest.go`

- `manifestBuilder.Build` 增加 `ExtraBinaries`。
- 新增 `mcpServerConfigBinaries`，把 `contract.MCPServerConfig` 转成 provider manifest 可消费的 `dto.MCPBinary`。

### `internal/module/turn/mcp_server_config_test.go`

- 新增 `TestPrepareTurnMergesConfiguredMCPServersIntoAssemblyInput`。
- 验证配置 server 同时进入：
  - `assembly.lastTurnInput.MCPSnapshot.Servers`
  - `assembly.lastTurnInput.MCPSnapshot.ServerConfigs`
  - `req.MCP.Binaries`

### `internal/module/prompt/e2e_test.go`

- 新增 datasource FX harness store。
- 新增 `TestDatasourceInjectsIntoTurnPrompt`。

## 生产装配确认

`internal/app/modules.go` 中已经同时加载：

```go
datasource.Module,
mcpserver.Module,
prompt.Module,
thread.Module,
turn.Module,
```

因此生产 FX 图中：

- datasource prompt provider 会注册到 prompt 动态 section registry。
- mcp_server config provider 会作为 optional 依赖注入 turn service。

## 已执行验证

单文件 code size guard：

```powershell
& 'C:\Program Files\Go\bin\go.exe' run .\scripts\code_size_guard.go -- `
  .\internal\module\turn\service.go `
  .\internal\module\turn\factory.go `
  .\internal\module\turn\manifest.go `
  .\internal\module\turn\module.go `
  .\internal\module\turn\service_helpers.go `
  .\internal\module\turn\service_test.go `
  .\internal\module\turn\mcp_server_config_test.go `
  .\internal\module\prompt\e2e_test.go
```

结果：通过，静默退出。

受影响包测试：

```powershell
& 'C:\Program Files\Go\bin\go.exe' test ./internal/module/turn ./internal/module/prompt -count=1
```

结果：

```text
ok  	github.com/anthropic-ai/super-agent-v3/internal/module/turn	0.712s
ok  	github.com/anthropic-ai/super-agent-v3/internal/module/prompt	22.112s
```

diff 检查：

```powershell
git diff --check -- .\internal\module\turn .\internal\module\prompt\e2e_test.go
```

结果：通过，仅出现 Windows `core.autocrlf=true` 的 LF/CRLF 提示。

## 已知验证说明

仓库 wrapper：

```powershell
$env:REAL_GO_BIN='C:\Program Files\Go\bin\go.exe'
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/turn ./internal/module/prompt -count=1
```

当前会先执行全量 guard，并被既有的无关包基线挡住：

```text
包文件数 internal/provider/codexapp: 31 个 > 上限 30
包行数 internal/provider/codexapp: 10010 行 > 上限 10000
```

该失败不来自本次修改文件。
