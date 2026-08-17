# 运行中 nav RPC 红框根因文档审查 findings（源码可达性修正版）

- 日期：2026-06-04
- 工作区：`/Users/ai/Desktop/Super-Dolphin/.worktrees/integration/nav-rpc-failures-during-turn-20260604`
- 当前审查 HEAD：`e23aad58f`
- 被审查文档：`docs/cc/nav-rpc-failures-during-turn-root-cause-20260604.md`
- 本轮修正范围：对上一版 findings 的高票项与边界票项逐项追溯源码，确认风险是否真实可达、是否已有上层防护，并调整结论/级别。
- 业务代码变更：无。本文件仅记录审查 finding。

## 证据来源边界

本汇总只采用以下证据：

1. 被审查文档本身。
2. 当前 worktree 源码。
3. 被审查文档引用的 trace/log：
   - trace：`~/.super-dolphin/log/workflow-sync-root-cause-20260604/traces/trace-2026-06-04.jsonl`
     - `sha256=f63f31971d1b4812a359cf5db4f5fbc366e1aee6b69c831ee538be5fa2959e45`
     - `3903` 行。
   - log：`~/.super-dolphin/log/workflow-sync-root-cause-20260604/agent-terminal-2026-06-04-1.log`
     - `sha256=ade645241069c1d47587cc6fdcc86a10dd397a727da63599bee9e4e894fb7c51`
     - `207920` 行。

明确不作为证据：`docs/cc/workflow-sync-root-cause-20260604.md` 等旧报告/旧专项结论。若后续文档保留旧报告引用，只能标成“背景索引/未复核”，不能继承为本问题 finding。

## 源码追溯后的总体结论

被审查文档的主链路方向仍可保留：`ui/sidebar/changed` 风暴、前端无合流地拉全量 `ui/sidebar/get`、后端 sidebar enrichment 因 batch 查询失败退化为逐线程读取，并与 dev runtime RPC timeout / pre-dispatch wait 叠加，能够解释运行中切 nav 页红框/重试失败。

源码追溯后的主要修正是：

1. `ui/sidebar/get` 后端耗时数字不是完全无源，基本可解释为“有 frontend trace 可配对的 377 条子集”，但文档写在“backend done 1008 次”之后，未标注口径，容易被误读为全量统计；因此从“统计错误”修正为“统计口径未标注/基线不可复算”。
2. SQL batch 风险真实可达；但 `GetSidebar()` 当前对 runtime config 的应用函数 `applyTaskRuntimeToThreadRuntimeConfig()` 是 no-op，因此 missing runtime config 的主要风险是性能/warn 风暴和修复不充分，而不是直接破坏 sidebar 字段展示。
3. `legacy refresh` 与 `frontend single-flight freshness` 属于“修复实施边界风险”：当前生产问题中事件放大真实可达，但对应 finding 应避免写成已经发生的数据丢失；它们是后续修复必须守住的兼容/新鲜度边界。
4. dev shim / native Wails 边界、页面 8s timeout 与 runtime 30s timeout、pre-dispatch wait 与具体队列点，均仍需在文档中显式区分。

## 高票/边界票项追溯总表

| ID | 原票数 | 原级别 | 源码追溯结论 | 上层防护 | 修正后级别 |
|---|---:|---|---|---|---|
| F1 | 4/5 | P1 | 风险真实：统计口径未标注；不是完全无源，疑似 paired subset | 无文档口径/命令防护 | P2 |
| F2 | 2/5 | P2 | 风险真实：当前失败证据是 `web-debug-shim`，native 未证明 | `client_kind` 可区分，但原文未使用它限定范围 | P2 |
| F3 | 3/5 | P2 | 风险真实：trace 只证明 pre-dispatch wait，不能定位具体队列点 | jrpc2 并非仓库内强制单并发；无队列埋点 | P2 |
| F4 | 2/5 | P2 | 风险真实：page-level 8s 与 runtime 30s 是两层 timeout | `Promise.race` 不 abort；`callAPI` 无 AbortSignal | P2 |
| F5 | 1/5 | P1 | 风险真实：修 SQL 后仍可能触发 fail-fast/fallback；但 sidebar 字段功能风险被 no-op 限制 | pending launch 免 binding；`applyTaskRuntime...` no-op 限制功能影响；无性能防护 | P1 |
| F6 | 1/5 | P2 | 风险真实：事件扩展可达；删除 legacy refresh 会破坏兼容测试 | 已有 tests 锁定兼容；没有运行时降噪防护 | P2 |
| F7 | 1/5 | P2 | 当前不是已发生 bug，是未来 single-flight 实施风险 | 现有 seq 只防 stale apply，不保证 reuse-only 后 freshness | P3 |
| F8 | 4/5 | P2 | 风险真实：缺少跨层验收；已有局部测试但缺 burst/fallback/manual trace 关口 | 局部单测存在，不覆盖本事故闭环 | P2 |
| F9 | 5/5 | P2 | 风险真实：旧报告引用是文档证据污染 | 无上层防护 | P2 |

