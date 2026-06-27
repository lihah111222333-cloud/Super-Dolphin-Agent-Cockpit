---
name: 编写技能
description: 当在 super-agent-v3 中创建、编辑、迁移或部署 repo-local 技能，或修复 provider mirror / canonical skill 漂移时使用。
aliases: ["@编写技能", "@writing-skills"]
---

# 编写技能

## 仓库事实

- project canonical skills：`.agent/skills/<skill>/SKILL.md`
- active personal roots：`~/.super-dolphin/skills/personal/{user,agent,imported}`
- provider mirrors：`.agents/skills`、`.claude/skills`、`~/.agents/skills`、`~/.claude/skills`
- `.agents/skills` 和 `.claude/skills` 是生成物，不是默认编辑目标。
- `personal/hub` 是 catalog-only，不能当 runtime canonical root 扫描、复制或镜像。

## 工作流

1. 先确认要改的是 canonical 源；不要先手改 provider mirror。
2. 为技能适配写 RED 检查：缺关键路径、含旧项目词、错误命令、错误触发路由时必须失败。
3. 修改 `.agent/skills` 中的最小技能文本。
4. 如当前会话依赖 `.agents/skills`，可同步本地 mirror，但最终报告要说明它是生成物。
5. 更新 `.agent/skills/.super-dolphin-skill-policy.json` 中已存在 project 选项的 `selected_content_hash` / source hash，hash 为对应 `SKILL.md` 的 sha256。
6. 运行文本校验和 `git diff --check`。

## 技能写法

- frontmatter 至少包含 `name` 和 `description`。
- `description` 只写触发条件，不总结完整流程。
- 常用入口保持短；复杂参考放子文件。
- 项目专属约定写进 repo-local skill，不要污染个人通用技能。
- 涉及子代理时不要强制绑定 `mcp-orch`；只有任务确实需要持久 DAG、重试、租约或跨代理交接记录时，才把 `task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node` 写成可选编排路径。

## super-agent-v3 禁止项

| 禁止 | 改为 |
|---|---|
| 默认使用其他仓库、旧业务领域、旧数据库栈或旧子模块语境 | 使用 README、codemap、docs/契约 和源码事实 |
| 把 `.agents/skills` 当 canonical truth | 改 `.agent/skills`，必要时同步 mirror |
| 写其他仓库的子目录测试命令、旧守卫入口或旧提交门禁 | 使用 `./scripts/test_with_guard.sh`、`make guard`、`make test` |
| 子代理只能走 mcp-orch 或工具缺失就停止 | 允许平台原生子代理/多代理；mcp-orch 只是可选观测与编排路径 |
| provider mirror 漂移不报告 | 明确 owned canonical 与 ignored mirror 的状态 |
