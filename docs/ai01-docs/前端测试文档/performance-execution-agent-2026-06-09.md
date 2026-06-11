# Super-Dolphin 性能优化执行 Agent 任务书

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans` 按任务逐项执行。每个任务必须有独立检查点、独立验证结果和可回滚边界。

**Goal:** 根据 `docs/ai01-docs/前端测试文档/performance-audit-2026-06-09.md` 的审计结论，分阶段修复 Super-Dolphin 前后端结构性性能瓶颈，并用可量化指标证明优化收益。

**Architecture:** 当前产品主界面是 `frontend-app/` React/Vite UI，经 Wails v3 桌面宿主调用 Go RPC，再进入 `internal/module/*`、`internal/store/*` 和 PostgreSQL。执行策略必须先补齐观测，再处理 P1 核心瓶颈，最后处理 P2/P3 局部优化；不要一次性重写大文件或混合多个风险面。

**Tech Stack:** React 19、Vite 8、Zustand 5、TanStack Query 5、Go 1.25.7、Wails v3、jrpc2、Fx、pgx/sqlc、PostgreSQL。

---

## 0. 输入文档

执行 agent 开始前必须读取：

1. `AGENTS.md`
2. `README.md`
3. `docs/doc/codemap/README.md`
4. `docs/ai01-docs/前端测试文档/performance-audit-2026-06-09.md`
5. 当前任务涉及的源码和同包测试。

当前审计报告已经确认的最高优先级问题：

| 编号 | 问题 | 优先级 | 主要文件 |
|---|---|---|---|
| PERF-P1-01 | assistant streaming 每 50ms flush 且 timeline O(n) 更新 | P1 | `frontend-app/src/entities/client/model/useClientStore.js` |
| PERF-P1-02 | Wails/RPC done trace 缺少 `result_bytes` | P1 | `internal/ui/wails/binding.go`、`internal/platform/rpc/server.go` |
| PERF-P1-03 | bootstrap 多轮 Wails RPC | P1 | `frontend-app/src/entities/client/model/runtimeSlice.js`、`frontend-app/src/shared/api/backendApi.js`、`internal/module/uistate/*` |
| PERF-P1-04 | `ui/sidebar/get` enrich 仍需 rows/bytes 证据和 binding 范围核查 | P1/P2 | `internal/module/uistate/service.go`、`internal/module/uistate/module.go`、`internal/store/binding/*`、`sql/queries/thread_binding.sql` |
| PERF-P1-05 | 多处 `%keyword% ILIKE` 和 JSONB tag scan | P1/P2 | `sql/queries/*.sql`、`migrations/*` |
| PERF-P2-01 | 页面级 lazy import 与 `AppShell` shallow selector 已落地，需要回归锁定与收益采样 | P2 | `frontend-app/src/App.jsx`、`frontend-app/src/pages/chat/*` |

## 1. 硬性执行约束

1. 不要在 `main` 直接做实现。执行前创建隔离 worktree 或确认用户已指定工作区。
2. 不要修改 legacy Vue 前端 `cmd/agent-terminal/frontend`，除非任务明确要求打包嵌入路径。
3. 不要一次性实现所有优化。默认一个 P1 任务一个分支/PR。
4. 不要新增生产依赖，除非先给出必要性、替代方案、风险和用户确认。
5. 不要引入静默兜底。异常、配置为空或数据缺失必须 fail-fast。
6. 不要修改 SQL/migration/index 之前先做 `EXPLAIN` 或等价证据；没有真实数据证据时只提交诊断文档或埋点。
7. 不要降低 guard、测试或架构约束。
8. 每改完 Go 文件，先运行单文件守卫：`./scripts/test_with_guard.sh <file.go>`。
9. 前端变更至少运行相关测试，重要 UI 变更再运行 `npm run lint`、`npm test`、`npm run build`。
10. 最终报告必须说明改了什么、验证了什么、哪些测试没跑、剩余风险是什么。

## 2. 推荐多 Agent 组织

执行阶段不建议继续使用 81 个审计 Agent。修复需要更小、更可控的工作面。

### 2.1 主执行 Agent：Performance-Execution-Orchestrator

职责：

1. 创建隔离 worktree。
2. 读取审计报告和当前源码。
3. 按 P1/P2 优先级拆任务。
4. 每个任务分派给单独执行 Agent。
5. 每个任务完成后安排验证 Agent 做交叉检查。
6. 只合并经过验证的任务。
7. 输出中文执行报告。

### 2.2 执行 Agent

| Agent | 任务面 | 默认优先级 |
|---|---|---|
| Exec-Agent-01 | 观测补齐：RPC result bytes、frontend vitals、sub-span | P1 |
| Exec-Agent-02 | Streaming timeline：O(n) 更新优化 | P1 |
| Exec-Agent-03 | Bootstrap：多轮 RPC 证据采样与合并方案 | P1 |
| Exec-Agent-04 | `ui/sidebar/get`：保留现有分段日志，补 rows/bytes，核查 binding 范围 | P1/P2 |
| Exec-Agent-05 | SQL 搜索：EXPLAIN、索引/查询策略计划或实现 | P1/P2 |
| Exec-Agent-06 | 已落地项回归：页面 lazy import、`AppShell` shallow selector、bundle 采样 | P2 |
| Exec-Agent-07 | ThreadRail/Timeline 虚拟化和大文本渲染 | P2 |
| Exec-Agent-08 | sharedFiles / Observability 读放大优化 | P2 |

### 2.3 验证 Agent

| Agent | 验证对象 |
|---|---|
| Verify-Agent-Frontend | React/Vite lint、test、build、bundle size、关键页面行为 |
| Verify-Agent-Backend | Go guard、同包测试、RPC trace 字段、日志安全 |
| Verify-Agent-SQL | SQL explain、migration 幂等、sqlc verify |
| Verify-Agent-Regression | 用户核心链路：启动、打开线程、发送消息、打开文件、Observability 查询 |

## 3. 总体执行顺序

必须按以下顺序执行，除非用户明确指定跳过：

1. **Phase 0：建立基线**
   只采样、不修复。记录当前 bundle、启动 RPC 次数、慢接口、关键页面行为。

2. **Phase 1：补齐观测**
   先让后续优化有 `duration + bytes + rows + frontend render` 指标。

3. **Phase 2：首屏与全局重渲染**
   页面 lazy import 和 `AppShell` shallow selector 已落地；本阶段只做回归锁定、bundle 采样和 Chat 子树 selector 收窄。

4. **Phase 3：聊天高频路径**
   优化 streaming timeline O(n)、ThreadRail/Timeline 渲染。

5. **Phase 4：后端读路径**
   优化 `ui/sidebar/get`、`thread/messages`、dashboard/sharedFiles。

6. **Phase 5：SQL 与数据加载策略**
   基于 EXPLAIN 和真实表数据再决定索引、分页、查询改写。

## 4. Phase 0：基线采样任务

### Task 0.1：确认工作区和基线状态

**Files:** 不修改源码。
**Commands:**

```bash
git status --short
git branch --show-current
rg --files | rg '(^AGENTS.md$|README.md$|docs/doc/codemap/README.md$)'
```

**Expected:**

- 工作区没有与本任务无关的已跟踪脏改动。
- 如有未跟踪审计文档，记录但不要删除。
- 当前实现工作不在 `main` 上直接进行。

### Task 0.2：采样前端 bundle

**Files:** 不修改源码。
**Commands:**

```bash
cd frontend-app
npm run build
du -sh dist
find dist/assets -maxdepth 1 -type f -print0 | xargs -0 du -h | sort -h | tail -n 20
```

**Record:**

- `dist` 总体积。
- 最大 10 个 chunk。
- `index-*.js`、`react-core-*.js`、`katex-*.js`、`cytoscape-*.js` raw size。

### Task 0.3：采样后端关键测试面

**Files:** 不修改源码。
**Commands:**

```bash
./scripts/test_with_guard.sh ./internal/module/uistate -count=1
./scripts/test_with_guard.sh ./internal/module/thread -count=1
./scripts/test_with_guard.sh ./internal/module/dashboard -count=1
```

**Expected:**

- 记录当前 pass/fail。
- 若失败，先判断是否已有失败；不要把预存失败归因到后续优化。

## 5. Phase 1：观测补齐

### Task 1.1：Wails/RPC trace 增加 `result_bytes`

**Files:**

- Modify: `internal/ui/wails/binding.go`
- Modify: `internal/platform/rpc/server.go`
- Test: 现有 `internal/ui/wails/*test.go`、`internal/platform/rpc/*test.go`，必要时新增 focused test。

**Implementation requirements:**

1. `wails.call_api.done` 必须记录 `result_bytes`。
2. `backend.rpc.dispatch.done` 必须记录 `result_bytes`。
3. 只记录字节数，不记录 raw result。
4. failed/start 事件可以不记录 result bytes，或记录为 0。
5. `param_bytes`、`param_keys` 原有语义不能变。

**Suggested shape:**

```go
metadata := map[string]any{"param_bytes": len(params)}
if resultBytes > 0 {
    metadata["result_bytes"] = resultBytes
}
```

**Verification:**

```bash
./scripts/test_with_guard.sh internal/ui/wails/binding.go
./scripts/test_with_guard.sh internal/platform/rpc/server.go
./scripts/test_with_guard.sh ./internal/ui/wails ./internal/platform/rpc -count=1
```

**Acceptance:**

- Trace 事件包含 `result_bytes`。
- 没有泄露响应内容。
- Go guard/test 通过。

**Rollback:**

- 回退 `result_bytes` 字段传递和测试，不影响原慢调用 trace。

### Task 1.2：前端补充 Web Vitals / Long Task / 子组件 Profiler

**Files:**

- Modify: `frontend-app/src/main.jsx`
- Modify: `frontend-app/src/App.jsx`
- Modify: `frontend-app/src/pages/chat/ChatPage.jsx`
- Test: `frontend-app/src/App.test.jsx` 或相关页面测试。

**Implementation requirements:**

1. 不新增生产依赖。使用 `PerformanceObserver` 和现有 `emitFrontendTraceEvent`。
2. 记录 FCP、LCP、Long Task、App/Chat/ThreadRail/ConversationTimeline 慢渲染。
3. 只在 slow/error/default phases 触发远端 flush，避免日志风暴。
4. metadata 不包含用户消息正文、prompt、文件内容。

**Suggested trace phases:**

```text
frontend.vitals.fcp
frontend.vitals.lcp
frontend.longtask
frontend.render.slow
frontend.render.chat.slow
frontend.render.threadrail.slow
frontend.render.timeline.slow
```

**Verification:**

```bash
cd frontend-app
npm test -- src/App.test.jsx
npm run lint
npm test
npm run build
```

**Acceptance:**

- App 级慢渲染原行为保留。
- 新增指标可以通过 mock `emitFrontendTraceEvent` 验证。
- build 通过。

**Rollback:**

- 删除新增 observer/profiler 包装，保留原 `main.jsx` App Profiler。

### Task 1.3：后端热点服务增加 sub-span/分段日志

**Files:**

- Modify: `internal/module/uistate/service.go`
- Modify: `internal/module/thread/history.go`
- Modify: `internal/module/dashboard/ui_page.go`

**Implementation requirements:**

1. `ui/sidebar/get` 已有 `get_prefs_ms/snapshot_ms/enrich_db_ms`，保持原字段不变。
2. 新增字段必须可帮助定位 rows/bytes/source，例如 `threads_count`、`bindings_count`、`messages_count`、`loader`。
3. 不记录消息正文、文件内容、prompt。

**Verification:**

```bash
./scripts/test_with_guard.sh internal/module/uistate/service.go
./scripts/test_with_guard.sh internal/module/thread/history.go
./scripts/test_with_guard.sh internal/module/dashboard/ui_page.go
./scripts/test_with_guard.sh ./internal/module/uistate ./internal/module/thread ./internal/module/dashboard -count=1
```

**Acceptance:**

- 编译和测试通过。
- 日志字段能支撑 `ui/sidebar/get`、`thread/messages`、dashboard loader 耗时定位。

## 6. Phase 2：首屏加载与全局重渲染

### Task 2.1：页面级 lazy import 回归锁定与 bundle 采样

**Files:**

- Read/Verify: `frontend-app/src/App.jsx`
- Modify only if regression is found: `frontend-app/src/App.jsx`
- Test: `frontend-app/src/App.test.jsx`

**Implementation requirements:**

1. 先确认当前 `frontend-app/src/App.jsx` 已通过 `lazyNamedPage` + `Suspense` 加载页面。
2. 不要把页面回退成静态 import。
3. 记录 `npm run build` 后页面 chunk 与最大 vendor/图表 chunk。
4. 如发现某个页面又被静态引入，只修复该页面并保留稳定 fallback。
5. 继续关注 Mermaid/KaTeX/Cytoscape 大 chunk，但不要为了体积引入新生产依赖。

**Current source shape:**

```jsx
const ChatPage = lazyNamedPage(() => import('./pages/chat/ChatPage.jsx'), 'ChatPage');
const FilesPage = lazyNamedPage(() => import('./pages/files/FilesPage.jsx'), 'FilesPage');
```

**Verification:**

```bash
cd frontend-app
npm test -- src/App.test.jsx
npm run build
du -sh dist
find dist/assets -maxdepth 1 -type f -print0 | xargs -0 du -h | sort -h | tail -n 20
```

**Acceptance:**

- 路由切换正常。
- build 通过。
- 页面 chunk 仍独立存在，例如 `ChatPage-*`、`FilesPage-*`、`ObservabilityPage-*`。
- 最大 chunk 若仍超过 500KB，报告是哪类依赖导致，不要强行复杂拆包。

**Rollback:**

- 如果本任务只做验证，无需回滚。
- 如果修复了 lazy 回归，回滚只限该页面 import，不要撤销已存在的 lazy 框架。

### Task 2.2：`AppShell` selector 回归锁定与 Chat 子树 selector 收窄

**Files:**

- Read/Verify: `frontend-app/src/App.jsx`
- Modify if needed: `frontend-app/src/pages/chat/ChatPage.jsx`
- Modify if needed: `frontend-app/src/pages/chat/components/ThreadRail.jsx`
- Modify if needed: `frontend-app/src/entities/client/model/useClientStore.js`
- Test: `frontend-app/src/App.test.jsx`、`frontend-app/src/pages/chat/ChatPage.test.jsx`、`frontend-app/src/entities/client/model/useClientStore.test.js`

**Implementation requirements:**

1. 先确认当前 `AppShell` 使用 `useClientStore(useShallow(selectAppShellStore))`。
2. 不要回退到 `const store = useClientStore()` 全量订阅。
3. 增加或保留测试，证明高频 timeline delta 不触发整个 App shell 高频重渲染。
4. 如继续优化，只收窄 Chat / ThreadRail / Timeline 的订阅面，不要一次性改完整 store 数据结构。
5. 高频 timeline、runtime stats、warnings 不应穿过整个 App shell。

**Verification:**

```bash
cd frontend-app
npm test -- src/App.test.jsx src/pages/chat/ChatPage.test.jsx src/entities/client/model/useClientStore.test.js
npm run lint
npm test
npm run build
```

**Acceptance:**

- App 测试通过。
- 发送/流式事件时 shell 不因 timeline delta 高频重渲染。
- Chat 子树的 selector 或 memo 改动有对应测试，不出现 stale closure 或路由不同步。

**Rollback:**

- 如果只是补测试/采样，无需回滚。
- 如果 Chat 子树 selector 改动导致回归，只回退该子树改动，不要回退 `AppShell` 的 shallow selector。

## 7. Phase 3：聊天高频路径

### Task 3.1：assistant delta flush 从 O(n) 降到接近 O(1)

**Files:**

- Modify: `frontend-app/src/entities/client/model/useClientStore.js`
- Test: `frontend-app/src/entities/client/model/useClientStore.test.js`

**Implementation requirements:**

1. 优先在 store 内维护 `threadId -> itemId -> index` 的索引。
2. 历史加载、mergeTimelineItems、snapshot apply、thread 切换时必须重建或校验索引。
3. flush 时优先用索引定位 item；索引缺失时允许单次 scan 并回填索引。
4. 不改变 timeline item 对外数组结构，避免一次性改 ChatPage。
5. 保留现有 assistant/thinking/optimistic/runtime item 语义。

**Required tests:**

- 1000 个 timeline item 中更新最后一个 assistant item，确认目标 item 更新且顺序不变。
- 索引缺失时 fallback scan 后能继续更新。
- 新 item 不存在时仍 append。
- thinking item 与 assistant item 都覆盖。

**Verification:**

```bash
cd frontend-app
npm test -- src/entities/client/model/useClientStore.test.js
npm run lint
npm test
npm run build
```

**Acceptance:**

- 流式输出行为不变。
- 大 timeline fixture 下 flush 测试通过。
- 没有把 timeline 数据结构大规模外泄到页面层。

**Rollback:**

- 删除索引结构，恢复原 `timeline.map` 更新。

### Task 3.2：ThreadRail 虚拟化或分段渲染

**Files:**

- Modify: `frontend-app/src/pages/chat/ChatPage.jsx`
- Test: `frontend-app/src/pages/chat/ChatPage.test.jsx`

**Implementation requirements:**

1. 不新增虚拟列表依赖，除非用户批准。
2. 优先实现轻量 fixed-height windowing 或分段渲染。
3. 保留归档切换、rename、pin、delete、active state。
4. hover 状态不应导致不可见线程卡片渲染。

**Verification:**

```bash
cd frontend-app
npm test -- src/pages/chat/ChatPage.test.jsx
npm run lint
npm test
npm run build
```

**Acceptance:**

- 500/1000 threads fixture 下可见卡片数量受控。
- 现有 thread actions 行为不回归。

### Task 3.3：大 diff/Markdown 渲染保护

**Files:**

- Modify: `frontend-app/src/pages/chat/ChatPage.jsx`
- Test: `frontend-app/src/pages/chat/ChatPage.test.jsx`

**Implementation requirements:**

1. 对超过阈值的 diff/log/code block 默认折叠或分页渲染。
2. 阈值必须是明确常量，例如 `STRUCTURED_OUTPUT_INLINE_LINE_LIMIT`。
3. 用户可以展开查看完整内容。
4. 不丢失复制/打开代码相关交互。

**Verification:**

```bash
cd frontend-app
npm test -- src/pages/chat/ChatPage.test.jsx
npm run lint
npm test
npm run build
```

**Acceptance:**

- 10k 行 diff 不一次性生成 10k DOM span。
- 小 diff 渲染保持原体验。

## 8. Phase 4：后端读路径

### Task 4.1：`ui/sidebar/get` binding/runtime config 优化

**Files:**

- Modify: `internal/module/uistate/module.go`
- Modify: `internal/module/uistate/service.go`
- Modify if needed: `internal/store/binding/store.go`
- Modify if needed: `sql/queries/thread_binding.sql`
- Test: `internal/module/uistate/*test.go`、`internal/store/binding/*test.go`

**Implementation requirements:**

1. 先确认当前 `enrichFromDB` 是否仍全量读取 binding。
2. 如果只需要当前 sidebar threads 的 binding，新增按 thread IDs 或 cwd 限定的读取方法。
3. 不允许回到 per-thread runtime config N+1。
4. batch runtime config 失败时必须记录错误和 rows，不要静默吞掉。
5. SQL 变更后必须运行 sqlc 验证。

**Verification:**

```bash
./scripts/test_with_guard.sh internal/module/uistate/module.go
./scripts/test_with_guard.sh internal/module/uistate/service.go
./scripts/test_with_guard.sh ./internal/module/uistate ./internal/store/binding -count=1
make sqlc-verify
```

**Acceptance:**

- `ui.sidebar.get.duration` 仍输出原字段。
- 增加 binding/runtime config 行数或耗时字段。
- 启动日志不出现 batch config fallback 风暴。

**Stop gate:**

- 如果需要改变 sidebar snapshot contract，先输出方案等待用户确认。

### Task 4.2：`thread/messages` 降低首屏历史读取压力

**Files:**

- Modify: `frontend-app/src/entities/client/model/useClientStore.js`
- Modify if needed: `internal/module/thread/history.go`
- Test: `frontend-app/src/entities/client/model/useClientStore.test.js`、`internal/module/thread/*test.go`

**Implementation requirements:**

1. 默认历史页大小从 300 调整到 100-150 之前，必须确认滚动加载 older messages 行为正常。
2. 若调整后端返回结构，必须保持旧字段兼容。
3. 增加 `messages_count`、`result_bytes`、`read_source_ms` 等观测字段优先于大改接口。

**Verification:**

```bash
cd frontend-app
npm test -- src/entities/client/model/useClientStore.test.js
npm run build
cd ..
./scripts/test_with_guard.sh ./internal/module/thread -count=1
```

**Acceptance:**

- 打开线程仍能显示最新消息。
- 向上滚动仍能加载 older messages。
- 首屏返回 messages 数量和 bytes 降低。

## 9. Phase 5：SQL、sharedFiles、Observability

### Task 5.1：SQL 搜索热点先 EXPLAIN，再决定索引

**Files:**

- Read: `sql/queries/system_log.sql`
- Read: `sql/queries/shared_file.sql`
- Read: `sql/queries/command_card.sql`
- Read: `sql/queries/prompt_template.sql`
- Modify only after evidence: `migrations/*.sql`、相关 query。

**Implementation requirements:**

1. 对每条高风险查询跑 `EXPLAIN (ANALYZE, BUFFERS)`。
2. 记录表行数、扫描方式、buffers、sort、耗时。
3. 若数据量不足以证明慢，先提交诊断报告，不加索引。
4. 若加索引，优先用幂等迁移；涉及 `CREATE INDEX CONCURRENTLY` 时遵守现有 migration split 规则。
5. 不要把所有 ILIKE 一次性改掉。

**Verification:**

```bash
make sqlc-verify
./scripts/test_with_guard.sh ./internal/store ./internal/module/dashboard -count=1
```

**Acceptance:**

- EXPLAIN 证明优化前后差异。
- migration 幂等。
- 查询语义不变或变更有明确说明。

### Task 5.2：sharedFiles metadata/detail 分离

**Files:**

- Modify: `internal/module/dashboard/rpc.go`
- Modify: `internal/module/dashboard/ui_page.go`
- Modify: `internal/store/sharedfile/store.go`
- Modify: `frontend-app/src/pages/files/FilesPage.jsx`
- Test: sharedfile/dashboard/frontend files tests。

**Implementation requirements:**

1. `dashboard/sharedFiles` 列表默认只返回 metadata，不返回大 content。
2. `readSharedFile` 继续按需读取详情。
3. 大文件详情增加 preview/range/open 策略前，先补 bytes 埋点。
4. 保持 finalOutputRefs 和 retention 展示不回归。

**Verification:**

```bash
./scripts/test_with_guard.sh ./internal/module/dashboard ./internal/store/sharedfile -count=1
cd frontend-app
npm test -- src/pages/files/FilesPage.test.jsx
npm run lint
npm test
npm run build
```

**Acceptance:**

- 列表响应 bytes 明显下降。
- 点击文件仍可加载详情。
- 大文件不在列表阶段跨桥传输。

### Task 5.3：Observability recent/list 读放大优化

**Files:**

- Modify: `internal/module/observability/rpc.go`
- Modify: `frontend-app/src/pages/observability/ObservabilityPage.jsx`
- Test: `internal/module/observability/rpc_test.go`、`frontend-app/src/pages/observability/ObservabilityPage.test.jsx`

**Implementation requirements:**

1. 保持默认 recent 行为可用。
2. 优先下推 status/component/method/thread/keyword 过滤，减少 raw limit 读放大。
3. metadata 大对象延迟格式化或只在展开时格式化。
4. 不改变 traceId 展开语义。

**Verification:**

```bash
./scripts/test_with_guard.sh ./internal/module/observability -count=1
cd frontend-app
npm test -- src/pages/observability/ObservabilityPage.test.jsx
npm run lint
npm test
npm run build
```

**Acceptance:**

- 展示 50 行时 raw query rows 明显减少，或有明确不可减少原因。
- 查询和展开 trace 的测试通过。

## 10. 必须保留的用户核心链路

每个阶段结束后至少抽查：

1. 应用启动进入 Chat。
2. 新建对话并发送首条消息。
3. 打开已有线程并加载历史消息。
4. 切换 Skills / Prompts / Workflows / Memory / Files / Observability / Settings。
5. 打开 shared file 详情。
6. Observability 查询 recent list 并展开 trace。

前端命令：

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Go 命令按改动面选择：

```bash
./scripts/test_with_guard.sh <changed-file.go>
./scripts/test_with_guard.sh <affected-package> -count=1
make sqlc-verify
```

## 11. 停止并回报的条件

遇到以下情况不要继续改代码：

1. 需要新增生产依赖。
2. 需要新增或修改数据库索引，但没有 EXPLAIN 证据。
3. 需要改变前后端 RPC contract。
4. 需要重写 `useClientStore.js` 大部分结构。
5. 需要迁移用户数据或删除历史数据。
6. 测试失败且无法证明是预存失败。
7. 工作区出现非本任务改动且影响当前修改。

回报格式：

```markdown
## 停止原因

## 已确认事实

## 相关文件

## 可选方案

| 方案 | 收益 | 风险 | 回滚 |
|---|---|---|---|

## 建议选择
```

## 12. 每个执行 Agent 的输出格式

```markdown
# 执行结果：<任务名>

## 1. 目标

## 2. 改动文件

| 文件 | 改动摘要 |
|---|---|

## 3. 关键实现

## 4. 验证结果

| 命令 | 结果 |
|---|---|

## 5. 性能指标变化

| 指标 | 优化前 | 优化后 | 采集方式 |
|---|---|---|---|

## 6. 风险与回滚

## 7. 是否建议进入提交/PR
```

## 13. 最终主执行 Agent 汇总格式

```markdown
# Super-Dolphin 性能优化执行汇总

## 1. 执行摘要

## 2. 已完成任务

| 任务 | 优先级 | 状态 | 关键收益 |
|---|---|---|---|

## 3. 指标对比

| 指标 | 优化前 | 优化后 | 说明 |
|---|---|---|---|

## 4. 验证命令

## 5. 未执行或延期任务

## 6. 残余风险

## 7. 建议下一步
```

## 14. 推荐首轮执行范围

首轮不要碰 SQL 索引、bootstrap contract 或大型 timeline 虚拟化。页面 lazy import 和 `AppShell` shallow selector 已在当前源码落地，首轮不要重复实现。推荐先执行这 3 个任务：

1. Task 1.1：Wails/RPC trace 增加 `result_bytes`。
2. Task 1.2：前端补充 Web Vitals / Long Task / 子组件 Profiler。
3. Task 3.1：assistant delta flush 从 O(n) 降到接近 O(1)。

推荐理由：

- 三者都直接对应当前仍未落地的 P1 或 P1 观测前置。
- 改动边界相对清晰。
- 能用现有测试和 build 验证。
- 能快速形成 before/after 指标，为后续 sidebar、SQL、sharedFiles 优化提供证据。
