# P1b-UI: Cron 定时任务前端

> 配套 [`P1b_CronScheduledTasks.md`](P1b_CronScheduledTasks.md) 的宿主端 UI 落地。后端 `cronjob/*` 7 个 RPC 已就绪（见 `internal/module/cron/rpc.go:60-68`），本文只覆盖 `cmd/agent-terminal/frontend/vue-app` 这一前端层。

## 目标

为 P1b 的 cron 后台调度补一个用户可见的管理界面，覆盖：列表 / 创建 / 编辑 / 启停 / 删除 / 历史 runs / 下次触发预览。v1 不暴露 agent-visible tool，也不暴露 approval allow-list 的可视化编辑（T-13 落地后再补）。

## 现状校准

- **RPC 已就绪**（不需要新增后端方法）：`cronjob/create | update | get | list | delete | setEnabled | listRuns`，定义于 `internal/module/cron/rpc.go:60-68`；wire shape 见同文件 `cronCreateParams` 等（line 21-55）。
- **`next_run_at` 由调用方传**：`internal/module/cron/rpc.go:17-19` 注释明确，host RPC 当前**不**根据 `schedule_expr` 自动算下一次时间；省略时退化为 `now + 1min`。"真正的 cron 表达式解析" 排在 phase 2b。**因此 v1 前端必须自己用 cron 解析器算 `next_run_at` 并显式传给后端**，否则任何 cron 表达式都会退化为 1 分钟后单次触发。
- **v1 provider 白名单**：`provider=codex` 才接受；`config.codexHome / codexInstanceKey / codexModelProvider` 三字段在 `provider=codex` 时缺一报错。后端 sentinel 实测见 `internal/module/cron/contract.go:31-40`，**实际只有 8 个**：`ErrProviderNotSupported / ErrMissingCWD / ErrMissingName / ErrMissingPrompt / ErrMissingSchedule / ErrInvalidMaxAttempts / ErrInvalidConfig / ErrNotFound`。**没有** `ErrCodexHomeRequired` —— codex identity 三字段缺失实际归为 `ErrInvalidConfig`（service 层走 `providershared.CanonicalizeCodexHome` 校验后包成此 sentinel）。
- **错误码全部折叠成 `InvalidParams`**：`internal/module/cron/rpc.go:209-234` 的 `mapRPCError` 把 7 个 validation sentinel 一律映射为 `jrpc2.InvalidParams`，**只有 `ErrNotFound` 走独立 not-found 码**。前端不能靠 jrpc2 code 区分 kind，必须按 message 文本前缀（所有 cron error message 以 `cron: ` 开头）匹配；建议 `cron-api.js` 用一张 `messagePrefix → kind` 映射表。
- **状态机**：runs 的状态固定为 `pending → submitting → submitted → running → finished | failed | observe_lost`，详见 P1b 文档"Crash-window idempotency state machine"段。`observe_lost` 是不可自动恢复终态。
- **前端壳**：Vue 3 + 组合式风格；Sidebar 入口集中在 `cmd/agent-terminal/frontend/vue-app/app.js:29-39` 的 `NAV_ITEMS`；JSON-RPC 通道是 `services/api.js` 的 `callAPI(method, params)`（`services/api.js:234`）。
- **不存在 `cronjob/runOnce`**：本期手动触发暂不做，UI 不出该按钮；后续若后端补，再加 row action。

## 推荐架构

