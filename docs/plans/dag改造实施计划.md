# Super-Dolphin DAG 改造实施计划

> 配套文档：`docs/plans/dag改造蓝图v2.md`（决策与设计）
> 本文是执行级清单：每行一个可独立提交的任务，含触动文件、验收、依赖、size、可并行性
> Size 直觉：S = 半天以内 / M = 1-2 天 / L = 3 天以上（仅相对量级，非时间承诺）
> 修订历史：2026-05-10 初稿 / 4 处小补 / 2-pass 审查 / S2.4 推迟 F / **骨架阶段封板**；**2026-05-10 T 阶段快车道完成**：T0.4/5/6/8 + T1.1 + T2.1+T2.2 + T4.1+T4.4 全部 done（6 commit）；T 阶段二次审查 4 轻度 findings 全推迟到 T0.1 / F4.1 / F 阶段
> 2026-05-10/11 T 阶段第二批 done：T1.2-mid（StartDAG 真业务 + RunStore CRUD）+ T3.1/T3.2（task_get_run / task_list_runs）+ 路线 N 幂等语义切换 + migration 0079/0080；T1.2-full → F6.5
> 2026-05-11 套餐 B/C/A+ 落地同步：套餐 B（pre-T0.2 收尾 + stub 接口扩张补齐 + sharedfilegitignore race 根除 + memoryCoordinator getter 化）已记 §10 ledger；套餐 C（T0.8 doc-sync Check 4 真验证 + 状态机 retrying→cancelled 合法转移 + migration 0081 task_dags.trigger CHECK + stubDashboardOrchestration `var _` 编译期断言 + ADR 0001 §2.10 DB 不变量基线 + 会话习惯 §10.60 MCP 命名约定）落地 5 commit；套餐 A+/D（MCP enum 校验：handler `requireEnum` + 包级 `var` 单源 + DB CHECK 三层互锁 + `runtime.UpdateRuntime` provider silent Warn → fail-fast + migration 0082 task_dag_runs.trigger_source CHECK + ADR-003 + 会话习惯 §10.61）落地 5 commit + 1 idempotent 修复（31f2ad75，0082 重跑 42710 防御）
> 2026-05-11 F1.5/F4.1/F6.3 并行 worktree 落地 + DAG dogfood 审查：3 worker agent 中并行落地 13 commit + 3 merge commit + 1 follow-up fix `970cb5aa`（从聚合 Store 拆出 NodeSpawnRecorderStore 过 archtest TestInterfaceIsolationBudgets）。F 阶段 done 计数：9 → **12**。审查用 review-dag-2026-05-11-merged-commits DAG 本身跳走，剩下 1 节点 n6 stuck pending 揭示：mcp-orch 服务在跑旧二进制，**需重启**才能走上 F6.3 promote / F6.2 finalize。sqlc 手维 5 文件（4 W1 + 1 W3 + 1 W2 db_accessor）集中标 marker 在 `internal/sidecar/orch/sqlc.yaml` 顶部。
> 2026-05-12 F1.2/F2.2/F4.2/F7.1 第二轮并行 worktree 落地 + DAG dogfood 自动 promote 闭环验证：F1.2 AgentExecutor inputs 注入（`3317b00f` + merge `877193cf`）+ F2.2 AutomationExecutor inputs/outputs（`3d8526ab` + merge `4dd5307a`）+ F4.2 ApplyOps update_node 真业务（`7611c268` `65c977d8` `848f1188` + merge `d63a623d`）+ F7.1 AI 设计师 prompt 中文版 seed 0084 + archtest 守卫（`49fd0143` `52da9d36` + merge `94502cec`）+ 冲突修 `6f333dd1`（测试桩去重 + 双端口 TODO）。F 阶段 done 计数：12 → **16**。dogfood 第一轮 review-dag-74 n6 stuck pending 揭示旧二进制 → mcp-orch 重启后 verify-dag-75 验证 F6.3 promote 单点闭环 → 第二轮 review-dag-76 6 节点全 done + run.status=succeeded（F6.2 + F6.3 真生效）。
> 2026-05-12 round-3 wiring batch 落地 + dogfood 第三/四轮：5 reviewer（代码质量 + 测试覆盖 + ADR + SQL + F1.5/F4.1/F6.3 端到端）揭露 7 项 P0：dispatcher wiring 红线（10 个 executor task 是 dead code）+ SQL fence drift + AppendTaskDagRunEvent jsonb object-merge 陷阱 + make sqlc regen 红线 + F4.5 add_node depends_on→done 不变量未实装 + 端口分裂三件套 + docs sync 未做。并行启动 5 worker batch 落地（W1 dispatcher wiring + W2 端口收敛 + size_cap enforce + events ring trim + W3 SQL/migration 工艺 + F4.5 不变量 + Tarjan SCC + 0083 CONCURRENTLY 拆事务 + W4 测试加固（134 t.Parallel + NodePatch DisallowUnknownFields + 嵌套 banned key 深扫 + testcontainer 占位 + DAGEvent helpers）+ W5 文档同步 + ADR-008/009/011v1 升 Accepted + ADR-012 立卡）。合并后 mcp-orch 启动 fail-fast暴露 3 处 fx wiring 漏：ProvideAgentExecutor `898ee595` / NodeSpawnRecorderStore + AutomationCommandRunner adapter `6e32b39e` / sharedfile.Reader narrow port `1bb47955`。dogfood：verify-round3-wiring-2026-05-12（task_dispatch_node enqueued wakeup_id=1）+ verify-w6-typed-config-2026-05-12（apply_ops 写 typed cfg + W4 嵌套 banned key `agent_key` 真拒 + run.status=succeeded）。F 阶段 done 计数：16 → **17**（+ F4.5）。完整 wiring 链：dispatcher → NodeExecutorRouter → RunContext（PrevResults + SharedFileReader + SharedFileWriter）三端口预填 → AgentExecutor.Execute 真调。archtest 守卫：TestDispatcherWiringGuard + sqlc_bypass_guard_test + sharedfile_adapter.go。
> 2026-05-12 第四轮 worktree 并行 + 互审落地（F1.3 / F7.2）：F1.3 AgentExecutor outputs 写 sharedfile / node.result（`ae35c0a2` + fix `f985e83d` + merge `b0fcf77b`，抽公共 `validateOutputsForbiddenKeys` 让 automation 与 agent 共用边界校验；互审抓出 sharedfile IO 失败错误分类 blocker：`FailureClassValidation` → `FailureClassInfrastructure` 避免误导 F1.4 dispatcher；+ 3 个缺失测试用例补齐）+ F7.2 AI 设计师 prompt 英文版 seed 0085 + archtest EN 守卫（`4b49c5fd` + fix `fe4d00f2` + merge `da01120a`，互审补英文 routing tags 15 个）。**互审机制**：2 个 codex agent 物理交叉在对方 worktree 审，避开「自己审自己」盲点。F 阶段 done 计数：17 → **19**。同期顺手做 F7.3 编号让位（migration 0085 已被 F7.2 占用，F7.3 让位到 0086/0087）：`266e664b`。账本同步：`6bc0e230`。
> 2026-05-12 第五轮 codex 实装 + 2 claude 默认模型互审（F14.1）：F14.1 list_models 改读 provider registry（PT-1 推迟项补位）。codex 1 轮 turn 跑完 3 commit：`9a395e5e`（modelregistry 包骨架 + yaml 配置加载 + reload 即时反映）+ `2cbf389f`（HandleListModels 改 Registry DI 注入 + 单测）+ `737fcc7b`（fx wiring Registry provider）。2 个 claude 默认模型互审两视角：架构 reviewer 抓出 3 真 blocker（DefaultPath 相对路径 systemd/docker 部署炸 + 每次 Reload 性能 + 静默吞 yaml error），实装 reviewer 都标 nit；主 agent 仲裁：路径 + 错误日志化是真 blocker，性能优化降级。codex 修复 `90195458`：加 env `SUPER_DOLPHIN_MODEL_REGISTRY` 覆盖 + fx provider 层 catch 失败回落 `StaticRegistry`（不让 mcp-orch fail-fast）+ Reload error log.Warn 保留旧 providers。merge `0b3078f4`。F 阶段 done 计数：19 → **20**。账本同步：`9d79e2dc`。
> 2026-05-12 第六轮 DAG dogfood 揭示 F1.1 真 bug + 修复（F1.1-followup）：用 task_create_dag + task_dag_apply_ops + task_dispatch_node + task_start_dag 走完整端到端验证（DAG `dag-validation-2026-05-12`），节点 result=`{kind:exhausted_retries, reason:transient: launch agent: agent id is required}` 揭露 `buildLaunchRequestFromAgentConfig`（executor_agent.go:360-371）只填 4 个字段（AgentKey/Language/Prompt/AgentType），漏 **AgentID** → `service_launcher_bridge.go:390 submissionAgentID` 校验失败 → 所有 agent 节点必失败（store_complete_downstream.go:193 注释早就预言）。codex 修复 `046ea694`：用项目统一 `internal/util/idgen.NewAgentID()` 生成 `agent_<monotonicNumericTimestamp>` 填 AgentID + Name 取 `sanitizeLaunchName(node.Title)`（去控制符 + 80 字符上限）+ 41 行新测断言 AgentID/Name/不重复。merge `6d97cb55`。**链路验证**：create / apply_ops / dispatch / start / dispatcher 路由到 AgentExecutor 都通了（70% M3 路径绿）；最后一公里 LaunchAgent 调用因旧 binary 仍跑（PID 13405 未重启）导致复跑验证需 mcp-orch 重启（与 F6.3 部署注同样规律：服务重启后才生效）。次 DAG `dag-validation-v2-2026-05-12` 复跑同失败证明进程未换 binary。
> 2026-05-12 第七轮 **M3 DAG 端到端真打通**（F1.1-followup-2 + dogfood v4）：用户质疑「不是走 prompt_template 不走命令卡？」拨开误判方向——`Command` 是 OS 进程 exec args，但 mcp-orch 在 `GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8090` 下走 **remoteLauncher**（不读 `req.Command`），由 dolphin-cli 主进程托管子 thread。真 bug 是 `service.validateLaunchRequest`（launch_helpers.go:223-232）僵硬校验 `len(req.Command)>0`——这是 ADR-014 立案前 local launcher 假设残留。修法：抽 `validateLaunchRequestForLauncher(req, launcher)` + `requiresCommand(launcher) bool` type-assertion `*remoteLauncher` 跳过 Command 校验 / `cbb02016` + merge `deb0b7c9`，3 文件 +67 -5 含 4 case table-driven 单测。**M3 真打通时刻 `2026-05-12 19:20:38`** dag-validation-v4-2026-05-12 跑通：节点 spawn 出新 thread `agent_1778584828878143000`（Name 取 node.Title 验证 followup-1 Name 字段也生效）+ F1.5 `spawning_thread_id` 字段写回 task_dag_nodes + spawned thread 真按 first_turn prompt 一字不漏回复「✅ M3 DAG 端到端跑通 — service validate gate fix 生效」。**剩余 lifecycle 尾巴**：节点 status 停在 `ready`（不是 `done`），因为 `node_router.go:291-296 dispatchAgent` 不调 completeAgentNode 写回 status（对比 dispatchAutomation:307-313 有此处理）；agent 节点 lifecycle 设计本意是 spawned child thread 自调 task_update_node close out，但当前 spawned thread 无此 prompt 指引——属 F1.x lifecycle 设计 ambiguity，立 F1-followup-3 ticket 跟进。
> 2026-05-12 第八轮 **C-A 路径 C 阶段 + A1 全部落地**（F1-followup-3 ticket 关闭）：W-C1 codex 累加器 4 commit `2a392e61`..`3c115ebf` + W-C2 claude e2e 脚手架 2 commit `b2220bb7`+`fe5572b0` + W-C3 stop_helper 4 commit `9ff6059f`..`a5345514` + W-A1 subscriber+fallback 10 commit `94ebdba4`..`71312fd2`（3 reviewer 一审揭 1 P0+2 P1+3 P2+3 P3 一次收摆 `bcf68488` + H13 follow-up `1e097c2c` + 3 项二审补 `71312fd2`）。**M3 DAG 节点卡 ready 不 promote 问题真闭环**：dag_turn_completed_subscriber.go 订阅 ev.Result、反查 task_dag_nodes.spawning_thread_id、推 done/failed + 下游 schedule、锁外调 StopSpawnedAgent。§2.6 4 race window（A/B/C/D）在 14+3 case 单测全覆盖。`go test ./cmd/mcp-orch/... -count=10` + `-race` 全 PASS；scripts/test_with_guard.sh --guard-only 代码守卫全过；archtest InterfaceIsolation freeze=40 贴顶警示已记。**3 ADR 状态升 Accepted**：ADR-015 v4.1 `f923ebd7` / ADR-016 v1.2 `cddb3ea2` / ADR-017 v1.2 `00864aa7`。**三项非阻塞 follow-up 全部记账**：H12 + H13 §5.2 e2e + §5.3 race C 时序模拟 + nil 哨兵根治 + freeze 0 余量警示。**F 阶段 done 计数仍 19**（A1 不在 F 表，C-A 路径独立记账）。F1.3 仍 🟡 部分完成，outputs 重做是 A2 范围。下一站：A2（ADR-018）。
> 2026-05-13 第九轮 **C-A A2 / F1.3-rework 落地**：A2 outputs 重做 commit `3e70e468` + review-fix `02009e22`，ADR-018 升 Accepted。AgentExecutor launch 成功不再写 `{thread_id, agent_key}` metadata outputs；DAG TurnCompleted subscriber 基于 `ev.Result + config.outputs` 物化真实输出，默认写 `node.result`，显式 sharedfile 写真实 payload，sharedfile-only 的 `node.result` 只保留小引用 envelope；sharedfile 写入前先经 `ClaimTaskDagNodeOutputMaterialization` claim fence，`awaiting_verify` 支持写文件后 CompleteNode 临时失败的 replay 恢复；`to_node_result` 超 4KB 按 ADR-006 fail，不 fallback。`go test ./cmd/mcp-orch/... -count=1` + `scripts/test_with_guard.sh --guard-only` PASS，2 reviewer 复核 APPROVED；review-fix 后 `go test ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/orchestration/nodeexec ./internal/sidecar/orch/store/taskdag -count=1` + `go test ./internal/archtest -run TestCodeSizeGuard -count=1` PASS。**F 阶段 done 计数：19 → 20**；F1.3 从 🟡 部分完成重新升 ✅ done。
> 2026-05-13 第十轮 **F7.3 prompt_template seed 库落地**：`329d525d` 加 `manually_edited` 防覆盖列、cmd/mcp-orch prompt store/sqlc/DTO 字段穿透与 mapping 单测；`d0de46ed` seed 13 张通用 skill cards（morning_briefer / paper_summarizer / pr_summarizer / weekly_reviewer / data_inspector / email_drafter / health_reporter / topic_curator / source_monitor / note_organizer / todo_prioritizer / learning_card / trip_briefer）并加 archtest 守卫；review follow-up `5db953b3` 补齐 root/internal prompt 写路径，让 UI/后台更新已有 prompt 时置 `manually_edited=TRUE`，并收紧 0087 archtest 为 exactly 13 + per-row 字段守卫。seed 走 `DO UPDATE ... WHERE manually_edited=FALSE`，`variables='{}'::jsonb`，不写 `router_priority` / `{{var}}`，tags 仅作 UI/admin 列表元数据。`go test ./internal/sidecar/orch/store/prompt ./internal/sidecar/orch/tools ./internal/store/prompt ./internal/module/prompt ./internal/archtest -count=1` + `go test ./cmd/mcp-orch/... -count=1` + `sqlc compile -f sqlc.yaml` PASS。**F 阶段 done 计数：20 → 21**；F7.3 从新增未开工升 ✅ done。
> 2026-05-13 第十一轮 **F1.4 failure-class 基础重试落地**：commit `e64e4234` 将 dispatcher retry 决策收敛到 `retry_strategy.go`，transient/quota/launcher validation 三类 AgentExecutor failure class 走现有 `RetryPolicy.MaxAttempts` bounded retry；硬配置 validation（bad JSON / 缺字段 / wiring）保持 fail-fast 并同步 fail DAG node + downstream cancel。review-fix `02009e22` 补齐 `FailWakeup` affected-row fence：rows=0 时不再继续 DAG fail/cascade，避免 stale/expired lease 误伤节点。未提前实现 F12.1 的 by_class / escalate_model / append_error / replan。TDD 红灯验证 `validation_first_failure_retries` 旧行为直接 fail 后转绿；规格复审 + 代码质量复审均 APPROVED。验证：`go test ./cmd/mcp-orch/... -count=1` + `bash scripts/test_with_guard.sh --guard-only` + `git diff --check` PASS；review-fix 后相关 orchestration/taskdag 与 CodeSizeGuard PASS。**F 阶段 done 计数：21 → 22**。
> 2026-05-13 第十二轮 **F15.1 dispatch/retry metrics 落地**：commit `e2d8aa6c` 增加 `dispatch_failed_total` / `retry_count_per_node` / `dispatch_retry_alert_total` / `retry_count_per_node_overflow_total`，dispatcher 只在 `FailWakeup` / `RetryWakeup` 等状态写成功后计数；`retry_count_per_node` 以 claim 后 `attempt_count` 为准并设置 256 条 series cap，溢出走 overflow counter；retry attempt ≥3 通过现有 `notify_channel` webhook 队列告警。验证覆盖 dispatcher 5 节点 batch 跑通 + node-3 retry=3 alert + `/metrics` 读取、max-attempt fail alert、真实 TLS httptest webhook capture、per-node series cap；2 轮 subagent 规格/质量复审均 APPROVED。**F 阶段 done 计数：22 → 23**。
> 2026-05-13 第十三轮 **Phase 4 / M3 dogfood backend 验收通过**：10 节点 prompt_template-first DAG 正向 run 10/10 done，`paper_summarizer.md` sharedfile 保留 agent-authored 长内容（DB `octet_length=13690`，`node.result={"sharedfile":{"path":"reports/m3-dogfood-20260514-0021/paper_summarizer.md"}}`），负向 run `long_node_result_rejected` 按 ADR-006 4KB cap validation failure，metrics 默认 family 校验通过。dogfood 同轮补 3 commit：`b9e8269d` hook-delivered `turn.completed` 复用 DAG completion path + configured sharedfile 已存在时防覆盖；`ddf1b16f` provider/model/effort/disabled_tools runtime hints 传入 remote launcher `thread/start.config`；`019cf8a5` dogfood harness 本地 loopback URL 绕过代理、remote URL 保留代理，并接受 `retry_count_per_node_overflow_total` 作为无样本 collector fallback。2 个 reviewer 复审无阻塞；C-A 计划 v2.9 已同步。**F 阶段 done 计数仍 23**；这是 M3 backend dogfood 通过，不等同 UI/T5-T8 前端闭环完成。
> 2026-05-14 第十四轮 **final output 后端 MVP 落地**：commit `362be7f0` 将最终产物从 Shared Files 无差别列表中标记出来：DAG 模板用 `task_dags.metadata.final_node_key` 显式声明最终节点，run 成功终态时 `FinalizeTaskDagRunIfAllNodesTerminal` 把 final node 的 sharedfile/text/json 结果索引到 `task_dag_runs.metadata.final_output`；`task_get_run` 通过既有 `Run.Metadata` 暴露，不新增 migration/UI。sharedfile 仍是文件存储与协作空间，也可以承载最终产物；`final_output` 负责标记/索引本次 run 的最终交付物，避免中间产物噪音。UI/清理策略后续另立 H14/H15。2 个 reviewer + 1 个复审 agent 通过；验证：`go test ./cmd/mcp-orch/... -count=1`、关键 archtest、`git diff --check`、临时单 query `sqlc compile` PASS；全目录 `sqlc compile` 仍受既有 0083 schema list 缺 `spawning_thread_id` 阻塞。**F 阶段 done 计数仍 23**。
> 2026-05-14 第十五轮 **H14 final_output 前端入口落地**：feature commit `253e7244` + merge `c772b6ac`。dashboard 增加 `dagRuns` RPC 与 Shared Files 页 `finalOutputRefs`，DAG 卡片可打开详情，详情页读取最新 run 的 `metadata.final_output` 并展示 file/text/json；Shared Files 页面高亮并筛选被 final_output 引用的文件，filter 在 refs 清空时自动复位，文件读取仍走 path-based `ui/memory/shared-file/get`。本轮只做单 final_output pointer，不提前实现 bundle/multi-artifact；H15 retention / cleanup 仍独立保留。验证：feature pre-commit + `npm test` 110 files / 1209 tests、`npm run build`、`go test ./internal/app ./internal/ui/wails ./internal/module/dashboard -count=1`、merge diff `git diff --check` 均 PASS。
> 2026-05-14 第十六轮 **H15 sharedfile retention/delete guard 首切**：先做安全底座，不做危险自动清理。dashboard `memory` 页新增 `sharedFileRetention` 元数据，按 `finalOutputRefs` 标出 protected / cleanup candidate；`ui/memory/shared-file/delete` 在后端扫描 DAG run `metadata.final_output`，命中最终产物引用时 fail-closed 拒绝删除；Shared Files 页同步禁用 final_output 文件删除按钮。后续 H15 仍需 UI 批量清理、TTL、导出确认和 pinned / running run 更完整保护。
> 2026-05-14 第十七轮 **F4.3 ApplyOps remove_node 后端落地**：`task_dag_apply_ops` 接通 `remove_node` 真实业务。wire 层既有 `OpRemoveNode` 不变；service 层在同一 DAGOps transaction 内做 OCC、active running run / running DAG 禁删、pending/ready 状态门禁、目标存在校验、同批同 node 跨 op kind 去重，以及“仍被其它节点 depends_on 引用则拒绝”；store `DeleteNode` 的 SQL 也带 `status IN ('pending','ready')` 执行级护栏，删除 leaf 节点后 bump version。依赖仍是 `task_dag_nodes.depends_on` JSONB 数组，没有自动改写下游；需要先用 `update_node` 显式解除依赖再删。
> 2026-05-14 第十八轮 **F4.4 ApplyOps update_dag 后端落地**：`task_dag_apply_ops` 接通 `update_dag` 真实业务。wire 层既有 `DAGPatch` 字段位保持 `title/description/trigger/cron_expr/owner_id`，新增 wrapper + patch strict unknown-field / missing patch / empty patch 拒绝；service 层在同一 DAGOps transaction 内复用 OCC、terminal DAG 禁改、active running run / running DAG 禁改、trigger 枚举校验、cron_expr 解析校验、按 patch 后最终 trigger/cron 状态维护排程、同批多个 update_dag 去重；store 侧用窄口 `UpdateDAGPatch` 只写 DAG 元数据 5 列并维护 `next_run_at`，最后 bump version，不改 status / nodes / runs / metadata。F4.1-F4.5 ApplyOps 后端闭环完成。
> 2026-05-14 第十九轮 **F13.1 lifecycle hooks 真实触发落地**：`NodeExecutorRouter` 新增 `node_executor_dispatch.go`，对 executor `Hooks()` 做 bounded best-effort 触发：`before_execute`/`after_execute` 包住 `Execute`，`on_state_change` 覆盖 agent `ready/pending → running`、subscriber / hook-consumer agent `→ done/failed`、automation `→ done`、dispatcher terminal failure `→ failed`，`on_failure` 只在 `FailNodeAndCancelDownstream` 成功后触发；hook error / panic 只 log，不影响 primary execution，慢 hook 超过 `lifecycleHookDispatchWait` 后转异步，并受 `lifecycleHookExecutionTimeout` 取消约束，避免 claimed lease 被 hook 卡住造成重复 launch 或资源悬挂。生产 fx 通过 `ProvideNodeLifecycleHooks` 给 Agent/Automation executor 注入默认 structured-log hook map，构造器仍支持测试或后续审计实现替换 hook。`RetryWakeup` SQL hard-cap fallback 也补齐 DAG node fail + terminal hooks。**F 阶段 done 计数：25 → 26**；剩余后端主线优先 F12.1 / F6.5，UI F8-F11 仍按用户计划统一设计。
> 2026-05-14 第二十轮 **F12.1 smart retry dispatcher 落地**：`exec.on_failure` 从骨架 schema 接入生产 dispatcher。`by_class` 命中后支持 capability→`escalate_model`（先 `RetryWakeup` fence 成功，再用 `PatchTaskDagNodeConfigIfUnchanged` 窄口 patch agent `exec.model`）、validation→`append_error`（先 fence 成功，再窄口追加上一轮 validation 诊断进 `first_turn`）、`replan`（spawn `dag_designer` planner agent 并提示使用 `task_dag_apply_ops` 修图）和 `fail_fast`；`RetryPolicy` 仍是未配置 `on_failure` 节点的 fallback。复审补强：hard/needs_human 未显式 by_class 映射时保持永久失败、不被 default retry/replan 误接管；`max_attempts` 在 replan 前生效；`skip`/`ask_human` 暂未实现业务语义时 fail-closed；smart retry 终态失败使用 DAG `fail_fast`；retry fence 未命中不 patch 节点 config，且 config patch 不再走整行 `UpsertNode` 覆盖并发 ApplyOps。**F 阶段 done 计数：26 → 27**；剩余后端主线优先 F6.5，UI F8-F11 仍按用户计划统一设计。
> 2026-05-14 第二十一轮 **F6.5 multi-run DAG isolation 落地**：StartDAG 创建 run 后复制模板节点为 `run_id` runtime 节点，并只 promote 当前 run 的 root；运行时 node mutation / downstream promote+enqueue / finalize / wakeup / retry / subscriber / thread.stopped fallback / spawning_thread_id write-back 均带 run_id，`task_dispatch_node` 改为显式 `run_id` 定位 runtime node。迁移 0089 移除单 DAG 只能一个 running run 的 partial unique，改为模板 `(dag_key,node_key) WHERE run_id IS NULL` 与 runtime `(dag_key,run_id,node_key) WHERE run_id IS NOT NULL` 唯一约束，并给 wakeup 增加 run_id FK/index。测试补齐同 DAG 两 run 节点状态、下游 wakeup idempotency key、final_output finalize、fail-fast cascade、node_spawn 事件隔离；`AppendTaskDagRunEvent` 不再接受 run_id=0 作为“任意 running run”。**F 阶段 done 计数：27 → 28**；后端主线剩余主要是 UI 相关 F8-F11 与若干 H follow-up。
> 2026-05-23 第二十二轮 **DAG Console v1 / U1 落地并封版**：本地 `main` 上 `fee60718` 补 dashboard DAG runtime bridge 与 `dashboard/dagStart`，`2912c602` 将 DAG 页升级为两 pane console shell，`66afaffb` 接通 detail / Start / run history / `final_output` / 节点 metadata / `spawning_thread_id` 子 thread 跳转，`f18bc138` 增加 Playwright e2e smoke，merge `81f0893b` 封入主线。封口修正后，选中 run 的节点列表改由 `dashboard/dagRun` / `task_get_run` 返回 runtime nodes，模板节点只作无 run fallback，避免 UI 把模板节点误当运行态节点。U1 只覆盖“能看见、能启动、能追踪”：不做 AI 设计按钮、节点编辑表单、Mermaid 拓扑、完整事件/错误时间线、TTL/批量清理、bundle/multi-artifact。**M2 用户可见 DAG 闭环完成**；**F 阶段 done 计数：28 → 29**（F10.1）。
> 2026-05-23 第二十三轮 **U2 AI 设计 + 用户微调最小闭环落地**：feature commit `0d8afe45` + merge `8ca3cfa2`。DAG 页新增“AI 设计流程”入口，创建 `dag_designer` thread 并用 `main/dag_designer_zh` kickoff；DAG detail 新增 agent 节点编辑表单，覆盖 provider/model/prompt_key/first_turn/depends_on/inputs/outputs/sharedfile lock，并通过新增 `dashboard/dagApplyOps` RPC 调 `task_dag_apply_ops`，保存时携带 `DAGSummary.version` 作为 `baseVersion` 做 OCC；同时增加只读 mermaid source 拓扑和 detail 内 sharedfile 读写/lock 摘要。审查修正已覆盖：缺 `baseVersion` fail-fast、显式 `baseVersion=0` 可用、缺 provider 不写默认 `claude`、kickoff 失败仍保留新 thread、Mermaid 使用安全 ID、并发测试桩加锁。**F 阶段 done 计数：29 → 31**（F8.1 + F9.1）。F8.2 仅完成本地 provider/model options 与 prompt_key 输入的最小形态，动态 `list_models` / `prompt_list` picker 后置，不算 full done；F11.1 全局 Shared Files 锁可视化仍留 U3。
> 2026-05-23 第二十四轮 **U2 审查封口修正**：补齐列表/detail `DAGSummary.version` 映射，避免 `dashboard/dagApplyOps` 从列表入口拿到 stale/0 baseVersion；Start CTA 增加 UI 门禁（`draft/ready` + manual/scheduled + 无 active run），节点编辑表单在 active run / running / 终态时禁用并解释原因；running DAG 下 `add_node` 当前改为 fail-fast，直到 runtime append 能把新节点写入当前 run runtime nodes 并参与调度，避免“模板写成功但当前 run 看不到/不调度”的假成功。拓扑口径收敛为只读 mermaid source，节点点击联动后置。**F 阶段 done 计数不变**，本轮是 U2 质量封口。

