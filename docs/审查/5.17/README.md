# 量化引擎潜在风险审查（2026-05-17）

## 任务口径

- 目标：审查“量化引擎”的潜在风险，累计 300 轮。
- 轮次要求：每轮不少于 30 分钟。
- 更新：用户在 2026-05-17 05:18:05 KST 指示“不需要等满，直接写入后启动下一轮”。从 Round 002 起按最新指令执行，不再等待满 30 分钟。
- 更新：用户在 2026-05-17 07:35 KST 将目标从 100 轮调整为 300 轮。
- 输出位置：`docs/审查/5.17/`。
- 参考资料：用户要求参考 `docs/审查/5.16`；当前仓库未发现该路径或同名目录，因此本目录先按仓库既有 review 文档风格记录“范围、发现、证据、后续动作”。

## 当前范围判定

当前仓库未发现字面命名的“量化引擎”。按源码证据，首批审查对象先定义为 FBSD 频次量化/分层引擎：

- `internal/module/fbsd/`：频次统计、指数衰减评分、workspace/global 合并、Hot/Warm/Cold/Frozen 分层、manifest 渲染。
- `internal/platform/toolbridge/skill_read_section.go`：成功读取 skill section 后进行 FBSD 打点。
- 后续轮次可扩展到其他评分/排名链路：`internal/module/memory/retrieval/`、`internal/module/thread/router_resolve.go`、cron/DAG 调度排序等。

## 执行状态

