# Standalone MCP 复核记录（2026-09-07）

**状态：本轮登记矩阵 34/34 PASS，0 SKIP。** 这是当前工作树的分层本机验证，不表示全部产品能力已完成；本记录不授予发布、跨任务 transport 或跨崩溃恢复能力。

> 后续自动恢复修复及 35/35 矩阵见 [2026-09-08 续验记录](mcp-orch-standalone-auto-recovery-validation-2026-09-08.md)。本页保留上轮源码与证据，不将后续通过结果追溯填入旧运行。

## 对象与证据边界

- 主工作树：`/Volumes/hello/wj/super-agent-v3`，分支 `main`，起始 HEAD `6e4a08fa51c3e5747c46e232b5c17728ade42156`。
- 使用独立 `mcp-orch serve`、官方 Codex CLI `0.153.3`、Claude CLI `2.1.237`；没有启动 V3 主程序辅助。
- 真实模型：Codex `gpt-5.6-sol`；Claude `claude-haiku-4-5-20251001`，沿用宿主默认 keychain/config，不重置登录或改写认证。
- 日志保存在主工作树 `.build-cache/orch-verification-20260907/`，不是发布制品。所有 wrapper 本机证据均为 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，不替代受信远程、Windows 或 release 验收。
- 本轮尚未提交或推送，未修改冻结基线，未跳过 hooks。用户原有未跟踪 LSP 二进制保持不动。

## 最近完整运行

`TestStandaloneAcceptanceMatrix` 动态登记 34 个场景：15 个控制/存储、8 个客户端、8 个 provider 生命周期、3 个投递场景。16 个涉及真实 daemon binary；其余明确分为 Execution/Fx、真实 SQLite 或合成协议负例，不能合称 34 个完整二进制测试。登记从源码 AST 校验新增/遗漏，任何 skip 均使矩阵失败。

最终完整运行 **34 PASS / 0 FAIL / 0 SKIP**，矩阵测试包耗时 **317.627 秒**，前置架构测试 **208.735 秒**；wrapper 退出成功。日志 `matrix-final.log`；逐场景结果 `matrix-final-summary.json`。

| 分组 | 结果 | 矩阵分组耗时 |
|---|---|---|
| control | 15/15 PASS | 6.63 秒 |
| clients | 8/8 PASS | 153.43 秒 |
| lifecycle | 8/8 PASS | 122.22 秒 |
| delivery | 3/3 PASS | 35.33 秒 |

### 中间失败及最终复测

修复前完整运行是 **32 PASS / 2 FAIL，256.42 秒**，保留 `matrix-before-recover-fix.log` 和 `matrix-before-recover-fix-summary.json`，不覆盖 RED 证据。

1. `TestStandaloneBinaryCodexLifecycleThroughMCP` 原先在 public recover 失败。修复后 **PASS，97.68 秒**：真实 cwd 随机 challenge、重复 launch、父历史 fork 继承、子线程后续轮次、stop，以及同一 ThreadID 的原生恢复与恢复后真实模型轮次全部通过。恢复后同时验证旧对话随机 proof 继承和新 cwd challenge；97.68 秒是完整生命周期，不是 fork 单步耗时。
2. `TestStandaloneBinaryFullClaudeLifecycle` 原先错误期待 Claude recover 成功，违反既有 `thread_resume` 不支持契约。纠正测试而非伪造能力后 **PASS，11.18 秒**：两轮真实模型、独立 process_stopped 终态、stop 成功但不伪造 archived，以及 recover 精确返回 `capability_unsupported`，身份、持久终态和报告均不变。

## 已取得的确定性修复证据

| 修复 | 当前可复核日志 |
|---|---|
| 已完成 turn 停止时不能覆盖模型终态；处理 durable 已提交、runtime 尚未投影的窗口 | `idle-stop.log` |
| 旧 turn outbox 晚于 process ACK/清理或新代际恢复时仍交付 DAG 并 ACK，严格身份围栏不放宽 | `old-turn-red.log`、`old-turn-green.log` |
| 失败 turn 的 runtime 目标是 Idle，不能用 public failed 展示状态判断是否已投影；DAG/ACK 失败重试 | `failed-turn-replay-red.log`、`failed-turn-replay-green.log` |
| stop 超时/取消不再错误编码为 LSP 索引错误，保留安全的操作提示 | `stop-timeout.log` |
| Codex 有 ID 的反向请求不能被 notification 快速路径吞掉 | `codex-routing.log` |
| public recover 协调持久终态、保留同 thread、推进新执行代际、避免对已清理 session 二次 Stop；失败路径保持真实已提交 head | `native-recover-orch-final-green.log`，最终聚合回归也覆盖旧 turn 延迟 outbox |
| 原生恢复归档分页、严格身份/cwd、重复 cursor/缺数据/超界 fail-fast | `native-recover-archive-green.log` |
| 不可变 provider binding 与重复创建事务回滚 | `recover-binding-identity.log` |
| 最终规模、冻结棘轮及 SSA 守卫；相关源码和 E2E 的 LSP 检查 | `native-recovery-freeze-guard.log`、`native-recovery-gopls.log`、`lsp.log`、`binary-tests-gopls.log` |