---

## 0. 总览

| 阶段 | 任务数 | 状态 | 总 size | 关键产出 |
|---|---|---|---|---|
| **S 骨架** | **24** | **✅ 封板：17 done / 1 推迟 F (S2.4) / 2 推迟 T5 (S6.1+S6.2) / 4 转 T0 作业** | ~25% | 14 处补丁 + ADR 0001 + 删死代码；行为完全不变 |
| **T 工具** | 18 + 9 T0 | **🟡 T1.1 + T1.2-mid + T2.1+T2.2 + T3.1+T3.2 + T4.1+T4.4 后端八连发 done；T5.1/T5.2/T5.3/T6.1/T7.1 已由 DAG Console v1 覆盖；T8.1/T8.2 已由 U2 最小闭环覆盖。** | ~70% | MCP 工具 9/9 就位（含 registry）；DAG Console v1 已能看见、启动、追踪；AI 设计师 UI 已能创建 designer thread 并 kickoff |
| F 功能 | 38 | 🟡 **31 done**（+ F1.5 / F4.1 / F6.3 于 2026-05-11；+ F1.2 / F2.2 / F4.2 / F7.1 于 2026-05-12 第二轮；+ F4.5 于 2026-05-12 round-3 wiring batch；+ F7.2 于 2026-05-12 第四轮 worktree 并行 + 互审；+ F14.1 于 2026-05-12 第五轮 codex 实装 + claude 互审；**F1.3 于 2026-05-13 经 C-A A2 / ADR-018 `3e70e468` + review-fix `02009e22` 重新升 ✅ done**；**F7.3 于 2026-05-13 `329d525d` + `d0de46ed` 升 ✅ done**；**F1.4 于 2026-05-13 `e64e4234` + review-fix `02009e22` 升 ✅ done**；**F15.1 于 2026-05-13 `e2d8aa6c` 升 ✅ done**；**F4.3 / F4.4 / F13.1 / F12.1 / F6.5 于 2026-05-14 升 ✅ done**；**F10.1 于 2026-05-23 DAG Console v1 升 ✅ done**；**F8.1 / F9.1 于 2026-05-23 U2 最小闭环升 ✅ done**） / 1 完成占位 F6.1 / 1 部分完成 F8.2 / 1 未开工 F11.1 / 4 降 H（F3.1-3.4，详 ADR-014）。另：dispatcher wiring 全链接通 + size_cap 4KB enforce + events ring trim N=50 + Tarjan SCC + task_dispatch_node MCP tool + schema sanity gate（不在 F 表位，但在 round-3 batch 中携带落地）；**Phase 4 / M3 backend dogfood 已通过 `b9e8269d` / `ddf1b16f` / `019cf8a5`** | ~75% | 行为兑现：cron 真跑、AI 设计师上岗（含 seed 库菜单）、智能重试/dispatch 观测落地、multi-run DAG runtime 隔离落地；DAG Console v1 + U2 让用户可见与微调闭环成型；backend dogfood 10 节点闭环已绿 |
| H 加固 | 按需 | 🟡 H14 已落地；H15 删除保护/retention 元数据首切已落地，其余按需 | ~10% | 生产问题驱动，不预排；final_output UI 入口已补齐，retention/cleanup 先保护最终产物，再做批量清理体验 |

里程碑：
- **M1（S 完成）**：删除 `auto_handoff_phase1` 全代码 0 命中；旧 DAG 100% 兼容
- **M2（T 完成）**：✅ 已由 DAG Console v1 覆盖：UI 上能看到 DAG → 点 Start → 节点状态变化，并可追踪 run history / final_output
- **M3（F 完成）**：每日 cron + AI 帮你设计流程 + 智能重试 三大需求端到端通

---

## 1. 阶段 S 骨架（24 任务，已封板）

状态图例：✅ done / 🟡 部分完成 / ⏸ 推迟 / ⛔ 未做

