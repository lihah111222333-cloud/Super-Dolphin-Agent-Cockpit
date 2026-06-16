# 第 55 轮审查结论

## 审查范围

- `internal/sidecar/orch/orchestration/archive.go`（ArchiveAgent、normalizeArchiveAgentArgs、stopArchiveTarget、archivePersistedArchiveTarget、resolvePersistedArchiveTarget、lookupPersistedArchiveBinding、lookupPersistedArchiveThread、lookupPersistedArchiveThreadByIDs、lookupPersistedArchiveThreadByList、getPersistedArchiveThread、archiveLookupNotFound）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `archive.go:60-69` normalizeArchiveAgentArgs | 静默 fallback | `ctx == nil` 时 fallback Background | 与全项目 ctx-nil 同问题 | 改 panic |
| `archive.go:91-129` archivePersistedArchiveTarget | 数据一致性 | 先 UpdateStatus thread（line 102-112）→ 后 SetArchived binding（line 117-126）；非原子 | thread 标记 archived 但 binding 标记失败 → 不一致（thread archived 但 binding 仍 active） | 包在事务中；或 binding 失败时回滚 thread status |
| `archive.go:107-112` | 静默 | thread UpdateStatus 失败时 Warn + return error | 合理（返 error 让 caller 处理）——但 caller `ArchiveAgent` line 43-46 会返 error 给 MCP 工具层 | OK（error 上抛） |
| `archive.go:166-184` lookupPersistedArchiveBinding | 静默 | `agentBindings == nil` 时 Warn + return nil | 依赖未注入时归档操作静默跳过 binding 标记——agent 不会被标记 archived | 改为 return error（binding store 是归档的必要依赖） |
| `archive.go:186-196` lookupPersistedArchiveThread | 静默 | `agentThreads == nil` 时 Warn + return nil | 同上：thread store 未注入时归档操作静默跳过 thread 标记 | 同上 |
| `archive.go:208-226` lookupPersistedArchiveThreadByList | 性能 | `s.agentThreads.ListAll(ctx)` 全量 list 所有 thread | 大量 thread（>1000）时全量 list 慢 + 内存高 | 改为 DB 层 WHERE 查询（按 agentID 过滤） |
| `archive.go:25-58` ArchiveAgent | 弱契约 | line 33-35 `archiveErr != nil && !errAgentNotFound` 时返 error；但 errAgentNotFound 被吞 | agent 不在 runtime 中（已停止/从未启动）时 archiveErr 被吞——继续走 persisted archive 路径 | 合理设计（runtime 不存在仍可归档 persisted 数据），但应加注释 |
| `archive.go:71-89` stopArchiveTarget | 弱契约 | line 76 `s.ensureRuntimeForPersistedAgent` 是 void（第48轮 P0）；如果 rehydrate 失败，后续 stop 可能找不到 agent | 依赖第48轮 P0 的修复 | 修复第48轮 P0 后此处自动受益 |
| `archive.go:87-88` stopArchiveTarget | 弱契约 | 构造临时 `agentRuntime` 仅填 id/threadID/remoteThreadID 传给 `s.launcher.Archive` | 临时 agent 缺少 cmd/processGuard/state 等字段——launcher.Archive 实现需容忍这些零值 | 文档化 launcher.Archive 的最小输入契约 |
| `archive.go:92-96` archivePersistedArchiveTarget | 静默 | `threadID == "" && !bindingFound` 时 Warn + return false | 无 thread 无 binding 时归档操作静默成功（return false, nil）——caller 不知道什么都没做 | 改为 return error 让 caller 知道「无可归档对象」 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `archive.go:25-58` ArchiveAgent | 同步路径：resolve → stop → archive persisted；每步可能涉及 DB + RPC | 加 per-step duration 日志 |
| `archive.go:208-226` lookupPersistedArchiveThreadByList | ListAll 全量 list | 加 list count + duration 监控 |
| `archive.go:71-89` stopArchiveTarget | ensureRuntimeForPersistedAgent（DB 查询）+ archiveAgentViaLauncher（RPC）+ launcher.Archive（RPC） | 加 per-call duration |
| `archive.go:131-164` resolvePersistedArchiveTarget | 最多 3 次 DB 查询（binding + thread + binding again） | 加 total resolve duration |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `archive.go:60-69` | ctx nil fallback Background |
| `archive.go:166-184` | agentBindings nil 时 Warn + return nil |
| `archive.go:186-196` | agentThreads nil 时 Warn + return nil |
| `archive.go:92-96` | 无 thread 无 binding 时 Warn + return false, nil |
| `archive.go:175-179` | binding not found 时 Warn + return nil（合理：not found 不是 error） |
| `archive.go:210-212` | ListAll not found 时 return nil |
| `archive.go:222-225` | thread 全量扫描未找到时 Debug + return nil |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `archive.go:91-129` | thread + binding 非原子标记 |
| `archive.go:87-88` | 临时 agentRuntime 最小字段 |
| `archive.go:208-226` | 全量 ListAll 性能 |
| `archive.go:131-164` | 最多 3 次 DB 查询的 resolve 链 |
| `archive.go:25-58` | ArchiveAgent 的 error 优先级逻辑复杂 |

