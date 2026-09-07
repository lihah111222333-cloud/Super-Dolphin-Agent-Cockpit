# Standalone CLI 自动恢复续验（2026-09-08）

**状态：本轮登记矩阵 35/35 PASS，0 FAIL、0 SKIP；定向回归及真实 binary 强杀恢复均通过。** 上轮 34/34 仅是上轮源码的登记矩阵，不覆盖本记录新增的进程崩溃、提交交错及回执确认场景。

## 范围

- 主工作树 `/Volumes/hello/wj/super-agent-v3`，`main`；沿用上轮未提交修改，不改用户认证或 LSP 二进制。
- 本轮闭环对象是**同一 daemon 内受管 Codex CLI 进程异常退出**，不是独立 daemon 的跨崩溃恢复，也不是未获批准的 OUT/recovery-custody 协议。
- 真实模型沿用 Codex `gpt-5.6-sol` 和 Claude Haiku；日志目录 `.build-cache/orch-verification-20260907-auto/` 保留本任务起始日期。
- 全部本机 wrapper 证据仍为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`；不代表远程 CI、Windows 或发布授权。

## 已复现的问题与修复方向

| 问题 | 可复核 RED | 收敛方式 |
|---|---|---|
| 自动恢复跳过持久进程终态，先清理 generation，恢复随后被 active head 围栏拒绝 | `auto-red.log` | 先持久 process_failed，再唯一 projector，再同 thread 原生恢复及新代际 CAS |
| 未完成 turn 的 managed CLI 已死时，手动 Recover 误入 legacy 本地命令路径并修改状态 | `auto-green.log` 中的剩余失败 | managed CLI 归属不依赖当前进程是否存活；未终结 active turn 拒绝恢复，不重放 |
| 成功/失败 turn 结束后 crash、旧 outbox 晚到 | `auto-red.log` | 保留原模型历史，独立进程生命周期 head；旧投影仍交付 DAG 并 ACK |
| CLI 在提交确认前退出，临时 turn 身份没有对应 durable head | `auto-red.log` 的 submit-window 子项 | 原提交 owner 暂存并消费精确退出，不能伪造 provider ACK 或重发输入 |
| completed 早于 ACK、退出随后到达，内层退出消费抢占了尚未交付的成功结果 | `early-terminal-red.log` | 精确 attempt ref 传回 service owner；先严格交付结果、确认执行器保留回执，再处理退出 |
| Unexpected=true、退出码零不触发恢复 | `early-terminal-red.log` 的 unexpected-zero-exit 子项 | 以进程 owner 的 Unexpected 为依据，不能把零退出码当作用户 stop |
| 旧请求可认领不同执行代际的保留回执 | `reconcile-generation-red.log` | 暂时移除 generation 比较后负例确实失败；恢复比较后需重新验证 |
| durable report head 序号与 runtime 自增序号分叉，恢复后对外序号可能倒退 | `report-fence-red.log`：实际复现 seq 3 → 2 与时间漂移 | canonical 报告缓存必须镜像持久 head 的序号，不制造第二套计数 |

## 回执与字段边界

新执行器入口使用原 agent/thread/launchSeq 及完整 `TurnTerminalV2`：一次查找并锁定原 session，完整 canonical 值深比较，精确认领保留对象，再复用已有 `finishTerminalReceipt`。这里只重新交付结果，不调用 provider、不直接清除 pending、不使用 Stop 代替 ACK。缺失、取消、不同身份或正在交付时明确失败。完整 canonical 值比较随类型字段动态扩展；generation mutation 的 RED 明确锁住跨代际拒绝。

## 验收分层

- 真实 SQLite/service 回归：自动恢复、旧 outbox、手动恢复拒绝、退出与提交的两种顺序、早到模型终态的普通及 typed 两入口、旧 seq、显式 close、Claude 不支持和重试预算。
- 真实 Execution/严格 sink fixture：退出后保留 receipt、精确重交付后才释放 reservation；错误身份、完整 truth、取消、inflight、持续失败及新 session 隔离。
- 新 binary 场景：`TestStandaloneBinaryCodexAutoRecoversProviderCrash`。通过本测试 daemon 的直接子进程与稳定身份强杀 Codex；不调用手动 recover，不提交额外输入触发恢复；对照原 thread 的精准 rollout `response_item/message`（`role=user`、`input_text`）计数、SQLite 历史、真实 cwd/proof 和随后模型轮次。
- 新场景已登记并通过完整矩阵（35 个），其中 17 个涉及真实 daemon binary，其余 18 个为 Execution/Fx、SQLite 或协议 fixture；不得从 fixture 成功推断所有交错已做完整二进制真实模型测试。

## 已完成的定向验证与整轮历史

| 验证 | 结果 | 日志 |
|---|---|---|
| 自动恢复、提交窗口、早到终态、旧 outbox、手动恢复拒绝、报告序号等聚合 | PASS，测试包 0.120 秒；wrapper 成功 | `final-orchestration-green.log` |
| 执行器固定 session 回执收束与错误真相/代际/取消/inflight | PASS，测试包 0.014 秒；wrapper 成功 | `reconcile-green.log` |
| 删除真实 generation 检查的 mutation | RED，generation 负例明确报 invalid reconciliation accepted；已恢复原检查 | `reconcile-generation-red.log` |
| 真 binary 强杀受管 Codex 后自动恢复 | PASS，测试包 58.696 秒；wrapper 成功 | `crash-binary-green.log` |
| 最终受影响 Go 源码 LSP、E2E LSP | 无 diagnostics，exit 0 | `final-scope-lsp.log`、`report-final-lsp.log`、`rollout-parser-lsp.log` |

真实强杀证据：本测试 Codex PID `96967` → 新 PID `97481`，保持 thread `01a07cab-a618-72c3-b54c-cfead142cd42`；测试未调用 `recover_agent` 或用新输入触发恢复。恢复后连续五秒没有新输入/活动 turn，原用户输入仅一条，原成功历史不变且新增独立 process_failed；随后新轮真实读取新文件 proof 和 cwd，并回忆原对话 proof。58.696 秒是整个测试包耗时，不是自动恢复单步耗时。

保留两次完整运行的失败历史：

1. `matrix-first-attempt.log`：33/35 PASS，489.841 秒。官方 Codex 客户端第二轮等待失败，新强杀场景首轮等待失败，尚未执行 kill。仅凭脱敏错误不足以确认官方容量原因；未扩大超时、未改认证，原样重试。
2. `matrix-rollout-parser-red.log`：34/35 PASS，318.223 秒；前述两个等待场景恢复正常，但新测试的原生历史计数为零。精确检查本轮自有线程记录发现，Codex CLI 0.153.3 实际保存的是 `response_item/message` 用户消息，而非测试误用的 `event_msg/user_message`。只修测试解析，补用户/assistant/tool/其他输入/旧格式六个断言，不接受 assistant 回显代替输入证据；随后独立强杀测试已通过。
3. 最终完整复跑 **35/35 PASS，0 SKIP**，测试包 **369.199 秒**；前置架构测试 **133.074 秒**，wrapper exit 0。原始日志 `matrix-final.log`，逐场景与指纹复核摘要 `matrix-final-summary.json`。

| 分组 | 结果 | 分组耗时 |
|---|---|---|
| control | 15/15 PASS | 6.41 秒 |
| clients | 8/8 PASS | 148.25 秒 |
| lifecycle | 9/9 PASS | 179.25 秒 |
| delivery | 3/3 PASS | 35.27 秒 |

最终矩阵中的强杀测试再次 PASS（59.70 秒）：PID `12908` → `13337`，thread `01a07cb1-86c5-74a2-a6c6-26ca42040209` 保持不变。该耗时包括首轮、五秒观察、恢复后模型轮次和清理，不是自动恢复单步耗时。

最终 fork 分段：父启动及首轮 `13.450974833s`，`Execution.Fork` 含子进程启动 `2.932628834s`，子轮 `15.208449875s`。前一轮 fork 执行段为 `190.4655ms`；未复现旧 102 秒，不能据此断言历史慢点根因已解决。

## 本轮受影响的字段链

- `remoteTurnSubmitAttempt.ref()` 冻结 agent/thread/launchSeq；由控制器显式返回到两个 service 提交入口，结果交付后传入执行器精确确认和退出消费。不得在外层改用当前快照补造旧身份。
- provisionalTurnID 只用于已有提交 ownership/buffer 围栏；原生 ACK 返回后使用真实 terminal/turn，provisional ID 不作为 provider receipt。
- `TurnTerminalV2` 完整值贯穿 retained receipt → service → reconciliation，执行器深比较生产类型的全部字段，未知新增字段不会被手工白名单漏掉。
- canonical head/report → projector cache → active-head 下 `GetReport` 的受影响字段是 ReportSeq、Report、UpdatedAt；回归覆盖读源切换及旧 outbox ACK，不宣称全仓字段链覆盖。

## 构建与源码一致性

主工作树分支 `main`，HEAD `6e4a08fa51c3e5747c46e232b5c17728ade42156`，只有主工作树。本轮尚未提交或推送；没有修改冻结基线、跳过 hooks、重置登录或处理用户原有 LSP 二进制。

```bash
/Volumes/hello/Go/toolchains/go1.26.5-official/bin/go build -buildvcs=false \
  -o .build-cache/orch-verification-20260907-auto/mcp-orch ./cmd/mcp-orch
