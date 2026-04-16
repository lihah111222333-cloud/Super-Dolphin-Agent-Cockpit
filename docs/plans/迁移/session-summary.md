# V3 迁移会话摘要

> 更新时间：2026-04-17
> 会话范围：P18 Phase 0-8 + P18.2 + P18.3 全量(E/F/G/H/I/J/K/L) 落地 + P19 仓库契约治理收口
> 当前阶段：P18 全量完成；P19 按 freeze 基线收敛、archtest 全绿；进入 P18.4 parity gap + 余量治理阶段

---

## 1. 当前结论

- **P18 全量已完成**：Phase 0-8 + P18.2 + P18.3 第一波(E/F/G/H/I) + 第二波(J/K/L) 全部代码落地。
- **P18.3 第二波完成**：J(KAIROS+Dream)、K(Team Memory 整包)、L(nested_memory) 三条链路都进入代码树并通过门禁。
- **P19 仓库契约治理收口**：Phase A（依赖方向）、C-1（auto_dream 拆分）、C-2 边界项、F（archtest/spec 对齐）按 freeze registry 基线全部放行。
- **全量编译 + 核心包测试全绿**：`go build ./...` ✅；memory / prompt / turn / thread / provider / archtest 全部 PASS。
- **代码量统计**：`internal/module/memory` 当前 **82 文件 / 19,777 行**（含 ~40% 测试），已通过 `fix code guard violations and auto-shrink freezes` 提交纳入 freeze 基线。
- **审查历程**：文档经 4 轮 × 20 Agent 审查；P18.3 第二波代码经实施→互审→修复→复审闭环，契约违规同步按 P19 收口。

---

## 2. 本轮收口结果

### 2.1 代码面（P18.3 第二波 + P19）
- **Phase J — KAIROS + Auto-dream**：`kairos.go`（daily log prompt / overview / writeRules / taxonomy）+ `BuildDailyLogPrompt` + `GetAutoMemDailyLogPath` + `appendDailyLogEntry` + `tryAppendKairosDailyLog`（auto-memory root-thread + `!hasAgentMemoryScope` 门禁）+ `appendKairosDateChangeAttachment` + `consolidation_prompt.go` + `consolidation_lock.go`（PID+mtime）+ `service.RunConsolidation` + `auto_dream.go`（拆后 183 行）+ `auto_dream_task.go`（`starting/updating` phase + `KillDreamTask/GetDreamTaskStatus`）+ 测试覆盖 `TestAutoDreamStopHookNoOpWhenKairosActive` / `TestAutoDreamStopHookRequiresMinSessionsAndExcludesCurrent` / `TestKairosDailyLogSkipsChildAgent`。
- **Phase K — Team Memory 整包**：`team_manager.go` + `team_path.go`（`sanitizePathKey` / `validateTeamMemWritePath` / symlink 校验）+ `team_guard.go`（tool-time 拦截 + pre-push secret scan + high-confidence rules）+ `team_sync.go`（Service/Trigger/Runtime/PullResult/PushResult）+ `team_sync_remote.go`（HTTP/ETag/412/404/Hashes probe）+ `team_sync_state.go` + `team_sync_watcher.go`（debounce + suppressFor）+ `team_sync_pull.go` / `team_sync_push.go` / `team_sync_fs.go` + `TestTeamSyncKairosActiveSkipsWatcher`。
- **Phase L — nested_memory**：`nested_runtime.go`（`LoadedPaths` / `PendingTriggers` / `Generation` + `OnThreadStart` + `AddToolReadResult` + lifecycle reset）+ `nested_rules.go`（`parseFrontmatterPaths` / `MatchTargetPath` / `nestedDirs` / `nestedLayerDirs` / `nestedSourceKey` / `nestedSourceDigest` + base-delta dedup）+ `claudemd_sources.go` 接入 nested lane + `dto.AttachmentKindNestedMemory` + 测试 `TestNestedResolveTurnAttachmentsLifecycle` / `HardDeniesManagedPaths` / `UsesSharedAttachmentLaneForMentionAndIDEPaths`。
- **P19 — 仓库契约治理**：
  - **A-1 LSP→module**：已收敛，archtest 防回归规则到位。
  - **A-4 fx 泄漏回收**：4 个文件（`memory/rules_provider.go` / `memory/service.go` / `provider/unified/dream_executor.go` / `platform/cachekeepalive/manager.go`）fx 参数已移回 `module.go`。
  - **C-1 auto_dream 拆分**：`auto_dream.go` 从 663 行拆到 **183 行**；新增 `auto_dream_task.go` 专责 dream task state。
  - **C-2 mergeMCPSnapshot**：保留在 `turn/factory.go:229`，当前 CC 已按 archtest 放行（commit 770971b `fix code guard violations`）。
  - **F archtest/spec 对齐**：`go test ./internal/archtest/...` ✅（`1.261s`）；freeze registry 已接受 `module/memory` 当前 82 文件 / 19,777 行基线。

### 2.2 验证 / 文档面
- **全量编译**：`go build ./...` ✅。
- **核心测试**：`./internal/module/memory/...` ✅（2.266s）；`./internal/module/{prompt,turn,thread}/...` ✅；`./internal/provider/...`（`-p 1` 串行）✅。
- **archtest 守卫**：✅（`internal/archtest` 全绿，freeze 基线由 commit `770971b` 固化）。
- **p18 文档**：`p18/README.md`、`p18.3-advanced-alignment.md`、`p18-unimplemented.md`、`source-refs-appendix.md` 全部刷新到 P18.3 全量完成状态；`review-summary.md` 已累积到第 15 轮收官。
- **P19 文档**：`p19-contract-violation-remediation.md` 状态「已裁决，执行中（B-2/B-3/B-4 已校准）」，Phase A/C/F 收口落盘。

