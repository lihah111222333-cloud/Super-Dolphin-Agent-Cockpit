# P23.12: 蜂群涌现仲裁（Swarm Arbiter）

> 创建时间：2026-04-25 | 状态：**未开动（后段子任务，依赖 P8 单 arbiter 已合入）**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §裁决 8
> 用户 2026-04-25 提出：「LLM 裁决（蜂群涌现）」

## 目标

把 P8 的「单 arbiter LLM 调用」升级为「**多 LLM 蜂群并行仲裁 + emergent consensus**」：N 个 LLM 实例独立出 verdict → 聚合层按共识阈值 / dissent detection / weighted voting 出最终 verdict。提高判决可靠性、检测模型失偏、为高风险场景（金融 / 医疗 / 关键 infra）提供"committee" 级裁决。**后段子任务**，是 P8 的 ensemble 升级。

## 现状校准（事实层）

- P8（方案 C）已实现单 arbiter：`dagArbiterActor` 调一次轻量 LLM 调用 → 出 verdict
- 当前 schema：`verify.arbiter_provider` / `verify.arbiter_model` 只支持单一 model（见 `P8_VerificationGate.md` schema 段）
- 当前**无** ensemble / consensus / voting 逻辑
- 当前**无** dissent detection（多个 LLM 意见分歧时怎么办的处理）

## 推荐架构

### 蜂群形态

`verify.arbiter_swarm` 配置 N 个 arbiter spec：

```json
{
  "verify": {
    "verdict_strategy": "arbiter",
    "arbiter_swarm": {
      "members": [
        {"provider": "codex", "model": "gpt-5.4"},
        {"provider": "claude", "model": "claude-4.7-sonnet"},
        {"provider": "codex", "model": "gpt-5.4-thinking"}
      ],
      "consensus": {
        "strategy": "majority|unanimous|weighted",
        "threshold": 0.66,
        "weights": [1.0, 1.0, 1.5],
        "dissent_action": "verdict_lost|escalate_judge|repair_with_dissent_summary"
      },
      "parallel": true,
      "timeout_sec": 90
    }
  }
}
```

### 聚合策略

- **majority**：N/2+1 个 LLM 同意才算 verdict
- **unanimous**：全员一致才算（最严，金融场景默认）
- **weighted**：按权重加权（让 thinking 模型权重更高之类）

### dissent action（分歧处理）

- `verdict_lost`：分歧 → 落 P8 的第三类终态（保守，不冒险）
- `escalate_judge`：分歧 → 触发 opt-in B 路径（拉一个 judge node 走常规 launcher）
- `repair_with_dissent_summary`：分歧 → 把多个 LLM 的不同意见汇总进 repair_prompt，让原 agent 自己判断怎么改

### actor 形态

新增第 7 个 actor `dagSwarmArbiterActor`：
- 消费 enqueued swarm arbiter job
- **并行**调 N 个 LLM（共用 P8 前置的 `cmd/mcp-orch/orchestration/llm/light/*` 调用层）
- 等齐 N 个结果（或超 `timeout_sec` 用已到的）
- 走 consensus 算法 → 出 final verdict
- 落 N 行 `dag_arbiter_calls` 审计 + 1 行 `dag_swarm_consensus` 聚合记录

### 与 P8 兼容

- `verify.arbiter_swarm` 缺失或 `members` 长度=1 → 走 P8 单 arbiter 路径（`dagArbiterActor`）
- `members` 长度≥2 → 走 P12 swarm 路径（`dagSwarmArbiterActor`）
- 用户/agent 不必改 schema 主体，只在需要 swarm 时加 `arbiter_swarm` 块

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| swarm actor | `cmd/mcp-orch/orchestration/runtime/swarm_arbiter_actor.go` [NEW] | 第 7 actor；必走 `group:"runners"` Fx tag（a1 调研 medium）；并行调 N LLM；consensus 聚合；Runner.Run(ctx) + interrupt/drain |
| schema 扩展 | `cmd/mcp-orch/tools/task_tools.go`（扩展） | `verify.arbiter_swarm.members[] / consensus / parallel / timeout_sec` |
| consensus 算法库 | `cmd/mcp-orch/orchestration/runtime/consensus.go` [NEW] | majority / unanimous / weighted 三套策略 |
| dissent 处理器 | `cmd/mcp-orch/orchestration/runtime/dissent_handler.go` [NEW] | 三种 dissent_action 实现 |
| 审计扩展 | `0070_dag_swarm_consensus.sql` [NEW]（编号校准） | `dag_swarm_consensus` 聚合表；`dag_arbiter_calls` 加 `swarm_round_id` 列 |
| archtest | `internal/archtest/dag_swarm_test.go` [NEW] | swarm 必须经统一 actor，不绕路 |

