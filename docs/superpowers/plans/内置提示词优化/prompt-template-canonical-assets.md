# Prompt Template Canonical Assets Decision

本文件固定 Task 4.5 的决策：本轮内置提示词优化默认走保守落点。它只是设计决策和后续机制要求，不表示 canonical prompt-template asset 机制已经可用。

## 当前决策

Task 5 / Task 6 只允许修改 DB prompt template 的 metadata、`when_to_use`、tags、scope，或做小范围 `REPLACE` 类修补。0104 之后的新 migration 不得写入大段 `prompt_text`，也不得写入大段 `prompt_template_sections.body`。

如果后续测试或产品验收证明必须重写大段 DB system-owned prompt body，必须先完成 Task 4.6 的实现和测试，或单独立项同等机制；不能通过 `0105+` SQL migration 直接内联长正文绕过 `internal/archtest/prompt_builtin_config_test.go`。

## Registry 边界

`builtin registry` 继续只承载 core system prompt。当前 `internal/platform/shared/builtinprompts/assets/manifest.json` 只注册：

- `main/default`
- `main/general-zh`

DAG designer、企业流程卡片、专家卡片不迁入 `builtin registry`。`main/dag_designer_zh` 来自历史 DB seed migration `0084`，企业/技能卡片来自 DB prompt template seed migration `0087`，它们仍属于 DB prompt template 发现面，而不是 registry core。

这个边界保留了项目现状：

- 系统内置提示词与用户提示词已经解耦；registry-backed system prompt 由 repo-owned asset 管理，DB 用户资产和 DB system seed 走 prompt template 存储。
- 普通用户资产 UI 不读取 `builtin registry` 作为用户资产来源，也不应把 `builtin:system` 或 system-managed 行当成普通用户资产展示。
- `RuntimePromptCatalog` 会合并 `builtin registry` 与 DB templates，供 thread prompt assembly、显式 `prompt_key` 启动和相关运行时链路使用。
- DAG/MCP `prompt_list` 是不同发现面：它基于 DB runtime-visible prompt templates，不合并 `builtin registry`，主要受 `enabled` 以及 `scope.global` / `scope.cwd:*` 可见性影响。

## 何时需要 canonical prompt-template asset

以下任一需求出现时，必须先完成 Task 4.6：

- 大段改写 DAG designer 正文，例如重写 `main/dag_designer_zh` / `main/dag_designer_en` 的完整执行方法论。
- 大段中文化或重塑企业流程卡片、技能卡片、专家卡片的 `prompt_text`。
- 给 DB system-owned prompt 增加结构化 section body，并希望它成为 repo-owned canonical truth。
- 要求节点执行 prompt 必须包含完整固定输出字段，而不是仅在 `description`、tags、`when_to_use` 或短 `prompt_text` 摘要中提供发现提示。

未完成 Task 4.6 前，上述任务必须暂停或降级为 metadata / scope / tags / 小范围 `REPLACE` 修补。

## Task 4.6 机制要求

### Source Directory

推荐新增独立 repo-owned source directory：

```text
internal/platform/shared/prompttemplateassets/assets/
```

该目录与 `internal/platform/shared/builtinprompts/assets/` 分离，避免把 DB prompt templates 误归入 `builtin registry`。目录内只放 repo-owned canonical prompt-template assets，不扫描用户目录、个人技能目录、marketplace catalog 或 provider mirror。

### Asset Format

每个 prompt template 必须有稳定、可 review 的结构化 asset。建议格式：

```text
internal/platform/shared/prompttemplateassets/assets/
  manifest.json
  templates/<prompt-key-slug>.json
  bodies/<prompt-key-slug>.md
  sections/<prompt-key-slug>/<ordinal>-<section-key>.md
```

`templates/*.json` 至少包含：

- `prompt_key`
- `title`
- `agent_key`
- `tool_name`
- `description`
- `when_to_use`
- `tags`
- `scope`
- `enabled`
- `body_ref` 或 `sections`
- ownership metadata，例如 `created_by` / `updated_by` 的目标值

正文可以是单个 `body_ref`，也可以是 ordered sections，但同一个 template 的来源必须唯一、可生成、可测试。