| ID | 状态 | 标题 | 主要触动文件 | commit |
|---|---|---|---|---|
| **S0.1** | ✅ | 在 p23/README.md 顶部加 deprecation 提示 | `docs/plans/迁移/p23/README.md` | 66c42c82 |
| **S1.1** | ✅ | NodeExecutor interface + NodeOutcome / RetryHint | `nodeexec/types.go` | 5e1c731e |
| **S1.2** | ✅ | FailureClass 7 / OnFailureStrategy 7 / HookPoint 4 / HookHandler interface | `nodeexec/types.go` | 5e1c731e |
| **S1.3** | ✅ | AgentExecutor stub | `nodeexec/stubs.go` | 5de5dd44 |
| **S1.4** | ✅ | AutomationExecutor stub | `nodeexec/stubs.go` | 5de5dd44 |
| **S1.5** | ✅ | HybridExecutor stub | `nodeexec/stubs.go` | 5de5dd44 |
| (重构) | ✅ | NodeExecutor 抽到 nodeexec 子包 | `nodeexec/` | 9dda3a41 |
| **S2.1** | ✅ | service.StartDAG / TerminateDAG stub + ErrLifecycleNotImplemented | `orchestration/dag.go` | c504441e |
| **S2.2** | ✅ | service.ApplyOps stub | `orchestration/dag.go` | da79df11 |
| **S2.3** | ✅ | Scheduler interface + noopScheduler stub | `orchestration/scheduler.go` | c504441e |
| ~~S2.4~~ | ⏸ | 节点完成时自动 promote 下游 status pending→ready (B-14) | **推迟到 F 阶段**跨 SQL/sqlc/dispatcher 三层 | 84c5b0da 说明 |
| **S3.1** | ✅ | migration: task_dags 加 5 列 (trigger/owner_id/cron_expr/next_run_at/version) | `migrations/0072_dag_v2_dag_columns.sql` | 9130f601 |
| **S3.2** | ✅ | migration: task_dag_nodes 加 run_id/reads/writes | `migrations/0073_dag_v2_node_columns.sql` | 9130f601 |
| **S3.3** | ✅ | migration: task_dag_runs 表 + 3 索引 | `migrations/0074_dag_v2_runs.sql` | 9130f601 |
| **S3.4** | ✅ | migration: auto_handoff_phase1 一次性映射 → trigger='auto' | `migrations/0075_dag_v2_compat.sql` | 9130f601 |
| **S3.5** | ✅ | store contract: Run / RunStore / CreateRunInput / ListRunsFilter | `store/taskdag/contract.go` | 9130f601 |
| (补) | ✅ | migration 0076-0080 续：T1.2/T3.x 落地补 task_dag_runs CHECK 与字段对齐（详见 §12.4） | `migrations/0076_*.sql`-`0080_*.sql` | T 阶段多 commit |
| **S4.1** | ✅ | typed ops payload (4 动词 + Op interface + custom (Un)Marshal) | `nodeexec/ops.go` | 89073074 |
| **S4.2** | ✅ | OpsRequest / OpsResponse | `nodeexec/ops.go` | 89073074 |
| **S5.1** | ✅ | typed node.config schema (3 种 node_type + 共享 Inputs/Outputs) | `nodeexec/config.go` | 0883254b |
| **S5.2** | ✅ | ParseNodeConfig dispatcher | `nodeexec/config.go` | 0883254b |
| **S5.3** | ✅ | OnFailureConfig 解码 + by_class lookup + EscalationModelFor | `nodeexec/on_failure.go` | 61bff08b |
| **S6.1** | ⏸ | UI `DagDetailModal` 真实组件结构 (推迟 T5) | `components/DagDetailModal.js` | T 阶段 |
| **S6.2** | ⏸ | UI 状态色 token (推迟 T5) | `styles/dag-tokens.css` | T 阶段 |
| **S7.1** | ✅ | 9 态 NodeStatus + ValidateTransition + IsTerminal | `nodeexec/status.go` | af542629 |
| **S7.2** | ✅ | service.UpdateNodeStatus 接通 ValidateTransition + dispatcher fast-lane 说明 | `orchestration/dag.go` | c972b3f1 |
| **S15.1** | ✅ | 删 auto_handoff_phase1 全部写入点 (grep 代码 0 命中) | `tools/task_tools.go` | 4c355d5e |
| **S16.1** | ✅ | ADR 0001 骨架阶段契约 (276 行) | `docs/adr/0001-dag-v2-contracts.md` | 83d83ea0 |

## 1.1 T0 启动作业（骨架阶段二次审查后转入）

骨架阶段二次 DAG 审查（`dag_skeleton_audit_20260510`）产出 8 个非阻塞型 findings，全部转为 T0 启动作业。必须在 T1.1 / T2.1 开工前处理：

| ID | 状态 | 问题 | 处理 |
|---|---|---|---|
| **T0.1** | ⏸ 推迟 | PD-1: 缺 e2e 测试 fixture（合并 PT-3: T1.1 缺端到端 fixture） | 与 T1.2/T3.x 真实路径一起做（需 PG） |
| ~~**T0.2**~~ | ✅ done | PB-2: migration 0072-0075 未在 PG 跑过验证 | 2026-05-10 应用：pg_dump 备份 `/tmp/super_agent_v3.before_0072_20260510_145812.dump` + psql 逐个单事务 + `INSERT schema_migrations` 同步。0075 实际转换 2 行（v3-arch-violations-fix-2026-05-06 + contract-audit-fixes）trigger=manual→auto、metadata 删 auto_handoff_phase1 |
| **T0.3** | ⏸ 推迟 | PB-1: 缺 service↔store 跨层集成测试 | 与 T1.2/T3.x 一起做 |
| ~~**T0.4**~~ | ✅ done | PA-1: dag_retry_policy.go 导航注释 | commit `8d32ea1f` |
| ~~**T0.5**~~ | ✅ done | PC-1: archtest 守护 RunStore 待 T1.2 | commit `8f61c839` |
| ~~**T0.6**~~ | ✅ done | PC-4: ADR 0001 §2.5 加三方映射注释 | commit `8d32ea1f` |
| **T0.7** | 🔁 前置 | PD-2: thread-DAG 关联 (spawning_thread_id) | 并入 F 阶段新增 **F1.5**（spawn 时写入）；T8.1 仅消费，不再推迟。详 ADR-009 |
| ~~**T0.8**~~ | ✅ done | PD-3: doc-sync check | commit `f972627d`（4 项检查全过） |
| ~~**T0.9**~~ | ✅ done（F6.4 背面实装） | PE-1（吃狗粮）: dispatcher 对无 assigned_to 节点处理 | 已归 F6.4 commit `d068e04c`：方案 A 对无 assigned_to 节点不 enqueue wakeup；节点保持 pending（依赖已满足时承担 ready 语义）。详 ADR-004 |

详见审查报告 `handoff/skeleton-audit-{pass1-adr,pass2-tests,pass3-cross-cutting,pass4-prev-closed,synthesis,final-verdict}.md`。

## 1.2 骨架阶段验收总结

**已达成**：
- 17/24 task done + 1 推迟 F (S2.4 → **F6.3**) + 2 推迟 T5 (S6.1+S6.2)
- 14 commit / 28 files / 3113 insertions
- `go build ./...` / `go test ./...` / `go vet ./...` / `scripts/test_with_guard.sh` 全过
- `cmd/agent-terminal/frontend && npm test` 通过（vitest）
- 旧 DAG 创建/查询/更新调用 100% 兼容
- `grep -r auto_handoff_phase1 cmd/ internal/` 0 命中
- ADR 已 commit

- 67 单测全 PASS / 架构守卫全过 / `auto_handoff_phase1` 全代码 0 命中
- ADR 0001 固化全部契约

**实际 14 commit 列表**：
```
4c355d5e  refactor(tools): 删 auto_handoff_phase1 (S15.1)
9130f601  feat(orch): DAG v2 migration + Run 接口位 (S3.1-S3.5)
83d83ea0  docs(adr): 0001 DAG v2 骨架阶段契约 (S16.1)
da79df11  feat(orch): service.ApplyOps stub (S2.2)
61bff08b  feat(nodeexec): OnFailureConfig (S5.3)
0883254b  feat(nodeexec): typed node.config (S5.1+S5.2)
84c5b0da  docs(dag): S2.4 推迟 F + dispatcher fast-lane 说明
c504441e  feat(orch): StartDAG/TerminateDAG/Scheduler stub (S2.1+S2.3)
c972b3f1  feat(orch): UpdateNodeStatus 接通 ValidateTransition (S7.2)
af542629  feat(nodeexec): ValidateTransition + IsTerminal (S7.1)
89073074  feat(nodeexec): typed ops payload (S4.1+S4.2)
66c42c82  docs(p23): deprecation 提示 (S0.1)
9dda3a41  refactor(orch): NodeExecutor → nodeexec
5de5dd44  feat(orch): 三 NodeExecutor stub (S1.3-1.5)
5e1c731e  feat(orch): NodeExecutor 接口契约 (S1.1+S1.2)
```

**骨架阶段封板。**进 T 阶段必读：`docs/adr/0001-dag-v2-contracts.md` + 本节 T0 启动作业。

---

## 2. 阶段 T 工具（18 任务，快车道 5 项 done）

状态图例：✅ done / 🟡 部分完成 / ⛔ 入 PG 阔 / ⛔ 入前端阔 / ⏸ 推迟

| ID | 状态 | 标题 | 文件 / Commit |
|---|---|---|---|
| ~~**T1.1**~~ | ✅ done | MCP `task_start_dag` schema + handler（stub） | `tools/task_tools.go` + `contract/orchestration.go` (StartDAGRequest/Response) / commit `2ef76d2e` |
| ~~**T1.2**~~ | ✅ done (mid) | `service.StartDAG` 真实实现：创建 run + status 转 running | `internal/sidecar/orch/orchestration/dag.go` + `store/taskdag/{contract.go,store_run.go}` + `sql/queries/task_dag_run.sql` / commit `57075943` (store 层) + `bbf8a988` (service)。T1.2-mid 范围完成：RunStore 5+1 方法 (CRUD/Count/Promote/WithRunTx) + StartDAG 真业务 + 3 sentinel error + 10 unit test。Integration test 合并 T0.1 + T0.3。T1.2-full 升级 → **F6.5**。**ledger**：commit `3f6c6a80` — StartDAG 幂等语义路线 N（failed/cancelled 命中返 ErrIdempotencyKeyExhausted，running/succeeded 仍复用 RunKey） |
| ~~**T2.1**~~ | ✅ done | MCP `task_dag_apply_ops` schema + handler（stub） | `tools/task_tools.go` / commit `2af9539c`（PT-4: raw ops 透传测试由 F4.1-F4.5 各自单测自然覆盖，不单独立项） |
| ~~**T2.2**~~ | ✅ done | `service.ApplyOps` 接通 contract.ApplyOpsRequest（stub）；真实实现归 F4.x | `orchestration/dag.go` / commit `2af9539c`（PT-2: ops 形状校验 / unmarshal fail-fast → **F4.0** 顶层前置） |
| ~~**T3.1**~~ | ✅ done | MCP `task_get_run` schema + handler | `internal/sidecar/orch/tools/task_tools.go` + `orchestration/dag_query.go` + `contract.GetRun{Request,Response}` / commit `360f9bfd`（RunStore.GetRun 接通 + 中英双语 ErrRunNotFound 转译 + A2 不 inline 节点） |
| ~~**T3.2**~~ | ✅ done | MCP `task_list_runs` schema + handler | `internal/sidecar/orch/tools/task_tools.go` + `orchestration/dag_query.go` + `contract.ListRuns{Request,Response}` / commit `cf335dbf`（RunStore.ListRuns 接通 + status 枚举对齐 0080 CHECK + mapRuns 复用 dagRunDTO + {runs} 包对象） |
| ~~**T4.1**~~ | ✅ done | MCP `list_models` 工具 | `tools/registry_tools.go` 新建 / commit `c311259e`（PT-1: F 阶段改读 provider registry） |
| ~~**T4.2**~~ | ✅ 复用 | MCP `prompt_list` 已存在 | `tools/prompt_tools.go` |
| ~~**T4.3**~~ | ✅ 复用 | MCP `command_list` 已存在 | `tools/command_tools.go` |
| ~~**T4.4**~~ | ✅ done | MCP `shared_file_list` + 暴露 allowed_prefixes | `tools/registry_tools.go` / commit `c311259e` |
| ~~**T5.1**~~ | ✅ done | UI `useDagDetail` composable（dashboard detail/run bridge） | `composables/useDagDetail.js` / DAG Console v1 |
| ~~**T5.2**~~ | ✅ done | UI DAG Console 节点列表（替代旧 `DagDetailModal` 路线） | `components/dag/DagNodeList.js` / DAG Console v1 |
| ~~**T5.3**~~ | ✅ done | UI Start 按钮（`dashboard/dagStart`） | `pages/DagsPage.js` / DAG Console v1 |
| ~~**T6.1**~~ | ✅ done | UI 节点行 → 子 agent thread 链接（`spawning_thread_id`） | `components/dag/DagNodeList.js` / DAG Console v1 |
| ~~**T7.1**~~ | ✅ done | UI DAG 列表字段显示（刷新走 dashboard bridge） | `pages/DagsPage.js` / DAG Console v1 |
| ~~**T8.1**~~ | ✅ done | UI 「AI 帮你设计流程」按钮 | `pages/DagsPage.js` + `app.js` / U2 commit `0d8afe45` + merge `8ca3cfa2` |
| ~~**T8.2**~~ | ✅ done | base 设计师 prompt kickoff | 复用已落地 `main/dag_designer_zh`，U2 从 UI 创建 `dag_designer` thread 后主动发送 kickoff |
| **T9.1** | 🟡 部分 done | codemap 索引随 T1-T4 同步刷新中（02-mcp-orch.md / 04-app-contract.md / 10-store.md 已同步） | `docs/doc/codemap/` |

### 2.1 T 阶段快车道总结 (2026-05-10 二次审查后)

**完成（8 条 commit，含 T0）**：
```
c311259e  T4.1+T4.4 list_models + shared_file_list
2af9539c  T2.1+T2.2 task_dag_apply_ops 接通 contract
2ef76d2e  T1.1 task_start_dag stub
8f61c839  T0.5 archtest 守护
f972627d  T0.8 doc-sync script
8d32ea1f  T0.4 + T0.6 导航注释 + ADR 三方映射
```

**MCP 工具表面 9/9 全就位**：task_create_dag / get_dag / update_node / start_dag / dag_apply_ops / list_models / shared_file_list / prompt_list / command_list。AI 设计师在 thread 里能查全部资源，stub 路径允许“骨架走一趟”。

**T 阶段二次 DAG 审查（`dag_t_phase_audit_20260510`）产出 4 轻度 findings**，全部推迟立项：
- **PT-1**：list_models 硬编码 → F 阶段改读 provider registry
- **PT-2**：ApplyOps stub 不验证 ops 形状 → F4.1 真实落地时 unmarshal fail-fast
- **PT-3**：T1.1 缺端到端 fixture → 合并到 T0.1
- **PT-4**：T2.1 raw ops 透传缺测试 → F4.1 一起做

**剩余 task 阔住点**：
- T0.1 / T0.2 / T0.3 → 需本地 PG 环境（T1.2 / T3.1 / T3.2 已完成）
- T5.x / T6.1 / T7.1 / T8.x → 已分别由 U1/U2 覆盖；后续前端新能力仍按 UI 决策台账先定范围

**UI 决策台账**：2026-05-14 已新增 `docs/plans/dag-ui-decision-ledger.md`，集中记录 DAG UI 已锁边界、仍需用户拍板项、旧 P10 愿景降级项与推荐实现顺序；后续前端任务开工前先对齐该台账。

详见审查报告 `handoff/t-phase-audit-{pass1-adr,pass2-layer,pass3-tests,pass4-t0-closed,synthesis,final-verdict}.md`。

---

## 2.legacy 原 T 阶段表格（历史保留，可跳过）

> 本表为历史排期，状态以上方 §2 当前表为准。T1.1 / T1.2-mid / T2.1 / T2.2 / T3.1 / T3.2 / T4.1 / T4.4 已 done。

