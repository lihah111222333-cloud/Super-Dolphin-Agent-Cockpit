# P23.12: 蜂群涌现仲裁（Swarm Arbiter）

> 创建时间：2026-04-25 | 状态：**未开动（后段子任务，依赖 P8 单 arbiter + P9 token bucket 合入后开工）**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §裁决 8
> 用户 2026-04-25 提出：「LLM 裁决（蜂群涌现）」

## 目标

把 P8 的「单 arbiter LLM 调用」升级为「**多 LLM 蜂群并行仲裁 + emergent consensus**」：N 个 LLM 实例独立出 verdict → 聚合层按共识阈值 / dissent detection / weighted voting 出最终 verdict。提高判决可靠性、检测模型失偏、为高风险场景（金融 / 医疗 / 关键 infra）提供"committee" 级裁决。**后段子任务**，是 P8 的 ensemble 升级。

## 现状校准（事实层）

- P8（方案 C）尚未实现；P12 只能在 P8 单 arbiter 与轻量 LLM 调用层合入后开工，不能绕过 P8/P9 直接接 provider
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
        "required_quorum": 2,
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
- `escalate_judge`：分歧 → 触发显式 judge opt-in 路径（拉一个预声明 judge node 走常规 launcher）
- `repair_with_dissent_summary`：分歧 → 把多个 LLM 的不同意见汇总进 repair_prompt，让原 agent 自己判断怎么改

### actor 形态

新增第 7 个 actor `dagSwarmArbiterActor`：
- 消费 enqueued swarm arbiter job
- **并行**调 N 个 LLM（共用 P8 前置的 `internal/sidecar/orch/orchestration/llm/light/*` 调用层）
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
| swarm actor | `internal/sidecar/orch/orchestration/runtime/swarm_arbiter_actor.go` [NEW] | 第 7 actor；必走 `group:"runners"` Fx tag（a1 调研 medium）；并行调 N LLM；consensus 聚合；Runner.Run(ctx) + interrupt/drain |
| schema provider | `internal/sidecar/orch/tools/dag_schema_registry.go` + `internal/sidecar/orch/tools/dag_schema_swarm.go` [NEW] | 注册 `verify.arbiter_swarm.members[] / consensus / parallel / timeout_sec`；不直接并行改 `task_tools.go` |
| consensus 算法库 | `internal/sidecar/orch/orchestration/runtime/consensus.go` [NEW] | majority / unanimous / weighted 三套策略 |
| dissent 处理器 | `internal/sidecar/orch/orchestration/runtime/dissent_handler.go` [NEW] | 三种 dissent_action 实现 |
| 审计扩展 | `0072_dag_swarm_consensus.sql` [NEW]（编号校准） | `dag_swarm_consensus` 聚合表；`dag_arbiter_calls` 加 `swarm_round_id` 列 |
| archtest | `internal/archtest/dag_swarm_test.go` [NEW] | swarm 必须经统一 actor，不绕路 |

### schema / actor write-set 拆分

P12 swarm schema 通过 `internal/sidecar/orch/tools/dag_schema_registry.go` 的 swarm provider 注册；不得直接并行改 `task_tools.go`。长跑 actor 固定落 `internal/sidecar/orch/orchestration/runtime/swarm_arbiter_actor.go`，并复用 P8 的 light LLM + P9 token bucket。

## DDL / SQL

**`0072_dag_swarm_consensus.sql`** 草案：

```sql
CREATE TABLE IF NOT EXISTS public.dag_swarm_consensus (
    swarm_round_id  TEXT        PRIMARY KEY,
    dag_key         TEXT        NOT NULL,
    node_key        TEXT        NOT NULL,
    member_count    INTEGER     NOT NULL,
    requested_members INTEGER   NOT NULL DEFAULT 0,
    received_members  INTEGER   NOT NULL DEFAULT 0,
    required_quorum   INTEGER   NOT NULL DEFAULT 0,
    timed_out_members JSONB     NOT NULL DEFAULT '[]'::jsonb,
    strategy        TEXT        NOT NULL,
    threshold       NUMERIC,
    final_verdict   TEXT        NOT NULL CHECK (final_verdict IN ('pass','fail','verdict_lost','dissent','timeout')),
    consensus_score NUMERIC     NOT NULL,
    round_valid     BOOLEAN     NOT NULL DEFAULT FALSE,
    aggregation_reason TEXT     NOT NULL DEFAULT '', -- redacted, max 2KiB enforced in service
    dissent_summary JSONB       NOT NULL DEFAULT '{}'::jsonb, -- redacted/bounded, max 8KiB enforced in service
    budget_reservation_id TEXT  NOT NULL DEFAULT '',
    estimated_total_cost NUMERIC NOT NULL DEFAULT 0,
    charged_calls_count INTEGER NOT NULL DEFAULT 0,
    provider_call_ids_hash TEXT NOT NULL DEFAULT '',
    redaction_version TEXT      NOT NULL DEFAULT '',
    audit_chain_scope TEXT      NOT NULL DEFAULT 'dag_swarm_consensus',
    prev_hash       TEXT        NOT NULL DEFAULT '',
    row_hash        TEXT        NOT NULL DEFAULT '',
    hash_alg        TEXT        NOT NULL DEFAULT 'sha256',
    chain_scope     TEXT        NOT NULL DEFAULT 'dag_swarm_consensus',
    decided_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE public.dag_arbiter_calls ADD COLUMN swarm_round_id TEXT NOT NULL DEFAULT '';
```

