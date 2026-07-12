---
name: 全量项目地图生成
description: "仅当用户明确点名 `全量项目地图生成` 技能时使用。"
disable_model_invocation: true
aliases: ["@全量项目地图生成", "@codemap"]
---

# super-agent-v3 项目地图生成

## 唯一事实源与产物

- 生成器：`scripts/generate_ai_project_map.mjs`。
- 规则输入：`.ai-project-map.overrides.json`。
- 生成物：`AI_PROJECT_MAP.md`、`AI_PROJECT_DRIFT.md`、`AI_PROJECT_MANIFEST.json` 和 `docs/guide/index/*.tsv`。

分卷 codemap 由 `make codemap-*` 管理；能力契约由 `make capcontract-*` 管理。不得用 `make codemap-check` 代替项目地图校验。

## 规则

1. 先读 README、codemap README 和生成器中的排除规则，不维护第二份排除清单。
2. 修改生成器或 overrides 后运行 `make project-map-refresh`，不手改生成物。
3. 完成前运行 `make project-map-check`；若同时改分卷 codemap 或能力契约，再追加对应 check。

## 常见错误

| 错误 | 修正 |
|---|---|
| 生成旧业务子域维度 | 使用当前 cmd/internal/frontend-app 结构 |
| 直接编辑过期生成物 | 修改 owner 后运行 `make project-map-refresh` |
| 只跑 codemap-check | 项目地图必须运行 `make project-map-check` |