| 轮次 | 状态 | 文件 | 重点 |
|---:|---|---|---|
| 000 | 启动审查 | [bootstrap.md](bootstrap.md) | 范围识别；FBSD 路径隔离、持久化错误处理、生命周期竞态、manifest budget 的初步风险种子 |
| 001 | 已完成 | [round-001.md](round-001.md) | FBSD tracker 启动吞错、hostname workspace 混桶、manifest 真实预算缺失、`skill_read_section` 默认上限缺失 |
| 002 | 已完成 | [round-002.md](round-002.md) | FBSD 写盘错误被吞、`skill_read_section` name 路径校验缺失、Calls 历史无界增长 |
| 003 | 已完成 | [round-003.md](round-003.md) | memory manifest 先按更新时间截断、选择预算与最终渲染体积不一致、prefetch 错误被折叠成无结果 |
| 004 | 已完成 | [round-004.md](round-004.md) | classifier 返回 key 未限于候选集、零分候选按更新时间进入 top-K、skill disclosure tier 与 FBSD manifest tier 语义不同源 |
| 005 | 已完成 | [round-005.md](round-005.md) | forget/delete 模糊匹配缺少置信度与歧义保护、dedup overflow 低阈值自动合并删除、name/search_keys 命中未传入保护性决策 |
| 006 | 已完成 | [round-006.md](round-006.md) | LLM merge 内容缺少来源约束、多相似对依赖污染导致部分成功、similarity 子包错误脱敏依赖上层 |
| 007 | 已完成 | [round-007.md](round-007.md) | team sync 多批 push 基线过期、secret skip 不阻止 delete、ignored set 损坏后相似对重新出现 |
| 008 | 已完成 | [round-008.md](round-008.md) | watcher push 失败清 dirty、remote pull suppression 吞同路径本地编辑、fail-closed 后不自恢复 |
| 009 | 已完成 | [round-009.md](round-009.md) | turn attachment 聚合无总预算、template ordinal 不能交错内置 section、token budget 语义不等于输入容量约束 |
| 010 | 已完成 | [round-010.md](round-010.md) | thread projection token 进入 per-turn observed 查询、observation merge 可回退、UI token patch 缺少 fallback |
| 011 | 已完成 | [round-011.md](round-011.md) | compact token 为 4-rune 粗估、压缩成功态不校验真实 tokenUsage、Codex 空历史掩盖观测失败 |
| 012 | 已完成 | [round-012.md](round-012.md) | Codex last/total token 口径混用、typed/raw token 双发、缺失 window 时不注入权威上下文 |
| 013 | 已完成 | [round-013.md](round-013.md) | dashboard list 混入 unobserved 0、token observed store 能力未暴露、duration_ms 最大值合并不可修正 |
| 014 | 已完成 | [round-014.md](round-014.md) | command card 不按风险排序、prompt CWD 二次过滤会截掉匹配项、router priority 受更新时间窗口限制、命令卡风险不参与执行 |
| 015 | 已完成 | [round-015.md](round-015.md) | auto-continue 启动态漏触发、compact 成功不校验降级、manual abort 不抑制 token critical、watchdog gate 早记账、清 stuck 不清持久化计数 |
| 016 | 已完成 | [round-016.md](round-016.md) | handoff 预检忽略真实 handoffFile、progress 协议要求追加但工具覆盖写、taskId 路径拼接缺约束、worker 失败丢 seed、读失败降级为无进度 |
| 017 | 已完成 | [round-017.md](round-017.md) | ready→running 写失败被吞但 wakeup 标 sent、终态节点 spawn 指针可被覆盖、dispatch assign/enqueue 非事务、lease 空 owner、sent-unbound 不回收 |
| 018 | 已完成 | [round-018.md](round-018.md) | retry max_attempts 被 SQL 8 次硬截断、replan planner 无显式状态/绑定、smart retry CAS 竞争直接 fail、infrastructure class 不可达、permanent default replan 被抑制 |
| 019 | 已完成 | [round-019.md](round-019.md) | 持久化 turn 绑定无生产调用点、StartTurn 失败不回滚 sent wakeup、turn.completed DB 错误无 durable retry、sent-unbound 无 reclaimer、ready 未指派口径冲突 |
| 020 | 已完成 | [round-020.md](round-020.md) | DAG version snapshot 永远为 0、预算字段无闭环、无根 DAG 可启动 running、idempotency_key 直拼 run_key、final_output 缺失原因不可判读 |
| 021 | 已完成 | [round-021.md](round-021.md) | running add_node 只写模板不进当前 run、done 判定误用模板状态、base_version 读写断层、ApplyOps 与 StartDAG 发布语义交错、update_dag 多 patch 演进风险 |
| 022 | 已完成 | [round-022.md](round-022.md) | replan prompt 要求 apply_ops 但不给 base_version、缺 run scope、普通 agent prompt 不注入 DAG 上下文、dispatch_node run_id 不可见、工具描述仍称 skeleton |
| 023 | 已完成 | [round-023.md](round-023.md) | task_get_dag 不暴露 version、run snapshot 传播 0、list_runs 无节点进度摘要、get_run 不含模板漂移信息、final_output helper 只识别 file |
| 024 | 已完成 | [round-024.md](round-024.md) | run events 非完整审计流、首次 spawn 不入 events、events 50 条截断、spawn event append 软失败、dashboard/delete guard 只识别 file final_output |
| 025 | 已完成 | [round-025.md](round-025.md) | sharedfile lock_mode 未执行、agent 已存在文件复用旧输出、写入审计只有 node-router、automation sharedfile 只写 stdout、DAG 输出可写系统 handoff 路径 |
| 026 | 已完成 | [round-026.md](round-026.md) | inputs.summarization 未执行、agent 输入无边界转义、sharedfile-only 上游可变成 empty、agent/automation 输入类型漂移、空 ref 静默跳过 |
| 027 | 已完成 | [round-027.md](round-027.md) | command_card 走 sh -c 且无 sandbox、执行不绑定版本快照、args_schema 不校验、risk_level 不参与审批、非零退出统一 hard |
| 028 | 已完成 | [round-028.md](round-028.md) | command_card_runs 表有读无写、dashboard run_count/last_run_at 可能恒假、review 状态机未接 DAG、无法按 command card 聚合失败率 |
| 029 | 已完成 | [round-029.md](round-029.md) | scheduled DAG 先推进 next_run_at 再 StartDAG、单 DAG 失败中断批次、scheduled run 缺幂等 key、高风险 automation 可无人值守运行 |
| 030 | 已完成 | [round-030.md](round-030.md) | run finalization 仅挂部分路径、非 fail-fast 下游悬挂、failed run 无失败摘要、final_output 读取当前模板、finalized wakeup 残留 |
| 031 | 已完成 | [round-031.md](round-031.md) | sent-unbound 无回收、fence miss 缺分类、reclaim 不记录原因、lease 边界不一致、bind turn 缺上下文校验 |
| 032 | 已完成 | [round-032.md](round-032.md) | smart retry schema 不同源、rows=0 误当 hard cap、config CAS fail closed、replan 复用 sent 语义、append_error 无体积限制 |
| 033 | 已完成 | [round-033.md](round-033.md) | active turn 绑定未接生产流、launch/running/sent 分散写入、turn.completed 只按 thread 反查、running 不校验 assignee、fallback 可能误杀 ready |
| 034 | 已完成 | [round-034.md](round-034.md) | lifecycle hook 仅 debug log、异步审计乱序、subscriber/running/fallback 指标未导出、task status event 未发布、emitEvent 静默丢未知类型 |
| 035 | 已完成 | [round-035.md](round-035.md) | spawned agent stop best-effort、lookup 失败不补偿、remote archive 跳过本地归档、先停 runtime 再写 DB、stop metrics 未导出 |
| 036 | 已完成 | [round-036.md](round-036.md) | rehydrate 惰性触发、重建后固定 idle、仅支持 codex、exit monitor 可丢事件、persisted running 投影为 idle |
| 037 | 已完成 | [round-037.md](round-037.md) | 本地队列非持久、claim 后启动失败丢 turn、远端 busy 不排队、recover 清 active、remote submit/stop 竞态 |
| 038 | 已完成 | [round-038.md](round-038.md) | 空 turn_id 可终结当前 turn、force-idle 半更新、终态错配被降级、completion 双入口、tool approval 误投影 |
| 039 | 已完成 | [round-039.md](round-039.md) | completion 缺 active turn fence、no-node 只 stop 不补偿、多节点同完成、awaiting_verify 无 retry、FailFast 固定 false |
| 040 | 已完成 | [round-040.md](round-040.md) | upstream path 缺 run_id、depends_on 解码吞错、冲突 wakeup 保留旧 payload、ready-unassigned 无自动补派、失败 run 无 final_output |
| 041 | 已完成 | [round-041.md](round-041.md) | claim 即耗 attempt、sent-unbound 无超时回收、lease 过期成功会 fence miss、claim 入参缺校验、worker lease 空 key |
| 042 | 已完成 | [round-042.md](round-042.md) | running 写回失败不传播、spawn 指针写回失败仍 done、stale wakeup 可派发终态、automation 完成失败被吞、lifecycle hook 非阻塞 |
| 043 | 已完成 | [round-043.md](round-043.md) | command template 走 sh -c、args_schema/risk_level 不执行、outputs.schema/lock_mode 未执行、sharedfile 仅 stdout、写入失败被归 validation |
| 044 | 已完成 | [round-044.md](round-044.md) | sharedfile 磁盘/DB 非原子、删除先删 DB、已存在文件复用旧内容、读路径放宽 prefix、list limit 不夹紧、审计身份固定 |
| 045 | 已完成 | [round-045.md](round-045.md) | shared_file 工具读权限宽、写覆盖无 CAS/append、UI 删除只护 final_output、scan limit fail-closed、promote 可篡改内容、_internal state 删除无所有权 |
| 046 | 已完成 | [round-046.md](round-046.md) | command card 版本未自动归档、DAG 不固定版本、runs 统计无写入、delete 无引用保护、list 暴露模板、risk/schema 仅展示 |
| 047 | 已完成 | [round-047.md](round-047.md) | DAG batch upsert 与单条 upsert 语义不一致、StartDAG 丢 clone/root 行数、run snapshot 不冻结 command card、ApplyOps add/update 写入模型漂移 |
| 048 | 已完成 | [round-048.md](round-048.md) | CreateDAG automation config 丢失、ApplyOps add_node 不校验可执行 config、node_type 默认/未知延迟失败、hybrid 对外可建但未实现、config 非 strict |
| 049 | 已完成 | [round-049.md](round-049.md) | sharedfile 输入无 run scope、from_nodes 未完成误报 unknown、agent 输出复用旧 sharedfile、writer 身份固定、automation 写错分类 validation |
| 050 | 已完成 | [round-050.md](round-050.md) | retry attempt 双口径、infrastructure 默认普通重试、SQL 8 次硬上限、skip/ask_human 枚举未实现、策略解析损坏静默默认 |
| 051 | 已完成 | [round-051.md](round-051.md) | stale dispatching 回收无原因、launcher nil 只 claim 不执行、automation 完成失败仍 sent、sent-unbound 无自动回收、recovery 只覆盖已绑定 agent turn |
| 052 | 已完成 | [round-052.md](round-052.md) | spawning_thread_id 多命中推进所有节点、TurnCompleted 失败不继承 fail_fast、完成事务失败无 durable retry、sharedfile claim/write 崩溃窗口、depends_on 损坏静默未满足 |
| 053 | 已完成 | [round-053.md](round-053.md) | next_run_at 先推进再 StartDAG、单 DAG 失败中断 tick、scheduled_at 未入幂等键、advisory lock 可选、首次 next_run_at 依赖调用方 |
| 054 | 已完成 | [round-054.md](round-054.md) | SetActiveTurn 非原子、recovery 依赖旧 claim、lease 过期误判 observe_lost、submitted 终态事件丢弃、随机幂等键 |
| 055 | 已完成 | [round-055.md](round-055.md) | claimed_by 默认非实例唯一、recovery 仅启动一次、终态失败无 retry、progress 队列无界、drain 错误被吞 |
| 056 | 已完成 | [round-056.md](round-056.md) | schedule_expr 未校验、默认 next_run_at 过早触发、update 全量覆盖重置排程、RunOnce 被 retry 屏蔽、delete/disable 不护运行态 |
| 057 | 已完成 | [round-057.md](round-057.md) | bootstrap agent_id 未进 PrepareTurn、首次线程缺任务语义、不能用 pending_launch 路由、resolver 失败留孤儿线程、config 损坏静默降级 |
| 058 | 已完成 | [round-058.md](round-058.md) | dedupe registry best-effort 静默失效、30 分钟 zombie 误判、sweep 未接生产、provider_id 冲突保旧、更新缺 rows affected |
| 059 | 已完成 | [round-059.md](round-059.md) | handle 完成不等于 bus terminal、Codex failTurns 不发终态、observation local/provider 分桶、turn:interrupted 可被译成功、Claude stop 不发终态 |
| 060 | 已完成 | [round-060.md](round-060.md) | insight 把 provider TurnID 当 local、终态队列满即丢、observation race 只重试一次、upsert 失败无补偿、extractor 退出不 drain |
| 061 | 已完成 | [round-061.md](round-061.md) | candidate slug 用 turn id、approval cache 未查、supersede 粗暴覆盖、store 缺 wiring 无指标、promotion 继承 turn-id 命名 |
| 062 | 已完成 | [round-062.md](round-062.md) | candidate get 无项目过滤、审批看不到完整 SkillMD、approve 先改状态再写文件、reject ctx 缺 cwd、路径指纹搬迁失效 |
| 063 | 已完成 | [round-063.md](round-063.md) | 相对路径 symlink 读写逃逸、默认覆盖已有技能、import 无总量限制、project skill 可自声明 trust:user |
| 064 | 已完成 | [round-064.md](round-064.md) | project trust:user 绕过 expand 审批、legacy 审批缓存未按 repo/artifact 隔离、审批 cwd 错位、SKILL.md symlink 逃逸 |
| 065 | 已完成 | [round-065.md](round-065.md) | skill_read_section 不查审批、expanded state 未接生产、trusted body hydration 无消费方、短 hash 身份、section 无硬上限 |
| 066-300 | 待执行 | 后续追加 | 按最新指令直接连续执行，直到 300 轮完成 |

## 轮次规则

每轮必须写入一个 `round-NNN.md` 文件，并包含：

- 开始/结束时间；Round 002 起不再等待满 30 分钟。
- 本轮范围，列出实际阅读的源码、测试或文档。
- Findings，按 severity 排序，必须带文件和行号。
- 误报/已覆盖说明，避免重复报告同一问题。
- 下一轮建议范围。

如某轮没有发现新问题，也必须记录已审范围、为什么未形成 finding、剩余风险。
