# 全仓已识别风险清零执行计划

> 基线：`main@fd66df8b2a5c80862c655ac1e783d61b65f930d7`。本计划只处理 2026-07-13 全仓分片审查及主 agent 复核后仍成立的风险、纵深防御项和诊断债务；工作区现有 README 多语言修改不属于本计划，执行时必须保留。

## 目标

消除本轮审查确认的状态一致性、停机竞态、MCP 输入资源、生成物门禁、架构扫描、前端契约、provider 依赖、Cron 查询边界、开发入口和诊断债务风险，并把每类问题上移为可重复、fail-fast、无法静默绕过的仓库约束。

“清零”必须同时满足：

- 每个 finding 先有能稳定失败的 RED 测试，再有最小 GREEN 修复。
- 生产状态转换、资源边界和停止入口由实现层保证，不能只依赖调用方约定。
- 新增或修改的字段从生产类型、SQL/store、adapter、RPC 到前端消费端完整追踪，missing、stale、未知字段均阻断。
- 生成物和架构规则的权威检查进入 CI；本地 hook 只提供提前反馈，不是唯一防线。
- 本轮已报告的 Error、Warning、Information、Hint 全部修复，或以带 owner、原因和验证证据的明确 blocker 阻断完成。
- 没有全仓验证、LSP diagnostics、生成物漂移和 Git 状态证据，不得声明完成、可提交或可合并。

## 非目标与禁止项

- 不修改或回退 `README.md`、`README.zh-CN.md`、`README.de.md`、`README.es.md`、`README.ja.md`、`README.ko.md` 的既有用户改动。
- 不用默认值、吞错、重试掩盖、扩大豁免、降低 baseline、重录 snapshot 或缩小扫描范围制造假绿。
- 不把 SQL/LSP 方言误诊当产品缺陷；必须记录复现命令、语言服务器和收窄结果后再裁决。
- 不手改 `internal/store/sqlc/*.go`、代码地图、project map 或 embed 产物；必须修改真值源并运行生成器。
- 不把 `.env` 风险描述为权限提升；本计划仅移除隐式执行本地文件的入口，明确开发信任边界。

## 执行顺序与写集

| Wave | Lane | 责任域 | 优先级 | 依赖 |
|---|---|---|---|---|
| 1 | L01 | Cron 恢复原子性 | P1 | 无 |
| 1 | L02 | worker 停机与入队原子门 | P2 | 无 |
| 1 | L03 | MCP framed header 资源上限 | P2 | 无 |
| 2 | L04 | 生成物、未知 gate、worktree 扫描门禁 | P2/P3 | L01-L03 可并行完成 |
| 2 | L05 | 前端时间与 preview URL 契约 | P2/P3 | 无 |
| 2 | L06 | Provider approval fail-fast | P3 | 无 |
| 3 | L07 | Cron 分页字段链与容量边界 | P3 | L01 后合并，避免 Cron 写集冲突 |
| 3 | L08 | 开发入口、dead code、诊断与 build-tag 清理 | P3 | L03-L06 后执行 |

执行时每个 lane 使用独立 worktree 和 `codex/risk-zero-lXX-*` 分支。共享文件只有 L01/L07 的 Cron 契约与测试，必须串行集成；其他 lane 不允许越界编辑。任何新增写集必须先由主 agent 更新本计划。

## 全局证据要求

每个 lane 都必须保存以下证据：

- LSP 定位：`grep` 或 `structure`。
- LSP 理解：`inspect(definition|hover|type_definition)`。
- LSP 影响面：`xref(references|call_hierarchy)`。
- LSP 精读：`file(read_file)`。
- LSP 诊断：`file(diagnostics)`；所有 severity 都必须处理。
- RED：准确命令、预期失败原因和实际失败摘要。
- GREEN：同一测试恢复通过，且测试确实命中新实现。
- 写集、HEAD、diff、未触碰 dirty 文件和最终 Git 状态。

---

## L01：Cron 恢复状态原子收口

### 风险

`finalizeRecoveredFailure` 和 `finalizeRecoveredObserveLost` 在 run 状态 CAS 失败后仍调用 `MarkFailed`，可能让 run 保持 `submitting/submitted/running`，而 job 已释放、标记失败或安排重试，形成状态分裂和重复提交。

### 写集