- **路由层**：在 `NAV_ITEMS` 增加一项 `{ key: 'cron', label: '定时任务' }`，复用现有 `page` 单状态切页机制（不引入路由库）。
- **页面切片**：单页内部 `view ∈ { 'list', 'detail' }`，`selectedJobId` 配套；与现有 `chat` / `dags` 页面同形，无需 history API。
- **API 封装**：新增 `services/cron-api.js`，对 7 个 RPC 做薄包装 + 类型注释，**唯一**调用方是 store。禁止组件直接 `callAPI('cronjob/...')`。
- **Store**：新增 `stores/cron.js`，holds `jobs: Job[]`、`runsByJob: Map<jobId, Run[]>`、`loading / error`；提供 `loadJobs / createJob / updateJob / setEnabled / deleteJob / loadRuns` 异步 action。所有写操作走乐观更新 + 失败回滚（参考 `stores/threads.js` 的写法）。
- **Cron 解析**：用 `cron-parser` npm 包算"未来 5 次触发"和提交时的 `next_run_at`。**当前 `cmd/agent-terminal/frontend/package.json` 没有该依赖**，必须在本期作为 PR 一部分新增 `cron-parser` 到 dependencies；不要手写 cron 解析。Vite 6（`cmd/agent-terminal/frontend/vite.config.js`）会正常打包该依赖。前端始终在用户本地浏览器算，并以 timezone 字段一并传后端，便于后端 phase 2b 复算时口径一致。
- **实时刷新**：列表页**不**轮询；订阅 wails event `cron.job.run_state_changed` → 收到后增量刷新对应 row。**事实：当前 `internal/module/cron/subscribers.go:1-41` 仅订阅内部 `platformbus`，并没有任何 `EmitEvent` 调用 / wails 桥透传**——本期必须新写桥点（详见下文"事件桥"段）。降级策略：事件桥失败 / 不可用时回退到打开页面时一次性 `cronjob/list` + 用户手动刷新按钮。
- **错误展示**：`cron-api.js` 把 jrpc2 error 解析回 `{ code, kind, message }`；`kind` 取值由 message 文本匹配得出：`cwd_required`（`cron: cwd is required`）/ `invalid_config`（`cron: config is invalid for provider`，**即 codex identity 三字段缺失**）/ `provider_unsupported`（`cron: provider not supported in v1...`）/ `name_required` / `prompt_required` / `schedule_required` / `invalid_max_attempts` / `not_found` / `unknown`，在表单层映射到具体字段红框，而不是 toast。

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| 路由 | `cmd/agent-terminal/frontend/vue-app/app.js` | `NAV_ITEMS` 加 `cron` 项；render 分支挂载 `<CronPage />`；新增 `<keep-alive>` 视情况复用 |
| API 包装 | `cmd/agent-terminal/frontend/vue-app/services/cron-api.js` [NEW] | `listJobs / getJob / createJob / updateJob / deleteJob / setEnabled / listRuns`；统一 `mapCronRpcError(err)` |
| Store | `cmd/agent-terminal/frontend/vue-app/stores/cron.js` [NEW] | reactive jobs / runs；乐观更新 + 失败回滚；订阅 wails 事件 `cron.job.run_state_changed` |
| 页面壳 | `cmd/agent-terminal/frontend/vue-app/components/cron/CronPage.js` [NEW] | view 切换 / breadcrumb / 全局错误条 |
| 列表 | `cmd/agent-terminal/frontend/vue-app/components/cron/CronJobList.js` [NEW] | 见下文"列表页"；含搜索 / 状态筛选 / 启停 toggle / 删除确认 |
| 表单 | `cmd/agent-terminal/frontend/vue-app/components/cron/CronJobForm.js` [NEW] | 创建 + 编辑同组件；按"基本 / 调度 / Provider 身份 / 执行策略"四段；联动校验 |
| 调度控件 | `cmd/agent-terminal/frontend/vue-app/components/cron/ScheduleField.js` [NEW] | cron 表达式 + 时区选择 + 未来 5 次本地预览；用 `cron-parser` |
| 身份控件 | `cmd/agent-terminal/frontend/vue-app/components/cron/ProviderIdentityField.js` [NEW] | 复用 P1a binding identity（`codexHome / codexInstanceKey / codexModelProvider`）三字段，单选预设 + 手填回退 |
| 详情 | `cmd/agent-terminal/frontend/vue-app/components/cron/CronJobDetail.js` [NEW] | 元信息 + 当前 claim + 历史 runs |
| Run 行 | `cmd/agent-terminal/frontend/vue-app/components/cron/CronJobRunRow.js` [NEW] | 6 态色板 + `observe_lost` 单独提示；点击 `turn_id` 跳 chat |
| 桥事件 | `internal/module/cron/subscribers.go` + 注入 `internal/ui/wails/bridge.EventBridge` + 视情况补 `eventsurface` 注册项 | 在 cron 现有 `subscribeCronProgress` / `subscribeCronTerminalEvents` 回调内调 `EventBridge.publish("cron.job.run_state_changed", payload)`，复用 `bridge.go:66-79` → `lifecycle.go:128-136` 的既有透传链；见"事件桥"段 |
| 测试 | 各 `*.test.js` 同目录伴随 | 至少覆盖：cron-parser 边界、表单字段联动校验、6 态色板、错误映射、store 乐观更新与回滚 |

> `[NEW]` 表示目标新增路径，当前仓库尚不存在。

## 状态色板（贯穿列表 / 详情）