```

独立构建产物 SHA-256：`a9b986f7ea4cc005406519223f6d3e53a7e5a2d69231abd49b943e5bd14cae78`。矩阵还会自行从主工作树构建测试 daemon；这个 SHA 不是所有临时测试 binary 的签名。

最终矩阵命令：

```bash
env -u ANTHROPIC_BASE_URL -u ANTHROPIC_API_KEY -u ANTHROPIC_AUTH_TOKEN \
ORCH_E2E_CODEX_BIN=/opt/homebrew/Caskroom/codex/0.153.3/bin/codex \
ORCH_E2E_CODEX_HOME=/Users/mima0000/.codex \
ORCH_E2E_CODEX_MODEL=gpt-5.6-sol \
ORCH_E2E_CLAUDE_BIN=/opt/homebrew/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe \
ORCH_E2E_CLAUDE_HOME=/Users/mima0000/.claude \
ORCH_E2E_CLAUDE_MODEL=claude-haiku-4-5-20251001 \
scripts/test_with_guard.sh --host-test medium -tags=e2e ./cmd/mcp-orch \
-run '^TestStandaloneAcceptanceMatrix$' -timeout=600s -count=1 -v
```

最终运行前冻结 `cmd/mcp-orch`、`internal/contract` 内 1080 个 Go/SQL 文件；运行后重新枚举集合并逐文件比较 SHA-256，完全一致。`source-fingerprint-final.json` 摘要为 `f51088ee17c4bad2e990189fc5caba1a93d84c4b4d2a8cb58891bdb6d935869c`，算法为有序 file-hash map 的紧凑 JSON SHA-256。测试之后只补充文档；`git diff --check` 通过。所有指纹与日志是本机复核证据，不是受信发布 provenance。

## 明确未覆盖

存储不可写时阻断恢复，不保证存储恢复后自动重投一次性退出；协议消费者尚未接收并持久化的最后一帧不保证保留。Claude 持久恢复不支持。公开 HTTP trusted-agent 成功 transport、Room、跨 daemon incarnation、完整 OUT、Windows 和受信发布验收仍不在本轮已验证范围。