| ID | 标题 | 主要触动文件 | 验收 | 依赖 | Size | 并行 |
|---|---|---|---|---|---|---|
| **T1.1** | MCP `task_start_dag` schema + handler | `internal/sidecar/orch/tools/task_tools.go` | 集成测试：调 task_create_dag → task_start_dag → status running | S2.1 | M | Y |
| **T1.2** | `service.StartDAG` 真实实现：创建 run + status 转 running | `internal/sidecar/orch/orchestration/dag_lifecycle.go` | 单测覆盖；run 表插入正确 | T1.1, S3.5 | M | N |
| **T2.1** | MCP `task_dag_apply_ops` schema + handler（draft/ready 状态可改） | `internal/sidecar/orch/tools/task_tools.go` | 集成测试：apply_ops 后 version+1，base_version 不匹配返 conflict | S4.1, S4.2 | M | Y |
| **T2.2** | `service.ApplyOps` stub 接通（draft/ready 状态允许调用，返回 NotImplemented）；真实实现完全归 F4.x | `internal/sidecar/orch/orchestration/dag_ops.go` | 单测：工具 schema 正确 + service stub 调用通 + base_version 不匹配返 conflict | T2.1, S5.2 | M | N |
| **T3.1** | MCP `task_get_run` schema + handler | `internal/sidecar/orch/tools/task_tools.go` | 集成测试：返回完整 run + 节点状态 | S3.5 | S | Y |
| **T3.2** | MCP `task_list_runs(dag_key, limit)` schema + handler | 同上 | 集成测试：分页正确 | S3.5 | S | Y |
| **T4.1** | MCP `list_models` 工具（hardcoded provider→models） | `internal/sidecar/orch/tools/registry_tools.go` 新建 | 集成测试：claude/codex 各自 model 列表 | — | S | Y |
| **T4.2** | MCP `list_prompt_templates` 工具（已有 prompt_list 复用） | 同上 | 集成测试 | — | S | Y |
| **T4.3** | MCP `list_command_cards` 工具（已有 command_list 复用） | 同上 | 集成测试 | — | S | Y |
| **T4.4** | MCP `list_sharedfiles` 工具 | 同上 | 集成测试 | — | S | Y |
| ~~**T5.1**~~ ✅ done | UI `useDagDetail` composable（fetch task_get_dag / dashboard DAG detail bridge） | `cmd/agent-terminal/frontend/vue-app/composables/useDagDetail.js` / commits `2912c602` `66afaffb` + merge `81f0893b` | 单测：response → state 映射正确；e2e smoke 覆盖 detail load | T1.1 | M | Y（前端独立） |
| ~~**T5.2**~~ ✅ done | UI DAG Console 详情渲染节点列表（含状态色 + provider/model/agent_key 显示；刷新先走 dashboard bridge） | `cmd/agent-terminal/frontend/vue-app/pages/DagsPage.js` + `components/dag/DagNodeList.js` / commits `2912c602` `66afaffb` + merge `81f0893b` | e2e smoke 覆盖节点 `status`、`node_type`、`spawning_thread_id` 文本；full vitest 通过 | T5.1, S6.1, S6.2 | M | N |
| ~~**T5.3**~~ ✅ done | UI Start 按钮（`draft/ready` + manual/scheduled + 无 active run 时可点 → 调 `dashboard/dagStart` → `task_start_dag`） | `cmd/agent-terminal/frontend/vue-app/pages/DagsPage.js` / commit `66afaffb` + merge `81f0893b` + U2 审查封口 | e2e smoke 覆盖 Start CTA 请求与运行反馈；behavior test 覆盖 running latest_run 阻断 | T5.2, T1.1 | M | N |
| ~~**T6.1**~~ ✅ done | UI 节点行展示 `spawning_thread_id` 子 agent thread 链接 | `components/dag/DagNodeList.js` + `cmd/agent-terminal/frontend/vue-app/app.js` / commit `66afaffb` + merge `81f0893b` | e2e smoke 覆盖 `thread-child` 文本；AppRoot 单测覆盖切到 child chat thread | T5.2 | S | Y |
| ~~**T7.1**~~ ✅ done | UI 列表显示 `trigger / status / version / latest_run_status`（刷新仍走 dashboard bridge） | `cmd/agent-terminal/frontend/vue-app/pages/DagsPage.js` / commits `2912c602` `66afaffb` + merge `81f0893b` | behavior tests + e2e smoke 覆盖列表/详情路径 | T1.1, T3.1 | M | Y |
| ~~**T8.1**~~ ✅ done | UI「AI 帮你设计流程」按钮（创建 `dag_designer` thread + kickoff） | `cmd/agent-terminal/frontend/vue-app/pages/DagsPage.js` + `app.js` / commit `0d8afe45` + merge `8ca3cfa2` | behavior test 覆盖点按钮 → `startThread` 创建 AI 设计流程 thread → `sendMessage(..., {kickoff:true})` → 切到 chat；kickoff 失败时仍保留新 thread 并清理 kickoff marker | T4.1-T4.4 | M | N |
| ~~**T8.2**~~ ✅ done | base 设计师 prompt kickoff（实际 prompt 已由 F7.1/F7.2 seed） | `main/dag_designer_zh` + UI kickoff prompt | UI 复用 `dag_designer` / `main/dag_designer_zh`，不新增重复 prompt seed | T8.1 | S | N |
| **T9.1** | codemap 索引刷新 | `docs/doc/codemap/02-mcp-orch.md` 等 | 新工具入索引 | T1-T4 完成 | S | N |

**T 阶段验收**：
- M2 里程碑：UI 上能看到 DAG → 点 Start → 节点状态变化；U2 后 AI 设计师入口也可创建 designer thread 并 kickoff
- 端到端 `task_create_dag → task_dag_apply_ops 改一个节点 → task_start_dag → 看到节点状态变化` 通过
- AI（任意 thread 中的 LLM）能调 `task_dag_apply_ops` 改 DAG（手动让它做也能成）

**T 阶段提交粒度**：
- T1.1+T1.2 一次
- T2.1+T2.2 一次（ops 是大改，单独提）
- ~~T3.1+T3.2 一次~~ ✅ commit `360f9bfd` + `cf335dbf` + `caa9f13b` + `d1f5b0e4` + `498be56d`（审查应修） + `3f6c6a80` + `1877f401`（路线 N） + `5fed929c` + `9f302bf9`（fx wiring + codemap）
- T4.1-T4.4 一次（registry 工具集）
- T5.1 / T5.2 / T5.3 各一次（前端**每次方案先发用户**）
- T6.1 单独
- T7.1 单独
- ~~T8.1 / T8.2~~ ✅ 已由 U2 合并到一次前端闭环提交 `0d8afe45` + merge `8ca3cfa2`
- T9.1 单独（codemap）

约 12-14 个 commit。

---

## 3. 阶段 F 功能（38 行 / 5 未开工 + 28 ✅ done + 1 完成占位 + 4 降 H：详 ADR-014）

> **口径说明**：38 表位 ÷ 状态 = 28 条 strikethrough ✅ done（F1.1 / F1.2 / **F1.3** / **F1.4** / F1.5 / F2.0 / F2.1 / F2.2 / F4.0 / F4.1 / F4.2 / **F4.3** / **F4.4** / F4.5 / F5.1 / F5.2 / F5.3 / F6.2 / F6.3 / F6.4 / **F6.5** / F7.1 / **F7.2** / **F7.3** / **F12.1** / **F13.1** / **F14.1** / **F15.1**）+ 1 条 F6.1 完成占位（由 T1.2-mid 接手 snapshot）+ 4 条 ⏸ 降 H（F3.1 / F3.2 / F3.3 / F3.4，详 ADR-014：prompt_template-first 路线下 hybrid 节点可由 agent+depends_on 两节点表达）+ 5 条未开工。
>
> 26 个原计划 + 5 个从推迟项补位（F4.0 / F6.3 / F6.4 / F6.5 / F14.1） + 1 个从 T0 前置项补位（F1.5） + 1 个 S5.1 schema 返修补位（F2.0） + 1 个 H6 前置补位（F15.1） + 3 个 Hybrid v2 拓扑占位（F3.2 / F3.3 / F3.4） + 1 个 ADR-014 新增补位（F7.3 prompt_template seed 库） = 38 表位。
>
> 推迟项拼装表：S2.4 → F6.3；T0.9/PE-1 → F6.4；PT-1 → F14.1；PT-2 → F4.0；T1.2-full → F6.5；**T0.7/PD-2 → F1.5**（spawning_thread_id 字段位，详 ADR-009）；**S5.1 schema 漏 kind 字段位 → F2.0**（详 ADR-007）；**H6 dispatch metric 前置 → F15.1**（详 ADR-010）；**Hybrid v2 拓扑 → F3.2/F3.3/F3.4 占位**（详 ADR-011）。
>
> **2026-05-12 ADR-014 prompt_template-first 路线落地同步**：(1) F3.1 / F3.2 / F3.3 / F3.4 **⏸ 降级 H 阶段**（hybrid 节点在 prompt_template-first 下可由 agent+depends_on 两节点表达，独立 node_type 价值低；地基 schema/parser/router 占位/stub 骨架阶段已铺，降 H 不损失沉没成本）。(2) **F7.3 新增**：prompt_template seed 库（10-15 张通用 AI 微技能 — 晨报 / 文献摘要 / PR 摘要 / 周复盘 / 数据巡检 / 邮件起草 / 健康简报 / 选题整理 / 学习卡片 等）；区别于 F7.1 ✅ done 的 designer 自用 prompt（migration 0084 只 seed 了 `main/dag_designer_zh` 1 条），F7.3 是 designer **能挑卡组 DAG 的菜品库**——是 Need 2 端到端真打通的最后一公里。

