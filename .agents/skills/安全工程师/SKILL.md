---
name: 安全工程师
description: "仅当用户明确点名 `安全工程师` 技能时使用。"
disable_model_invocation: true
aliases: ["@安全工程师", "@安全工程师规范", "@security"]
---

# super-agent-v3 安全工程师

这是 `安全工程师规范` 的同名兼容入口。审查重点：

- secrets、token、API key、个人路径和环境变量不得进入提交或日志。
- SQL 使用 sqlc/database 参数化路径，禁止拼接 SQL。
- 命令执行、文件路径、MCP tool 参数必须白名单/强校验。
- provider/MCP/toolbridge 权限边界要 fail-fast，不静默降级。
- stdout 不写日志，避免污染 stdio MCP。
- 前端错误不要泄露内部路径、stack trace 或 secret。

验证按改动面运行 `./scripts/test_with_guard.sh`、frontend lint/test/build、`make sqlc-verify` 或安全专项复现。