## Accepted findings（修正版）

### F1 / P2 — `ui/sidebar/get` 统计需要标注口径；不能把 paired subset 当全量 backend 基线

**追溯结论：风险真实可达，但上一版 P1 需降级为 P2。**

**源码/trace 证据：**

- 被审查文档 `docs/cc/nav-rpc-failures-during-turn-root-cause-20260604.md:108-115` 写：
  - `ui/sidebar/get` backend done `1008` 次。
  - backend 耗时 `min 2659ms，p50 7453ms，p90 8985ms，max 11935ms`。
- 对同一 trace 全量复算 `method == "ui/sidebar/get" && kind == "backend.rpc.dispatch.done"`：
  - `n=1008, min=2659ms, median=8242.5ms, p90≈10552ms, max=13119ms`。
- trace 中存在超过文档 max 的全量 backend done：
  - `trace-2026-06-04.jsonl:3285`：`duration_ms=12983`。
  - `trace-2026-06-04.jsonl:3287`：`duration_ms=13086`。
  - `trace-2026-06-04.jsonl:3327`：`duration_ms=13119`。
- 复算与 frontend trace 可配对的 `ui/sidebar/get` backend done 子集为 `n=377, min=2659ms, median=7453ms, p90≈9003ms, max=11935ms`，可解释文档中的 `p50=7453/max=11935` 来源。

**上层防护检查：**

无。文档没有写明“全量 1008 条”与“frontend-paired 377 条子集”的差异，也没有附复算命令或 percentile 取法。

**修正后建议：**

1. 文档应把 `:114` 改成“paired frontend subset”统计，或替换成全量 backend done 统计。
2. 同时列出两张表：
   - 全量 backend done：`n=1008, median=8242.5ms, p90≈10552ms, max=13119ms`。
   - frontend-paired subset：`n=377, median=7453ms, p90≈9003ms, max=11935ms`。
3. 附复算脚本/过滤条件，避免后续验收引用错误基线。

---

### F2 / P2 — 当前失败证据覆盖 dev `web-debug-shim`；native/packaged Wails 影响仍未证明

**追溯结论：风险真实可达；保留 P2。**

**源码/trace 证据：**

- trace 关键失败均带 `client_kind="web-debug-shim"`，例如：
  - `trace-2026-06-04.jsonl:1504`：`turn/start` 30s timeout。
  - `trace-2026-06-04.jsonl:1553`：`ui/dashboard/get` 30s timeout。
  - `trace-2026-06-04.jsonl:1563`：`dashboard/sharedFiles` 30s timeout。
- `frontend-app/src/shared/api/wailsBridge.js:212-215` 明确按 `window.__WAILS_SHIM_DEBUG__` 标记 `web-debug-shim` / `desktop-wails`。
- 当前 React/Vite dev shim 明确只服务本地开发：`frontend-app/public/wails/runtime.js:2-9`。
- Vite build 将 `/wails/runtime.js` external 化：`frontend-app/vite.config.js:9-14`。
- 被审查文档 `:178` 引用 `cmd/agent-terminal/frontend/wails/runtime.js`；当前 React/Vite UI 主证据应指向 `frontend-app/public/wails/runtime.js:277-327`。

**上层防护检查：**

有部分观测防护：`client_kind` 已能区分 dev shim 与 desktop Wails。但原文未用该字段收窄结论范围，所以仍会误导生产影响判断。

**修正后建议：**

1. 原文结论拆成：
   - production-independent：事件扩展、前端无合流、SQLC batch 查询失败。
   - dev-shim-specific：`web-debug-shim` WebSocket/pending map、30s short timeout。
2. runtime shim 主证据改为 `frontend-app/public/wails/runtime.js`。
3. 若要覆盖 native/packaged product，必须补 `client_kind=desktop-wails` 或 packaged run trace。

