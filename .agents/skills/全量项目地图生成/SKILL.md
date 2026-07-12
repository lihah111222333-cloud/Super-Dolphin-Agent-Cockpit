---
name: 全量项目地图生成
description: "仅当用户明确点名 `全量项目地图生成` 技能时使用。"
disable_model_invocation: true
aliases: ["@全量项目地图生成", "@codemap"]
---

# super-agent-v3 项目地图生成

## 当前入口

- codemap 目录：`docs/doc/codemap`
- 目录索引：`docs/doc/codemap/README.md`
- 项目地图：`docs/doc/codemap/project-map/AI_PROJECT_MAP.md`
- 能力清单：`docs/doc/codemap/capability-contract/capability_manifest.json`

不要使用旧后端子模块项目地图规则。

## 规则

1. 先读 `README.md` 和 `docs/doc/codemap/README.md`。
2. 只打开相关 codemap 卷，避免扫生成目录。
3. 不递归索引 `.worktrees`、`.agents`、`.claude`、frontend `node_modules/dist` 等生成/缓存目录。
4. 修改 codemap 后必须跑 `make codemap-check`。

## 常见错误

| 错误 | 修正 |
|---|---|
| 生成旧业务子域维度 | 使用当前 cmd/internal/frontend-app 结构 |
| 直接编辑过期生成物 | 查生成器或目标源 |
| 不跑 codemap 校验 | 运行 `make codemap-check` |