| ID | 标题 | 主要触动文件 | 验收 | 依赖 | Size | 并行 |
|---|---|---|---|---|---|---|
| ~~**F1.1**~~ ✅ done（+ followup `046ea694` 2026-05-12 第六轮）| `AgentExecutor` 解码 `node.config.exec` → `orchestration_launch_agent` 参数映射 + 错误分类（F1.2-1.4 留位）+ followup：`buildLaunchRequestFromAgentConfig` 填 `AgentID`（`idgen.NewAgentID()`）+ `Name`（`sanitizeLaunchName`）修复 agent 节点必失败 bug（dogfood `dag-validation-2026-05-12` 现场复现 → `service_launcher_bridge.go:390 submissionAgentID` 校验拒）| `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go` / commits `0f65833b` `046ea694` + merge `6d97cb55` | 单测：provider/model/agent_key/effort/language/tools 映射正确；4 处 FailureClass 映射 + classifyAgentLaunchError；followup 补 AgentID/Name/不重复断言（+41 行）| S1.3, S5.2 | M | Y |
| ~~**F1.2**~~ ✅ done | `AgentExecutor` 处理 `inputs`：注入 prev nodes results / sharedfiles（构造 RunContext + inputs.fromPrev / inputs.sharedFiles 注入到 prompt 前缀） | `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go` + `nodeexec/inputs.go` + `nodeexec/executor_agent_inputs_test.go` / commit `3317b00f` + merge `877193cf` + 冲突修 `6f333dd1` | 单测覆盖 prev result 注入 / sharedfile 注入 / 缺失节点降级 / 双端口 wiring TODO | F1.1 | M | N |
| ~~**F1.3**~~ ✅ done（A2 rework `3e70e468` + review-fix `02009e22`） | `AgentExecutor` outputs 已从 launch-time metadata 改为 TurnCompleted-time 真实输出物化。2026-05-12 曾因 `{thread_id, agent_key}` 被误当下游输出降级；2026-05-13 A2 通过 ADR-018 收口：launch 成功不写 metadata outputs，subscriber 基于 `ev.Result + config.outputs` 写真实输出；sharedfile-only 时 `node.result` 仅保留小引用 envelope；sharedfile 写入前先 claim `ready/running/awaiting_verify`，避免 duplicate/stale `turn.completed` 在 DB terminal fence 拒绝后仍写外部文件；`to_node_result` 超 4KB 按 ADR-006 validation fail，不 fallback。**边界**：仅负责本节点输出落地，仍不得调外部 webhook / 命令卡（ADR-011 §4 Q3）；automation outputs 不在 A2 范围。 | `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go` + `executor_agent_outputs_test.go` + `internal/sidecar/orch/orchestration/dag_turn_completed_subscriber.go` + `dag_turn_completed_subscriber_test.go` + `internal/sidecar/orch/sql/queries/task_dag_node_write.sql` + `internal/sidecar/orch/store/taskdag/*` + ADR-018 / commits `ae35c0a2` `f985e83d` + merge `b0fcf77b` + A2 `3e70e468` + review-fix `02009e22` | 单测：launch 成功不写 metadata outputs + 默认 node_result 真输出 + sharedfile 真输出 + sharedfile-only 小引用 envelope + node_result/sharedfile 同时配置 + webhook_url/command_ref 负例 + 4KB size_cap validation + SharedFileWriter 未注入 + IO 写失败 + materialization claim fence + `awaiting_verify` replay；`go test ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/orchestration/nodeexec ./internal/sidecar/orch/store/taskdag -count=1` + CodeSizeGuard PASS | F1.2 ✅ / C-A A1 ✅ / ADR-018 ✅ | S | N |
| ~~**F1.4**~~ ✅ done | `AgentExecutor` transient/quota/launcher validation 三类失败基础重试：dispatcher 根据 `NodeOutcome.FailureClass` 进入 `RetryPolicy.MaxAttempts` bounded retry；硬配置 validation 保持 fail-fast 并同步 fail DAG node；review-fix 补 `FailWakeup` rows=0 fence，stale/expired lease 不再触发 DAG fail/cascade；F12.1 by_class / escalate_model / append_error / replan 未提前实现。 | `internal/sidecar/orch/orchestration/retry_strategy.go` + `wakeup_dispatcher.go` / commits `e64e4234` + review-fix `02009e22` | 单测：三类 failure class 首次失败 retry、二次失败 exhaust；hard validation 不 retry；策略纯函数覆盖 F1.4/F12.1 边界；FailWakeup fence miss 不 cascade | F1.1, S7.1 | M | N |
| ~~**F1.5**~~（T0.7 前置） ✅ done | `spawning_thread_id` 字段位：migration 0083 + AgentExecutor spawn 成功后 UPDATE + task_get_dag DTO 透出。详 ADR-009 | `migrations/0083_dag_v2_spawning_thread_id.sql` + `internal/sidecar/orch/orchestration/nodeexec/executor_agent.go` + `store/taskdag/store_node_spawn.go` + sqlc 手维 / commits `f111c12b` `edc22076` `2c2e0044` `61d41a7a` `4d8f0755` `bec17a85` + merge `b69e24da` + follow-up fix `970cb5aa`（从聚合 Store 拆出 NodeSpawnRecorderStore 过 archtest） | 单测全过：FirstSpawn / RetryOverwrite、3 种 assigned_to 形态 / RetryWithoutRunningRun_SoftMiss / InputValidation；PG 集成测试推迟 T0.1/T0.3 | F1.1 ✅ / S3.x migration 基设 | M | Y |
| ~~**F2.0**~~（S5.1 schema 返修）✅ done | `AutomationExecConfig` 加 `Kind` 字段位（默认 `command_card`）+ `ParseAutomationConfig` 兜底「未知 kind → fail-fast 拒绝 / 空 kind → 默认 command_card」 / commit `3629a77a` | `internal/sidecar/orch/orchestration/nodeexec/config.go` + `config_test.go` | 单测全过：3 个新增用例（unknown kind 拒绝 / 空 kind 默认 / command_card round-trip） + 守卫全绿 | S5.1 ✅ / ADR-007 ✅ | S | Y |
| ~~**F2.1**~~ ✅ done | `AutomationExecutor` 解码 `command_ref` → command_get + 执行 / commit `77e6e5bd` | `internal/sidecar/orch/orchestration/nodeexec/executor_automation.go` | 单测：happy / unsupported kind / command not found / timeout / nil launcher；错误分类按 ADR-008 口径 | S1.4, S5.2, F2.0 | M | Y（与 F1 并行） |
| ~~**F2.2**~~ ✅ done | `AutomationExecutor` 处理 inputs/outputs（共用 nodeexec/inputs.go RunContext + 写回 node.result / sharedfile outputs） | `internal/sidecar/orch/orchestration/nodeexec/executor_automation.go` + `nodeexec/executor_automation_test.go` / commit `3d8526ab` + merge `4dd5307a` | 单测：inputs 注入到 command args / outputs 写 sharedfile / 错误分类按 ADR-008 | F2.1 | S | N |
| ⏸ **F3.1**（v1 单拓扑） | `HybridExecutor v1` — **⏸ 降级 H 阶段**（详 ADR-014 §5：prompt_template-first 下 hybrid 可由 agent+depends_on 两节点表达，独立 node_type 价值低；地基已铺不浪费）。原描述：automation → agent verifier；v2 多向拓扑见 F3.2/F3.3/F3.4 占位。详 ADR-011 | `internal/sidecar/orch/orchestration/executor_hybrid.go` | 集成测试：automation 失败时 verifier 不跑；测试名带 v1 后缀 | F1, F2 完成 | M | N |
| ⏸ **F3.2**（v2 占位） | **⏸ 降级 H 阶段**（详 ADR-014）。原占位：待 ADR-011a 拍板 `agent → automation` 拓扑（agent 输出触发 webhook / 写 sharedfile / 调命令卡） | `executor_hybrid.go` 内分支 | 集成测试 | F3.1 + ADR-011a | M | N |
| ⏸ **F3.3**（v2 占位） | **⏸ 降级 H 阶段**（详 ADR-014）。原占位：待 ADR-011b 拍板 `agent A → agent B` 并行仲裁拓扑（与 F12.1 智能重试正交） | 同上 | 集成测试 | F3.1 + ADR-011b | M | N |
| ⏸ **F3.4**（v2 占位） | **⏸ 降级 H 阶段**（详 ADR-014）。原占位：待 ADR-011c 拍板 `automation A → automation B` 编排两条命令卡 | 同上 | 集成测试 | F3.1 + ADR-011c | M | N |
| ~~**F4.0**~~（顶层前置）✅ done | `ApplyOps` 顶层 unmarshal + 形状校验 + 错误分类（PT-2） | `internal/sidecar/orch/orchestration/dag.go` `ApplyOps` 顶层 + `nodeexec.Ops UnmarshalJSON` / commit `131feb75` | 单测：非法 op_kind / 缺字段 / 非法 base_version 全拒；错误分类清晰；`applyTypedOps` 仍 stub（F4.1+ 真业务） | T2.2 | S | Y |
| ~~**F4.1**~~ ✅ done | `ApplyOps` add_node 真实实现 + Kahn 环检测（CycleError 携带 node_keys）+ OCC 双重护栏（pre-check + bump 失锁 post-check 都返 ErrVersionConflict） | `internal/sidecar/orch/orchestration/nodeexec/cycle.go` + `nodeexec/plan.go` + `orchestration/dag.go` + `dag_query.go` + `store/taskdag/store_dag_ops.go` + `store/sqlc/db_accessor.go` / commits `f716aa5c` `13a81828` `3b2e621e` `31aed200` + merge `e89f9231` | 21 单测：9 cycle（self-loop / diamond / external-dep-ignored / 3-node-loop） + 12 add_node（PT-4 raw ops 透传 + OCC + 重名 + 引用未知节点） | S2.2, T2.2, F4.0 | M | Y |
| ~~**F4.2**~~ ✅ done | `ApplyOps` update_node 真实实现 + `NodePatch` strict UnmarshalJSON + `PlanUpdateNodes` 纯函数 + 节点 status 防御 + 同批 add+update 串行执行 | `internal/sidecar/orch/orchestration/dag.go` + `dag_ops_update_node_test.go` + `nodeexec/ops.go`（NodePatch + AssignedTo）+ `nodeexec/plan.go`（PlanUpdateNodes）+ `nodeexec/ops_update_node_test.go` + `nodeexec/plan_update_test.go` + `store/taskdag/store_dag_ops.go` UpdateNode / commits `7611c268` `65c977d8` `848f1188` + merge `d63a623d` + fix `6f333dd1` | 单测：未知字段拒 / 不可改字段拒 / 状态门禁（done 节点不可改 config）/ 同批 add 后 update / OCC | F4.1 | S | N |
| ~~**F4.3**~~ ✅ done | `ApplyOps` remove_node 真实实现：在 DAGOps transaction 内做 OCC + active running run / running DAG 禁删 + pending/ready 状态门禁；删除 leaf 节点时清掉该节点自己的 `depends_on` 行内容（随行删除），但**不自动改写下游**，若其它节点仍 depends_on 目标节点则拒绝，调用方需先 `update_node` 显式解除依赖 | `internal/sidecar/orch/orchestration/dag_query.go` + `nodeexec/plan.go` + `store/taskdag/store.go` + `internal/sidecar/orch/sql/queries/task_dag_node_write.sql` + `internal/sidecar/orch/store/sqlc/task_dag_node_write.sql.go` | 单测：leaf 删除 bump version / 被依赖节点拒绝 / active run 与 running DAG 拒绝 / delete 0 rows 状态竞态拒绝 / 同批同 node 跨 kind 拒绝 / missing node 拒绝 / remove planner / store DeleteNode | F4.2 | M | N |
| ~~**F4.4**~~ ✅ done | `ApplyOps` update_dag 真实实现：在 DAGOps transaction 内做 OCC + terminal DAG 禁改 + active running run / running DAG 禁改；`update_dag` wrapper + `DAGPatch` strict unknown-field / missing patch / empty patch 拒绝；只允许 patch `title/description/trigger/cron_expr/owner_id`，trigger 必须是 `manual/auto/scheduled/external`，cron_expr 写前解析校验，并按 patch 后最终 trigger/cron 状态决定 `next_run_at`；同批多个 update_dag 拒绝；写入后 bump version，不改 status / nodes / runs / metadata | `internal/sidecar/orch/orchestration/dag_query.go` + `orchestration/cron/scheduler_cron.go` + `nodeexec/ops.go` / `ops_dag_patch.go` + `store/taskdag/store_dag_ops.go` + `dag_ops_update_dag_test.go` / `ops_update_dag_test.go` / `store_update_dag_patch_test.go` / `scheduler_cron_sql_test.go` | 单测：happy patch + version bump / terminal 与 active run 拒绝 / invalid trigger + invalid cron_expr 拒绝 / scheduled 复用 existing cron / manual DAG 单写 cron 拒绝 / missing or misspelled or empty patch 拒绝 / duplicate update_dag 拒绝 / stale base_version / DAGPatch unknown field / store narrow SQL + next_run_at / scheduler 只扫 scheduled | F4.1 | S | Y |
| ~~**F4.5**~~ ✅ done | `ApplyOps` `status=running` / active run 时拒绝模板改写。历史设计曾允许 add_node + depends_on 指向 done 节点；F6.5 runtime nodes 后该路径只写模板、不进入当前 run runtime nodes，因此 U2 审查封口改为 running add_node fail-fast，待 runtime append 真闭环后再恢复动态追加。`bumpDAGVersionTx` 拆 helper 压 CC。 | `internal/sidecar/orch/orchestration/dag_query.go` + `dag_ops_running_invariants_test.go` / commits `845ee566` `403e5b7e` + merge `30f6837c`（随 W3 round-3 batch）+ U2 审查封口 | 新测覆盖：draft update_node happy；running 状态拒 update_node；running add_node 无 deps / depends_on pending / depends_on done / same-batch deps 均 fail-fast，且不写模板节点 | F4.1 | M | N |
| ~~**F5.1**~~ ✅ done | cron daemon 进程入口 + 接 robfig/cron 库（F5.2/F5.3 留位） | `internal/sidecar/orch/orchestration/cron/scheduler_cron.go` 新建 / commit `07ec1317` | 单测：cron 表达式解析正确；Tick 占位 | S2.3 | M | Y |
| ~~**F5.2**~~ ✅ done | `Scheduler.Tick` 真实实现：扫 `next_run_at <= now` → StartDAG；TickTimeout 配置化；Stop cancel in-flight；goleak 替换 goroutine 容差 / commit `2f9341b3` | `internal/sidecar/orch/orchestration/cron/scheduler_cron.go` | 单测：Tick_ScansAndStarts / NextRunAtUpdated / TickTimeout / StopCancelsInflight；错误分类 infrastructure / validation / StartDAG 透传 | F5.1, T1.2 | M | N |
| ~~**F5.3**~~ ✅ done | cron 多实例锁：`pg_try_advisory_lock(lock_id)` 获取后执行 Tick，拿不到跳过，退出时释放 / commit `self:F5.3` | 同上 | 单测：MultiInstance_OneAcquires / ReleaseOnExit | F5.2 | M | N |
| ~~**F6.1**~~ | (snapshot dag.version 部分由 T1.2-mid 完成；events 字段位的业务化写入归 H 阶段。本行保留作为契约位、不再单独 commit) | — | — | T1.2 | — | — |
| ~~**F6.2**~~ ✅ done | run 终态判定：所有节点 done/failed/cancelled/skipped → run.status 按优先级写入 | `internal/sidecar/orch/orchestration/dag.go` + store 层 / commit `0a7cc0ca` | 集成测试通过 | T1.2 | M | N |
| ~~**F6.3**~~ ✅ done | 节点完成时自动 promote 下游 pending→ready（S2.4 / B-14）。`PromoteSingleNodePendingToReady` SQL 用 status='pending' 作幂等护栏；与 F6.4 分工清晰（promote=状态机真相全集 PromotedDownstream / enqueue=路由后子集 ScheduledDownstream）。**部署注**：mcp-orch 服务重启后才生效。 | `internal/sidecar/orch/sql/queries/task_dag_node_write.sql` + `store/sqlc/task_dag_node_write.sql.go` 手维 + `store/taskdag/store_complete_downstream.go` + `store/taskdag/contract.go` PromotedDownstream 字段 / commits `303e01af` `34240412` `05d93f96` + merge `7f51b91e` | 10+ 单测：sequential / fan-out / diamond / idempotency / concurrent upstreams / unassigned + whitespace + mixed assignment + 5 F63 新测 | T1.2, S2.4 推迟 | M | N |
| ~~**F6.4**~~ ✅ done | dispatcher 对无 assigned_to 节点**跳过自动 wakeup enqueue**（T0.9 / PE-1，实装方案 A，与 §10 follow-up DAG 73 证据对齐）；详 ADR-004 | `internal/sidecar/orch/orchestration/dag.go` `UpdateNodeStatus` 路由 + `internal/sidecar/orch/store/taskdag/store_complete_downstream.go` flow store 实装 + TrimSpace 守护测试 | 单测覆盖：无 assigned_to 节点不 enqueue wakeup，状态保持 pending（依赖已满足时承担 ready 语义）；commit `d068e04c` + `91057348` | F6.3（实际乱序：F6.4 先于 F6.3 done） | M | N |
| ~~**F6.5**~~ ✅ done | T1.2-full：复制模板节点为当前 run 的 runtime 节点，允许同 DAG 多个 running run 并发；`StartDAG` 在同一事务内 CreateRun → CloneNodesForRun → PromoteRootNodesToReady(run_id)，运行时 complete/fail/retry/finalize/wakeup/spawn/subscriber/fallback 均按 run_id fence；`task_dispatch_node` / `task_update_node` 显式传 `run_id`，DAG wakeup 缺 run_id fail-loud，避免任何运行时路径回到模板节点 | `migrations/0089_dag_v2_multi_run_node_isolation.sql` + `internal/sidecar/orch/sql/queries/task_dag_{run,node_*,wakeup_*}.sql` + hand-maintained `store/sqlc` + `store/taskdag` + `orchestration` router/dispatcher/subscriber/nodeexec | 单测：同 DAG 两 run 模板不变、runtime 节点状态独立；run A complete/finalize/fail-fast 不影响 run B；失败路径 finalize run；下游 wakeup 带 run_id 且 idempotency key 按 run 分离；`from_nodes` 同 node_key 只读目标 run；node_spawn write-back/events/fallback 只写目标 run；`AppendTaskDagRunEvent` run_id=0 不再命中任意 running run；`go test ./cmd/mcp-orch/... -count=1` PASS | T1.2 (mid)、F6.3、F6.4 | L | N |
| ~~**F7.1**~~ ✅ done | AI 设计师 prompt（中文版）seed 0084 + archtest 守护关键内容不被悄悄抽干 | `migrations/0084_seed_dag_designer_prompt_zh.sql` + `internal/archtest/dag_designer_prompt_seed_test.go` / commits `49fd0143` `52da9d36` + merge `94502cec` | archtest 校验 prompt 包含完整可用资源列表 / 关键指令 keyword 不丢失 | T4.1-T4.4 | M | Y |
| ~~**F7.2**~~ ✅ done | AI 设计师 prompt（英文版）seed 0085 + archtest EN 守卫 | `migrations/0085_seed_dag_designer_prompt_en.sql`（200 行）+ `internal/archtest/dag_designer_prompt_seed_en_test.go`（136 行）/ commits `4b49c5fd` `fe4d00f2` + merge `da01120a` | archtest 校验完整可用资源列表 + 关键 keyword + 英文+中文 routing tag 不丢失 | F7.1 ✅ | S | N |
| ~~**F7.3**~~ ✅ done（新增 / ADR-014 §2.3） | **prompt_template seed 库**：13 张通用 AI 微技能已落地（`morning_briefer` / `paper_summarizer` / `pr_summarizer` / `weekly_reviewer` / `data_inspector` / `email_drafter` / `health_reporter` / `topic_curator` / `source_monitor` / `note_organizer` / `todo_prioritizer` / `learning_card` / `trip_briefer`）。字段约束已守住：`variables='{}'::jsonb`、`prompt_text` 不写 `{{var}}` 替换符、tags 仅供 UI/admin 列表元数据、不写已 drop 的 `router_priority`。 | `migrations/0086_prompt_template_manually_edited.sql` + `migrations/0087_seed_prompt_template_skill_cards.sql` + `internal/sidecar/orch/store/prompt/*` + `internal/sidecar/orch/sql/queries/prompt_template.sql` + `internal/sidecar/orch/store/sqlc/prompt_template.sql.go` + `internal/sidecar/orch/tools/prompt_tools.go` + root/internal `prompt_template` store/sqlc/UI 写路径 + `internal/archtest/dag_skill_seeds_test.go` / commits `329d525d` `d0de46ed` `5db953b3` | 单测/archtest：`PromptTemplate` mapping 覆盖 `ManuallyEdited`；0086 DDL 守卫；0087 required key / exactly 13 / per-row seed 字段 / guarded DO UPDATE / 禁 dead routing 与模板占位符守卫。验证：`go test ./internal/sidecar/orch/store/prompt ./internal/sidecar/orch/tools ./internal/store/prompt ./internal/module/prompt ./internal/archtest -count=1` PASS。人工改 prompt 后重跑 0087 不覆盖由 SQL `WHERE public.prompt_templates.manually_edited = FALSE` + UI/后台写路径置 TRUE 保证。 | T4.2 prompt_list ✅ / F1.1 ✅ / F1.2 ✅ / **F1.3-rework via C-A A2** ✅ / F7.1 ✅ | M | Y |
| ~~**F8.1**~~ ✅ done | UI agent 节点编辑表单首切：node/title/provider/model/prompt_key/first_turn/depends_on/inputs.from_nodes/inputs.from_sharedfiles/outputs.to_sharedfile/lock_mode/to_node_result → `task_dag_apply_ops` `update_node`；保存携带 `DAGSummary.version` 作为 `baseVersion`，缺版本 fail-fast；active run / running / 终态时禁用编辑并解释原因 | `cmd/agent-terminal/frontend/vue-app/components/dag/DagNodeEditForm.js` + `composables/useDagDetail.js` + `internal/module/dashboard/rpc.go` / commit `0d8afe45` + merge `8ca3cfa2` + U2 审查封口 | 单测覆盖表单 payload、缺 provider 不写默认 `claude`、缺 `baseVersion` 不调用 RPC、显式 `baseVersion=0` 可保存、adapter ops payload 透传、active run 下表单禁用且不提交 | S5.1 / F4.x ApplyOps ✅ | L | Y |
| **F8.2** | 🟡 部分完成：provider/model 使用本地 provider config options，prompt_key 先用文本输入；动态接 `list_models` / `prompt_list` 的 picker 后置，不阻塞 U2 最小闭环 | `DagNodeEditForm.js` | 后续若恢复：e2e 覆盖 registry 下拉数据与 prompt_template 选择；当前 U2 已可手动填写 prompt_key / model 并保存 | F8.1, T4.1-T4.4 | M | N |
| ~~**F9.1**~~ ✅ done | UI 只读 Mermaid source 拓扑（DAG nodes + depends_on → 安全 ID 的 mermaid 字符串）；拖拽编辑/d3/canvas 后置 | `cmd/agent-terminal/frontend/vue-app/components/dag/DagTopologyPanel.js` / commit `0d8afe45` + merge `8ca3cfa2` | behavior test 覆盖拓扑组件挂载、依赖边生成与 Mermaid 安全 ID，不提前做大图渲染/拖拽 | T5.2 | M | Y |
| ~~**F10.1**~~ ✅ done | UI recent run 历史面板 | `cmd/agent-terminal/frontend/vue-app/components/dag/DagRunHistoryPanel.js` + `useDagDetail.js` / commit `66afaffb` + merge `81f0893b` + runtime-node 封口修正 | e2e smoke 覆盖 recent runs、selected run 的 `final_output`、selected run runtime nodes 与 `spawning_thread_id`；`dashboard/dagRuns` / `dashboard/dagRun` 错误在 UI 状态中显式暴露。完整 event/error timeline 后置 | T3.2 | M | Y |
| **F11.1** | UI sharedfile 锁可视化（节点 reads/writes 联动；final_output 高亮已由 H14 完成，F11 不再重复承担） | `pages/SharedFilesPage.js` 修改 | e2e：sharedfile 显示"被节点 X 占用" | F1.3 | M | Y |
| ~~**F12.1**~~ ✅ done | 智能重试 strategy dispatcher：`exec.on_failure.by_class` 分发已接生产 dispatcher。capability→`escalate_model` 升级 agent model 后重跑；validation→`append_error` 注入上一轮错误后重跑；`replan` spawn `dag_designer` planner agent 改图；`fail_fast` 立即终态失败。边界：`RetryPolicy` 仍是无 `on_failure` 节点 fallback；hard/needs_human 未显式 by_class 映射时不重试，也不被 default replan 接管；`max_attempts` 约束 replan；`skip`/`ask_human` 业务语义未落地前 fail-closed；subscriber/output materialization validation retry 仍是后续 follow-up。 | `internal/sidecar/orch/orchestration/retry_strategy.go` + `wakeup_dispatcher.go` + `nodeexec/executor_agent.go` + `internal/sidecar/orch/sql/queries/task_dag_node_write.sql` + store/sqlc/taskdag 窄口 patch + tests + ADR 0001 | 单测：capability 升级到 Opus 并 retry；validation 追加 diagnostic 并 retry；replan spawn planner；unmapped hard 不 retry；hard/needs_human + default replan 不启动 planner；replan at max 不 spawn；skip/ask_human fail-closed；escalation exhausted 使用 DAG fail_fast；RetryWakeup fence miss 不 patch config；config patch 携带 old-config fence。验证：orchestration/nodeexec/taskdag 包测 + `cmd/mcp-orch/...` + guard + diff check。 | F1.4 ✅ | L | N |
| ~~**F13.1**~~ ✅ done | lifecycle hooks 真实触发（before/after/on_state_change/on_failure）：executor 构造期注入 hook map；production fx 注入默认 structured-log hook；router 触发 before/after 与 agent running / automation done 状态变更；subscriber 与 hook-consumer 触发 agent done/failed；dispatcher 在 terminal failed 写库成功后触发 failed state_change + on_failure；SQL hard-cap fallback 也补齐 DAG node fail + hooks；hook error/panic/慢执行不改变主执行结果 | `cmd/mcp-orch/fx.go` + `internal/sidecar/orch/orchestration/node_executor_dispatch.go` + `node_router.go` + `hook_consumer.go` + `dag_turn_completed_subscriber.go` + `wakeup_dispatcher.go` + `retry_strategy.go` + `internal/sidecar/orch/orchestration/nodeexec/*` | 单测：production executor provider wired hooks；agent before/after/running 顺序；慢 hook 不阻塞 dispatch；automation before/after/done 顺序；subscriber / hook-consumer agent done/failed lifecycle hook；dispatcher terminal failure / retry exhausted / SQL hard-cap 后保留真实 FailureClass 并触发 hooks；默认 nil hooks 兼容既有测试；archtest guard 同步 helper + fx wiring 抽取 | S1.1, S10 | M | Y |
| ~~**F14.1**~~（工具升级）✅ done | `list_models` 改读 provider registry（PT-1）+ env 覆盖 + fx fallback 到 StaticRegistry 防止启动失败 + Reload error 日志化 | `internal/sidecar/orch/tools/modelregistry/`（新建包：registry.go + models.yaml + test）+ `internal/sidecar/orch/tools/registry_tools.go`（HandleListModels 改 Registry 注入）+ `cmd/mcp-orch/runtime.go` fx provider / commits `9a395e5e` `2cbf389f` `737fcc7b` `90195458` + merge `0b3078f4`（2026-05-12 第五轮 codex 实装 + claude 互审两视角抓 2 blocker） | 单测：env 覆盖 / yaml 损坏保留旧数据 / fx fallback / 增改 yaml 即时反映 | T4.1 | S | Y |
| ~~**F15.1**~~（H6 前置部分）✅ done | dispatch / retry metric：`dispatch_failed_total` / `retry_count_per_node` 计数器 + 节点重试 ≥ 3 次告警 webhook。H6a 已前置到 F 阶段，避免「上层调度看不见下层执行」瞎区到 M3 后才补；未提前实现 H6b / F12.1。详 ADR-010 | `pkg/dagmetrics` + `internal/platform/metrics/dag.go` + `internal/sidecar/orch/orchestration/dispatch_agent_running_metric.go` + `wakeup_dispatcher.go` + `internal/sidecar/orch/notify/dispatch_retry_alert.go` + `cmd/mcp-orch/fx.go` / commit `e2d8aa6c` | 单测/集成：dispatcher 5 节点 batch mark-sent 跑通 + node-3 retry=3 alert；max-attempt final fail 仍 alert；真实 TLS httptest webhook capture；`/metrics` 读取 `dispatch_failed_total` / `retry_count_per_node`；per-node series cap + overflow counter | F1.4 ✅ / F6.3 ✅ | M | Y |

