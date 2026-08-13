# V3 架构决策：双端 Provider 收敛 + 统一 MCP 工具路径

> **日期:** 2026-03-19
> **决策:** Codex 和 Claude 收敛为统一 Provider，所有工具走 MCP 路径
> **影响:** 砍掉 ~3,000-4,000 行 provider-specific 代码

---

## 1. 决策背景

### V2 现状：双端分裂

```
Claude                              Codex
──────                              ─────
MCP config → CLI flag               DynamicTools → ThreadStart RPC
CLI spawn MCP server                App-server 管 MCP 生命周期
tool_use block (stream)             tool/list + RPC (request/response)
NDJSON tool_result                  JSON-RPC response/notification
7 env vars                          call context
2,637 行                            3,462 行
```

两端共享的只有 `agentcore.Client` 接口和 `server_dynamic_tools.go` 分发层。
其余全部重复：工具注入、事件解析、结果回传、错误处理。

### V3 决策：统一走 MCP

**Codex 不再系统内注入工具**，改为和 Claude 一样通过 MCP Server 提供工具。

---

## 2. V3 统一 Provider 架构

```
                    ┌─────────────────┐
                    │   API Server    │
                    │  (tool registry)│
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │  MCP Server     │  ← 统一工具层
                    │  (go-agent-mcp) │     所有 provider 共用
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
    ┌─────────▼───┐  ┌──────▼──────┐  ┌────▼────────┐
    │  Claude CLI  │  │ Codex App   │  │ Future      │
    │  (MCP client)│  │ (MCP client)│  │ Provider    │
    └─────────────┘  └─────────────┘  └─────────────┘
```

### 关键变化

| 组件 | V2 | V3 |
|---|---|---|
| legacy Codex provider slice | 3,462 行，独立工具注入链 | **删除独立注入链**，复用统一 MCP 路径 |
| legacy Claude provider slice | 2,637 行，MCP config 生成 | 提取 MCP config 生成到**共享层** |
| `internal/provider/unified/` | 不存在 | **新建**：统一 Provider 实现 |
| `internal/apiserver/codexadapter/` | 3,575 行，Codex 专用 | **合并**到统一 adapter |
| `internal/apiserver/commonadapter/` | 155 行 | **扩展**为统一 adapter 基座 |
| `ThreadStart.DynamicTools` | Codex 专用字段 | **移除** |
| `AllDynamicToolSchemas())` | 注入到 RuntimeAdapter | **移除**，MCP Server 自行获取 |

---

## 3. 统一 Provider 设计

### 3.1 新包结构

```
internal/
├── contract/              ← Provider/session 合同
│   └── provider.go        ← Driver / Session / TurnHandle 接口
└── provider/
    ├── unified/           ← 统一 registry/session 编排
    │   ├── client.go      ← 统一 client 入口
    │   ├── session.go     ← 会话装配
    │   ├── registry.go    ← driver registry
    │   └── event_map.go   ← 统一事件翻译
    ├── claudecli/         ← Claude-specific 传输与 session
    │   ├── driver.go      ← CLI spawn/connect
    │   └── transport.go   ← stream transport
    └── codexapp/          ← Codex-specific 传输与 session
        ├── driver.go      ← AppServer connect
        └── transport.go   ← JSON-RPC transport
```

### 3.2 工具注入统一流程

```
1. API Server 注册所有 dynamic tools
2. MCP Server 启动，暴露 tools（统一入口）
3. Provider 连接 MCP Server（而不是接收 tool 数组）
4. Provider 通过 MCP protocol 调用工具
5. 工具结果通过 MCP protocol 返回
```

### 3.3 Codex 改造要点

**删除的代码路径：**

1. `adapter.go` — 删除 `Deps.AllSchemas` 字段
2. `adapter_submit.go` — 删除 `AllDynamicToolSchemas` 接线
3. `turn_runtime_logic.go` — 删除 `dynamicTools` 参数传递
4. `turn_runtime_adapters.go` — 删除 `RuntimeAdapter.AllDynamicToolSchemas`
5. `client_appserver_protocol.go` — `ThreadStart` 移除 `DynamicTools` 字段
6. `client_appserver_protocol.go` — `filterDynamicTools()` 删除

**新增的代码路径：**

1. Codex app-server 配置 MCP Server 连接（类似 Claude 的 `--mcp-config`）
2. Codex 通过 MCP protocol 发现和调用工具
3. 统一的 MCP bridge 处理工具结果

### 3.4 Claude 改造要点

**提取到共享层：**

1. `buildDynamicToolsMCPConfig()` → `provider/mcp_bridge.go`
2. `resolveDynamicMCPBridgeCommand()` → `provider/mcp_bridge.go`
3. `marshalDynamicToolsPayload()` → `provider/mcp_bridge.go`
4. `buildDynamicMCPEnv()` → `provider/mcp_bridge.go`

**保留在 claude/：**

