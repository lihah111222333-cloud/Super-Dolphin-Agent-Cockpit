---
name: 测试规范
description: 当在 super-agent-v3 中编写、审查或修复测试，选择验证命令，或处理 guard/test 失败时使用。
aliases: ["@测试规范", "@testing"]
---

# super-agent-v3 测试规范

## 基本原则

- fix 类改动先锁 bug，再修复；测试、fixture、golden、snapshot 或可执行验收脚本必须能防回归。
- Go 文件改完先跑单文件守卫：`./scripts/test_with_guard.sh <file.go>`。
- 不用旧项目命令：没有独立后端子模块，不用其他仓库的子目录测试命令、旧守卫入口或旧提交门禁。
- 禁止静默兜底测试：缺字段、错误配置、异常状态应断言 fail-fast。

## 验证矩阵

| 改动面 | 最低验证 |
|---|---|
| Go 单文件 | `./scripts/test_with_guard.sh <file.go>` |
| Go 包 | `./scripts/test_with_guard.sh <affected packages> -count=1` |
| guard/archtest | `./scripts/test_with_guard.sh ./internal/archtest -count=1` 或 `make guard` |
| frontend-app | `cd frontend-app && npm run lint && npm test && npm run build` |
| legacy Vue | `cd cmd/agent-terminal/frontend && node scripts/size-guard.cjs && npx vitest run && npm run build` |
| SQL/store | `make sqlc-verify` |
| codemap | `make codemap-check` |
| docs/skills-only | `python3 scripts/validate_super_agent_skills.py` + `git diff --check`；涉及 provider mirror 时还要检查 canonical 与 mirror 一致 |

## 测试质量

1. 优先真实边界：MCP/CLI/HTTP/DB/provider 能用真实进程或 contract harness 时不要只测 mock。
2. 表驱动测试要覆盖成功、失败、空值、权限、状态迁移和幂等。
3. 新增 helper 只服务测试可读性，不要为测试污染生产 API。
4. baseline 或 guard diff 必须逐项解释；不要用 freeze 或调低阈值过关。

## 报告

最终报告写清楚：

- RED 证据或为什么不可自然 RED。
- GREEN 命令和退出状态。
- 未跑命令及原因。
- unrelated dirty 文件是否保持未触碰。
