# P23.13: JSON 严格输出模式（Strict JSON Output / 金融合规）

> 创建时间：2026-04-25 | 状态：**未开动（后段子任务，依赖 P0/P1/P2 + P8 sanitize layer）**
> authoritative：本文件 + [`README.md`](README.md) + [`RESEARCH_VERDICT.md`](RESEARCH_VERDICT.md) §裁决 8
> 用户 2026-04-25 提出：「JSON 输出模式（金融场景）」

## 目标

为 DAG node 提供「强制 JSON schema 输出」能力：声明 `nodes[].output_schema` 后，agent turn terminal 时 runtime 验证 final answer 必须严格符合 schema，否则视为 `failed` 或触发 repair。**金融 / 合规 / 审计 / 自动化 pipeline** 场景的硬需求——下游系统不能解析 free-form 文本。**后段子任务**。

## 现状校准（事实层）

- 当前 node 输出走 `result jsonb`：`migrations/0004_ack_dag.sql:62`、`task_dag_node_read.sql:1-18`；写入路径 `task_dag_node_write.sql:14-20`；**不**做 schema 验证
- `TurnCompleted.Result/Summary` 是 string：`internal/dto/turn/event.go:10-21`；hook consumer 直接落库（`hook_consumer.go:148-151`）
- 当前**无** `output_schema` 字段
- 当前**无** structured output 强制：codex / claude provider 是否支持 native structured output 由 owner 调研（codex 应支持 JSON mode；claude tool use 也算一种）

## 推荐架构

### node 级 / DAG 级声明

```json
{
  "nodes": [
    {
      "node_key": "extract_tx",
      "output_schema": {
        "type": "object",
        "required": ["tx_id", "amount", "currency"],
        "properties": {
          "tx_id": {"type": "string", "pattern": "^TX-[0-9]{12}$"},
          "amount": {"type": "number", "minimum": 0},
          "currency": {"type": "string", "enum": ["USD", "EUR", "JPY"]}
        },
        "additionalProperties": false
      },
      "output_validation": {
        "on_invalid": "repair|fail",
        "max_repair_rounds": 2
      }
    }
  ]
}
```

DAG 级 `dag.output_schema_defaults` 提供继承默认。

### 验证时机

hook consumer 接收 `TurnCompleted` → 在 enqueue tap（P23 阶段 0 ⑤ enqueue-only）前先做 **schema validate**。

> ✅ **archtest 例外已写入 P23 阶段 0 ⑤**（2026-04-25 决策）：JSON schema validate 是纯 CPU 轻量操作，**允许**在 hook tap 内同步执行（先 parse 再 validate 再 enqueue）；archtest `dag_hook_tap_enqueue_only` 对此例外白名单——**只允许 parse + validate + 轻量 CAS + enqueue，禁止网络 / LLM / 阻塞循环 / 重 DB 查询**。

具体流程：
1. 解析 `final answer` 为 JSON（jsonpath / json5 容错可选）
2. 用 `output_schema` 校验
3. valid → 推 `done`
4. invalid + `on_invalid=repair` → 推 `repairing`，把 schema + 错误信息拼进 repair_prompt 发回 agent
5. invalid + `on_invalid=fail` → 推 `failed`，`result.error` 记录 schema validation error
6. 超 `max_repair_rounds` → `failed`

### Provider 适配

需要 owner 调研 + 实现：
- **codex**：是否支持 OpenAI 风格 JSON mode / function calling 强制结构化输出？
- **claude**：tool use 是 structured output 的天然形态；可在 launch spec 加 `output_tool: { name, schema }`，让 agent 必走该 tool 出结果
- **provider 不支持 structured output**：fallback 到 prompt 工程（在 system prompt 加 schema 描述）+ runtime validate（双保险）

### 金融场景预设（与 P10 模板配合）

P10 模板库提供"金融场景预设"：
- 默认 `output_validation.on_invalid = repair`
- 默认 `verify.verdict_strategy = arbiter` + `verify.arbiter_swarm.consensus.strategy = unanimous`（P12 配合）
- 默认 `output_schema.additionalProperties = false`（拒绝多余字段）
- 默认 `dag.audit_log = true`（所有 turn / verdict / schema validation 都落审计）

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| schema | `cmd/mcp-orch/tools/task_tools.go`（扩展） | `nodes[].output_schema` + `nodes[].output_validation` + `dag.output_schema_defaults` |
| validator | `cmd/mcp-orch/orchestration/runtime/output_validator.go` [NEW] | JSON schema validate 库（Go 建议 `github.com/santhosh-tekuri/jsonschema`）；缓存 compiled schema |
| hook tap 扩展 | `cmd/mcp-orch/orchestration/hook_consumer.go`（扩展） | terminal 前先 validate；invalid 走 repair / fail |
| repair prompt 模板 | `cmd/mcp-orch/orchestration/runtime/repair_prompt.go` [NEW] | 拼装 schema + 错误信息 + 原 agent 输出，发回原 agent |
| provider 强制结构化 | `cmd/mcp-orch/orchestration/llm/light/codex_json_mode.go` [扩展] + `cmd/mcp-orch/orchestration/llm/light/claude_tool_mode.go` [扩展] | 利用 provider native 能力（如 codex JSON mode、claude tool use）；原 stub 写 `internal/llm/light/*`，被 P22 allowlist 判为位置错误，已修正 |
| state machine | `task_dag_node` schema 不变 | `verify_phase` 共用：output_validation 失败走的是主 `failed` / `repairing`，不是 verify 流程（注意区别） |
| 审计 | `0071_dag_output_validation.sql` [NEW]（编号校准） | `dag_output_validations` 表：node_key, schema_hash, valid, error_path, validated_at |
| archtest | `internal/archtest/dag_output_validation_test.go` [NEW] | output_validator 必须经统一入口；不绕路 |