1. CLI spawn/connect（Claude-specific 传输）
2. stream-json 事件解析（Claude-specific 格式）
3. NDJSON 写入（Claude-specific 结果回传）

---

## 4. 接口简化

### 4.1 V2 的 agentcore.Client 接口

```go
type Client interface {
    SpawnAndConnect(ctx, prompt, cwd, model, instructions, dynamicTools) error
    SubmitTurn(ctx, prompt) error
    ResumeThread(ctx, threadID, prompt, cwd, model, instructions, dynamicTools) error
    SendDynamicToolResult(callID, output, requestID) error
    Compact(ctx) error
    Interrupt() error
    Close() error
}
```

### 4.2 V3 简化后

```go
type Client interface {
    // Launch 统一启动（不再传 dynamicTools）
    Launch(ctx context.Context, cfg LaunchConfig) error

    // Submit 提交新一轮
    Submit(ctx context.Context, prompt string) error

    // Resume 恢复已有线程（不再传 dynamicTools）
    Resume(ctx context.Context, cfg ResumeConfig) error

    // Compact 上下文压缩
    Compact(ctx context.Context) error

    // Interrupt 中断当前操作
    Interrupt() error

    // Close 关闭连接
    Close() error
}

type LaunchConfig struct {
    Prompt       string
    CWD          string
    Model        string
    Instructions string
    MCPConfig    string   // MCP 配置文件路径（统一）
}

type ResumeConfig struct {
    ThreadID     string
    Prompt       string
    CWD          string
    Model        string
    Instructions string
    MCPConfig    string   // MCP 配置文件路径（统一）
}
```

**关键变化：**
- `dynamicTools []DynamicTool` 从所有方法签名中移除
- 替换为 `MCPConfig string`（MCP 配置文件路径）
- `SendDynamicToolResult` 移除（MCP Server 自行处理结果）
- 接口从 7 个方法减少到 6 个

---

## 5. 代码量预估

| 包 | V2 行数 | V3 预估 | 减少 |
|---|---|---|---|
| `internal/provider/claudecli/` | 2,637 | ~800 | -1,837 |
| `internal/provider/codexapp/` | 3,462 | ~600 | -2,862 |
| `internal/provider/unified/` (新) | 0 | ~1,200 | +1,200 |
| `internal/contract/` | 285 | ~200 | -85 |
| `internal/apiserver/codexadapter/` | 3,575 | ~1,500 | -2,075 |
| `internal/apiserver/commonadapter/` | 155 | 0 (合并) | -155 |
| legacy runtime/service slice | 1,372 | ~900 | -472 |
| **合计** | **11,486** | **~5,200** | **~-6,286** |

**净减少 ~6,286 行**（比原估计的 3-4K 更多，因为 adapter 层也大幅简化）

---

## 6. 迁移顺序影响

这个决策改变了 P2 的迁移策略：

### 原 P2：分别迁移 runner + provider stack
### 新 P2：

```
P2a: 收敛 internal/contract/（统一 Provider 合同）
     └── provider.go         ← Driver / Session / TurnHandle

P2b: 收敛 internal/provider/unified/（统一 Provider）
     ├── client.go           ← 核心逻辑
     ├── session.go          ← session 装配
     ├── registry.go         ← driver 注册
     └── event_map.go        ← 统一事件抽象

P2c: 收敛 internal/provider/claudecli/（只留 Claude 传输）
     ├── driver.go           ← 精简 SpawnAndConnect
     └── transport.go        ← 精简 stream-json 解析

P2d: 收敛 internal/provider/codexapp/（只留 Codex 传输 + MCP 接入）
     ├── driver.go           ← 精简连接，增加 MCP config 支持
     └── transport.go        ← 精简事件解析

P2e: 迁移 internal/runner/（使用统一 Provider）
     └── 不再传递 dynamicTools 参数
```

---

## 7. 风险与缓解

| 风险 | 缓解 |
|---|---|
| Codex app-server 不支持 MCP client | Codex app-server 需要适配 MCP client 能力，或通过 sidecar 模式桥接 |
| MCP protocol 性能开销 | 本地 stdio 传输，延迟可忽略 |
| 工具发现延迟 | MCP Server 预注册所有工具，启动时一次性 list |
| 兼容性过渡 | V3 不需要兼容 V2，直接新架构 |

---

## 8. 与 Codex App-Server 的协调

**如果 Codex app-server 已支持 MCP client：**
- 直接配置 MCP server endpoint
- ThreadStart 不再传 DynamicTools

**如果 Codex app-server 尚未支持 MCP client：**
- 方案 A：在 go-agent 侧启动 MCP sidecar，通过 HTTP 代理工具调用
- 方案 B：向 Codex 团队提 MCP client 支持需求
- 方案 C：在 go-agent 侧做 MCP-to-RPC 转换层（临时方案）

**推荐方案 A**（sidecar），因为：
- go-agent 完全掌控
- 不依赖外部团队排期
- 代码复用 Claude 的 MCP bridge 逻辑