---

### F3 / P2 — “同一通道/队列”仍应降级为 pre-dispatch wait 推断

**追溯结论：风险真实可达；保留 P2。**

**源码/trace 证据：**

- dev runtime 通过 `/wails/ws` 发 JSON-RPC：`frontend-app/public/wails/runtime.js:20, 297-318`。
- 前端 send 只是写入同一 WebSocket，没有 queue depth / in-flight limit：`frontend-app/public/wails/runtime.js:322-343`。
- 后端 Wails route 接入同一个 RPC WS handler：`internal/ui/wails/http_server.go:29-32`。
- 通用 `/ws` 也是 `rpc.WSHandler(server, nil)`：`internal/platform/rpc/module.go:132-140`。
- RPC server options 只设置 `AllowPush` / `RPCLog`，未在仓库层设置 `Concurrency`：`internal/platform/rpc/server.go:547-558`。
- trace 直接证明的是 frontend/runtime duration 远大于 backend dispatch duration：
  - `prompt-assets/list`：backend `44ms`（`trace:1267-1268`），frontend `30011ms` timeout（`trace:1507`）。
  - `ui/dashboard/get`：backend `65ms`（`trace:1275,1292`），frontend `30016ms` timeout（`trace:1553`）。
  - `turn/start`：backend dispatch `271ms`（`trace:1282`），frontend `30040ms` timeout（`trace:1504`）。

**上层防护检查：**

没有能定位或限制该等待点的上层防护：runtime 只有 pending timeout；trace 没有 queue depth；server options 也未记录并发/队列指标。并且 jrpc2 未被仓库配置成单并发，因此“单 WebSocket 串行队列”不能写死。

**修正后建议：**

将原文“堆积在同一个 dev Wails JSON-RPC/WebSocket 通道上”改为：

> trace 证明在 `frontend callAPI/runtime shim -> /wails/ws -> backend dispatch` 之间存在 pre-dispatch wait；单 WebSocket/RPC 路径是源码事实，具体等待点仍需 in-flight/concurrency telemetry 证明。

---

### F4 / P2 — 页面 8s 红框与底层 `callAPI` / runtime 30s trace timeout 是两层问题

**追溯结论：风险真实可达；保留 P2。**

**源码/trace 证据：**

- Prompt 页页面层 timeout 为 8 秒：`frontend-app/src/features/prompts/PromptPageView.jsx:21`。
- Prompt 页 `withTimeout()` 是 `Promise.race`，不会取消底层 RPC：`frontend-app/src/features/prompts/PromptPageView.jsx:446-453`。
- `prompt-assets/list` 和 `ui/preferences/get` 使用该 8s timeout：`frontend-app/src/features/prompts/PromptPageView.jsx:464-493`。
- shared page 也使用 `Promise.race`：`frontend-app/src/pages/shared/pageShared.js:15-23`；缓存错误文案在 `frontend-app/src/pages/shared/pageShared.js:107-112`。
- `callAPI()` 没有 `AbortSignal` 参数，只 await runtime call 并记录 trace：`frontend-app/src/shared/api/wailsBridge.js:602-624`。
- backend API wrapper 只把参数传给 `callBackend/callAPI`，没有 abort 入口：`frontend-app/src/shared/api/backendApi.js:742, 761, 769-772, 834-835`。
- dev runtime short RPC timeout 为 30 秒：`frontend-app/public/wails/runtime.js:277-294, 322-327`。

**上层防护检查：**

无取消防护。页面层 timeout 只让页面 promise reject；底层 RPC 仍继续 pending，直到返回或 runtime timeout。React Query/cache 只能影响页面显示，不会取消已发出的 Wails RPC。

**修正后建议：**

1. 表格列名从“前端耗时”改为“bridge/runtime 层 `frontend.rpc` duration”。
2. 增加说明：用户可能 8s 已看到红框；trace 中 23-30s 是底层 RPC 生命周期，不等同于用户一直等 23-30s。
3. 若后续考虑 AbortSignal，需要贯通 page -> `callAPI` -> runtime shim/Wails -> backend ctx；单纯 `Promise.race` 不是取消。

---

### F5 / P1 — SQL batch 修复不足风险真实可达，但功能影响需收窄为性能/warn 风暴

**追溯结论：风险真实可达；保留 P1，但修正文案。**

**源码/trace/log 证据：**

