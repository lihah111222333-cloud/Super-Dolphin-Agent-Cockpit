---
name: 日志与错误处理
description: 当在 super-agent-v3 中设计、实现或审查错误传播、JSON-RPC/MCP 错误、日志字段、诊断输出或 fail-fast 行为时使用。
aliases: ["@日志与错误处理", "@error-handling", "@logging"]
---

# super-agent-v3 日志与错误处理

## 原则

- 错误要带上下文并保留根因；不要吞错继续。
- MCP/JSON-RPC 边界返回结构化错误，避免泄露 stack trace 或 secret。
- stdout 属于 stdio MCP 协议帧；日志必须写 stderr 或日志系统。
- 配置缺失、未知 transport、字段缺失、状态非法必须 fail-fast。
- 不用其他业务系统的 logger 示例，不引入旧项目包路径。

## 常见边界

| 边界 | 要求 |
|---|---|
| jrpc2 | 使用标准 code + 可诊断 message |
| MCP tool | schema 校验失败直接返回错误；payload envelope 保持一致 |
| provider | 外部 CLI/API 失败要暴露可执行诊断，不静默 fallback |
| store/sqlc | 事务错误向上传递，空结果和不存在要区分 |
| frontend | bridge/API 错误给用户可见状态，不吞掉 Promise rejection |

## 验证

Go 改动先跑单文件守卫，再跑受影响包：

```bash
./scripts/test_with_guard.sh <file.go>
./scripts/test_with_guard.sh <affected packages> -count=1
```