| 状态 | 颜色 | 形态 |
|---|---|---|
| `pending` | 灰 #9CA3AF | 静态 |
| `submitting` | 蓝 #3B82F6 | spinner |
| `submitted` | 蓝 #3B82F6 | 实心 |
| `running` | 紫 #8B5CF6 | spinner |
| `finished` | 绿 #10B981 | 实心 |
| `failed` | 红 #EF4444 | 实心 |
| `observe_lost` | 橙 #F59E0B | 带 ⚠️；hover 提示"观察链丢失，需人工核对 turn"，链接到 P1b 文档锚点 |

## 列表页（CronJobList）

顶部操作栏：`+ 新建任务` / 搜索框 / 状态筛选（启用 / 暂停 / 失败中）/ 刷新按钮。

**列**（默认）：

| 列 | 数据源 | 备注 |
|---|---|---|
| 启用 | `enabled` | toggle，乐观更新；调 `cronjob/setEnabled` |
| 名称 | `name` | 点击进详情 |
| Schedule | `schedule_expr` + `timezone` | hover tooltip 显示"下一次：`next_run_at` 的本地化时间" |
| Provider | `provider` + `codexInstanceKey` | v1 全是 codex，仍显式列出便于多实例区分 |
| CWD | `cwd` | 截断 + 完整路径 tooltip |
| 上次状态 | `last_status` + `last_run_at` | 6 态色板 |
| 失败 / 预算 | `failure_count / max_attempts` | `max_attempts=0` 显示"不重试" |
| 操作 | 编辑 / 删除（双击确认） | 不出"立即触发"，因为 `runOnce` 后端未实现 |

## 创建 / 编辑表单（CronJobForm）

四段 + 字段联动校验。

### ① 基本
- 名称（必填，前端去重检查走列表已加载数据）
- Prompt（多行；可挂 skills 选择器，复用 `LaunchSkillPicker.js`）
- CWD（必填，强制走 `PathChoiceModal.js`，禁止手敲空白）

### ② 调度（ScheduleField）
- Cron 表达式（必填）+ 内联校验（用 `cron-parser` 试 parse）
- 时区（默认 `Intl.DateTimeFormat().resolvedOptions().timeZone`）
- "未来 5 次预览"（前端纯算 cron）
- 提交时把首条预览作为 `next_run_at` RFC3339 字符串带上，**避开后端 1 分钟 fallback**（详见"现状校准"）

### ③ Provider 身份（ProviderIdentityField）
- Provider 下拉：`codex`（默认）、`claude`（v1 disable + tooltip：`v1 仅支持 codex`）
- `provider=codex` 时三字段必填：`codexHome / codexInstanceKey / codexModelProvider`
  - 推荐 UX：列出已存在的 codex binding identity（来自 P1a 的存量实例），用户挑一个即一并塞 `config`；提供"手动填写"折叠区作为兜底
- Model（可选，留空 = 后端默认）

### ④ 后台执行策略
- 重试预算 `retry_budget`（线框 = `max_attempts`，UI 文案不用 max_attempts）；默认 0；选 0 时下方文案"失败后等下一次 schedule，不做额外重试"
- 通知渠道 `notify_channel`（下拉，从 P2 channel 列表取；空值 inline 警告"未配置 = 失败时不通知"，无默认兜底——与 P1b 文档一致）
- 启用开关

### 联动校验（前端阻断提交）
- `provider=codex` 且 identity 三字段任一为空 → 三字段红框 + 禁用提交
- `cwd` 为空 → 阻断
- cron 表达式无法 parse → 阻断
- 后端 sentinel 错误回写到对应字段：`cwd_required → cwd`、`invalid_config → identity 三字段`（即 codex `codexHome / codexInstanceKey / codexModelProvider` 任一缺失）、`provider_unsupported → provider`、`schedule_required → schedule_expr`；cron 表达式本身不合法由前端 `cron-parser` parse 失败推到 `schedule_expr` 字段红框，后端 v1 不对 cron 表达式语法做校验。

## 详情页（CronJobDetail）

**顶部**：任务元信息 + `enabled` toggle + 编辑 / 删除。

**KV 卡片**：
- 上次成功 / 上次失败 / 当前 claim：`claimed_by` + `lease_expires_at` 倒计时（≤ 5 分钟内剩余时变橙）
- 当前 active turn：`active_turn_id` 点击跳到 chat 页面（复用 thread 路由）