## 修复优先级

### P0（必须本周修）
1. **`archive.go:91-129` archivePersistedArchiveTarget thread + binding 非原子**——thread 标记 archived 成功但 binding 标记失败时数据不一致。UI 可能显示 thread archived 但 binding active → 用户困惑。改为事务包装或 binding 失败时回滚 thread。

### P1（本月）
2. `archive.go:166-184, 186-196` store nil 改 return error
3. `archive.go:208-226` ListAll 改 DB WHERE 查询
4. `archive.go:92-96` 无可归档对象改 return error
5. `archive.go:60-69` ctx nil 改 panic
6. `archive.go:87-88` 文档化 launcher.Archive 最小输入契约

### P2（下个 sprint）
7. `archive.go:25-58` ArchiveAgent 加 per-step duration 日志
8. `archive.go:131-164` resolve 链优化（减少 DB roundtrip）
9. `archive.go:71-89` stopArchiveTarget 依赖第48轮 P0 修复

## 边界条件

1. **`archive.go:25-58` ArchiveAgent 的 error 优先级设计**：line 33-35 `archiveErr != nil && !errAgentNotFound` 时返 error——这意味着 runtime stop 失败（非 not-found）会阻止归档。但 errAgentNotFound 被容忍——agent 不在 runtime 中仍可归档 persisted 数据。这是合理的：runtime 可能已经停止（进程已退出），但 DB 中仍有 binding/thread 需要标记 archived。
2. **`archive.go:71-89` stopArchiveTarget 的多路径设计**：①先尝试 `archiveAgentViaLauncher`（远端归档）；②如果远端不支持，检查 launcher 是否 `StopSettlesAgent`；③构造临时 agent 调 `launcher.Archive`。三条路径覆盖了 local/remote/hybrid 三种 launcher 模式。但临时 agent（line 87）只有 id/threadID——如果 launcher.Archive 需要更多字段（如 cwd），会 panic 或静默失败。
3. **`archive.go:208-226` lookupPersistedArchiveThreadByList 的全量扫描**：这是 fallback 路径——只有当 `lookupPersistedArchiveThreadByIDs` 未找到时才走。正常情况下 ID 查找应该命中。全量扫描是「最后手段」——处理 ID 不匹配但 agentID 字段匹配的边界情况。但 ListAll 在大数据量下不可接受。
4. **`archive.go:131-164` resolvePersistedArchiveTarget 的 3 次查询设计**：①查 binding（by agentID）；②查 thread（by agentID + hintedThreadID）；③如果 thread 返回的 agentID 与原始不同，再查一次 binding（by 新 agentID）。这是为了处理 agent ID 重映射（remote agent 可能有不同的 persisted ID）。设计合理但复杂——建议加流程图注释。
5. **`archive.go:91-129` 非原子标记的实际影响**：thread archived + binding active 的不一致状态下：①UI 列表可能仍显示 agent（binding active）；②但 thread 已 archived → 无法提交新 turn。用户看到 agent 但无法操作——需要手动修复 binding。这是 P0 因为它影响用户体验且需要人工干预。
6. **`archive.go` 整体代码质量**：函数拆分清晰（resolve → stop → archive 三阶段）；错误处理路径完整（每个 DB 调用都有 error check）；日志覆盖充分（每个关键步骤都有 Info/Warn/Debug）。主要问题是非原子操作和全量 ListAll 性能。

---

**本轮总结**：发现 1 个 P0 问题：archivePersistedArchiveTarget thread+binding 非原子标记导致数据不一致。`archive.go` 整体代码质量较高（函数拆分清晰、错误处理完整、日志覆盖充分），主要问题是非原子操作和 ListAll 性能。ArchiveAgent 的 error 优先级设计（容忍 runtime not-found）是合理的多模式归档策略。

**累计进度**：55 轮完成。cron `fd4b4728` 继续推进。

---

## 第55轮后累计状态

- 已完成：第27-55轮，共29轮
- 累计 P0 问题：**62 个**
- orchestration 包已全面覆盖（14 个生产代码文件）
- 下一轮计划：`orchestration/dag.go` 或 `orchestration/dag_query.go`（DAG CRUD 操作）