- SQLC 生成码确实是 `WHERE thread_id IN ($1)`，并将 `[]string` 作为单参数传入：`internal/store/sqlc/agent_thread.sql.go:268-286`。
- 日志确认 batch encode 失败：`agent-terminal-2026-06-04-1.log:51`，错误为 `unable to encode []string ... into text format for text (OID 25)`。
- `loadBatchConfigs()` 对 batch error 只 warn 后返回 nil：`internal/module/uistate/module.go:119-123`。
- `enrichFromDB()` 在 batch nil 后逐 thread fallback：`internal/module/uistate/module.go:144-152`。
- 逐线程 fallback error 已在 log 中真实出现：
  - `session not found`：`agent-terminal-2026-06-04-1.log:56`。
  - `get_by_agent_id binding: no rows`：`agent-terminal-2026-06-04-1.log:57-58`。
- `ReadRuntimeConfigs()` 对任一 thread runtime 解析错误直接返回 error：`internal/module/thread/history.go:177-197`。
- missing session / missing binding 是当前测试锁定的 fail-fast 行为：`internal/module/thread/factory_config_failfast_test.go:306-338`。
- pending launch 是已有上层例外：`internal/module/thread/history.go:238-244` 允许 pending launch 无 binding。

**上层防护检查：**

有一个重要功能性防护：`GetSidebar()` 当前拿到的 runtime config 在 `applyTaskRuntimeToThreadRuntimeConfig()` 中没有实际应用，该函数为 no-op：`internal/module/uistate/module.go:204-208`。因此 missing runtime config 不会直接破坏 sidebar 字段展示。

但没有性能防护：batch error 后仍进入逐 thread fallback，且 logger 没有节流/上限；本次 log 复核计数为 `ReadRuntimeConfigs failed=1030`、`ReadRuntimeConfig failed=199515`，其中 `session not found=150012`、binding no rows `47834`。

**修正后建议：**

1. 原 finding 不应写成“只修 SQL 会破坏 UI 字段”，应写成“只修 SQL 未必消除 fallback/warn 风暴”。
2. 修复拆成两层：
   - SQLC/pgx 数组查询编码修复。
   - uistate sidebar enrichment 对 stale/missing runtime config 的明确性能策略。
3. 不要在 thread runtime config reader 内吞掉 fail-fast 错误；如果 sidebar 不需要 runtime config，应考虑在 uistate 层移除无用读取或做有界/可解释降级。
4. 验收要求：`ReadRuntimeConfigs failed == 0`、逐线程 `ReadRuntimeConfig failed` 不再爆量、`make sqlc-verify`、SQL/store 防回归测试、uistate 不产生逐 thread warn 风暴的测试。

---

### F6 / P2 — legacy refresh 降噪风险真实可达；已有测试是兼容防护但非运行时降噪防护

**追溯结论：风险真实可达；保留 P2。**

**源码证据：**

- `ExpandNotifications()` 会追加 legacy refresh：`internal/platform/eventsurface/legacy.go:20-43`。
- 带 thread/agent identity 的事件默认触发 sidebar refresh，workspace run 也显式触发：`internal/platform/eventsurface/legacy.go:46-68`。
- 抑制列表只包含 token usage、`ui/thread/patch`、output delta 等，不包含 `ui/thread/changed`：`internal/platform/eventsurface/legacy.go:71-83`。
- `UIProjectionUpdated` 会映射：`Projection == "sidebar"` -> `ui/sidebar/changed`，其他 projection -> `ui/thread/changed`：`internal/platform/eventsurface/bind.go:451-457`。
- `projectionUpdatedPayload()` 会把非空 `ThreadID` 放入 payload：`internal/platform/eventsurface/bind.go:460-468`；因此 timeline projection 的 `ui/thread/changed` 带 `threadId` 时，会被 `ExpandNotifications()` 再追加 `ui/sidebar/changed`。
- 前端没有把 `ui/sidebar/changed` 放入 revision-only 事件；`BRIDGE_REVISION_EVENTS` 不包含它：`frontend-app/src/entities/client/model/useClientStore.js:96-101`。
- `handleBridgeEvent()` 对 `ui/sidebar/changed` 直接调用 `refreshActiveChatSidebarInBackground()`：`frontend-app/src/entities/client/model/useClientStore.js:3495-3502`。
- 兼容测试锁定 direct legacy refresh：`internal/platform/eventsurface/legacy_test.go:5-42`，Wails bridge 兼容事件测试锁定 3 个事件：`internal/ui/wails/bridge_test.go:5-56`。