- `internal/module/cron/scheduler_recovery.go`
- `internal/module/cron/contract.go`
- `internal/app/storeadapter/cron/adapter.go`
- `internal/store/cron/contract.go`
- `internal/store/cron/store.go`、`internal/store/cron/recovery_finalize.go`，恢复终态事务单独分文件以满足生产文件尺寸门禁
- `sql/queries/cron_job.sql`
- `internal/store/sqlc/*`，仅由 `make sqlc-generate` 生成
- `pkg/cronmetrics/*`，保存不反向依赖 platform 的恢复收尾计数源
- `internal/platform/metrics/cron.go`、`internal/platform/metrics/metrics_test.go`，将计数注册到 Prometheus `/metrics`
- `internal/module/cron/scheduler_recovery_finalize_test.go`，隔离恢复事务 fixture 并满足测试文件尺寸与方法数门禁
- `internal/archtest/backend_boundary_registry.go` 及对应治理测试，将 `pkg/cronmetrics` 注册为禁止反向依赖 `internal` 的公共 leaf 包
- 对应 Cron module、adapter、store 测试

### 最优修复

短期安全修复是 CAS 失败立即返回，不得继续 `MarkFailed`。最终方案是新增窄的恢复终态端口，例如 `FinalizeRecoveredRun`，在同一个数据库事务中：

1. 以 `run_id + expected_run_status` 条件更新 run 终态。
2. 以 `job_id + claim_token + run_id + expected_active_turn_id` 条件更新 job。
3. 任一条件不满足则整体回滚并返回可分类冲突，不允许半成功。
4. 冲突时显式重读 run/job；只有两者已处于同一合法终态才返回幂等成功，否则返回错误并保留 claim，不安排新 turn。
5. 新事务端口必须承接现有 `MarkFailedParams` 的全部业务语义：`failure_count`、retry/next-run、claim 清理、last run/turn/error，以及既有 run/turn fencing；不得因收敛事务而丢字段或弱化条件。

### RED/GREEN

- [ ] RED：`TestFinalizeRecoveredFailureCASFailureDoesNotMarkJobFailed`，CAS 返回错误时 `MarkFailed` 调用次数必须为 0。
- [ ] RED：`TestFinalizeRecoveredObserveLostCASFailureDoesNotMarkJobFailed`，覆盖 observe-lost 路径。
- [ ] RED：store 事务 fixture 让第二个 UPDATE 失败，断言 run UPDATE 回滚。
- [ ] RED：并发 fixture 让旧 turn 状态改变，断言恢复端不会释放 job 或创建重试。
- [ ] GREEN：实现事务端口及 adapter 映射；删除“warn 后继续”的分支。
- [ ] 上层防御：通过 `pkg/cronmetrics` 增加 `cron_recovery_finalize_conflict_total`、`cron_recovery_finalize_error_total`，由 `internal/platform/metrics` 注册到 Prometheus `/metrics`；日志必须带 `job_id/run_id/turn_id/expected_status`。
- [ ] 验证：`make sqlc-verify`；`./scripts/test_with_guard.sh ./internal/module/cron ./internal/app/storeadapter/cron ./internal/store/cron -count=1`。

### 完成门禁

- run 与 job 不可能在一次恢复操作后处于互相矛盾的状态。
- 任意 DB/CAS 错误都向上返回；不得只写 warning。
- 事务、冲突、幂等和重试禁止四类测试全部存在。

---

## L02：worker 停机与入队原子门

### 风险

hooks dispatch 与 RPC push worker 在获取 mutex 前检查 `stopCh`。`Enqueue` 越过检查后等待锁，`Stop` 关闭并排空 worker，随后 `Enqueue` 仍可把数据写入已退出队列，造成 hook/UI push 静默丢失。

### 写集

- `internal/platform/hooks/dispatch_worker.go`
- `internal/platform/hooks/dispatch_worker_test.go`
- `internal/platform/rpc/push_worker.go`
- `internal/platform/rpc/push_worker_test.go`

### 最优修复

