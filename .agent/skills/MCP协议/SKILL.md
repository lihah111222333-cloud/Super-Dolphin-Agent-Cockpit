---
name: "MCP协议"
description: "当你需要在 Go 后端构建、扩展或调试 MCP Server，添加工具或资源，或配置 stdio/HTTP 传输时使用。"
trigger_words: ["mcp-server-patterns", "MCP", "Model Context Protocol", "mcp server", "stdio", "HTTP transport", "tool", "resource", "prompt", "协议"]
---

# MCP 协议与服务模式 (MCP Server Patterns)

## Overview
Model Context Protocol (MCP) 允许 AI 助手调用工具、读取资源和使用来自服务器的提示词。本技能专门用于指导在 `super-agent-v3` 中使用 Go 语言开发和维护 MCP Server。

## When to Use
- **症状与用例**:
  - 需要实现新的 MCP Server。
  - 为现有 Server 注册新的 Tools（工具）或 Resources（资源）。
  - 处理 stdio 与 Streamable HTTP 的传输层协议问题。
  - MCP 连接建立失败或传输中断时的排障。
- **何时不要使用 (When NOT to use)**:
  - 若需求可以通过纯 CLI 或简单脚本完成，不需要双向上下文交互时，无需强行包装为 MCP Server。

## Core Pattern: 传输解耦 (Transport Decoupling)
保持 Server 的业务逻辑独立于传输层，方便灵活切换。

**Before (强耦合 HTTP)**:
```go
func HandleMCP(w http.ResponseWriter, r *http.Request) {
    // 业务逻辑与 HTTP 强绑定
}
```

**After (业务与传输解耦)**:
```go
s := server.NewMCPServer("go-agent-orchestration", "1.0.0")
s.AddTool(...) // 纯业务逻辑
// 仅在入口点绑定 HTTP
handler := server.NewHTTPServer(s)
```

## Quick Reference
| 概念 | 用途与本项目约定 |
| :--- | :--- |
| **Tools (工具)** | 模型可调用的动作。**必须**具有强类型的 JSON Schema 参数校验。 |
| **Resources (资源)** | 提供大纲、诊断结果等只读数据。通过 `uri` 参数识别。 |
| **stdio 传输** | 主要用于本地客户端环境连接。 |
| **HTTP 传输** | **本项目（如 `orchestration_http.go`）的核心配置**，用于远程服务调用。 |
| **Idempotency** | 工具尽可能设计为幂等，防止多次重试造成破坏。 |

## Implementation
基于 Go 语言的 MCP Server 初始化示例：

```go
import (
    "context"
    "github.com/mark3labs/mcp-go/server"
    "github.com/mark3labs/mcp-go/mcp"
)

s := server.NewMCPServer("go-agent-orchestration", "1.0.0")

tool := mcp.NewTool("example_tool",
    server.WithDescription("Tool description"),
    server.WithString("param1", server.Required(), server.Description("param description")),
)

s.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    param1 := request.Arguments["param1"].(string)
    result := doSomething(param1)
    // 遵循无状态错误收口契约
    return mcp.NewToolResultText(result), nil
})
```

## Common Mistakes
- **错误**: 把 Raw Stack Traces 直接通过 Error 抛给大模型。
  **修复**: 将错误信息结构化，返回给模型的应该是有助于诊断的语义化信息，严格遵循本项目的错误处理收口（p1-F）契约。
- **错误**: 注册修改系统文件或执行系统命令的高危 Tool，但未提供审计确认。
  **修复**: 必须在 Tool 执行前集成拦截器，例如检查修改行数 (`replace_range < 15`) 或对接“交易守卫系统”进行审批。
- **错误**: Schema 定义过于宽松，允许 `map[string]any` 接收任意参数。
  **修复**: 必须使用 `server.WithString` 等明确参数类型，强制 Schema First。