实际 binary 探针先后复现 `stopping → recover_requested` 非法迁移，以及对已清理 session 再 Stop 导致 `agent not found`。这些中间 RED 不是最终结果；`recover-native-final-probe.log` 已记录独立 binary 的 launch、report、stop、recover、send_message 全部成功，最终矩阵再次验证同一链路。

## Codex 手动恢复实现与身份边界

原 `SessionStore.CreateSession` 是严格 INSERT，真实迁移的 `trg_prevent_agent_provider_binding_rebind` 禁止改写非空 provider thread。因此放弃通用重启新 thread 和三表替换方案：不通过 UPSERT、DELETE + INSERT、删除历史或放宽触发器/主键消除失败。恢复必须遵守已有不可变绑定。

已用官方 Codex CLI 对本轮自有测试 thread 验证：已归档 thread 直接 resume 会拒绝；原生 `thread/unarchive` 后 `thread/resume` 返回同一 ThreadID。重复 unarchive 并非幂等，因此实现必须先通过官方归档列表确认，不能吞掉错误或从错误文本猜测。相应日志为 `native-resume-probe.log`、`native-unarchive-resume-probe.log` 和 `native-unarchive-idempotency-probe.log`；官方 CLI 生成的协议 schema 保存在证据目录 `codex-schema/`。

最终实现：Codex 专用内部恢复入口验证旧 owner/head/thread，保持 binding 和 thread 不变，必要时 unarchive，再真实 resume，并由既有 terminal owner 推进新执行代际。归档列表最多 128 页、每页 100 条，任何身份、cwd、分页或 API 错误立即失败。新 head 已提交但 runtime adoption 失败时保留实际持久身份，提交失败终态并回收进程；失败写入不可用时明确报告错误，不发布 ready。普通 CreateSession 仍纯创建；Claude 仍明确拒绝持久恢复。上述手动 Codex 恢复已有最终独立 binary + 真实模型证据，不等同于异常退出后的自动恢复。

## fork 耗时

最终完整运行的分段观测：父任务启动及首轮 `9.165197375s`；`Execution.Fork`（包含子进程启动）`202.144917ms`；子任务模型轮次 `19.429770625s`。前一轮对应为 `7.896073s`、`192.898583ms`、`8.954405792s`。本轮主要耗时在模型轮次，不在 fork 调用。未复现此前 102 秒；缺少旧运行的分段 trace，不能据此断言旧慢点的原因或声称已修复 102 秒问题。

## 构建、命令与源码一致性

主工作树实际构建命令：

```bash
/Volumes/hello/Go/toolchains/go1.26.5-official/bin/go build -buildvcs=false \
  -o .build-cache/orch-verification-20260907/mcp-orch ./cmd/mcp-orch
```

该探针二进制 SHA-256：`dab8e3839ab904ef8b8117afe8064c00b001c6dfc5add6309aa3f5a0b8ba55c9`。矩阵自身也从主工作树构建独立测试 daemon；不得将上述 SHA 当作所有临时矩阵 binary 的签名。

最终矩阵命令（项目根目录）：

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

最终运行前冻结 `cmd/mcp-orch`、`internal/contract` 范围的 1071 个 Go/SQL 文件；运行后重新枚举文件集合并逐文件 SHA-256 比较，完全一致。指纹记录 `source-fingerprint-final.json`，摘要 `b317eaeae40f2d2fd67ac5360cc77b69a7ba74005469bea9defd3243d63e28e0`。源码冻结后仅补齐文档；`git diff --check` 通过。日志和本地指纹均不是受信发布 provenance。

## 未被这些测试证明的能力

- 公开 HTTP operator session 不能伪造 trusted agent 身份；已有 binary 负例。真实 direct-peer 成功投递仍是进程内 trusted Fx scope，不是完整 binary trusted-agent transport。
- Claude 持久恢复仍明确不支持。
- 本记录对应的上轮源码尚未闭合 Codex 异常退出自动恢复；后续同一 daemon 内恢复已在续验中修复并验证，但不授权未知输入的自动重放。
- Room、完整 OUT/recovery-custody、跨 daemon incarnation 恢复、原生 Windows、远程 CI 和发布 provenance 仍未验收。