- 每个 worker 增加由现有 mutex 保护的 `stopped bool`。
- `Enqueue` 的线性化区间必须同时包含：检查 `stopped`、queue mutation、accepted/enqueued 计数、coalesced/dropped/rejected 分类计数以及非阻塞 wake publication；不得在解锁后补加 `enqueuedTotal` 或发布 wake。
- `Stop` 在同一把锁内将 `stopped=true`，然后关闭 stop signal；不得持锁等待 worker 退出。Stop 获得该锁后，任何尚未线性化的 Enqueue 都只能进入 rejected-after-stop。
- RPC push 明确采用“先在 bounded shutdown context 内 drain 已接受通知，再 cancel push context”的语义；只有 drain 超时后才取消发送并把剩余项计入 dropped-on-shutdown。禁止先 `pushCancel` 再声称成功 drain。
- Stop 返回后入队必须明确拒绝并记录 rejected-after-stop；队列长度、accepted/enqueued 计数和 wake publication 均不得变化。
- 两个 worker 保留各自语义，不为少量重复强行建立跨包泛型队列；共享的是行为测试矩阵。

### RED/GREEN

- [ ] RED：使用 barrier 精确构造“Enqueue 已通过旧检查但未拿锁，Stop 已完成”的交错，断言当前实现丢事件。
- [ ] RED：构造“已 append 但尚未计数/wake，Stop 抢锁并返回”的交错，断言当前计数仍会在 Stop 后变化。
- [ ] RED：Stop 返回后连续 Enqueue，断言队列长度、accepted/enqueued 计数和 wake 均不再增加。
- [ ] RED：RPC push 在 Stop 前已接受的通知必须在 grace 内完成发送；超时路径必须产生 dropped-on-shutdown 而不是成功计数。
- [ ] GREEN：实现完整线性化区间，以及 push 的 drain-before-cancel 关闭协议。
- [ ] 上层防御：为 rejected-after-stop、drain-timeout、dropped/coalesced 分别计数，不能混成成功入队。
- [ ] 验证：`./scripts/test_with_guard.sh ./internal/platform/hooks ./internal/platform/rpc -count=1`；`go test -race ./internal/platform/hooks ./internal/platform/rpc -count=10`。

---

## L03：MCP framed header 有界读取

### 风险

framed stdio body 有 1 MiB 上限，但 header 使用 `ReadString('\n')`，超长无换行输入会先无界扩容，绕过 body 上限并耗尽 sidecar 内存。

### 写集

- `internal/mcpserver/common/stdio.go`
- `internal/mcpserver/common/stdio_test.go`
- 如常量需公共化，仅修改同包 contract/测试

### 最优修复

- 增加独立常量：单行 header 上限、累计 header 上限、最大 header 行数；不得复用 body 上限掩盖协议边界。明确换行符是否计入单行/累计上限。
- 使用 `bufio.NewReaderSize` 让 reader capacity 与 `MaxStdioHeaderLineBytes` 一致，再使用 `ReadSlice('\n')`。收到 `bufio.ErrBufferFull` 立即返回 header-too-large，不继续拼接；不得依赖默认 4096 字节缓冲形成隐式协议上限。
- 累计每行字节数并限制行数；在解析 Content-Length 和分配 body 前完成检查。
- 保持原有 body 大小限制、重复 Content-Length 拒绝和 JSON 校验。

### RED/GREEN

- [ ] RED：超长且无换行 header 必须在固定上限内返回错误。
- [ ] RED：多行累计超限和 header 行数超限必须失败。
- [ ] RED：在明确“是否包含换行符”的规则下，边界值恰好允许，下一字节拒绝；错误类型稳定可断言。
- [ ] GREEN：有界 reader helper，不在调用点复制限制逻辑。
- [ ] 上层防御：加入 framed 输入 fuzz corpus，断言无 panic、错误可分类；使用 counting reader 证明超限输入在预算内停止读取，使用 benchmark/`AllocsPerRun` 约束分配，不在 fuzz 中声称精确内存上限。
- [ ] 验证：`./scripts/test_with_guard.sh ./internal/mcpserver/common -count=1`；相关 `cmd/mcp-lsp`、`cmd/mcp-orch` transport 测试。

---

## L04：生成物、未知 gate 与 worktree 扫描门禁

### 风险

- pre-push 可将 project-map/codemap 漂移降级为 warning；CI 已硬跑 `codemap-check`，真实缺口是未硬跑 `project-map-check`。
- `executeGatePlan` 静默忽略没有 runner 的 required gate，新增路由遗漏时可能假绿。
- `DefaultSkipDirs` 未排除 `.worktrees/.workspace` 等非当前 checkout，扫描输入和耗时受其他工作树影响。

### 写集

- `.github/workflows/ci.yml`
- `.githooks/pre-push`
- `.githooks/README.md`
- `scripts/ai_maintenance/main.go`
- `scripts/ai_maintenance/main_test.go`
- `internal/archtest/guardlib.go`
- `internal/archtest/backend_boundary_evaluator.go`
- `internal/archtest/*_test.go`