**F 阶段验收**：M3 里程碑端到端用例通过：

> 「点『AI 帮你设计流程』按钮 → 新 thread → 在 thread 里说『帮我设计每天 8 点的报告生成 DAG』 → AI 调 `prompt_list` 看 prompt_template 菜单（F7.3 seed）→ 挑中 `morning_briefer` 等 prompt_template → AI 调 `task_dag_apply_ops` 加 `kind=agent` 节点 + `agent_key=morning_briefer` + `cron_expr=0 8 * * *` → 用户在 UI 改一处 prompt → 点 Start → 第一个 run 跑通 → 第二天 8 点自动起第二个 run → run 历史里看到两次执行 → 一次故意触发 capability 类失败 → 智能重试升级到 Opus → 通过」
>
> **关键路径**：全程不涉及 command_card；AI 设计师挑 prompt_template、`kind=agent` 节点跑 Claude/Codex CLI 子 thread——详 ADR-014 §3。

**M3 验收硬阈值（详 ADR-010）**：
- 用例必须覆盖 DAG ≥ 10 节点跑通
- 用例必须覆盖单节点 result > 4KB（验证 ADR-006 size_cap + summarization 触发）
- 用例必须能在 metric 端点读取 `dispatch_failed_total` / `retry_count_per_node` 计数（F15.1）

**2026-05-13 backend dogfood 结果**：上述三项后端硬阈值已通过。正向 DAG `m3-dogfood-20260514-0021` 为 10/10 done；负向 DAG `m3-dogfood-20260514-0021-negative` 的 `long_node_result_rejected` 按 ADR-006 失败；metrics 默认 family 校验通过。DAG Console v1 已补齐 T5/T6/T7/F10 的用户可见首切。

**2026-05-23 U2 UI 结果**：AI 设计按钮、agent 节点微调表单、OCC 保存、只读 mermaid source 拓扑与 detail 内 sharedfile 读写摘要已落地。M3 剩余缺口不再是“是否能在 UI 设计/微调 DAG”，而是完整产品路径验证：第二天 cron 真实复跑、capability 类失败升级到 Opus 的端到端 dogfood，以及是否需要把 F8.2 动态 picker / F11.1 全局 sharedfile 锁可视化纳入下一轮体验。

**2026-05-14 final output MVP/H14 结果**：backend dogfood 后的产品入口问题先做最小闭环。run 完成后的最终产物可以继续存在于 Shared Files 中；后端通过 `task_dag_runs.metadata.final_output` 暴露“哪个 sharedfile/text/json 是本次可收最终产物”的结构化指针。H14 已让 DAG 详情读取最新 run final_output，并让 Shared Files 页面基于该指针高亮/筛选最终产物；中间产物的 TTL/retention/批量清理仍留 H15。

**F 阶段提交粒度**：每个 F1-F13 子项独立 commit；同一 task 拆 .1/.2/.3 时，每子项独立 commit（按 prefer-small-commits）。约 25 个 commit。

---

## 4. 阶段 H 加固（按需触发，不预排）

| ID | 主题 | 触发条件 | 优先级 |
|---|---|---|---|
| H1 | 错误信息人话翻译（ErrCallerIdentityRequired / verdict_lost 等） | 用户看不懂报错 | 中 |
| H2 | 节点级 retry / fallback 策略调优 | 节点频繁失败、重试策略效果差 | 中 |
| H3 | 大 DAG 性能（N>50 拆批） | 真有 50+ 节点 DAG | 低 |
| H4 | `task_dag_revisions` 表（编辑历史/回滚 UI） | 用户想 undo | 低 |
| H5 | multi-tenant / 权限模型 | 多人协作场景 | 低 |
| H6a | ~~dispatch / retry metric~~ → **F15.1 前置**（详 ADR-010） | 已前置，本行作历史指针 | — |
| H6b | 监控/告警（cron miss / run timeout） | 跑漏一天后 | 中 |
| H7 | inputs.summarization 真实实现（**硬阈值见 ADR-010**：单节点 result > 4KB 或 DAG ≥ 10 节点必触发） | 长 DAG 上下文爆炸 | 中 |
| H8 | token budget enforcement（**硬阈值见 ADR-010**：单 run 累计 token > 100K 告警，× 2 强制降级 sonnet） | 出现 token 失控成本 | 中 |
| H9 | task_post_message 原语真实落地 | 节点对话 sharedfile 不够用 | 低 |
| H10 | waiting_human HITL 完整流程 | 出现需要人审决策的场景 | 中 |
| H11 | **Hybrid 节点拓扑（v1+v2）**（从 F3.1 / F3.2 / F3.3 / F3.4 降级而来，详 ADR-014 §5）。原 F3.1 = HybridExecutor v1（automation → agent verifier 单向）；F3.2/F3.3/F3.4 = v2 多向拓扑占位（agent→automation / agent A→agent B / automation A→automation B）。地基（HybridExecConfig schema / ParseHybridConfig / router 占位 / stub）骨架阶段已铺，重启时可直接实装 executor 真业务 | 用户场景出现明显需要 hybrid 编排（agent→外部能力 / agent→agent 仲裁 / 命令卡链）的需求 | 低（prompt_template-first 下可由 agent+depends_on 两节点表达，独立 node_type 价值低） |
| H12 | **claude long result e2e 测试重做（C2 follow-up）**。2026-05-12 实测揭出 W-C2 e2e 两个设计缺陷：(1) 用 "重复 ABC N 次" 的 nonsensical prompt 被 model alignment 拒绝（16KB 实测："I can't comply... pure filler output"）；(2) 180s timeout 对 8KB 不够（实测 signal: killed）。当前实测证据：3KB 档 gotLen=4509 纯 ABC CLI 不截断（情况 A 强信号）；8KB/16KB 未拿到证据。重做要点：(a) prompt 改用真实长文本任务（如生成长 markdown 指南 / 结构化报告）；(b) timeout 提到 600s；(c) 加 32KB / 64KB 档覆盖 ADR-006 4KB cap 之外边界 | dogfood / 生产真遇到 result 截断 OR M3 验收前主动验证 | 低 |
| H13 | **A1 §5.2 + §5.3 测试补足（W-A1 reviewer C P1 follow-up）**。2026-05-12 reviewer C 揭出 W-A1 8 commit 缺两项 ADR-017 v1.2 验证：(1) §5.2 端到端 2 节点 DAG e2e 测试（agent1 → agent2 mock provider + 验 node1.status=done + node2 promote 到 ready + < 3 秒无卡 ready）未落地；(2) §5.3 race C 时序模拟（TurnCompleted + thread.stopped 50ms 内并发到达验幂等只推一次）仅静态状态测，未真模拟并发。当前补偿：subscriber 9 case + handleStopped 5+2 case + dispatch 4 case 在单测层覆盖状态机。重做要点：(a) e2e 加 2 节点 DAG fixture + mock 2 provider + bus dispatcher 端到端；(b) race C 用 goroutine + chan 模拟 50ms 并发验 idempotent；(c) reviewer C P2 case 8 补 stop.stopped 调用次数断言；(d) reviewer C P2 case 5 双路径真验证（现仅测 nil-port 短路未验 FailNode 失败时 agent runtime publishAgentStopped 仍执行）— 注入 FailNode err + spy publishAgentStopped 断言两者均被调用一次；(e) reviewer B P2 ProvideDAGSubscriberAgentThreadLookup 根治修法：改返非 nil 哨兵 lookup（GetByThreadID 永返 ErrNotFound）避免 consumer 变多后隔离失效 | dogfood / M3 验收前主动验证，或 spawned agent crash 误推顶态问题开始出现时 | 低 |
| ~~H14~~ ✅ done | **final_output 前端入口**：run/task 详情页展示 `task_dag_runs.metadata.final_output`；Shared Files 页面高亮/筛选被 final_output 引用的文件；文件型 final_output 通过 path-based shared file get 读取，小 JSON/text 在详情中展示摘要。commit `253e7244` + merge `c772b6ac` 首切；DAG Console v1 在 `66afaffb` + merge `81f0893b` 中把同一 final_output 入口搬进 console detail / run history。当前只支持单 final_output pointer，不提前实现 bundle/multi-artifact | 已由“产物在哪找”产品问题触发并落地；中间产物默认折叠/retention 深化留 H15 | — |
| H15 | **sharedfile retention / cleanup**：按 run 状态、引用关系和 TTL 清理未被 `final_output` 引用的中间 sharedfiles；final_output 引用文件按用户可收产物保留或长 TTL，删除前需要 UI 可见确认/导出策略。**首切已落地**：dashboard 暴露 `sharedFileRetention`，后端删除 final_output 引用文件时拒绝，Shared Files 页禁用最终产物删除；剩余是批量清理、TTL 预览、pinned / running run 保护和导出确认。 | sharedfile 存储增长明显或进入定时任务产品化 | 中 |

加固阶段任务**不预排**：每条 H 触发后单独立任务清单。

---

## 5. 关键里程碑映射

### 里程碑 M1 完成 = 阶段 S 全部 22 任务
**用户可见**：什么都没变，但代码内部干净了。

### 里程碑 M2 完成 = 阶段 S + T1.1, T1.2, T3.1, T3.2, T5.1, T5.2, T5.3（DAG Console v1 已覆盖）
**用户可见**：UI 上能看到 DAG → 点 Start → 节点跑起来。
**还不能**：这里不声明 cron 真实隔天复跑、能力类失败升级、批量清理等 M3/M4 产品路径已经 dogfood。
**进度**：T1.1 / T1.2 / T3.1 / T3.2 / T5.1 / T5.2 / T5.3 ✅；T6.1 / T7.1 由 DAG Console v1 一并覆盖；T8.1 / T8.2 由 U2 覆盖。后端 MCP 表面全套已在位。

### 里程碑 M3 完成 = 阶段 S + T + F 全部
**用户可见**：两大需求 + 智能重试全部端到端通。

### 你两个核心需求的精确映射

**Need 1（每日定时任务）落地必备 task**：
- S2.3, S3.1, S3.3, S4, S5（cron 字段位 + run 表）
- T1.1 ✅ / T1.2 ✅ / T3.1 ✅ / T3.2 ✅ / T7.1 ✅（DAG Console v1 已覆盖）
- F5.1, F5.2, F5.3, F6.1, F6.2（cron daemon + Run snapshot）
- F10.1 ✅（DAG Console v1 recent runs；完整 event/error timeline 后置）

**Need 2（AI 帮你设计流程）落地必备 task**：
- S1.1 ✅, S2.2 ✅, S4 ✅, S5 ✅（NodeExecutor + ops + typed schema）
- T2.1 ✅ / T2.2 ✅ / T4.1 ✅ / T4.2 ✅ / T4.3 ✅ / T4.4 ✅ / T5.2 ✅ / T8.1 ✅ / T8.2 ✅（ops 工具 + registry + UI 按钮 + designer kickoff）
- F1.1-F1.3 ✅, F4.1-F4.5 ✅, **F7.1 ✅ / F7.2 ✅ / F7.3 ✅（seed 库已落地）**, F8.1 ✅, F9.1 ✅；F8.2 动态 picker 为体验后续，不阻塞 U2 最小闭环

---

## 6. 提交规范（commit message 模板）

```
<type>(<scope>): <subject>

<body 中文：动机 + 改动要点>

<footer：关联 task ID + 蓝图节号>
```

- `type`: feat / fix / refactor / docs / test / chore
- `scope`: dag / orch / mcp / ui / migrations / executor / scheduler
- `subject`: 中文，一句话
- 关联 task ID 写在 footer，例如 `Task: S1.1 / Blueprint: §10`

示例：
```
feat(orch): 定义 NodeExecutor 接口 + NodeOutcome 类型 (S1.1)

骨架阶段补丁 1：定义 DAG 节点执行器统一接口。
- NodeExecutor.Execute 返回 NodeOutcome 而不是裸 error
- NodeOutcome 含 Status / Result / FailureClass / RetryHint
- Hooks() map 接口位预留

Task: S1.1
Blueprint: docs/plans/dag改造蓝图v2.md §10 补丁 1, §9
```

---

## 7. 每次动手前的 4 步规矩

按 `feedback/任何-清死代码-重构-大批量改动-类工作开始前固定-4-步`：

1. `git fetch origin main`
2. `git log --left-right --oneline HEAD...origin/main` 看双向差距
3. 远程领先就先 pull
4. 写"改动清单 + 验证计划"短纸条给用户

前端 task（S6.x / T5.x / T6.1 / T7.1 / T8.x / F8.x / F9.x / F10.x / F11.x）按 `feedback/threadstore-whitelist-and-hmr.md` 必须先发方案给用户确认才动手。

---

## 8. 验证清单（每个 commit 前）

| 验证项 | 命令 | 何时跑 |
|---|---|---|
| Go build | `go build ./...` | 任何 Go 改动 |
| Go test | `go test ./...` | 任何 Go 改动 |
| Go vet | `go vet ./...` | 任何 Go 改动 |
| Architecture guard | `scripts/test_with_guard.sh` | 任何 Go 改动 |
| Frontend test | `cd cmd/agent-terminal/frontend && npm test` | 任何前端改动 |
| Frontend e2e（snapshot） | `npm run test:e2e` | UI 涉及视觉时 |
| Migration dry run | `make migrate-dry-run`（如有）| 任何 migration 改动 |
| golangci-lint | `golangci-lint run` | push 前必跑 |

push 必须等用户明确指令。

---

## 9. 并行计划（哪些 task 可同时开 worker）

**S 阶段并行图**（→ 表示依赖）：

```
S1.1 → S1.2 → S1.3 / S1.4 / S1.5 (三个 stub 并行)
S2.1 → S2.2
S2.3 (独立)
S3.1 → S3.2 / S3.3 / S3.4 (后两个并行)
       → S3.5
S4.1 → S4.2
S5.1 → S5.2
S6.1 → S6.2 (前端独立链)
S7.1 → S7.2
S15.1 (依赖 S3.4)
S16.1 (最后写)
```

可同时开 4-5 个 worker：
- worker A: S1 → S2 链
- worker B: S3 migration 链
- worker C: S4 / S5 typed schema 链
- worker D: S6 前端链（独立）
- worker E: S7 状态机链

**T 阶段**：T2.2（ApplyOps）是单点瓶颈，其他可并行。
**F 阶段**：F1 / F2 / F5 / F8 各自独立链，可 4 worker 并行。

---

## 10. 当前进度（用 grep 实时查）

```bash
# Need 1 cron 进度
grep -r "next_run_at" cmd/ migrations/ 2>/dev/null | wc -l

# Need 2 ops 进度
grep -r "task_dag_apply_ops" cmd/ 2>/dev/null | wc -l

# 死代码清理进度
grep -r "auto_handoff_phase1" cmd/ internal/ 2>/dev/null | wc -l   # 目标 0

# 状态机进度
grep -r "NodeStatusReady\|NodeStatusRetrying" cmd/ 2>/dev/null | wc -l  # 目标 ≥ 2

# typed schema 进度
grep -r "FailureClass\|OnFailureStrategy" cmd/ 2>/dev/null | wc -l  # 目标 ≥ 14
```