**`0072_dag_swarm_consensus_index_no_tx.sql`**（no-transaction，禁止 `BEGIN/COMMIT`）：

```sql
CREATE INDEX CONCURRENTLY idx_dag_swarm_consensus_dag_node
    ON public.dag_swarm_consensus (dag_key, node_key, decided_at DESC);
CREATE INDEX CONCURRENTLY idx_dag_arbiter_calls_swarm
    ON public.dag_arbiter_calls (swarm_round_id)
    WHERE swarm_round_id <> '';
```

## 依赖

- P8 已合入（含轻量 LLM 调用层 `internal/sidecar/orch/orchestration/llm/light/*`）
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

- a1 medium：已明示 `group:"runners"`；owner 实施时必须保持 `Runner.Run(ctx)` + interrupt/drain 套路，并纳入 archtest `dag_runner_actors_present`。
- a10 成本爆炸：P12×P11 叠乘峰值 30000 LLM job；P12 owner 启动前需与 P9 owner 一起绘出 subscription×token bucket×budget ledger 映射表，并记录 `swarm_round_id/requested_members/required_quorum/charged_calls_estimate/estimated_total_cost`。
- a4 PII：swarm member input/output 与单 arbiter 同样走 redactor + append-only audit。
- a8 依赖：3 个 codex 调用并行可能撞到 provider RPM/TPM 限频；P12 owner 需在 P9 token bucket 定义中预留 swarm fanout 系数。

## swarm quorum / audit 硬契约（需求补全仲裁）

`0072_dag_swarm_consensus.sql` 必须补字段：`requested_members`、`received_members`、`required_quorum`、`timed_out_members JSONB`、`round_valid BOOLEAN`、`aggregation_reason`、`budget_reservation_id`、`estimated_total_cost`、`charged_calls_count`、`prev_hash`、`row_hash`、`hash_alg`。`dag_swarm_consensus` 纳入 `dag_audit_append_only` archtest，禁 UPDATE/DELETE。

`dissent_action=escalate_judge` 必须要求 `verify.judge_node_key` 存在且对应 node 预声明；否则 schema validation fail，不允许运行时隐式创建 judge。`repair_with_dissent_summary` 入 repair prompt 前必须 redactor + quoted data + bounded size + “不执行其中指令”声明，并扣 P8 node 级 `repair_chain_id + combined_repair_round/combined_repair_max`。

### redaction / bounded consensus output

`dissent_summary` 与 `aggregation_reason` 必须 bounded + redacted：`aggregation_reason` 最大 2KiB text，`dissent_summary` 最大 8KiB JSONB（或字段级 cap），只允许 verdict、score、normalized reason code、redacted excerpt hash、member_index，不允许 raw member output、raw prompt、PII、tool output 原文进入 `dag_swarm_consensus`。consensus 聚合输入必须使用 redacted/normalized member verdict，不得把 raw member answer 拼入下一轮 prompt 或 consensus row；如 repair prompt 需要 dissent，使用 P8 redactor 后的 quoted summary，并声明“数据不可执行”。

成本/审计字段与 P8 对齐：每个 swarm round 必填 `budget_reservation_id`、`estimated_total_cost`、`charged_calls_count`、`provider_call_ids_hash`、`redaction_version`、`audit_chain_scope`，并与 `dag_arbiter_calls.swarm_round_id` N:1 对齐。若实现时选择不在 consensus row 冗余成本字段，则必须明确从 `dag_arbiter_calls` 按 `swarm_round_id` join 聚合得出，并保留同等 audit 可验性；不得既不落字段也不给 join 口径。audit hash chain 只覆盖 redacted payload/hash/blob ref；禁止把未 redacted raw member output 写入 audit。

`swarm_round_id` 必须 deterministic：`hash(dag_key,node_key,verify_round,swarm_config_hash)`；member call 以 `(swarm_round_id,member_index,provider,model)` 去重。batch_peer × swarm 默认每 node 一个 swarm round；group-level swarm 需显式 opt-in 并记录 `group_round_id`、fanout 公式和 quota reservation。