### 最优修复

- 保留已有 CI `make codemap-check`，新增硬执行 `make project-map-check`；远端检查是权威，不能依赖本地 hook。
- pre-push 可保留提示性快速体验，但不得把 CI 的结果软化；若本地执行了同一 gate，失败也应阻断。
- `executeGatePlan` 对 required gate 缺 runner 直接返回错误，错误包含 gate name。
- 架构扫描优先使用明确源码根 allowlist；仍需递归时，统一跳过 `.worktrees`、`.workspace`、`.build-cache` 及仓库约定生成目录。

### RED/GREEN

- [ ] RED：CI 契约测试证明缺少任一 map check 时失败。
- [ ] RED：required gate 无 runner 时 `executeGatePlan` 必须失败。
- [ ] RED：fixture 根内放置 `.worktrees/foreign/internal/bad.go`，断言 collector 不读取该文件。
- [ ] GREEN：更新 CI、runner fail-fast 和统一 skip/allowlist。
- [ ] 上层防御：主分支保护必须把 CI map-check job 设为 required；未配置前发布状态保持 BLOCKED。
- [ ] 验证：`go test ./scripts/ai_maintenance ./internal/archtest -count=1`；`make codemap-check`；`make project-map-check`；`make guard`。

---

## L05：前端时间与 preview URL 契约

### 风险

- `parseRequiredTimestamp` 接受不存在的日期并计算为另一天。
- preview URL 当前由 Go 安全生成，但前端 bridge 只验证非空；未来契约漂移会让 renderer 加载非本地 URL。

### 写集

- `frontend-app/src/entities/client/model/contractStoreModel.js`
- `frontend-app/src/entities/client/model/runtimeResults.test.js`
- `frontend-app/src/shared/api/wails/wailsBridgeNativeFiles.js`
- 对应 native file bridge/Workflow preview 测试
- `frontend-app/src/pages/workflows/components/WorkflowFinalOutputPanel.jsx`
- `frontend-app/src/pages/workflows/components/WorkflowFinalOutputPanel.test.jsx` 或现有同职责测试
- 如 Go 响应类型改变：`internal/ui/wails/sharedfile_open.go` 及同包测试

### 最优修复

- 时间：新增纯函数 `daysInMonth(year, month)`，在 epoch 计算前验证真实月日；保留现有严格 UTC 格式和手写 epoch 算法，避免 JavaScript `Date` 自动归一化。
- URL：以 Go `sharedFilePreviewResult` 为生产真值源。bridge 对 `url/path/contentType/sizeBytes` 四字段执行 required、类型、范围和 exact/unknown-key 校验；前端用 `URL` 解析并只接受 Go 实际产生的 loopback HTTP host、`/shared-file-preview` 路径和唯一非空 `id`，同时覆盖 IPv4、IPv6 和实际绑定端口。
- 不引入远端 URL 兼容兜底，不把非法值转为空 preview。

### 字段守卫链

```text
Go sharedFilePreviewResult
  -> Wails JSON bridge
  -> nativeSharedFilePreviewResponse
  -> previewSharedOutputFile
  -> WorkflowFinalOutputPanel 只消费 url/contentType
```

- [ ] 动态枚举 Go 响应字段或通过生成/类型契约得到 producer set；消费 registry 必须校验 missing 和 stale。
- [ ] one-field-at-a-time/roundtrip 测试证明 `url/path/contentType/sizeBytes` 都在 bridge 被 required/exact 校验。
- [ ] 消费 registry 明确 `url/contentType` 由最终 UI 使用；`path/sizeBytes` 以 `Field | Direction | Reason | Evidence/Owner` 登记为 bridge 终止校验，除非产品明确让 UI 使用它们。禁止制造无意义 UI 消费以凑覆盖。
- [ ] 临时删除 URL 校验分支，记录 frontend contract guard 的 fail-first 结果，再恢复。

### RED/GREEN

- [ ] RED：`2026-02-30`、`2025-02-29`、`2026-04-31` 均失败，`2024-02-29` 成功。
- [ ] RED：远端 HTTPS、非 loopback HTTP、错误路径、空/重复 id、userinfo 欺骗 URL 均失败。
- [ ] GREEN：实现严格日期与 preview URL validator。
- [ ] 验证：`cd frontend-app && npm run lint && npm test && npm run build`；Go bridge diagnostics/测试。