---

## 11. 下一步

进入 **S1.1（NodeExecutor 接口 + NodeOutcome 类型）**，按以下顺序执行：

1. `git fetch origin main` + 看双向差距
2. 写"改动清单 + 验证计划"短纸条给用户
3. 用户确认后落代码
4. `go build` / `go test` / `go vet` / `scripts/test_with_guard.sh` 全过
5. commit（模板见 §6）
6. 不主动 push

按 §9 并行计划，**S1.1 + S2.1 + S3.1 + S6.1（前端独立） + S7.1** 可同时起 5 个 worker，但建议先把 **S1.1 + S1.2** 跑通再扩散，让接口契约稳定后再让其他 task 引用它。

---

## DAG 改造 follow-up issues（非阻塞）

源自路线 N 三视角审查（commit `3f6c6a80` + 注释补强 commit `1877f401`）：

- **抽 RunStatus 常量包**：当前 `running/succeeded/failed/cancelled` 4 状态字面量在 13 处分散（`contract.go` 注释、`0080` CHECK、`dag.go` switch、测试 stub）。等加新 status（如 `timeout` / `paused`）时再统一抽 `taskdag.RunStatus` 常量包，与 `0080` CHECK 单一来源对齐。当前 `0080` CHECK 已锁定全集，分散字面量风险可控。
- **MCP 错误双语化拉齐**：本次仅 StartDAG handler 内 `ErrIdempotencyKeyExhausted` / `ErrDAGAlreadyRunning` / `ErrDAGNotFound` 三个错误双语，其他 `task_*` / `commands_*` / `orchestration_*` handler 仍英文单语。下次迭代统一定义"双语错误规范"（面向 AI agent 的业务错误必须双语，内部错误英文）后批量拉齐。
- **task_get_run.Events 全量返回**（commit `360f9bfd` 实装）：当前 GetRun 一次性返回完整 Events jsonb；run 长跑后可能很大，需要长期分页 / 截断方案。M2 阶段可接受，未来 F 阶段再做。

源自 T3.1/T3.2 落地 + 审查补修（commit `360f9bfd` + `cf335dbf` + `caa9f13b` + `d1f5b0e4` + `498be56d`）：

- commit `360f9bfd` — T3.1 task_get_run（A2 不 inline 节点 / RunStore.GetRun 接通 / ErrRunNotFound 双语转译）
- commit `cf335dbf` — T3.2 task_list_runs（status 枚举对齐 0080 CHECK / mapRuns 复用 dagRunDTO / {runs} 包对象）
- commit `caa9f13b` — T3.1/T3.2 审查应修 1+2：GetRun s==nil 统一返 ErrRunStoreUnset + ListRuns 返回值从指针改值类型（与 GetRun / ApplyOps 同款）
- commit `d1f5b0e4` — T3.1/T3.2 测试加固：stubRunStore 字段化（并发友好，退出包级 var） + limit 边界 3 例 + s==nil receiver 测试 + BudgetLimit cloneInt64 独立性断言
- commit `498be56d` — T3.2 service.ListRuns max=200 cap（防呆） + taskToolDefinitions 按 writes → lifecycle → reads 重排

源自 T3 尾声 codemap 全检与合并仓运作复盘（1877f401 / 5fed929c / 9f302bf9 / 8399ea1b）：

- **§10.58 已立项 → 见会话习惯.md §10.58 — cherry-pick hook 兜底纪律**：cherry-pick / rebase 自动提交路径默认 **不** 触发 pre-commit hook（git sequencer 行为，非配置问题）。本会话曾因合并 agent 跳 hook 导致 gofmt 违规漏检。push 前手跑 `bash .githooks/pre-commit` 自检（不是 bypass，是补跑）。
- **§10.59 已立项 → 见会话习惯.md §10.59 — docs/plans 状态同步纪律**：每次 commit 改 task 状态（新增 / done / 推迟）时，必须 grep 同名 task ID 反查 codemap 02-mcp-orch / 04-app-contract / 10-store / docs/adr / 实施计划主体（不只 ledger）四源同时同步。本会话 T3.1/T3.2 落地后 04/10 codemap 漏改被扫文档 agent 发现。
- **listRunsLastFilter 字段冗余**：`dag_query_test.go:211-220` 注释承认与 stubRunStore 字段命名重叠（`lastListFilter` vs stubRunStore.lastFilter）。可去除二选一，保留 stubRunStore 一侧即可。低优先级。
- **t.Parallel() 启用**：`dag_start_test.go` / `dag_query_test.go` 多用例未启用 `t.Parallel()`；T0.5/T1.2/T3.x stub 已并发安全（commit `d1f5b0e4` 字段化 stubRunStore + race test 验证），可启用以压缩本包测试总时。
- **FinishedAt 防御拷贝断言**：`dag_query.go:158` 用 `shared.CloneTime(row.FinishedAt)` 做防御拷贝，但当前测试断言只覆盖 Events / Metadata，未掰 FinishedAt 拷贝表现。低优先级；如后续发现 FinishedAt 在调用者侧被误改再补补丁。
- **service.ListRuns limit cap=200 抽常量**：commit `498be56d` 已在 service 层 cap，但 `200` 仍是字面量（出现于 service.go + dag_query_test.go）。建议提 `defaultListRunsLimitCap = 200` 或者走 contract 层常量，避免文档与代码双多头。
- ~~**F6.4 dispatcher 对无 assignee 节点应跳过自动 dispatch**~~ ✅ **已修**（commit `d068e04c` + `91057348`，实装方案 A）：本会话用 DAG 工具做审查 e2e 演练时发现，N1 root done 后 service.CompleteNodeAndScheduleDownstream 自动 promote N2 → ready，同时 dispatcher 立刻 dispatch N2 → 因 N2 无 assigned_to → "agent id is required" → retry 耗尽 → N2 自动 failed（终态）。导致 DAG 在 M2 阶段不能做“无 assignee 描述性任务编排”。原 2 候选方案 A/B 已由主线选定方案 A：节点 assigned_to 为空或 TrimSpace 后空白时不 enqueue wakeup，节点保持 pending（依赖已满足时承担 ready 语义），等外部 agent / 人工接管。证据：DAG id=73, run audit-2026-05-11-route-n-runstore-review#run-001。详 ADR-004。

源自套餐 C 审查 + 应修落地（commit 2b3fc1c0 / 096c0957 / 5c1e4646 / 1e3d4551 + 本提交）：

- **§10.60 已立项 → 见会话习惯.md §10.60 — MCP 工具命名约定**：业务领域工具用 `<domain>_<verb>_<noun>`（如 `task_create_dag`）；基础设施工具简洁 verb 优先；已存在 `list_models` / `shared_file_list` 因 wire 兼容保留作为已知例外，不再回头改名。本次套餐 C 审查 §1 “工具命名飘忽”作为触发案例，以 “线上不改名、未来新加严守” 折衷方案落地。
- **ADR 0001 §2.10 已立项 → 见 docs/adr/0001-dag-v2-contracts.md §2.10 — DB 不变量基线规则**：枚举字段必加 CHECK / jsonb 列必加 jsonb_typeof CHECK / 跨行业务唯一性必下沉到 partial unique index。未来新建 DAG 相关表必过这 3 条 baseline。配套 migration `0081` (`task_dags.trigger` CHECK 枚举) 已随套餐 C 落地。
- **状态机 retrying → cancelled 合法转移已补**：上游 fail_fast 级联且当前节点正在退避时可转 cancelled。主线 TestRetryingCanTransitionToCancelled 守护。全量转移表计数 13 → 14。
- **T0.8 doc-sync 脚本 Check 4 真验证 hash + 脚本注释 14 → 25 修正**：早期错误分支仅 `:`（恒成功），现改为 err 报错，未来 plan 误引不存在 hash 将被 Check 4 拦下。
- **stubDashboardOrchestration 加 var _ 编译期接口断言**：未来 contract.OrchestrationService 接口扩张将在 `go build` 阶段报错，不靠 `make test` 补漏。

源自 A+ 修复 5 commit（套餐 D — handler enum 校验 + runtime fail-fast + ADR-003）：

- **§10.61 已立项 → 见会话习惯.md §10.61 — MCP 工具 enum 字段必须 handler 层 requireEnum 兜底**：schema 仅是描述层，MCP server 不强制校验；enum 字段须经 handler `requireEnum` + 包级 `var` 单源 + DB CHECK 三层互锁。详见 ADR-003。
- **ADR-003 已落地 → 见 docs/decisions/ADR-003-mcp-input-enum-validation.md — MCP 工具 input enum 校验（A+ 方案）**：选 A+（handler 兜底 + 单源 + DB CHECK）而非 P13（jsonschema 框架级），避免依赖与 wire breaking。本批 commit 已接入 4 个字段：`task_list_runs.status` / `task_start_dag.trigger_source` / `task_update_node.status` / `orchestration_launch_agent.provider`。
- **`runtime.UpdateRuntime` provider 已 fail-fast**：原 silent Warn 后放行 + snapshot 暴露 `ProviderSource="runtime-unverified"` 已根治；非法 provider 现返中英双语 error 不污染 snapshot。`ProviderSource="runtime-unverified"` 字面量保留作 defense-in-depth（fail-fast 后不可达）。
- **migration 0082 task_dag_runs.trigger_source CHECK 已落地**：CHECK IN ('manual','auto','scheduled','external','')；显式允许空串与 0074 DEFAULT '' 兼容，dev DB 预检 3 行 DISTINCT={manual,''} VALIDATE 安全通过。
- **0082 idempotent 防御修复**（commit `31f2ad75`）：启动期 autoMigrate 重跑 0082 会报 SQLSTATE 42710（constraint already exists）— 根因在 `internal/platform/db/module.go:executeMigration` 把 SQL 执行（自带 BEGIN/COMMIT）与 INSERT schema_migrations 拆成两个独立 pool.Exec，bookkeeping 补入中断会造成 partial-apply drift。本次未动 runner（defensive）赋能：把 0082 的 ADD/VALIDATE 裹入 DO 块，查 `pg_constraint`（不存在 → ADD NOT VALID；未 validated → VALIDATE；都就绪 → no-op）。后续 migration 如需类似保护 可参考 0082 范式；runner 层根治（同事务 SQL+bookkeeping）另立 follow-up。
- **runner 原子化 schema_migrations bookkeeping**（未动 / open）：0082 partial-apply drift 暴露 `executeMigration` 两步拆事务。面向未来 migration 的根治是让 SQL 执行 + INSERT schema_migrations 同事务（或明确禁 SQL 内 BEGIN/COMMIT，由 runner 统一裹事务）。本次仅加防御 DO 块保证现状可复走，root fix 担保另立。

### Wave 1 (F6.2 + F4.0) reviewer follow-up — 2026-05-11 登记 / Reviewer follow-up registered

源自 Wave 1 并行 worktree（F6.2 commit `0a7cc0ca` + F4.0 commit `131feb75`）合入主干前 quality / spec reviewer 输出。本批仅文档登记，代码改动延后到对应 F 周期内闭环 / Items here are documentation-only; code touches land in subsequent F-phase commits.

1. **F6.2 FinalizedRun 消费链路未闭环**（quality reviewer Important #1 / Important）：store 层 `CompleteNodeAndScheduleDownstreamResult.FinalizedRun` 字段已落地，但 service / dispatcher / UI 链路尚未消费 — UI 看不到 `run.status` 终态切换。F6.x 周期内补 service 层读取 + dispatcher 广播 + UI 订阅，闭环到调用方。Store-layer `FinalizedRun` is populated but downstream service / dispatcher / UI consumers are not yet wired; close the loop within F6.x.
2. **0080 status 字面量抽 Go 常量包**（quality reviewer Important #2）：当前 `done` / `failed` / `cancelled` / `skipped` / `succeeded` 字面量在 SQL CASE（`task_dag_run.sql`）+ Go 测试（`store_finalize_run_test.go`）+ fake DB switch（`test_helpers_test.go`）+ commit message 4 处硬编码。建议抽 `taskdag.TerminalNodeStatuses` / `RunTerminalStatuses` 常量集合作单一 Go 真相源；SQL 单源仍留 0080 CHECK 约束（DB 与 Go 双源不可避免，但 Go 内不再分散）。Extract `taskdag.TerminalNodeStatuses` / `RunTerminalStatuses` constant sets; SQL stays the DB-side single source.
3. **手维 sqlc 代码 drift 跟踪**（quality reviewer Important #3）：`internal/sidecar/orch/store/sqlc/task_dag_run.sql.go` 头部 `// Code generated by sqlc. DO NOT EDIT.` 但本批 F6.2 手插约 70 行 `FinalizeTaskDagRunIfAllNodesTerminal` 相关代码绕过 sqlc 生成。建议二选一：(a) 挪到 `internal/sidecar/orch/store/sqlc_manual/` 独立子包并去掉生成注释；(b) 在 `docs/sqlc-drift.md` 维护 known-drift 清单，并在生成器升级时人工对账。当前每次 `sqlc generate` 重跑会丢这 70 行。Either relocate hand-written code out of `store/sqlc/` or maintain an explicit drift inventory before the next `sqlc generate` run.
4. **F4.0 错误链 `%w: %w` 改写**（quality reviewer Important）：`internal/sidecar/orch/orchestration/dag.go::ApplyOps` 顶层 unmarshal 错误现用 `fmt.Errorf("...: %w: %s", ErrApplyOpsInvalid, err.Error())`，丢失 nodeexec.UnmarshalJSON 子错误链 — `errors.Is(err, nodeexec.ErrXxx)` 失效。改 `fmt.Errorf("...: %w: %w", ErrApplyOpsInvalid, err)`（Go 1.20+ 双 `%w` 合法）。F4.1 顺手改。Switch to double-`%w` chaining; legal since Go 1.20.
5. **dag.go 拆 `dag_ops.go` 子包**（F4.0 spec reviewer 注释 / dag.go 自带 "future split when 包拆子包" 注释）：F4.0 把 `ApplyOps` + `applyTypedOps` + `ErrApplyOpsInvalid` 接在 `dag.go` 末尾，因 orchestration 包非测试文件数已 31（pre-existing 超守卫上限 30）。F4.x 完成业务后整体重构拆 `internal/sidecar/orch/orchestration/ops/` 子包（同时把 archtest baseline 重新收敛到 ≤ 30）。Orchestration package is at 31 non-test files (pre-existing); split `ops/` subpackage after F4.x to restore the ≤ 30 guard budget.
6. **F6.2 测试边界缺口**（quality reviewer Nit #6）：当前 `store_finalize_run_test.go` 主线覆盖 happy + priority + idempotent，缺以下边界：(a) 空 DAG（0 节点直接 finalize 行为）；(b) 单节点 DAG（边界 node_counts.total=1）；(c) 已 finalize run 再触发幂等（reentrant 防御）；(d) 同一 dag_key 下 multi-run 留位（确保 WHERE dag_key=$1 AND status='running' 只命中当前 run）。F6.x 内补全。Add 4 boundary tests: empty DAG / single-node DAG / re-trigger after finalize / multi-run isolation.

### Wave 2 (F5.1 + F1.1) reviewer follow-up — 2026-05-11 登记 / Reviewer follow-up registered

源自 Wave 2 并行 worktree（F5.1 commit `c0ead0d8` cron daemon 骨架 + F1.1 commit `c8b51cd2` AgentExecutor 解码）合入主干前 quality reviewer 输出。本批仅文档登记，代码改动延后到对应 F 周期内闭环 / Items here are documentation-only; code touches land in subsequent F-phase commits.

