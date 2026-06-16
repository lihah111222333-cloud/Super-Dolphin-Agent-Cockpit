# 最终裁定（2/3）

本文件只覆盖 10 份总报告复核中的 `verify-2` 组：

- `docs/plans/迁移/review-module-orch.md`
- `docs/plans/迁移/review-module-skill.md`
- `docs/plans/迁移/review-module-workspace.md`

## Blocker 验证

### B2 runKey：✅

- `runKey` 在进入路径拼接前已经校验，`validateRunKey` 明确拒绝 `..`、`/`、`\`，且 `buildRun` 会先调用该校验再进入 `resolveWorkspacePath`：`internal/module/workspace/service.go:77-89`、`internal/module/workspace/service.go:115-123`。

### B3 merge 门闩：✅

- 状态常量已补齐 `failed` 与 `merging`：`internal/module/workspace/service.go:29-33`。
- `MergeRun` 现在先要求 `active`，再原子迁移到 `merging`，随后才进入执行阶段：`internal/module/workspace/service.go:220-240`、`internal/module/workspace/service.go:289-311`。
- 成功路径从 `merging -> merged`，失败/冲突路径从 `merging -> failed`：`internal/module/workspace/service_merge.go:32-55`、`internal/module/workspace/service_merge.go:58-96`。

### B5 悬空接口：✅

- 对 `internal` 范围做 `workspace_symbol/text_search` 后，`ToolCallResponder`、`ThreadRepository`、`HandlerProvider` 均为零命中；当前相关接口面只剩 `orchestration.Service / SessionCleaner / TurnStarter` 与 `workspace.Service`：`internal/sidecar/orch/orchestration/contract.go:11-38`、`internal/module/workspace/contract.go:11-20`。

## 总审报告复审

### review-module-orch（verify-2 / 10 份总复核中的其中 1 份）

- `✅ 已修复` `agent.submit*` 无真实执行链
  `claimTurnWork` 现在保留完整 `submission` 到 `turnWork`，`startTurnExecution` 会调用 `turnStarter.StartTurn(submission)`，默认实现再进入 `turn.PrepareTurn -> turn.StartTurn -> session.StartTurn` 真执行链；`SelectedSkills` 和 `OutputSchema` 也会继续传入 turn 准备阶段：`internal/sidecar/orch/orchestration/service.go:301-321`、`internal/sidecar/orch/orchestration/helpers.go:140-174`、`internal/module/turn/orchestration_starter.go:22-60`、`internal/dto/turn/model.go:11-19`。

- `🔴 仍 Blocker` 缺失 `agent.saveSubAgent` / `agent.deleteSubAgent` / `agent.persistSubAgentBinding`
  当前 handler 列表仍无这 3 个 key，`Service` 接口也没有对应方法：`internal/sidecar/orch/orchestration/rpc.go:17-75`、`internal/sidecar/orch/orchestration/contract.go:11-28`。

- `🔴 仍 Blocker` `agent.launch` 仍不是 V2 wire
  `launchParams` 仍只接受 `agentId/name/cwd/command/parentId/env`，没有 V2 的 `id/prompt/instructions/dynamic_tools/config`；handler 仍返回 `nil`；`AgentID` 为空仍会直接报 `agent id is required`：`internal/sidecar/orch/orchestration/rpc_types.go:10-17`、`internal/sidecar/orch/orchestration/rpc.go:17-18`、`internal/sidecar/orch/orchestration/helpers.go:293-300`。

- `🔴 仍 Blocker` report requester / auto-delivery / alias 仍是最小实现
  `RememberReportRequest` 只把 requester 追加到当前 agent；`HandleReportEvent` 仍只有内存更新与 requester drain，文件内还保留了“后续接 UI timeline”的 TODO；`orchestration/report` 仍只是 `GetReport` alias，而 `reportParams.Report` 继续未消费：`internal/sidecar/orch/orchestration/report.go:73-169`、`internal/sidecar/orch/orchestration/rpc.go:73-75`、`internal/sidecar/orch/orchestration/rpc_types.go:102-105`。

- `🔧 当场修复` `SetReport` 仍是死接口
  `Service` 里仍声明 `SetReport`，实现也还在，但全仓当前无调用者；最直接的修法是二选一：要么删掉接口与实现，要么补上真实调用链：`internal/sidecar/orch/orchestration/contract.go:19`、`internal/sidecar/orch/orchestration/report.go:39-49`。

- `🔴 仍 Blocker` stall auto-recover 仍会误判且恢复有损
  stall 判定仍只看 `turn_running + updatedAt 超 30s`；恢复仍会先 `stopProcess`，再清空 `activeTurnID` 后重启；对外恢复原因仍写死为 `manual`。这意味着长时间无事件的正常 turn 仍可能被误判，且 in-flight turn 仍可能丢失：`internal/sidecar/orch/orchestration/runner_actor.go:26-44`、`internal/sidecar/orch/orchestration/recover.go:16-58`。

- `⏳ 推迟 P7` stop 生命周期时序仍偏早
  `StopAgent` 仍在进程真正退出前先 `removeSession` 和 `publishAgentStopped`；真实 `process_exited -> stopped/failed` 迁移还在后面的 waiter 回调里：`internal/sidecar/orch/orchestration/service.go:127-140`、`internal/sidecar/orch/orchestration/service.go:355-394`。

- `🔧 当场修复` `agent.stop` 返回 shape 仍不是 V2
  handler 还在直接 `return nil, svc.StopAgent(...)`；如果要对齐 V2，这里可直接改为返回 `{success:true}`，不影响 service 语义：`internal/sidecar/orch/orchestration/rpc.go:40-42`。

### review-module-skill（verify-2 / 10 份总复核中的其中 1 份）

- `🔴 仍 Blocker` `command/exec` 仍不是 V2 contract
  当前 RPC 参数仍是 `command + args + cwd`，`Service` 签名也同样没有 `argv/env`；handler 继续把这 3 个字段直接透传给 `ExecCommand`。这意味着调用方仍无法按 V2 显式 overlay `env`：`internal/module/skill/rpc_types.go:26-30`、`internal/module/skill/contract.go:13`、`internal/module/skill/rpc.go:51-53`、`internal/module/skill/exec.go:114-120`。

- `⏳ 推迟 P7` skills 写路径仍缺少 notify side effect
  `WriteRemote`、`WriteSkillContent`、`WriteSummary` 现在都只是写文件然后返回结果，没有任何 `skills/changed` 或 notify 调用；对应的变更名提取 helper 仍未被接入：`internal/module/skill/skills_fs.go:132-179`、`internal/module/skill/skills_match.go:189-204`。

- `⏳ 推迟 P7` `skills/config/read` 和 configured auto-match 仍是 stub
  `ReadConfig` 仍显式返回 `binding_source: "stub"` 的占位结构；configured collector 里也还保留了 `TODO(P7)`，说明 provider/backing-store 语义尚未接回：`internal/module/skill/skills_fs.go:143-157`、`internal/module/skill/skills_match.go:59-80`。

- `🔧 当场修复` `collectChangedSkillNames` 仍是未使用死代码
  该函数仍只剩声明本身，没有任何引用；若短期内不接 notify 链，最简单的处理就是删除：`internal/module/skill/skills_match.go:189-204`。

- `⏳ 推迟 P7` 覆盖率不足警告仍保留
  本次按 LSP 只读复核，没有重新跑覆盖率；因此这一项沿用报告结论，当前仍应视为未完成的测试债：`docs/plans/迁移/review-module-skill.md:261-325`。

### review-module-workspace（verify-2 / 10 份总复核中的其中 1 份）

- `✅ 已修复` `CreateRun` 的 runKey 路径逃逸
  当前已在 `buildRun` 阶段调用 `validateRunKey`，并显式拒绝 `..`、`/`、`\`：`internal/module/workspace/service.go:77-89`、`internal/module/workspace/service.go:115-123`。

- `🔴 仍 Blocker` `MergeRun` 仍不是 V2 的真实写回 merge
  现在仍然只基于 store 中已跟踪文件做哈希评估；真正写回 source tree 的位置仍是 `TODO`；`applyFileUpdates` 依旧只做 `UpsertFile`；`deleteRemoved` 仍未实现：`internal/module/workspace/service_helpers.go:73-148`、`internal/module/workspace/service.go:325-339`、`internal/module/workspace/service_merge.go:17-31`。

- `✅ 已修复` merge 状态门闩缺失
  当前已补上 `active -> merging -> merged/failed` 三阶段迁移，且状态迁移通过 `TransitionRunStatus` 做 expected-state 校验：`internal/module/workspace/service.go:29-33`、`internal/module/workspace/service.go:220-240`、`internal/module/workspace/service.go:289-311`、`internal/module/workspace/service_merge.go:32-96`。

- `🔴 仍 Blocker` bootstrap 守卫仍缺 symlink / 大小 / 总量限制
  bootstrap 仍只做相对路径归一化后复制；`copyFile` 继续 `os.Open + Stat` 跟随 symlink，仍未见 `Lstat`、`ModeSymlink`、单文件大小或总量上限检查：`internal/module/workspace/service.go:176-186`、`internal/module/workspace/service_helpers.go:20-50`、`internal/module/workspace/service_helpers.go:53-71`。

- `⏳ 推迟 P7` `ListRuns` 仍未恢复 V2 的上限钳制
  现在仍只在 `limit <= 0` 时回落默认值，没有 `limit > 5000` 的上限保护：`internal/module/workspace/service.go:193-202`。

- `⏳ 推迟 P7` `MergeRun(dryRun)` 仍不发事件
  `req.DryRun` 仍在状态迁移和事件发送前直接返回 `dryRunMerge` 结果；`dryRunMerge` 本身也只算结果，不发状态变化或 merge/error 事件：`internal/module/workspace/service.go:220-227`、`internal/module/workspace/service.go:325-339`。

## 当前结论

- `verify-2` 这 3 份报告里，已经确认修复的 blocker 是 `B2 runKey`、`B3 merge 门闩`、`orchestration agent.submit* 真执行链`、`B5 悬空接口`。
- 仍然卡迁移 gate 的项主要集中在 3 处：`module/orchestration` 的 V2 method/wire/report/recover 缺口，`module/skill` 的 `command/exec` contract 漂移，以及 `module/workspace` 的“非真实 merge + bootstrap 守卫缺失”。
- 其余 `ListRuns` 上限、dry-run event、skills notify、configured matcher、stop 时序、覆盖率等项，可以下沉到 `P7`，但不应再被误记为“已完成”。

## 互审

### 对 final-verdict-1 的批判

1. `B1 submit 执行链：✅修复完成` 判得过满，证据只覆盖了“能启动 turn”，没有覆盖“submission payload 完整保真”。`final-verdict-1` 在 `docs/plans/迁移/final-verdict-1.md:5-10` 直接判 B1 完成；但当前 `agent.submit` RPC 参数仍只有 `agent_id/prompt/images/files`，没有 `selectedSkills/manualSkillSelection/outputSchema`，见 `internal/sidecar/orch/orchestration/rpc_types.go:70-77`；`submissionFromParams` 也仍只填 `AgentID/ThreadID/Inputs`，见 `internal/sidecar/orch/orchestration/rpc.go:90-100`；更进一步，`ManualSkillSelection` 虽仍存在于 `TurnSubmission`，但 queued turn 进入 turn service 时已被丢弃，`prepareQueuedTurnInput` 只传 `Inputs/Skills/OutputSchema/AgentID/ThreadCaps`，见 `internal/dto/turn/model.go:11-19`、`internal/module/turn/orchestration_starter.go:54-61`、`internal/module/turn/contract.go:27-41`。这更接近“执行链修通，但 submission 语义仍不完整”，不该写成无保留的“修复完成”。

2. 把 `Cleanup/RestorePending/PendingSnapshot` 下沉到 P7 的理由站不住。`final-verdict-1` 在 `docs/plans/迁移/final-verdict-1.md:23` 说“live callback 主链已闭合，不阻塞当前交付”；但这 3 个生命周期方法当前全部 `references=0`，见 `internal/platform/rpc/approval_lifecycle.go:10-43`；而真实 dispatch 路径在没有 `bridge/server` 时会直接返回，不提供恢复或替代分发，见 `internal/platform/rpc/approval.go:149-163`；同时同一份裁定自己又在 `docs/plans/迁移/final-verdict-1.md:28` 承认 `request_user_input` 统一桥接仍缺失，而 push bridge 仍只推 3 类事件，见 `internal/platform/rpc/push.go:16-19`、`internal/platform/rpc/push.go:75-92`。在 fallback/restore 都没接的前提下，把 approval lifecycle 轻判为 P7，论证不充分。

3. `turn/interrupt` 与 `turn/forceComplete` 被一起降到 P7，也偏乐观。`final-verdict-1` 在 `docs/plans/迁移/final-verdict-1.md:66-67` 只把这两项列为 P7；但原总审已明确写出 V2 对 `turn/interrupt` 期望 `{"ok": true}`，对 `turn/forceComplete` 还要求独立 contract，见 `docs/plans/迁移/review-module-turn.md:81-98`。当前 RPC handler 仍都直接返回 `nil`，见 `internal/module/turn/rpc.go:60-71`；`ForceCompleteTurn` 也仍只是 `session.Interrupt(..., Source: "force_complete")` 包装，见 `internal/module/turn/service.go:138-153`。这是公开 RPC 合约的直接不兼容，不是单纯的“后续可打磨”。

### 对 final-verdict-3 的批判

1. `review-platform-rpc` 把 approval callback method family 从 blocker 降成 P7，降级过头。`final-verdict-3` 在 `docs/plans/迁移/final-verdict-3.md:21-25` 把这一组问题都列为 P7；但原总审明确把“approval method family / callback 兼容层”列入关键缺失能力，见 `docs/plans/迁移/review-platform-rpc.md:146-157`、`docs/plans/迁移/review-platform-rpc.md:301-304`。当前代码也确实仍固定发 `tool/approval/request`，仓内没有任何 `CallbackMethod:` 覆盖点，见 `internal/platform/rpc/approval_events.go:13`、`internal/platform/rpc/approval_events.go:37-39`。如果裁定口径还是“V2 兼容”，这不该被轻放到 P7。

2. `review-platform-rpc` 复核漏掉了更重的问题：`request_user_input` 统一桥接。`final-verdict-3` 全文对 `request_user_input` 是零命中；但原总审把它单列为关键缺失能力，见 `docs/plans/迁移/review-platform-rpc.md:304-305`、`docs/plans/迁移/review-platform-rpc.md:313-318`。当前代码里 `ApprovalManager.RequestUserInput` 仍只是在 approval 侧把 `Kind` 设成 `request_user_input`，见 `internal/platform/rpc/approval.go:98-103`；而 push bridge 仍只桥接 `ui/state/changed`、`turn/started`、`turn/completed` 三类事件，见 `internal/platform/rpc/push.go:16-19`、`internal/platform/rpc/push.go:75-92`。这不是小缺口，被完全漏掉会让 `final-verdict-3` 的 RPC 结论失真。

3. `review-module-orch` 复核写得过于泛化，漏了原报告里更具体的 wire 问题。`final-verdict-3` 在 `docs/plans/迁移/final-verdict-3.md:54` 只说“report 链仍是最小内存版”；但原总审还明确指出 `orchestration/report` 现在直接调用 `svc.GetReport`，`reportParams.Report` 完全未使用，`SetReport` 也是死接口，见 `docs/plans/迁移/review-module-orch.md:84-90`。当前代码确实如此：`orchestration/report` 仍直接走 `GetReport`，见 `internal/sidecar/orch/orchestration/rpc.go:73-75`；`reportParams` 仍定义了 `Report` 字段但没有消费者，见 `internal/sidecar/orch/orchestration/rpc_types.go:102-105`；`SetReport` 仍只有声明和实现本身，见 `internal/sidecar/orch/orchestration/contract.go:19`、`internal/sidecar/orch/orchestration/report.go:39-49`。把这些都压成一句“最小内存版”，会掩盖一个明确的 RPC wire 错误。

4. `review-store` 漏掉了原总审的第三个主要发现。`final-verdict-3` 在 `docs/plans/迁移/final-verdict-3.md:82-86` 只复核了 `sqlc` 漂移、独立 `AgentThreadBindingStore` 缺失、`dbquery` placeholder 三项，却没有回到原报告第 3 个主要发现“错误处理没有形成统一包装层”，见 `docs/plans/迁移/review-store.md:23-32`。当前 store 层这个问题仍在：例如 `thread.Store` 多处仍是原样 `return nil, err`，见 `internal/store/thread/store.go:17-56`；`binding.Store` 也基本原样透传错误，见 `internal/store/binding/store.go:18-25`、`internal/store/binding/store.go:74-80`。这不是已经解决，而是被漏审了。