---

## L06：Provider approval 依赖 fail-fast

### 风险

生产 Fx 当前提供 ApprovalManager，但公开 factory/session 构造允许 nil；收到审批请求时静默返回，provider turn 可能无限等待。

### 写集

- `internal/provider/codexapp/driver.go`
- `internal/provider/codexapp/session.go`
- `internal/provider/codexapp/session_approval.go`
- start/resume/factory/Fx 相关测试

### 最优修复

- `provideDriverFactory` 首先拒绝 nil ApprovalManager，阻止生产 Fx 构图继续。
- `StartSession`、`ResumeSession` 在任何 request prepare、option resolve、ServerPool acquire 或 transport dial 之前调用同一个 `requireApprovalManager`。
- `newSessionWithOptions` 再做纵深校验；若 options 已携带 pool slot，则在建 transport 前拒绝并调用 `releaseSessionPoolSlot(cfg)`，避免引用计数泄漏。
- `handleApprovalRequest` 不再把 nil 视为合法状态；若运行中状态损坏，必须终止当前 turn/session 并上报明确 provider error，不能仅日志后返回。
- Fx 构图测试证明生产装配始终提供非 nil manager。

### RED/GREEN

- [ ] RED：Fx、start、resume 缺 manager 均返回确定错误，且断言不 acquire pool、不 dial transport、不启动 runtime reader/turn。
- [ ] RED：运行中 manager 异常时审批请求导致 turn 终态失败，不得挂起。
- [ ] GREEN：Fx 与 start/resume 前置 fail-fast，constructor 纵深校验并释放预占 slot，删除 silent return。
- [ ] 上层防御：记录 approval-request received/responded/failed，使用 request id 对账；未响应请求必须在 deadline 内告警并收口。
- [ ] 验证：`./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1`；provider contract tests。

---

## L07：Cron 列表分页与字段链闭环

### 风险

`ListCronJobs` 从 SQL 到 RPC 全量物化。当前尚未证明生产规模已触发事故，但无限增长会形成数据库、Go DTO 和前端响应的共同容量风险。

### 产品边界裁决

当前前端只有 `backendApiFactoryOps.js` 和 API 测试调用 `cronjob/list`，没有生产页面/controller/store 消费者。本 lane 固定采用 **API-only 分页**，不授权新增 Cron UI、pager 或 store。若未来要新增 UI，必须另立产品计划并重新发现消费链，不能在本风险修复中顺带扩产品面。

### 写集

- `sql/queries/cron_job.sql`
- `internal/store/cron/contract.go`
- `internal/store/cron/store.go`
- `internal/store/cron/store_test.go`
- `internal/store/cron/list_page.go`、`internal/store/cron/list_page_test.go`，隔离分页实现与 fixture 以满足生产/测试文件尺寸门禁
- `internal/module/cron/rpc_list_page_test.go`，验证 `cursor` wire 必填与分页响应映射
- `internal/archtest/cron_list_field_guard_test.go`，动态锁定 Go/sqlc DTO 与 mapper 字段链
- `internal/module/cron/contract.go`
- `internal/module/cron/service.go`
- `internal/module/cron/rpc.go`
- 对应 module/service/RPC 测试
- `internal/app/storeadapter/cron/adapter.go`
- `internal/app/storeadapter/cron/adapter_test.go`
- `internal/store/sqlc/*`，仅由生成器更新
- `frontend-app/src/shared/api/backend/backendApiFactoryOps.js`
- `frontend-app/src/shared/api/backend/backendApiPayloadWorkflow.js`
- `frontend-app/src/shared/api/backendResponseValidators.js`
- `frontend-app/src/shared/api/backendResponseValidators.test.js`
- `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- `frontend-app/src/shared/api/backendApi.test.js`
- 新增或扩展的 Cron page DTO 字段 registry/guard 测试
- `frontend-app/src/shared/api/backendCronListContract.test.js`，逐字段验证前端请求/响应契约

### 最优修复

- 使用 keyset cursor，不用 offset。SQL 固定为：

```sql
WHERE created_at < :cursor_created_at
   OR (created_at = :cursor_created_at AND id < :cursor_id)