## DDL / SQL

**`0071_dag_output_validation.sql`** 草案：

```sql
CREATE TABLE IF NOT EXISTS public.dag_output_validations (
    id              TEXT        PRIMARY KEY,
    dag_key         TEXT        NOT NULL,
    node_key        TEXT        NOT NULL,
    turn_id         TEXT        NOT NULL,
    schema_hash     TEXT        NOT NULL,
    valid           BOOLEAN     NOT NULL,
    error_path      TEXT        NOT NULL DEFAULT '',
    error_detail    TEXT        NOT NULL DEFAULT '',
    repair_round    INTEGER     NOT NULL DEFAULT 0,
    validated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_dag_output_validations_dag_node
    ON public.dag_output_validations (dag_key, node_key, validated_at DESC);

CREATE INDEX IF NOT EXISTS idx_dag_output_validations_invalid
    ON public.dag_output_validations (dag_key, node_key)
    WHERE valid = FALSE;
```

## 依赖

- P0 / P1 / P2 全部合入
- P8 sanitize layer 已就位（agent 输出在传 arbiter / validator 前的安全处理）
- P10 模板（金融场景预设）

## 风险

- **schema 复杂度爆炸**：用户写复杂嵌套 schema 可能让 validator 慢；缓存 compiled schema + 版本化
- **repair 循环**：`max_repair_rounds` 严格上限；超限 `failed`
- **provider native 能力差异**：codex JSON mode 与 claude tool use 不等价；fallback 行为必须文档化
- **JSON parse 容错**：agent 可能输出 ```json ... ``` 包裹的 JSON 块；validator 必须先 strip markdown 再 parse；但 strict 模式拒绝任何 wrapper
- **空输出 / 部分输出**：agent 输出空字符串 / 截断 → 视为 invalid
- **与 P8 verifier 关系**：`output_validation` 是**语法层**（schema 符合），`verify` 是**语义层**（结果对错）；两者正交，可同时开启；但顺序固定：先 schema validate（不 valid 直接 repair / fail），通过后才进 verify gate
- **archtest `dag_output_validate_before_verify` 守此顺序**：落点 `internal/archtest/dag_output_validation_test.go`
- **共用 sanitize layer**：`cmd/mcp-orch/orchestration/llm/light/sanitize.go`（P8 前置 PR 落地）；P13 的 repair_prompt 拼装也复用此 sanitize（防 prompt injection 进 agent）
- **审计开销**：金融场景 `audit_log=true` 时每次 validate 都落表，N=1000 节点 + 多轮 repair → 表会大；P9 archive 策略要覆盖
- **sensitive data 进 schema**：金融数据可能含 PII；error_detail 落表前必须 redact

## 必测项

- 简单 schema valid pass
- schema invalid + `on_invalid=repair` → 进 repairing → agent 重出 → valid → done
- schema invalid + `on_invalid=fail` → 直接 failed
- 超 `max_repair_rounds` → failed
- 嵌套 schema（数组 / 对象 / enum）
- markdown 包裹的 JSON（strip 模式 vs strict 模式）
- 空输出 / 部分输出 → invalid
- codex JSON mode 强制结构化（owner 实现后单独验）
- claude tool use 强制结构化（owner 实现后单独验）
- 金融场景预设模板：`unanimous swarm + additionalProperties=false + audit_log=true` 全链路
- output_validation × verify gate 两层串联（语法 → 语义）

## 输入材料

- README §"P13 JSON 严格输出模式（Strict JSON Output / 金融合规）"
- 用户原话：「JSON 输出模式（金融场景）」（2026-04-25）
- JSON Schema spec（draft 2020-12 推荐）
- codex JSON mode / claude tool use 文档（owner 启动前调研锚点）

## 待办

- **金融场景 PII redaction**（a4 安全 + 多调研共识）：redactor 覆盖 `repair_prompt` / `error_detail` / arbiter input/output；drop key whitelist + JSON path mask；落库前调用。
- **audit 必须 append-only + hash chain**（a4 + a5 共识）：`dag_output_validations` 与 `dag_arbiter_calls` / `dag_audit_log` 同样 hash chain；archtest `dag_audit_append_only` 守。
- a8 依赖：provider 抽象当前未暴露 schema 强制入口（`internal/contract/provider.go` + `dream_executor` TODO）；P13 owner 必须在 P8 轻量 LLM 层中并估补齐 codex JSON mode / claude tool use 接口。
- a3 规模：json-schema compile cache 必须控容量上限，避免 N=10000 节点 × hot path 占 RSS；考虑 LRU + max entries 上限。
- a10 成本：audit_log=true 在金融预设下会双写 N=1000 node × 多轮 repair 表行；P9 archive TTL 必须走 7–14 天冷走。

