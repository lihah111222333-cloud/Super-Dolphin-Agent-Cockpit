# 提示词重构第二阶段摘要

本文档只保留本轮提示词改造的最终设计和验收入口。历史执行计划、评审记录和中间方案不随主线提交，避免主分支长期携带大段过期规划文本。

## 最终目标

提示词系统从“默认提示词里写死可用能力”改为运行时动态发现：

- 专家能力通过 `available_experts` 暴露给主 LLM，主 LLM 按需调用 `launch_agent(prompt_key=...)`。
- 参考资料通过 `recall_catalog` 暴露目录，主 LLM 按需调用 `prompt_recall(topic=...)` 读取正文。
- 默认规则通过 project-scoped default-rule section 注入，不作为用户可见的系统内置提示词暴露。
- 普通用户创建提示词时只描述意图，由创建期 AI 整理为确认卡，再保存为现有 prompt template / section 结构。
- 系统内置提示词改为 repo 内配置文件版本控制；用户导入、拖拽、AI 整理生成的资产只保存到 DB。

## 已落地范围

- prompt template 增加 `when_to_use`、section recall/default-rule 元数据、intent draft 表和 project/global scope 表达。
- 后端新增 prompt intent authoring / dry-run / commit / discard RPC。
- 后端新增 runtime prompt catalog，合并 builtin registry 和 DB 用户资产，并按 cwd/global/enabled 过滤。
- 内置提示词配置移入 `internal/platform/shared/builtinprompts/assets/**`，通过 `go:embed` 加载。
- 普通提示词 UI 改为 `prompt-assets/list`，只展示用户资产和待确认草稿，不展示 builtin system prompts。
- 文件拖拽进入意图式创建链路，Wails 层负责读取文件文本，创建期 AI 负责整理。
- 旧 prompt classifier 运行路径已移除，并由 archtest 防止回流。
- 保存草稿后，同批次其他待确认草稿会自动 rejected。

## 关键边界

- builtin prompt 是代码资产，不是用户资产；普通 UI 不展示、不编辑、不删除 builtin。
- 用户资产是 DB 资产；外部 Claude / Trae / 其他 system prompt 只能生成用户草稿或用户资产，不能写入 builtin registry。
- 外部 provider/system/persona prompt 原文不能直接成为默认规则；必须经过去身份、去外部工具协议、去权限污染后才能保存。
- 默认 scope 是当前项目；只有显式打开全局选项并确认后才保存为 global。
- disabled 用户资产不进入 runtime catalog，LLM 发现不到。
- `priority`、`match_when`、`section_key`、`region`、`ordinal`、`trigger_type`、`recall_topic`、`enable_when` 属于底层结构，不在普通编辑面板展示。

## 主要代码入口

- 意图式创建：`internal/module/prompt/intent/`
- 提示词 RPC surface：`internal/module/prompt/service_surface.go`
- 用户资产列表与 section 预览：`internal/module/prompt/template_sections.go`
- runtime catalog：`internal/module/threadprompt/runtime_catalog.go`
- expert / recall / default-rule providers：`internal/module/threadprompt/providers.go`
- 内置提示词配置：`internal/platform/shared/builtinprompts/`
- 前端提示词页：`cmd/agent-terminal/frontend/vue-app/pages/SystemPromptPage.js`
- 前端创建向导：`cmd/agent-terminal/frontend/vue-app/pages/PromptIntentWizard.js`
- 文件拖拽读取：`internal/ui/wails/dropped_text.go`

## 迁移入口

- `migrations/0092_seed_main_claude_style_zh.sql`
- `migrations/0093_remove_tags_has_from_template_match_when.sql`
- `migrations/0094_add_when_to_use_column.sql`
- `migrations/0095_rename_claude_style_templates.sql`
- `migrations/0096_add_section_recall_columns.sql`
- `migrations/0098_drop_classifier_preference.sql`
- `migrations/0100_seed_recall_packs_and_when_to_use.sql`
- `migrations/0101_prompt_intent_drafts.sql`
- `migrations/0102_remove_demo_prompt_templates.sql`
- `migrations/0103_prompt_intent_draft_scope.sql`
- `migrations/0104_disable_registry_backed_system_seed_prompts.sql`

## 验收口径

- `make guard`
- `make sqlc-verify`
- `./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/tools ./internal/module/uistate ./internal/module/thread ./internal/module/prompt ./internal/module/threadprompt ./internal/module/turn ./internal/module/dashboard ./internal/sidecar/orch/store/prompt ./internal/store/prompt ./internal/archtest -count=1`
- `make build-plain`
- `cd cmd/agent-terminal/frontend && node scripts/size-guard.cjs && npx vitest run && npm run build`

## 仍需产品 dogfood 的点

真实模型是否在非强制路径下稳定自发选择 `launch_agent` / `prompt_recall`，需要继续用真实 Claude/Codex 会话观察。当前代码验收已经覆盖启动链、snapshot、runtime catalog、动态段注入、RPC handler、前端创建/保存链路和 fixture-backed E2E。