ORDER BY created_at DESC, id DESC
LIMIT :limit_plus_one
```

- 首页使用空 cursor 并跳过 predicate；查询 `limit+1` 条，只返回前 `limit` 条。
- `HasMore` 由第 `limit+1` 条是否存在决定；`NextCursor` 从最后一条**已返回**记录编码。末页必须返回 `has_more=false` 和必填空字符串 `next_cursor`，不得省略字段或生成指向自身的 cursor。
- wire 请求字段固定为 `limit`、`cursor`；wire 响应字段固定为 `jobs`、`next_cursor`、`has_more`，前端不得接受 camelCase/旧字段别名。Go 内部字段使用 `Limit/Cursor/Jobs/NextCursor/HasMore` 并显式 mapper。
- `limit` 在 RPC 与前端 `listCronJobs(params)` 都必填，合法范围 `1..MaxCronListLimit`；不得使用隐式默认值。超限直接 invalid argument，store 不再接受无界调用。
- `cursor` 为必填字符串：空字符串唯一表示首页；非空值必须是有版本的不可歧义编码，固定携带数据库原始 `created_at` 精度、UTC 语义和 `id`。解码失败立即报错，不得回退第一页。

### 字段守卫链

```text
frontend listCronJobs API request
  -> cronListParams
  -> Service ListJobs page DTO
  -> App storeadapter page DTO
  -> Store page DTO
  -> sqlc query params/rows
  -> cronListResponse
  -> frontend response validator/API caller