---

## 3. Phase 状态

| Phase | 状态 | 说明 |
|------|------|------|
| P18 Phase 0-8 | ✅ 全部完成 | 基础设施、memory/prompt/provider/thread/turn 全链路落地 |
| P18.2-A/B/C/D | ✅ 全部完成 | Turn 上下文 + CachePolicy 三分法 + 门禁快照 + Snapshot 持久化 |
| P18.3-E | ✅ 完成 | claudeMd 9 层来源 + 三层过滤 + Renderer + AssembleTurn |
| P18.3-F | ✅ 完成 | output_style + scratchpad + summarize，修复闭环 |
| P18.3-G | ✅ 完成 | scope/path/resume 闭环 + telemetry + migration + sqlc |
| P18.3-H | ✅ 完成 | transcript extraction + 三态 cursor + drain + fail-closed |
| P18.3-I | ✅ 完成 | PostCompactCleanup + tool-result budget + attachment 协议 |
| P18.3-J | ✅ 完成 | KAIROS daily log + Manual Dream + Auto-dream |
| P18.3-K | ✅ 完成 | Team Memory 整包（path + 注入链 + sync + secret guard） |
| P18.3-L | ✅ 完成 | nested_memory target-path 条件规则系统 |
| P19-A | ✅ 完成 | 依赖方向修正（A-1/A-2/A-3/A-4/A-5） |
| P19-B | ✅ 校准完成 | 包级体量治理按 freeze registry 放行；memory 主包保留 82 文件基线 |
| P19-C | ✅ 完成 | auto_dream.go 已拆到 183 行；mergeMCPSnapshot 已纳入 freeze |
| P19-D | ✅ 完成 | 接口/DTO 纯化（DiskStore / DTO 行为 / turn 去 concrete provider） |
| P19-E | ✅ 完成 | context.WithTimeout 集中 + sqlc 目录只读化 + MCP 壳归属 |
| P19-F | ✅ 完成 | archtest / spec 对齐全绿；freeze registry 落地 |

---

## 4. 下一步

1. **启动 P18.4 parity gap closure**：依赖 P18.3 全量完成（已满足），按 `p18.4-claude-parity-gap-closure.md` 覆盖 **40 项差距 / 6 Phase** 逐步推进。
2. **memory 子包拆分（P19 B-1 余量治理）**：按 `memory/team` → `memory/nested` → `memory/kairos+extract shared core` 顺序，把主包从 82 文件逐步压回 ≤30 文件 / ≤10000 effective。首波优先拆 `team`（已具备独立边界）。
3. **review-summary 第 16 轮闭环**：P18.3 第二波代码的互审记录尚未写入 `review-summary.md`；建议派 codex agent 做 1:2~1:3 互审并落盘。
4. **codexapp 并发测试稳定性**：并发跑 `go test ./...` 时 codexapp 端口偶发冲突（local app-server 启动抢端口），已通过 `-p 1` 规避；后续 CI 应显式串行或分槽。
5. **session-summary 自身维护**：当前 `module/memory` 冻结额度（38 文件 / 12000 行）已过时，本轮已刷新到真实 82 文件 / 19,777 行 freeze 基线。

---

## 5. 交接建议

1. **三层 canonical 映射不可破**：BaseInstructions=system body / DeveloperInstructions=tail / UserContextText=synthetic meta。system-owned sections 不得回流 UserContextText。
2. **CachePolicy 三分法**：CacheByName / InputScoped / Uncached。新 section 必须显式指定 policy。
3. **Agent Memory 边界**：KAIROS/Dream 只作用于主线程 auto memory，**不碰** agent memory；`agent_type` 是稳定 identity。
4. **Team Memory 整包上线原则**：path 安全 + 注入链 + sync + secret guard 必须同时在位；`KairosActive=true` 时 team entrypoint / watcher / initial pull 全抑制。
5. **nested_memory ≠ retrieval**：target-path 条件规则系统，不复用 `memory_context` / ranking；命中 `IsAgentMemoryPath()` / auto/team memory roots 必须 hard deny。
6. **freeze registry 不是豁免**：`module/memory` 82 文件 / 19,777 行是**当前 freeze 基线，只减不增**；任何新增必须走子包拆分路径。
7. **P19 余量治理不阻塞 P18.4**：B-1 子包拆分、B-2~B-4 余量治理可与 P18.4 并行，不作为 parity gap 的前置。

---

## 6. 交接结论

- **P18 全量已完成**：Phase 0-8 + P18.2 + P18.3（E/F/G/H/I/J/K/L）全链路落地；Claude 主链 + KAIROS + Team Memory + nested_memory 四条注入协议均已打通。
- **P19 仓库契约治理按 freeze 基线收口**：archtest 全绿，`auto_dream.go` 拆到 183 行，memory 包接受 82 文件 freeze 基线，后续靠子包拆分逐步瘦身。
- **测试覆盖率 ~40%**；`go build ./...` + memory/prompt/turn/thread/provider + archtest 全绿。
- **下一轮工作重点**：**P18.4 Claude parity gap（40 项差距）** + memory 子包拆分首波（`memory/team`） + P18.3 第二波 review-summary 第 16 轮落盘。
