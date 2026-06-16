# Round 014 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 06:11:43 KST
- 结束：2026-05-17 06:18:05 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 command card、prompt template、prompt routing test、dashboard commands/prompts 页面和前端命令卡执行，重点看风险等级、优先级、更新时间和分页窗口是否会误导高风险项排序。

- `sql/queries/command_card.sql`
- `internal/store/commandcard/store.go`
- `internal/store/commandcard/contract.go`
- `internal/store/commandcard/store_test.go`
- `sql/queries/prompt_template.sql`
- `internal/store/prompt/store.go`
- `internal/store/prompt/contract.go`
- `internal/store/prompt/store_test.go`
- `sql/queries/prompt_routing_test.sql`
- `internal/store/routingtest/store.go`
- `internal/module/dashboard/ui_page.go`
- `internal/module/dashboard/rpc.go`
- `internal/module/thread/router_resolve.go`
- `cmd/agent-terminal/frontend/vue-app/app.js`
- `cmd/agent-terminal/frontend/vue-app/pages/CommandsPage.js`

## Findings

1. **[major] command card 列表按更新时间排序，不按 risk_level 或 enabled/risk 优先级排序**
   - 证据：`ListCommandCards` 查询返回 `risk_level` 和运行统计，但排序只用 `c.updated_at DESC, c.id DESC`（`sql/queries/command_card.sql:39-54`）。store 仅透传 `RiskLevel`，没有提供 risk 排序/过滤参数（`internal/store/commandcard/contract.go:15-35`、`internal/store/commandcard/store.go:26-57`）。dashboard commands 页面只请求固定 limit 的 command cards（`internal/module/dashboard/ui_page.go:198-201`）。
   - 风险：高风险命令卡如果不是最近更新，会被低风险但新更新的卡片挤出前 100；用户在命令页看到的“风险级别”并不代表列表优先级。对风险审查或运维操作台而言，高风险入口可能被隐藏，低风险入口反而占据首屏。
   - 建议：列表支持 risk priority 排序或 risk filter；默认视图可先按 enabled、risk severity、updated_at 排序，并把被 limit 截断的高风险数量返回给前端。

2. **[major] dashboard prompt CWD 过滤在 SQL limit 后又二次过滤，可能把当前仓库匹配 prompt 截掉**
   - 证据：SQL 先用 CWD 条件包含“无 scope.cwd tag 的全局 prompt”或当前 CWD scoped prompt，再按 `updated_at DESC LIMIT` 返回（`sql/queries/prompt_template.sql:39-55`）。dashboard 之后再次执行 `filterDashboardPromptsByCWD()`，并且只对已返回的窗口过滤（`internal/module/dashboard/ui_page.go:204-225`）。测试也锁定 dashboard 会在 reader 返回结果上二次过滤（`internal/module/dashboard/service_test.go:271-344`）。
   - 风险：如果全局 prompt 数量很多且更新时间较新，SQL limit 的 100 个窗口可能已被全局 prompt 填满，当前仓库 scoped prompt 即使存在也不会进入 dashboard 二次过滤输入。用户以为当前仓库没有专用 prompt，启动时也可能选择默认 prompt。
   - 建议：SQL 层按 scope relevance 排序，把当前 CWD scoped prompt 排在全局 prompt 前；或 dashboard 读取更大窗口后按 CWD relevance 截断。

3. **[moderate] prompt router 只取最近 200 个模板再做 match_when priority 排序，priority 高但更新时间旧的模板可能永远不参与路由**
   - 证据：router 读取 prompt templates 时固定 `Limit: 200`（`internal/module/thread/router_resolve.go:30-37`）。底层 `ListPromptTemplates` 按 `updated_at DESC` 截断（`sql/queries/prompt_template.sql:39-55`）。match_when 自动路由虽然会在候选池内按 `Priority DESC` 稳定排序（`internal/module/thread/router_resolve.go:437-573`），但只能排序已经被更新时间窗口选中的候选。
   - 风险：运营以为提高 `priority` 就能保证路由优先级，但当模板总量超过 200 且高 priority 模板较旧时，它不会进入候选集。路由结果由更新时间窗口先过滤，再由 priority 排序，调参语义不可解释。
   - 建议：router 查询应优先按 enabled + match_when + priority 取候选，而不是复用 dashboard 的 updated_at 列表；至少对 routing path 使用更大的 limit 并记录是否截断。

4. **[moderate] 命令卡执行只把 command_template 发送给会话，没有风险确认或参数化上下文**
   - 证据：前端命令页展示 `risk_level` 字段（`cmd/agent-terminal/frontend/vue-app/app.js:69-73`），但点击执行时 `runCommandCardForApp()` 只取 `command_template` 并发送“请执行以下命令并反馈结果”到当前会话（`cmd/agent-terminal/frontend/vue-app/app.js:211-218`）。`CommandsPage` 的按钮文案是“发送到当前会话”，没有按 risk_level 分支（`cmd/agent-terminal/frontend/vue-app/pages/CommandsPage.js:39-45`）。
   - 风险：高风险命令卡和低风险命令卡进入同一条自然语言消息路径，风险等级没有变成审批、确认或工具 allowlist 约束。模型可能直接执行高风险命令，而用户只看到一个普通发送按钮。
   - 建议：前端对 high/critical risk 增加确认和参数预览；后端最好提供 typed command-card run RPC，记录 card_key、risk_level 和参数，并接入审批策略。

## 误报与已覆盖项

- prompt routing 的 match_when pool 已避免高 priority `{}` fallback 覆盖低 priority specific rule，这是代码明确实现的（`internal/module/thread/router_resolve.go:437-573`），本轮不报告 match_when pool 顺序问题。
- routing test 表按 `id` 顺序执行且无 limit（`sql/queries/prompt_routing_test.sql:1-5`），本轮不报告 routing tests 被分页截断。

## 验证

```bash
./scripts/test_with_guard.sh ./internal/store/commandcard ./internal/store/prompt ./internal/store/routingtest ./internal/module/dashboard ./internal/module/thread -count=1
cd cmd/agent-terminal/frontend
npx vitest run app-root.behavior.test.js system-prompt-page.behavior.test.js
```

结果：Go guard 与相关 Go 包通过；前端 size guard 通过，2 个 vitest 文件共 50 个测试通过。

## 下一轮建议

- Round 015 审查 auto-continue / watchdog / stuck detection 的阈值、累计计数和 reset 条件，重点看状态量化是否会误触发或漏触发自动续接。