```

- [ ] 每个边界分别定义 producer：前端请求 contract、Go `cronListParams`、module page DTO、adapter page DTO、store page DTO、sqlc params/rows、Go `cronListResponse`、前端响应 contract。禁止把所有层笼统称为一个 producer。
- [ ] Go producer 字段通过反射/AST 动态枚举；前端请求/响应字段通过类型/schema/AST 动态枚举。消费 registry 检查 missing/stale，并用 mapper AST 或 one-field-at-a-time roundtrip 证明实现覆盖。
- [ ] 为 `limit/cursor/jobs/next_cursor/has_more` 与 Go 内部字段建立逐方向 mapper coverage；任何一层临时删除映射都必须令 guard 精确报出字段与方向。
- [ ] guard 文件和准确 fail-first 命令必须在 RED 证据中记录；通用 `frontend-contract-store-guard.mjs` 不能自动证明 Cron 字段链，必须新增专用 contract/mapper guard 或等价动态测试。

### RED/GREEN

- [ ] RED：超过一页的数据不能一次返回全部；相同 created_at 的记录以 id 稳定分界，多页遍历无重复无遗漏。
- [ ] RED：页间插入更“新”的记录时，该记录不进入旧 cursor 的后续页、重新从首页加载后可见；页间删除只允许删除项缺失，不得导致其他项重复。`created_at` 必须保持不可变排序键。
- [ ] RED：验证 `limit+1`、`HasMore/NextCursor` 来源和末页 `has_more=false,next_cursor=""`；不得只测行数。
- [ ] RED：limit=0、负数、超上限和非法 cursor 全部 fail-fast。
- [ ] GREEN：SQL/store/adapter/service/RPC/前端 API contract 全链分页，不新增 UI。
- [ ] 验证：`make sqlc-verify`；`./scripts/test_with_guard.sh ./internal/store/cron ./internal/app/storeadapter/cron ./internal/module/cron -count=1`；`cd frontend-app && npm run lint && npm test && npm run build`。

---

## L08：开发入口、dead code、diagnostics 与 build-tag 清理

### 范围

本 lane 清理所有不应占用高优先级 lane、但仍不得遗留的风险和 diagnostics：

- `run-new-ui-desktop.sh` 隐式 source `.env`。
- `internal/module/mcp_server/http_tools.go` 的无引用 `httpErrorBodySuffix`。
- `internal/module/mcp_server/service.go` 的无引用 `allowedNPXServerArgs`。
- `internal/platform/toolbridge/stdio_mcp_client.go` 的无引用 `newStdioMCPClient` 与过期注释。
- `internal/module/appupdate/service.go` 的 `stringscut` hint。
- `internal/module/observability/rpc.go` 的 `minmax` hint。
- `internal/ui/wails/code_scope.go`、`sharedfile_open.go` 的 `stringsseq` hint。
- `cmd/super-dolphin-updater/install.go` 的 unusedfunc/hint。
- `internal/devtools/sqlitereleasegate/gates.go` 的 writestring warning。
- `internal/devtools/capcontract/scanner.go` 未按 build tags 选择文件。
- `internal/module/workflowtemplate/rpc.go` 的 `omitempty` diagnostics 不稳定问题。

当前 LSP 复核稳定得到 29 条 diagnostics，执行前必须重新读取并以新鲜结果为准。其中 updater 明确包括 9 个 unused functions：`install`、`mountDMG`、`detachDMG`、`expectedTeamID`、`appTeamID`、`signingDetails`、`replaceTargetApp`、`copyApp`、`quarantineAttributeRemains`，以及 `stringsseq`、`stringscutprefix` 两项 hint；capcontract 还包括 `ParseDir`、两处 `ast.Package` deprecated 和一处 `stringscut`。

### 写集

- `run-new-ui-desktop.sh`
- `internal/app/new_ui_scripts_test.go`
- `internal/module/mcp_server/http_tools.go` 及同包测试
- `internal/module/mcp_server/service.go` 及同包测试，仅在重新确认 `allowedNPXServerArgs` 无引用时修改
- `internal/platform/toolbridge/stdio_mcp_client.go` 及同包测试
- `internal/module/appupdate/service.go` 及同包测试
- `internal/module/observability/rpc.go` 及同包测试
- `internal/ui/wails/code_scope.go`、`internal/ui/wails/sharedfile_open.go` 及同包测试
- `cmd/super-dolphin-updater/install.go` 及同包测试
- `internal/devtools/sqlitereleasegate/gates.go` 及同包测试
- `internal/devtools/capcontract/scanner.go`、manifest/schema 类型及测试
- `scripts/capcontract/main.go`、`scripts/capcontract/main_test.go`
- `docs/doc/codemap/capability-contract/capability_manifest.json`，只允许由 `make capcontract-refresh` 生成
- `internal/module/workflowtemplate/rpc.go` 及同包 RPC/序列化测试
- `sql/queries/db_query.sql`、`internal/store/sqlc/db_query.sql.go`、`internal/store/sqlc/querier.go`，仅消除 `PlaceholderDBQuery` 无类型 `NULL` 生成的 `interface{}` hint，保持零行语义
- `internal/store/dbquery/store.go`、`internal/store/dbquery/store_test.go`，同步收紧 Placeholder 消费类型并锁定零行契约
- `.github/workflows/ci.yml`、`internal/archtest/ratchet_test.go`，Linux 主 job 与 macOS/Windows smoke 共同执行同一 manifest 字节检查

不允许修改 `run-new-ui-desktop.ps1`：Windows 已有不执行脚本表达式的 `Import-DotEnvFile`，本风险只针对 Bash `source`。不修改任何 dirty README；开发入口行为通过 Bash `--help`/错误信息和 `internal/app/new_ui_scripts_test.go` 固定。

### 最优修复

- 开发入口删除自动 `. "$PROJECT_DIR/.env"`；要求调用方显式 export。若检测到 `.env`，Bash 入口直接报错并指向自身 `--help` 中的迁移说明，避免静默忽略；不得修改 dirty README。若必须保留文件加载，只能使用不含 eval/source 的严格 parser，并对未知键、命令替换、控制字符 fail-fast。
- 确认无调用的私有函数直接删除，不为保留注释制造新抽象。
- 标准库 modernization 使用等价 API，补充边界测试后修改。
- capcontract scanner 不得按当前宿主 GOOS/GOARCH 生成 manifest。固定 canonical target matrix 为 `darwin/amd64`、`darwin/arm64`、`linux/amd64`、`windows/amd64`，分别用 `go/packages.Load`/类型系统加载后按 `package + symbol signature` 确定性合并，并记录 target provenance；所有 map/slice 排序后再序列化。任何 target 加载失败都阻断生成。
- manifest 的真值 owner 是 `scripts/capcontract`，派生物是 `docs/doc/codemap/capability-contract/capability_manifest.json`；schema/manifest 新增 target provenance 时必须有 missing/stale/roundtrip 字段守卫，不得手改 JSON。
- workflowtemplate diagnostics 必须先通过 `open_file -> diagnostics` 和单文件收窄稳定复现；若 `omitempty` 对值 struct 确认无效，删除误导 tag，或在业务确需“缺失与零值不同”时改指针并建立字段 roundtrip。不能凭一次不稳定输出盲改。

### RED/GREEN

- [ ] RED：启动脚本 fixture 中 `.env` 含命令替换时不得执行副作用。
- [ ] RED：capcontract build-tag fixture 对四个 canonical targets 分别选择正确文件，合并后无重复；在 macOS/Linux/Windows runner 生成的 JSON 必须字节一致。
- [ ] RED：workflowtemplate 字段缺失/零值语义测试先固定产品行为。
- [ ] GREEN：删除 dead code，修复全部稳定 diagnostics。
- [ ] 运行所有目标文件 diagnostics；Error、Warning、Information、Hint 数量必须为 0。
- [ ] 验证：`go test ./internal/app -run 'Test.*NewUI.*Script' -count=1`；所有 L08 目标包定向测试；`make capcontract-refresh && make capcontract-check`；三平台 manifest 字节一致性 job；`python3 scripts/validate_super_agent_skills.py`；`git diff --check`。

---

## 集成与发布门禁

### 每个 lane 合并前

- [ ] 主 agent 按源码、测试和 LSP 重新裁决 finding，不按 worker 自报状态合并。
- [ ] 检查写集未越界，用户 README 改动未混入。
- [ ] RED 证据确实因目标缺陷失败，GREEN 使用同一命令通过。
- [ ] `file(diagnostics)` 对所有修改文件无任何 severity。
- [ ] `git diff --check` 通过，生成文件来自规范生成器。

### 串行集成顺序

1. L01 Cron 原子恢复。
2. L02 worker stop gate。
3. L03 MCP header cap。
4. L04 远端/本地门禁。
5. L05 前端契约。
6. L06 provider approval。
7. L07 Cron 分页字段链。
8. L08 清理与 diagnostics。

### 最终验证

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
make sqlc-verify
make build-plain
./scripts/test_with_guard.sh ./internal/module/cron ./internal/store/cron ./internal/app/storeadapter/cron ./internal/platform/hooks ./internal/platform/rpc ./internal/mcpserver/common ./internal/provider/codexapp ./internal/archtest ./scripts/ai_maintenance ./internal/module/mcp_server ./internal/platform/toolbridge ./internal/module/appupdate ./internal/module/observability ./internal/module/workflowtemplate ./internal/ui/wails ./internal/devtools/capcontract ./internal/devtools/sqlitereleasegate ./cmd/super-dolphin-updater ./internal/app ./scripts/capcontract -count=1
go test -race ./internal/platform/hooks ./internal/platform/rpc -count=10
make test
python3 scripts/validate_super_agent_skills.py
git diff --check
cd frontend-app
npm run lint
npm test
npm run build
```

