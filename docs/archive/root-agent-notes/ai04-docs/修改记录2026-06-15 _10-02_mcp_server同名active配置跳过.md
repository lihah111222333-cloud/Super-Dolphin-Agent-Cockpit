# 修改记录 2026-06-15 10-02 mcp_server 同名 active 配置跳过

## 背景

发送消息时遇到错误：

```text
configured mcp server conflicts with active server: deepwiki
```

当时项目配置中已经存在一条 MCP server：

```text
D:\Super-Dolphin  deepwiki  http  https://mcp.deepwiki.com/mcp
```

同时运行态 MCP registry 里也已经有一个 active server 名称为 `deepwiki`。旧逻辑在合并项目持久化配置和运行态快照时，只要发现同名 server，就直接 fail-fast 报错。

## 根因

`configured mcp server` 表示 `internal/module/mcp_server` 持久化的项目级 HTTP MCP 配置。

`active server` 表示当前会话或运行态工具注册表里已经在线的 MCP server。

旧逻辑在以下两条路径里都把同名视为冲突：

- thread/start 路径：`internal/module/thread/mcp_server_config.go`
- turn/prepare 路径：`internal/module/turn/service_helpers.go`

对于 `deepwiki` 这种情况，active server 已经可用，项目配置再次注入同名 HTTP server 没有必要。直接报错会阻断发送，但不会带来更安全的行为。

## 行为调整

现在合并项目 MCP 配置时，如果配置项名称已经存在于 active server 列表中，就跳过该配置项。

调整后的规则：

- active server 名称保留在 `MCPSnapshot.Servers` 中。
- 同名 configured server 不再写入 `MCPSnapshot.ServerConfigs`。
- 其它未 active 的 configured server 继续正常合并。
- 配置格式校验仍然 fail-fast，例如空名称、非法 transport、空 URL、空 header key/value 仍会返回错误。
- turn 侧仍然禁止配置项使用内置 managed server 名称 `lsp` 和 `orch`。

以 `deepwiki` 为例：

```text
active servers:      ["deepwiki"]
configured servers:  ["deepwiki", "my-search"]
```

合并后：

```text
MCPSnapshot.Servers:       ["deepwiki", "my-search"]
MCPSnapshot.ServerConfigs: {"my-search": ...}
```

`deepwiki` 使用运行态已经在线的 server，不再从项目配置重复加载。

## 主要代码变更

### `internal/module/thread/mcp_server_config.go`

`mergeConfiguredMCPServers` 不再调用旧的同名冲突检查，而是调用：

```go
skipPromptConfiguredMCPServersWithActiveNames(...)
```

该 helper 会基于当前 snapshot 的 `Servers` 构建 active 名称集合，并过滤掉同名项目配置。

### `internal/module/turn/service_helpers.go`

`mergeTurnConfiguredMCPServers` 同步采用同样策略：

```go
skipTurnConfiguredMCPServersWithActiveNames(...)
```

这样 thread 启动和已有会话继续发送两条链路的 MCP 配置行为保持一致。

## 回归测试

新增测试覆盖同名 active server 跳过行为：

- `internal/module/thread/mcp_live_context_test.go`
  - `TestMergeConfiguredMCPServersSkipsActiveServerNames`
- `internal/module/turn/mcp_server_config_test.go`
  - `TestMergeTurnConfiguredMCPServersSkipsActiveServerNames`

测试断言：

- 同名 `deepwiki` 不再导致错误。
- `deepwiki` 仍保留在 server 列表中。
- `deepwiki` 的项目 HTTP 配置不会进入 `ServerConfigs`。
- 其它配置项，例如 `my-search`，仍正常合并。

## 已执行验证

先确认回归测试在旧逻辑下失败：

```powershell
go test ./internal/module/thread -run TestMergeConfiguredMCPServersSkipsActiveServerNames -count=1
go test ./internal/module/turn -run TestMergeTurnConfiguredMCPServersSkipsActiveServerNames -count=1
```

旧逻辑失败原因均为：

```text
configured mcp server conflicts with active server: deepwiki
```

修复后重新执行并通过：

```powershell
go test ./internal/module/thread -run TestMergeConfiguredMCPServersSkipsActiveServerNames -count=1
go test ./internal/module/turn -run TestMergeTurnConfiguredMCPServersSkipsActiveServerNames -count=1
```

包级守卫和测试也已通过：

```powershell
$env:REAL_GO_BIN='C:\Program Files\Go\bin\go.exe'
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/thread -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/turn -count=1
```

结果：

```text
代码守卫: 全部通过
ok github.com/anthropic-ai/super-agent-v3/internal/module/thread
ok github.com/anthropic-ai/super-agent-v3/internal/module/turn
```

## 兼容性说明

这次只改变同名 active/configured MCP server 的合并策略，不改变 `mcpServer/add`、`mcpServer/list`、数据库存储结构或 provider manifest 的基本格式。

如果用户确实想让项目配置中的同名 HTTP server 生效，需要先停止或移除同名 active runtime server；否则运行态已经在线的 server 优先。