**上层防护检查：**

有测试防护，防止误删 direct legacy contract；但没有运行时防护来避免高频 timeline projection 被扩成 sidebar refresh。前端唯一 guard 是缺 cwd 时跳过：`frontend-app/src/entities/client/model/useClientStore.js:3119-3126`，在正常项目页面不构成降噪。

**修正后建议：**

1. “减少 legacy refresh”必须限定为有证据的二次放大路径，不能删除 direct `thread/started`、workspace run、approval/error/provider push 等 legacy refresh。
2. 文档应标明：已有测试是兼容边界，后续修改必须同步更新/保留这些测试。
3. 如需抑制 timeline projection -> sidebar 的放大，应新增表驱动测试证明只抑制该路径，不破坏 direct sidebar/list/order/name/status 更新。

---

### F7 / P3 — frontend single-flight freshness 是实施风险，不是当前已发生 bug

**追溯结论：当前代码不存在 reuse-only，所以该风险不是当前生产可达 bug；降级为 P3 实施边界。**

**源码/trace 证据：**

- 当前每个 `ui/sidebar/changed` 都触发 `refreshActiveChatSidebarInBackground()`：`frontend-app/src/entities/client/model/useClientStore.js:3495-3502`。
- 当前 refresh 每次真实发 `getSidebarState({ cwd })`，只有 `sidebarRefreshSeq` 丢弃旧结果：`frontend-app/src/entities/client/model/useClientStore.js:3075-3112`。
- 现有测试只覆盖单次 `ui/sidebar/changed` 会刷新：`frontend-app/src/entities/client/model/useClientStore.test.js:1955-1985`。
- 同一 turn trace 中 timeline revision 从 `102` 到 `1472`，说明慢请求期间仍有大量后续变化：`trace-2026-06-04.jsonl:1457`、`:3889`。

**上层防护检查：**

当前 `sidebarRefreshSeq` 防的是旧响应覆盖新状态；它不减少请求数。若未来实现 reuse-only single-flight，`sidebarRefreshSeq` 本身不能保证 in-flight 期间发生的最后一次事件会再次拉取。因此 freshness 风险是“后续实现可达”，不是当前已发生。

**修正后建议：**

1. 将该项归入 F8 的实现验收清单，或保留为 P3 实施边界。
2. 文档中把“复用或标记 pending-after-current”改成“single-flight + dirty bit/pending-after-current + trailing refresh”。
3. 增加测试：burst N 个 `ui/sidebar/changed` 最多 1 个 in-flight + 1 个 trailing，最终应用最新 snapshot；failed/timeout promise 不得长期缓存；`clearSurface` cwd 切换刷新不能被普通 background refresh 阻塞。

---

### F8 / P2 — 缺少跨层验收清单；已有局部测试但未覆盖事故闭环

**追溯结论：风险真实可达；保留 P2。**

**源码/测试证据：**

- 被审查文档 `:188-205` 只有方向性修复列表；`:206-210` 明确未启动新 turn/run、未运行测试。
- 已有局部测试：
  - 单次 `ui/sidebar/changed` 会刷新：`frontend-app/src/entities/client/model/useClientStore.test.js:1955-1985`。
  - fail-fast 行为：`internal/module/thread/factory_config_failfast_test.go:306-338`。
  - legacy compatibility：`internal/platform/eventsurface/legacy_test.go:5-42`、`internal/ui/wails/bridge_test.go:5-56`。
- 缺失覆盖：
  - sidebar burst coalesce + trailing refresh。
  - SQLC 多 ID 查询实际生成 SQL/pgx 编码防回归。
  - uistate batch failure 不触发无界逐 thread warn。
  - turn/run 期间切 nav 页的前后 trace/log 对比。

**上层防护检查：**

局部单测能防部分兼容回归，但没有一个验收关口覆盖“事件风暴 -> sidebar get 数量 -> batch/fallback -> nav RPC timeout”全链路。

**修正后建议：**

补 “Implementation acceptance” 小节，至少包含：