### 清零验收表

- [ ] Cron run/job 恢复终态为原子事务，无半成功路径。
- [ ] Cron 原子端口保留 failure count、retry/next-run、claim 清理和全部 run/turn fence 语义。
- [ ] Stop 返回后任何 worker 都不能再次入队、增加 accepted/enqueued 计数或发布 wake；push 已接受项按 drain-before-cancel 语义收口。
- [ ] MCP header/body/JSON 均有独立、可测试的资源上限，reader size 与 header 上限显式一致。
- [ ] CI 硬阻断 codemap/project-map 漂移，required gate 无 runner 必失败。
- [ ] 架构扫描输入不受其他 worktree、缓存或生成目录影响。
- [ ] 非法日期和非法 preview URL 在第一消费边界 fail-fast。
- [ ] ApprovalManager 缺失在任何 pool acquire/transport dial 前阻断启动或恢复，运行中异常能终止 turn。
- [ ] Cron list 按 API-only 边界完成 keyset 分页，无未授权 UI 扩面；字段守卫 missing/stale/roundtrip/fail-first 完整。
- [ ] 开发脚本不隐式执行 `.env`。
- [ ] capability manifest 在 canonical target matrix 下确定性生成，macOS/Linux/Windows 输出字节一致，生成 owner/派生物单向明确。
- [ ] 所有已报告 dead code、29 条 diagnostics、build-tag 和 omitempty blocker 均清零；新鲜 diagnostics 若数量变化，以实际全量结果为准且最终必须为 0。
- [ ] 主分支保护已将远端 map-check job 设为 required；未配置则发布状态为 BLOCKED。
- [ ] 工作区、提交、远端 SHA 和生成物状态均有可复查证据。

## 完成定义

只有当上述验收项全部勾选、最终验证全部通过、无未解释 diagnostics/blocker、用户既有 dirty 文件保持原样，并且主分支保护实际启用后，才能声明“本轮已识别风险全部清零”。局部测试绿、空日志、worker 自报完成或本地 hook 通过都不能替代该定义。