### Loader / Generator

Task 4.6 必须实现 loader 或 generator，负责读取上述 assets 并生成可写入 DB 的 seed payload。loader/generator 需要 fail-fast：

- manifest 缺失、重复 `prompt_key`、重复 section key、空正文、非法 tags、非法 scope 都必须报错。
- asset 不能依赖运行时扫描用户目录。
- 生成结果必须稳定排序，保证 golden diff 可读。

### Migration 交互

0104 之后的普通 migration 不得手写大段 `prompt_text` 或 section `body`。允许的交互方式必须二选一并由测试锁住：

- migration 调用或嵌入由 generator 产出的短标识/metadata，再由受控 seed 流程装载 canonical asset。
- migration 使用 generator 产物，但该产物必须被 guard 明确识别为 generated payload，并有 golden 测试证明与 source asset 一致。

无论采用哪种方式，SQL migration 不能成为长正文 canonical truth。

### Ownership Guard

DB row ownership 必须继承现有 prompt-template seed 保护规则：

- system-owned seed row 使用明确的 `created_by` / `updated_by`，例如 `system.seed` 或后续选定的 system asset owner。
- 刷新已有 row 时必须保护 `manually_edited = TRUE` 的用户/管理员修改。
- 对 `prompt_text`、sections、metadata 的更新必须只作用于 system-owned 且未手工编辑的 row。
- 普通 UI/admin 写路径仍不得修改 `builtin registry` core prompt。

### Rollback

每个使用 canonical asset 的 migration 必须同步更新 `migrations/ROLLBACK.md`：

- rollback 需要恢复到明确的 runtime 可见状态，不能含糊地恢复长正文而遗漏 `enabled` / scope 语义。
- 如果 rollback 恢复 legacy 正文，必须说明它是否会重新进入 DAG/MCP `prompt_list`、`RuntimePromptCatalog` 或 `available_experts`。
- rollback SQL 也不能把未受测的大段正文变成新的事实来源；需要通过同一 asset/golden 机制生成或验证。

### Drift / Golden Tests

Task 4.6 至少需要以下测试：

- loader/generator 单测：manifest、metadata、tags、scope、body/sections 全量解析。
- golden 测试：generated DB seed payload 与 source asset 一致，稳定排序。
- migration fixture / rollback 测试：forward 和 rollback 都满足 ownership、visibility、`manually_edited` 保护。
- archtest：`prompt_builtin_config_test.go` 继续拒绝 0104 之后普通 SQL migration 内联大段 builtin/system body；若允许 generated payload，必须有明确 allow rule 和对应 golden。
- runtime 边界测试：证明 canonical prompt-template assets 没有进入 `builtin registry` manifest，且 DAG/MCP `prompt_list` 与 `RuntimePromptCatalog` 的可见性仍按各自规则工作。

### Runtime Boundary

canonical prompt-template asset 机制只解决 DB system-owned prompt template 的 repo-owned source-of-truth 问题。它不改变以下运行时边界：

- `builtin registry` core 仍只允许 `main/default` 和 `main/general-zh`。
- DAG designer / 企业卡片 / 专家卡片仍通过 DB prompt templates 暴露给 DAG/MCP `prompt_list` 或其他 DB runtime-visible 发现面。
- `RuntimePromptCatalog` 仍是 thread runtime 的合并 catalog，负责把 registry-backed templates 和 DB templates 合并给 thread prompt assembly 使用。
- 普通用户资产 UI 仍不能把 registry core 或 system-managed prompt 当成用户资产列表来源。

## 执行门禁

Task 5 / Task 6 执行前必须按本文件判断路径：

- 如果只做 metadata、`when_to_use`、tags、scope 或小范围 `REPLACE` 修补，可以继续。
- 如果验收需要长正文、完整 section body、完整固定输出 schema 注入执行 prompt，必须先完成 Task 4.6 实现与测试。

写了本文件不等于机制已完成；任何任务如果依赖“canonical prompt-template asset 机制已可用”，必须先找到 Task 4.6 的代码、测试和验证结果。