1. Frontend：同 cwd burst `ui/sidebar/changed` 期间最多 1 in-flight + 1 trailing refresh，最终 snapshot fresh；运行 `frontend-app` scoped Vitest。
2. SQL/store：`ListAgentThreadConfigsByIDs` 多 ID 查询可用；运行 `make sqlc-verify` 与 affected Go tests。
3. Uistate：batch failure 不再触发无界逐线程 warn 风暴；`ReadRuntimeConfigs failed` 和 `ReadRuntimeConfig failed` 指标显著下降/清零。
4. Event surface：保留 legacy compatibility 测试，并只收窄有证据的二次放大路径。
5. Runtime/manual trace：turn/run 进行中切 prompts/skills/dashboard/shared files，不再出现 page-level 8s 红框和 runtime 30s timeout；采集前后 trace/log 对比。

---

### F9 / P2 — 旧专项文档引用必须删除或降级为非证据背景

**追溯结论：风险真实可达；保留 P2。**

**文档证据：**

- 被审查文档 `:182-186` 引用 `docs/cc/workflow-sync-root-cause-20260604.md`，并写“那份结论仍解释……”。

**上层防护检查：**

无。该段位于当前根因文档正文中，会被读者当作当前证据链的一部分。

**修正后建议：**

删除该节；或改成：

> 历史文档仅作背景索引；本文不继承其结论为证据。自动化页专项问题若仍需纳入，必须用当前 trace/source 重新列证据。

## 被剔除 / 降级候选（修正版）

| 候选 | 决议 | 理由 |
|---|---|---|
| 旧专项文档 `docs/cc/workflow-sync-root-cause-20260604.md` 的结论 | 剔除为证据 | 本轮未复核旧报告；只能作为非证据背景索引。 |
| 旧报告里的 P0/P1/P2 编号 | 未采纳 | 目标文档未发现明显旧编号夹带；本文件中的 P1/P2/P3 是本轮新分级。 |
| “prompt/skills/dashboard handler 本身慢或数据源损坏” | 剔除 | trace 显示其后端 dispatch 多为毫秒到百毫秒级，例如 `prompt-assets/list` 17/44ms、`ui/preferences/get` 4ms、`ui/dashboard/get` 65ms；慢主要发生在 frontend/runtime/pre-dispatch wait。 |
| “生产 native Wails 必然有相同 30s timeout” | 剔除 | 当前失败 trace `client_kind=web-debug-shim`；native/packaged 需另采 `desktop-wails` 证据。 |
| “精确证明瓶颈在同一 WebSocket 串行队列” | 降级为推断 | 源码证明 dev WebSocket/RPC 路径，trace 证明 pre-dispatch wait；没有队列深度/位置埋点。 |
| “直接增大 timeout 或吞掉 timeout 文案即可修复” | 剔除 | `withTimeout()` 与 runtime timeout 不取消底层 RPC；请求风暴和后端读路径成本仍在。 |
| `cmd/agent-terminal/frontend/wails/runtime.js` 作为当前 React UI 主证据 | 剔除/替换 | 当前 React/Vite UI shim 应引用 `frontend-app/public/wails/runtime.js`。 |
| `docs/cc/...md:114` 的 backend 耗时数字作为全量统计 | 剔除/修正 | 该数字可解释为 paired subset，但不能作为全量 1008 条 backend done 统计。 |
| “只要修 SQLC batch 就一定消除 warn 风暴” | 剔除 | missing session/binding/fail-fast 语义仍可能让 batch fail，并触发 uistate fallback；当前 no-op 只限制功能影响，不限制性能/warn 风暴。 |
| “frontend reuse-only single-flight 已经导致状态丢失” | 降级 | 当前代码没有 reuse-only；这是未来实现风险，应写入验收边界而非当前 bug 事实。 |

## 建议先改根因文档的最小清单

1. 修正 `ui/sidebar/get` backend duration 统计口径：区分全量 backend done 与 frontend-paired subset，并附复算脚本。
2. 修正 runtime shim 路径为 `frontend-app/public/wails/runtime.js`，并声明当前失败证据范围为 `web-debug-shim`。
3. 将“同一通道排队”改写为“pre-dispatch wait + 传输路径推断”。
4. 区分页面层 8s 红框与底层 `callAPI`/runtime 30s timeout。
5. 删除或降级旧专项文档引用。
6. 在修复方向下补：frontend trailing refresh、SQL/fail-fast、legacy compatibility、验收测试/trace 指标。
7. 对 F5 明确：`applyTaskRuntimeToThreadRuntimeConfig()` 当前 no-op，所以 missing runtime config 主要是性能/warn 风暴风险；不要扩大为已证实的 sidebar 字段错误。