## DDL / SQL

**`0070_dag_swarm_consensus.sql`** 草案：

```sql
CREATE TABLE IF NOT EXISTS public.dag_swarm_consensus (
    swarm_round_id  TEXT        PRIMARY KEY,
    dag_key         TEXT        NOT NULL,
    node_key        TEXT        NOT NULL,
    member_count    INTEGER     NOT NULL,
    strategy        TEXT        NOT NULL,
    threshold       NUMERIC,
    final_verdict   TEXT        NOT NULL,
    consensus_score NUMERIC     NOT NULL,
    dissent_summary JSONB       NOT NULL DEFAULT '{}'::jsonb,
    decided_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE public.dag_arbiter_calls ADD COLUMN swarm_round_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_dag_swarm_consensus_dag_node
    ON public.dag_swarm_consensus (dag_key, node_key, decided_at DESC);
CREATE INDEX IF NOT EXISTS idx_dag_arbiter_calls_swarm
    ON public.dag_arbiter_calls (swarm_round_id)
    WHERE swarm_round_id <> '';
```

## 依赖

- P8 已合入（含轻量 LLM 调用层 `cmd/mcp-orch/orchestration/llm/light/*`）
- P9 全局 token bucket 已就位（N 倍 LLM 调用必须在 quota 内）

## 风险

- **N 倍成本爆炸**：1000 node × 3 member swarm = 3000 LLM 调用 / DAG；必须在 quota 内 + UI 提示成本估算
- **timeout 一致性**：N 个 LLM 调用并行，慢的 timeout → 部分缺席 → consensus 怎么算（建议 floor=N/2 收到才算 valid round）
- **共享 LLM 限流**：3 个 codex 调用并行可能撞 codex 服务限流；必须用 P9 global token bucket 调度，不绕路
- **prompt injection × N**：sanitize layer 必须对每个 member 输入都做（继承 P8）
- **member spec 漂移**：DAG 实例化后模板 swarm spec 改变会导致同一 DAG 跨时段判决标准不一；建议实例化时 snapshot member spec
- **dissent_action=escalate_judge 的链式问题**：判决 → swarm 分歧 → 升级 judge → judge 也失败 → ?；必须有 final fallback `verdict_lost`
- **金融场景默认值**：金融 / 合规场景应推荐 `unanimous + dissent_action=verdict_lost`（最保守，宁可不判也不错判）；UI 上 P10 的模板里给"金融预设"

## 必测项

- 单 member swarm（退化为 P8 单 arbiter）
- 3 member majority pass / fail / 部分 timeout
- unanimous 一致 → pass；一票反对 → dissent_action 触发
- weighted（高权重模型主导）
- dissent_action 三路径全测
- swarm 调用占用 P9 global quota
- prompt injection 测试（每个 member 都要被 sanitize）
- audit：N 行 `dag_arbiter_calls` + 1 行 `dag_swarm_consensus` 完整落表

## 输入材料

- README §"P12 蜂群涌现仲裁（Swarm Arbiter）"
- [`P8_VerificationGate.md`](P8_VerificationGate.md)（P12 是其 ensemble 升级）
- 用户原话：「LLM 裁决（蜂群涌现）」（2026-04-25）
- 类比：Constitutional AI / debate 模式 / committee voting；金融场景的 4-eye principle

## 待办

- a1 medium：P12 stub 未明示 `group:"runners"`，必补 `Runner.Run(ctx)` + interrupt/drain 套路；archtest `dag_runner_actors_present` 守。
- a10 成本爆炸：P12×P11 叠乘峰值 30000 LLM job；P12 owner 启动前需与 P9 owner 一起绘出 subscription×token bucket 映射表。
- a4 PII：swarm member input/output 与单 arbiter 同样走 redactor + append-only audit。
- a8 依赖：3 个 codex 调用并行可能撞到 provider RPM/TPM 限频；P12 owner 需在 P9 token bucket 定义中预留 swarm fanout 系数。