**历史 runs**（分页 / 增量加载）：
渲染顺序按 `scheduled_at desc`，单行结构：
```
[状态色块]  scheduled_at(本地)  →  submitted_at  →  finished/failed
            turn_id (link to thread)   error 摘要(可展开)
            dedupe_key (折叠 / 复制按钮)
```

特殊处理：
- `observe_lost` 行展开"为何会出现 + 可能的人工动作"，并指向 P1b 文档锚点
- `submitting + turn_id` 为空状态（罕见）显式标注 "recovery 中"
- `failure_count` ≥ `max_attempts > 0` 时在卡片顶部出黄色提示条："已用尽重试预算，等下次 schedule"

## 事件桥（前后端共同改动）

仓内已有标准桥实现，**不要**自己写新的 emitter helper：

- `internal/ui/wails/bridge.go:15-22` 定义 `EventBridge`；其 `publish(method, payload)`（line 66-79）会调 `eventsurface.ExpandNotifications()` 把内部事件展开成多条对外通知，然后调 `lifecycle.EmitEvent(name, payload)`（`internal/ui/wails/lifecycle.go:128-136`）真正打到 wails runtime。
- 本期需要做的事：
  1. 在 cron 的 run state 推进点（`internal/module/cron/subscribers.go` 的 `subscribeCronProgress` / `subscribeCronTerminalEvents` 回调里）调 `EventBridge.publish("cron.job.run_state_changed", payload)`；这要求把 `*EventBridge` 通过 fx 注入到 cron subscribers，与 `notify` 模块的注入方式对齐。
  2. 如果 `eventsurface` 注册表里没有 `cron.job.run_state_changed`，要补一条注册项（`eventsurface` 是 publish 展开通知的真值表；漏注册会让事件被丢弃）。
- 事件 payload 约定：`{ job_id, run_id, status, turn_id?, error? }`，`status ∈ pending|submitting|submitted|running|finished|failed|observe_lost`。

前端：`stores/cron.js` 在初始化时通过 `services/api.js:466-482` 暴露的 `onBridgeEvent(callback)` 监听；callback 内根据 `evt.name === 'cron.job.run_state_changed'` 分发，触发增量刷新对应 job 的 runs。降级路径：事件不可用时回退到"打开详情时一次拉取 runs"。

## v1 范围切割

**纳入 v1**：列表 / 创建 / 编辑 / 删除 / 启停 / 历史 runs / 下次触发预览 / 状态色板 / 字段红框联动 / wails 事件订阅。

**v1 暂不做**：
- agent-visible 的 cron 工具（P1b 后端文档明确 v1 不开放给模型）
- "立即触发"按钮（需要后端补 `cronjob/runOnce`）
- approval allow-list 可视化编辑（依赖后端 T-13）
- 跨用户 / 团队 / 多角色权限视图

## 必测项（前端）

- `services/cron-api.js`：7 个 RPC 各自一条 happy path；`mapCronRpcError` 对四类已知错误的映射断言。
- `stores/cron.js`：乐观更新 + 失败回滚（`setEnabled` / `deleteJob` / `updateJob` 各一条）；wails 事件触达后 `runsByJob` 增量更新。
- `ScheduleField.js`：合法 / 非法 cron；夏令时切换日的"下一次"计算正确；时区切换后预览同步刷新。
- `CronJobForm.js`：`provider=codex` 时三字段联动校验；切换 provider 到 claude 时 disabled + tooltip；后端 sentinel 错误回写到字段红框。
- `CronJobRunRow.js`：6 个状态各一个快照；`observe_lost` 提示文案与链接锚点。
- `CronJobList.js`：搜索 / 筛选 / 启停 toggle 触发 store 调用；`max_attempts=0` 文案"不重试"；空状态文案。
- 集成快照：从列表点入详情，详情页 wails 事件刷新 row state，不触发整页重渲。

## 给后端的反馈（不在前端 PR 内）

1. **`next_run_at` 自动从 `schedule_expr` 算**：当前 host RPC 把这件事推给前端，多端实现极易出现口径漂移（不同浏览器 ICU 数据不一致 / 有 / 无 `cron-parser` 升级）。建议 phase 2b 尽早接 `robfig/cron` 之类后端解析器。
2. **`cronjob/runOnce`**："立即触发"在用户调试任务时是高频需求；必须复用 P1b 的三步原子协议生成 `idempotency_key + dedupe_key`，不能简单走旁路。
3. **wails event 透传**：`cron.job.run_state_changed` 这一前端订阅约定需要后端正式接进 emitter；目前仓内已有 `bus.subscribers` 形态，但缺前端可消费的事件桥。