1. **F5.1 Tick timeout 应到 Config**（quality reviewer Important #1）：`CronScheduler.Tick` 现 `context.WithTimeout(ctx, 30*time.Second)` 硬编码。F5.2 接真 SQL 扫描时若单次 Tick > 30s 会被静默 cancel，难以诊断。建议 `Config.TickTimeout time.Duration`（默认 30s）从 daemon 配置注入，便于 ops 调优。Promote hard-coded 30s Tick timeout to `Config.TickTimeout` so F5.2 SQL scans can be tuned without code change.
2. **F5.1 Tick 用 daemon 生命周期 ctx 而非 Background**（quality reviewer Important #2）：现 `Start(ctx)` 接收的 ctx 仅做启动期校验、未被 Tick goroutine 继承；daemon 关停时正在跑的 Tick 不会被取消（leak 风险隐性）。建议 `CronScheduler` 持 `rootCtx context.Context` + `cancel context.CancelFunc`，Tick 从 rootCtx 派生 timeout，Stop 时 cancel rootCtx 同步终止在跑 Tick。Hold a daemon-scoped rootCtx in CronScheduler and derive Tick contexts from it so Stop() cancels in-flight ticks.
3. **F5.1 goroutine 泄漏测试改用 goleak**（quality reviewer Important #3）：当前 `runtime.NumGoroutine` snapshot + `time.Sleep(50ms)` + ±2 数量容差，在 CI 高负载下偶发 flaky（其它 goroutine 噪声）。建议 `go.uber.org/goleak`（精准识别本测试新增 goroutine）或主动轮询等待 `runtime.NumGoroutine` 收敛到基线。Replace NumGoroutine ±2 heuristic with uber-go/goleak or convergence polling for deterministic leak detection.
4. **F1.1 unknown 错误保留信号给 F1.4**（quality reviewer Important #4）：`classifyExecError` 当前把无法识别的错误折叠成 `FailureClassTransient`，丢失"unknown"的可观测信号。F1.4 dispatcher 后续基于 FailureClass 路由 retry/backoff/escalate 时无法把"真未知"与"已知 transient"区分。建议 `FailureClass` 加 `FailureClassUnknown` 常量，或 `ErrorSummary` 加 `[unclassified]` 前缀，让下游可识别。Preserve the "unknown" signal (new FailureClassUnknown const, or `[unclassified]` prefix in summary) so F1.4 dispatcher can route differently.
5. **F1.1 buildLaunchRequestFromAgentConfig 字段覆盖不全**（quality reviewer Important #5）：`AgentConfig` 8 字段（Provider / Model / Isolation / AllowedTools / BudgetTokens / OnFailure / ...）中 build 仅映射 4 个，剩余字段静默丢弃（不报错、无日志）。AI agent 配置 Provider/Model 时不会察觉到被忽略。建议 (a) 扩展 `contract.LaunchRequest` 覆盖全字段；(b) 否则代码内显式 lint / panic 哪些字段被丢、并文档化。Either extend contract.LaunchRequest to cover all AgentConfig fields, or explicitly document/lint the silently dropped subset.
6. **F1.1 truncateErrSummary 全角省略号 → ASCII**（quality reviewer Important #6）：当前截断标记 `"…"`（U+2026）在非 UTF-8 客户端 / 老 Windows 终端 / 部分日志面板会变 `?`。建议 `"...(truncated)"` 纯 ASCII，附明确语义。Switch truncation marker from U+2026 to ASCII `"...(truncated)"` for client-agnostic rendering.

### memory_scope drift follow-up（A+ 套餐挑出 / 暂不动代码）

上一轮调研发现 `memory_scope` 在以下 **5 个来源** 不一致，且需要 **3 个独立业务决策点** 拍板，因此本批 A+ 修复仅 ADR-003 + 文档登记，**不动 memory_scope 实际代码**。

5 个来源：
1. `internal/sidecar/orch/tools/orchestration_tools.go` MCP schema：`project | user | local` （3 值）
2. `internal/sidecar/orch/tools/orchestration_tools.go::validateMemoryScope`：`"" | project | user | local`（4 值，含空串）
3. `internal/contract/agent_memory.go`（如有 const）：含 `team` 值（4 值）
4. `internal/module/agent_memory/service_test.go:111`：测试用例引用 `team` scope —— 性质待澄清（"未来要支持" vs "历史遗留 dead 引用"）
5. DB / store 实际接受的 scope 字面量集合（需现场 grep 收敛）

3 个独立决策点：
- **D1：`team` 作用域去留** — schema 缺 / service 缺 / test 引；若决定支持需补 schema + service + DB CHECK；若决定砍需删 test + const + 文档。
- **D2：空串归一** — `validateMemoryScope` 允许 `""`，service 是否把空串归一为 default？还是直接拒？handler 层兜底空串是否要走 requireEnum 的"可选字段"分支？
- **D3：service_test.go:111 性质** — dead code 还是 forward-compatible？需找 owner 拍板。

落地路径建议：
- D1/D2/D3 收敛后立 ADR-004 或扩展 ADR-003 第 6 章，定义最终 4 来源（schema / contract const / DB CHECK / handler 校验）单源真理；
- 然后在同一 commit 完成迁移：补 / 砍 `team`、归一空串、补 DB CHECK、迁 `memory_scope` 到包级 `var` 单源（与 launchAgentProviderEnum 同款）。

### MCP server schema 严格校验 follow-up（更长期）

如团队后续决定走 jsonschema 库做框架级强校验：
- 立 ADR-004 写明替代 ADR-003 第 2 章决策；
- audit 70+ tool 的 `additionalProperties` 与现有字段宽容度兼容性；
- 评估依赖（`xeipuuv/gojsonschema` 或 `santhosh-tekuri/jsonschema/v5`）的 license / size / 维护活跃度；
- 评估 wire breaking 影响（旧调用方传额外字段是否被默拒）。

### 返修轮 — 2026-05-11 登记（问题 4 + 5 + 6：token budget 硬阈值 + Hybrid 拓扑 + 观测前置）

源自第二轮返修审查（用户提出 6 个问题，其中 1 个上轮已解决，本轮解剩下 4 个）：

- **问题 1：harness ↔ DAG 双向追溯缝（spawning_thread_id）** ✅ **已前置 — 立 ADR-009 + T0.7 前置到 F1.5**：字段位不再推迟到 T8.1，改由 F1.5 在 spawn 后写入。详 ADR-009 / F1.5。
- **问题 3：AutomationExecutor 缺 automation.kind 多态分发** ✅ **已拍板 — ADR-007 Accepted + F2.0 schema 返修**：ADR-007 锁方案 A（command_card → webhook → http → shell 渐进）；F2.0 在 nodeexec/config.go 实装 Kind 字段位 + 未知 kind fail-fast。详 ADR-007 / F2.0。
- **问题 4：H7/H8 没硬阈值** ✅ **已立 ADR-010 + M3 验收锚点**：DAG ≥ 10 节点 + 单节点 result > 4KB + 单 run 累计 token > 100K（占位） 三档硬阈值，M3 验收用例必须覆盖。实装仍留 H 阶段。
- **问题 5：HybridExecutor 只一种拓扑（命名误导）** ✅ **已立 ADR-011 + F3.1 改 v1 + F3.2/3.3/3.4 占位**：v1 锁定 automation → agent verifier 单向（等同 AutomationWithVerifier 语义）；v2 多向拓扑（agent→automation / agent A→agent B / automation A→automation B）作占位行，开工前各自立 ADR-011a/b/c 子文档。**2026-05-12 后续**：F3.1 / F3.2 / F3.3 / F3.4 全部 ⏸ 降 H 阶段（H11），详 ADR-014 §5——prompt_template-first 路线下 hybrid 节点可由 agent+depends_on 两节点表达，独立 node_type 价值低。
- **问题 6：观测/告警全在 H 阶段** ✅ **H6 拆 H6a/H6b**：H6a（dispatch_failed_total / retry_count_per_node 计数 + retry≥3 告警）已前置到 F15.1（`e2d8aa6c`）；H6b（cron miss / run timeout）留 H 阶段。避免「上层调度看不见下层执行」瞎区拖到 M3 后才补。

---

## 12. 契约变更登记 / Contract Change Log

<!-- 说明：原本拟编号 §11，但§11 已被「下一步」占用，故作§12。Note: originally planned as §11, but §11 is taken by "Next Steps", so this is §12. -->

记录会向调用方暴露的契约级变更（接口签名 / 错误语义 / DB 约束 / 幂等行为等）。AI agent / 团队成员可从此处快速对齐“语义切换”决策。

### 12.1 路线 N — StartDAG 幂等语义（commit `3f6c6a80` / `1877f401`）
- **改前（路线 R）**：同 idempotency_key 重发，无论旧 run 状态如何，都返回旧 RunKey
- **改后（路线 N）**：按 status 分流：
  - running / succeeded → 返回旧 RunKey（去重网络重试 + 幂等成功结果）
  - failed / cancelled → 返回新 sentinel `ErrIdempotencyKeyExhausted`（含旧 RunKey + Status，要求换 idem 重试）
- **理由**：路线 R 把已死 run 的 RunKey 静默返给调用方，AI agent 拿到后会 wait 一个早已失败的 run，浪费 turn。路线 N 显式报错让调用方一次拿到正确信号

### 12.2 OrchestrationService 接口扩张（commit `bbf8a988` / `360f9bfd` / `cf335dbf` / `caa9f13b`）
- 新增 4 方法：StartDAG / ApplyOps / GetRun / ListRuns（Request/Response 配对）
- 接口签名一致：所有方法返值类型（不返指针），ListRuns 经 `caa9f13b` 修正

### 12.3 新增 sentinel 错误码（路线 N 系列）
- `ErrRunStoreUnset` — RunStore fx provider 未注册时启动期可能命中（commit `eb341e54` 已修）
- `ErrRunNotFound` — task_get_run 路径专用，含 run_key 上下文，中英双语转译
- `ErrIdempotencyKeyExhausted` — 路线 N 核心富错，含旧 RunKey + Status
- `ErrDAGAlreadyRunning` — F6.5 前 dag-level 单 run 约束的历史 sentinel；0089 移除该 running partial unique 后 StartDAG 正常路径不再返回
- `ErrDAGNotFound` — task_get_dag / task_get_run 路径，含 dag_key
- 双语化覆盖范围：StartDAG / GetRun 路径已双语；CreateDAG / GetDAG / UpdateNode / ApplyOps 半统一，待批量拉齐（见 follow-up §10.59 candidate / MCP 错误双语化拉齐 issue）

### 12.4 schema migration 0076-0080
- 0076 task_dag_runs partial unique（dag_key WHERE status='running'）— 历史上阻 dag-level 并发；0089 已移除并升级为 multi-run runtime node isolation
- 0077 metadata NOT NULL DEFAULT '{}'::jsonb — 消除读写不对称
- 0078 task_dag_nodes.depends_on CHECK array — NOT VALID + VALIDATE 两步
- 0079 task_dag_nodes.run_id FK + reads/writes CHECK array + run_id index
- 0080 task_dag_runs.status CHECK 4 全集（running / succeeded / failed / cancelled）

### 12.5 RunStore 接口隔离（commit `57075943` / `d27e82e7`）
- RunStore 故意不嵌入 taskdag.Store 聚合接口（保 InterfaceIsolation 预算）
- 通过 module.go ProvideRunStore(Store) RunStore 适配（非 fx.As 双绑定，因 Store 接口不嵌入 RunStore）

### 12.6 migration 0081 task_dags.trigger CHECK 枚举（commit `5c1e4646`）
- **背景**：ADR 0001 §2.10 DB 不变量基线第 1 条 — 枚举字段必加 CHECK。`task_dags.trigger` (TEXT) 之前仅靠 0075 一次性 backfill 中守取值集，后续 INSERT/UPDATE 无约束。
- **改动**：CHECK (trigger IN ('manual','auto','scheduled','external'))，NOT VALID + VALIDATE 两步（与 0078/0079/0080/0082 同款）。
- **调用方影响**：MCP `task_create_dag` / `task_update_node` schema 以及 AI agent 传入的 trigger 必须在白名单内；走远的 trigger 值现会被 DB 拒。

### 12.7 migration 0082 task_dag_runs.trigger_source CHECK 枚举（commit `f16fc58c` + `31f2ad75` idempotent 修复）
- **背景**：ADR 0001 §2.10 baseline 补齐。`task_dag_runs.trigger_source` (TEXT, 0074 DEFAULT '') 之前无枚举约束。
- **改动**：CHECK (trigger_source IN ('manual','auto','scheduled','external',''))— 显式允许空串与 0074 DEFAULT '' 兼容。
- **idempotent 防御**（`31f2ad75`）：autoMigrate 重跑可能报 42710。本 migration 裹 DO 块查 `pg_constraint`，存在/validated 状态让 ADD/VALIDATE 可复走。根治（runner 同事务）另立 follow-up。
- **调用方影响**：MCP `task_start_dag.trigger_source` handler 已接入 `requireEnum`（与 DB CHECK 同步）。

### 12.8 ADR-003 — MCP 工具 input enum 校验（A+ 方案）（commit `ef54cec5` / `4f3bb5be`）
- **决策**：选 A+（handler 兑底 + 包级 `var` 单源 + DB CHECK）而非 P13（jsonschema 框架级），避免依赖与 wire breaking。
- **helper**：`requireEnum(value, field, allowed)` 位于 `internal/sidecar/orch/tools/factory.go`，trim 后校验，中英双语 error（含 field + allowed 候选）。
- **首批接入 4 个字段**：`task_list_runs.status` / `task_start_dag.trigger_source` / `task_update_node.status` / `orchestration_launch_agent.provider`。包级 `var` 单源 → schema EnumStringSchema + handler requireEnum 同一源。
- **三层互锁**：MCP schema（描述）→ handler `requireEnum`（运行期拒）→ DB CHECK（兑底）。三层同源，任一漏改被后两层拦下。
- **memory_scope drift 未动**：5 个来源 + 3 个业务决策点（D1/D2/D3）未拍板，使本批仅 ADR-003 文档登记，代码不动（详 §10 memory_scope drift follow-up）。

### 12.9 状态机 retrying → cancelled 合法转移（commit `1e3d4551`）
- **背景**：上游 fail_fast 级联且当前节点正在退避重试时，需要能从 retrying 转 cancelled。
- **改动**：`nodeexec.ValidateTransition` 补 `retrying → cancelled` 合法边。
- **计数变化**：全量合法转移 13 → 14（主线 TestRetryingCanTransitionToCancelled 守护）。
- **对调用方影响**：dispatcher 在 fail_fast 级联路径上可直接 cancel 退避中节点，不再需先走 ready 中转。

### 12.10 runtime.UpdateRuntime provider fail-fast（commit `73651e34`）
- **改前**：传非法 provider 上下文 silent Warn 后放行，snapshot 暴露 `ProviderSource="runtime-unverified"`，下游 launchAgent 会拿不可用 provider。
- **改后**：返中英双语 error，不污染 snapshot；与 P23 README 默认值安全原则对齐（`unknown` 不默走）。
- **defense-in-depth**：`ProviderSource="runtime-unverified"` 字面量保留作不可达分支（fail-fast 后不会再出现）。

### English version

This section logs contract-level changes exposed to callers (interface signatures, error semantics, DB constraints, idempotency behavior). AI agents and team members can use this as the single point of reference for “semantic switch” decisions.

- **12.1 Route N — StartDAG idempotency** (`3f6c6a80` / `1877f401`): split by run status — running/succeeded reuse old RunKey; failed/cancelled raise sentinel `ErrIdempotencyKeyExhausted` (carries old RunKey + Status). Replaces “Route R” which silently returned dead RunKeys.
- **12.2 OrchestrationService surface** (`bbf8a988` / `360f9bfd` / `cf335dbf` / `caa9f13b`): added 4 methods (StartDAG / ApplyOps / GetRun / ListRuns) with paired Request/Response; all return values (no pointer).
- **12.3 New sentinel errors**: `ErrRunStoreUnset`, `ErrRunNotFound`, `ErrIdempotencyKeyExhausted`, legacy `ErrDAGAlreadyRunning`, `ErrDAGNotFound` — StartDAG / GetRun paths bilingual; CreateDAG / GetDAG / UpdateNode / ApplyOps half-uniform pending (see follow-up “MCP 错误双语化拉齐” issue).
- **12.4 Migrations 0076-0080**: historical running partial unique (removed by 0089 multi-run isolation); metadata NOT NULL default; depends_on/reads/writes CHECK arrays; run_id FK + index; status CHECK enum.
- **12.5 RunStore isolation** (`57075943` / `d27e82e7`): RunStore intentionally not embedded in `taskdag.Store` aggregate (preserves InterfaceIsolation budget); wired via `module.go ProvideRunStore(Store) RunStore`.


- **12.6 Migration 0081 — `task_dags.trigger` CHECK enum** (`5c1e4646`): adds `CHECK (trigger IN ('manual','auto','scheduled','external'))` (NOT VALID + VALIDATE). Satisfies ADR 0001 §2.10 baseline rule #1.
- **12.7 Migration 0082 — `task_dag_runs.trigger_source` CHECK enum** (`f16fc58c` + idempotent fix `31f2ad75`): adds `CHECK (trigger_source IN ('manual','auto','scheduled','external',''))` — empty string explicitly allowed for 0074 DEFAULT '' compatibility. Idempotent DO-block wrap (`31f2ad75`) defends against 42710 on autoMigrate replay; root fix (atomic runner SQL+bookkeeping) tracked as separate follow-up.
- **12.8 ADR-003 — MCP input enum validation (A+ scheme)** (`ef54cec5` / `4f3bb5be`): handler-layer `requireEnum` + package-level `var` single source + DB CHECK, three-layer interlock. First batch: 4 fields (`task_list_runs.status` / `task_start_dag.trigger_source` / `task_update_node.status` / `orchestration_launch_agent.provider`). `memory_scope` drift held open pending business decisions D1/D2/D3.
- **12.9 State machine — `retrying → cancelled` transition added** (`1e3d4551`): legal-transition count 13 → 14, guarded by `TestRetryingCanTransitionToCancelled`. Lets dispatcher cancel back-off-retrying nodes directly on fail_fast cascade without routing through ready.
- **12.10 `runtime.UpdateRuntime` provider fail-fast** (`73651e34`): replaces silent Warn + `ProviderSource="runtime-unverified"` snapshot pollution with bilingual error. Aligns with P23 README default-safety principle. The `runtime-unverified` literal is kept as defense-in-depth (unreachable post-fail-fast).
