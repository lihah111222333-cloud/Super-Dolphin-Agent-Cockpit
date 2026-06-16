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

hook consumer 通过 `dag_terminal_tap` / output validation provider 接收 `TurnCompleted` → 做 bounded parse / payload size cap / enqueue；完整 **schema validate** 在 `outputValidationActor` worker 中执行，且必须早于 terminal status 写入与 P8 verify。

> ✅ **archtest 例外已更新为交叉验证裁决**（2026-04-25）：hook tap 只允许 bounded parse + enqueue；完整 validate 移出 hook 热路径，避免 a3 指出的 CPU/DB 热点，同时保留 a4/a5 要求的 terminal 前强校验。

具体流程：
1. 按 `output_validation.parse_mode` 解析 `final answer`：`strict|strip_markdown|lenient`；金融 preset 默认 `strict`，禁止 json5/jsonpath/markdown wrapper 容错
2. 用 `output_schema` 校验
3. valid → 若 `verify.enabled=true` 则 enqueue P8 verify；否则 terminal CAS 推 `done`
4. invalid + `on_invalid=repair` → `output_validation_phase=repairing`，主 `status` 保持 `running`，把 schema + 错误信息拼进 repair_prompt 发回 agent
5. invalid + `on_invalid=fail` → 主 `status=failed`，`result.error` 只记录 redacted validation error
6. 超 `max_repair_rounds` → `failed`

### Canonical schema algorithm（冻结）

P13 canonical hash 固定采用 RFC 8785 JSON Canonicalization Scheme (JCS)，不得写“或等价规范”留自由度。算法顺序固定：
1. 仅接受 JSON Schema draft 2020-12 的 JSON object；拒绝非 object schema。
2. 在 schema registry 内解析本 DAG/template 允许的 internal `$ref`（`#/$defs/...`）；禁止 remote `$ref`、HTTP/file `$ref`、循环 `$ref`。解析失败 hard fail。
3. 合并 DAG `output_schema_defaults` 与 node `output_schema`；defaults 只允许填充缺失字段，不得覆盖 node 显式字段。
4. 对所有 `type: object` 递归注入 `additionalProperties:false`，除非该 object 显式设置 `additionalProperties` 或 `unevaluatedProperties`；若两者同时出现，按 JSON Schema 2020-12 语义校验但 hash 保留原字段。
5. 删除非语义 UI helper 字段（只允许白名单，如 `x-ui-*`）前必须记录 `schema_version`；不得删除 validation keyword。
6. 用 RFC 8785 JCS 排序/数字/字符串规则 canonicalize，`schema_hash = sha256(canonical_bytes)`。

边界：`default` 是 annotation，不在 runtime validation 时自动改写输出；`$ref` 解析只发生在 schema canonicalization/compile 阶段；`additionalProperties:false` 的递归注入只影响 object schema，不影响 map-like schema 显式 opt-out。

### Provider 适配

需要 owner 调研 + 实现：
- **codex**：`internal/dto/provider/turn.go` 已有 `OutputSchema` 形态；当前只能视为 pass-through / prompt hint / provider optimization，**不是 runtime hard guarantee**。即使 provider 声称 structured output，P13 合规仍以本地 runtime validate + audit 为准。
- **claude**：当前 claudecli 只能把 schema 拼进 prompt；tool use 强制结构化需另行实现，不得视为已具备。
- **最终合规保证**：provider native structured output 只作为优化；runtime validate + audit 才是 P13 hard guarantee。

### 金融场景预设（与 P10 模板配合）

P10 模板库提供"金融场景预设"：
- 默认 `output_validation.on_invalid = repair`
- 默认 `verify.verdict_strategy = arbiter` + `verify.arbiter_swarm.consensus.strategy = unanimous`（P12 配合）
- 默认 `output_schema.additionalProperties = false`（拒绝多余字段，递归生效，schema 显式 opt-out 除外）
- 默认 `output_validation.parse_mode = strict`、`schema_draft = 2020-12`、hash-chain audit
- 默认 `dag.audit_log = true`（所有 turn / verdict / schema validation 都落审计）
- 若启用 `verify.arbiter_swarm.consensus.strategy = unanimous`，该 preset 依赖 P12；未合入 P12 时隐藏 swarm 选项，只保留单 arbiter + strict JSON

## 改动清单

| 模块 | 文件落点 | 说明 |
|---|---|---|
| schema provider | `internal/sidecar/orch/tools/dag_schema_registry.go` + `internal/sidecar/orch/tools/dag_schema_output.go` [NEW] | 注册 `nodes[].output_schema` + `nodes[].output_validation` + `dag.output_schema_defaults`；不直接并行改 `task_tools.go` |
| validator | `internal/sidecar/orch/orchestration/runtime/output_validator.go` [NEW] | JSON schema validate 库（Go 建议 `github.com/santhosh-tekuri/jsonschema`）；缓存 compiled schema |
| output validation actor | `internal/sidecar/orch/orchestration/runtime/output_validation_actor.go` [NEW] | hook tap enqueue 后、terminal 前 validate；invalid 走 repair / fail；挂入 `group:"runners"` |
| repair prompt 模板 | `internal/sidecar/orch/orchestration/runtime/repair_prompt.go` [NEW] | 拼装 schema + 错误信息 + 原 agent 输出，发回原 agent |
| agent turn provider output schema 适配 | provider / launcher turn 请求路径（owner 开工前按当前 provider 代码事实补精确文件） | 将 `nodes[].output_schema` 传入 agent turn；provider native structured output 只作优化，不是 hard guarantee |
| arbiter light JSON mode 适配 | `internal/sidecar/orch/orchestration/llm/light/codex_json_mode.go` [扩展] + `internal/sidecar/orch/orchestration/llm/light/claude_tool_mode.go` [扩展] | 仅服务 P8/P12 arbiter 的结构化 verdict 输出；不得替代 agent final answer runtime validate |
| state machine | `task_dag_nodes` schema 扩展 | 新增独立 `output_validation_phase`（或等价独立列）；output_validation 失败不得把 `repairing` 写入主 `status`，主 `status` 只在最终 fail/done 时变更 |
| 审计 | `0073_dag_output_validation.sql` [NEW]（编号校准） | `dag_output_validations` 表：node_key, schema_hash/version/draft, valid, error_path, hash-chain, validated_at；索引拆 no-transaction migration |
| hook tap provider | `internal/sidecar/orch/orchestration/output_validation_tap.go` [NEW] + `hook_tap_registry.go` | 注册 output validation provider；主 hook consumer 不直接扩分发 |
| archtest | `internal/archtest/dag_output_validation_test.go` [NEW] | output_validator 必须经统一入口；不绕路 |

### schema / hook write-set 拆分

P13 output schema 字段通过 `internal/sidecar/orch/tools/dag_schema_registry.go` 的 output provider 注册；不得直接并行改 `task_tools.go`。hook 集成通过 `output_validation_tap.go` 注册到 `dag_terminal_tap` / `hook_tap_registry.go`；主 hook consumer 只做 bounded parse + enqueue。长跑校验固定落 `internal/sidecar/orch/orchestration/runtime/output_validation_actor.go`。P8 的 arbiter verdict 表/列应对 `parsed_verdict` 加 CHECK（`pass|fail|verdict_lost|repair|invalid` 或 P8 冻结 enum），P13 不新增另一套 verdict enum。

## DDL / SQL

**`0073_dag_output_validation.sql`** 草案：

```sql
ALTER TABLE public.task_dag_nodes ADD COLUMN output_validation_phase TEXT NOT NULL DEFAULT '' CHECK (output_validation_phase IN ('','pending','validating','repairing','failed','passed'));
ALTER TABLE public.task_dag_nodes ADD COLUMN output_validation_round INTEGER NOT NULL DEFAULT 0;
ALTER TABLE public.task_dag_nodes ADD COLUMN output_schema_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE public.task_dag_nodes ADD COLUMN output_schema_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE public.task_dag_nodes ADD COLUMN output_parse_mode TEXT NOT NULL DEFAULT 'strict';

CREATE TABLE IF NOT EXISTS public.dag_output_validations (
    id              TEXT        PRIMARY KEY,
    dag_key         TEXT        NOT NULL,
    node_key        TEXT        NOT NULL,
    turn_id         TEXT        NOT NULL,
    schema_hash     TEXT        NOT NULL,
    schema_version  INTEGER     NOT NULL DEFAULT 0,
    schema_draft    TEXT        NOT NULL DEFAULT '2020-12',
    schema_hash_alg TEXT        NOT NULL DEFAULT 'sha256',
    valid           BOOLEAN     NOT NULL,
    error_path      TEXT        NOT NULL DEFAULT '',
    error_detail    TEXT        NOT NULL DEFAULT '',
    redacted_raw_hash TEXT      NOT NULL DEFAULT '',
    raw_blob_ref     TEXT        NOT NULL DEFAULT '',
    repair_chain_id  TEXT        NOT NULL DEFAULT '',
    repair_round    INTEGER     NOT NULL DEFAULT 0,
    prev_hash       TEXT        NOT NULL DEFAULT '',
    row_hash        TEXT        NOT NULL DEFAULT '',
    hash_alg        TEXT        NOT NULL DEFAULT 'sha256',
    chain_scope     TEXT        NOT NULL DEFAULT 'dag_output_validations',
    validated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**`0073_dag_output_validation_index_no_tx.sql`**（no-transaction，禁止 `BEGIN/COMMIT`）：

```sql
CREATE INDEX CONCURRENTLY idx_dag_output_validations_dag_node
    ON public.dag_output_validations (dag_key, node_key, validated_at DESC);

CREATE INDEX CONCURRENTLY idx_dag_output_validations_invalid
    ON public.dag_output_validations (dag_key, node_key)
    WHERE valid = FALSE;
```

## 依赖

- P0 / P1 / P2 全部合入
- P8 sanitize layer 已就位（agent 输出在传 arbiter / validator 前的安全处理）
- P10 模板（金融场景预设）
- P12 已合入（仅当启用金融 swarm/unanimous preset；否则该 preset 的 swarm 字段必须 feature-gated 隐藏）

## 风险

- **schema 复杂度爆炸**：用户写复杂嵌套 schema 可能让 validator 慢；必须限制 schema size/depth/properties/regex 复杂度，compiled schema LRU + per-tenant 上限 + validation timeout/payload cap 为 hard limit
- **repair 循环**：`max_repair_rounds` 严格上限；超限 `failed`
- **provider native 能力差异**：codex JSON mode 与 claude tool use 不等价；fallback 行为必须文档化
- **JSON parse 容错**：`output_validation.parse_mode` 仅允许 `strict|strip_markdown|lenient`；金融 preset 默认 `strict`，拒绝 markdown wrapper/json5/jsonpath 容错；放宽需 H/O 审批
- **空输出 / 部分输出**：agent 输出空字符串 / 截断 → 视为 invalid
- **与 P8 verifier 关系**：`output_validation` 是**语法层**（schema 符合），`verify` 是**语义层**（结果对错）；两者正交，可同时开启；但顺序固定：先 schema validate（不 valid 直接 repair / fail），通过后才进 verify gate
- **archtest `dag_output_validate_before_verify` 守此顺序**：落点 `internal/archtest/dag_output_validation_test.go`
- **共用 sanitize layer**：`internal/sidecar/orch/orchestration/runtime/arbiter_sanitize.go`（P8 前置 PR 落地，可抽为 runtime 通用 sanitize）；P13 的 repair_prompt 拼装也复用此 sanitize（防 prompt injection 进 agent），不依赖旧 `llm/light/sanitize.go` 路径
- **审计开销**：金融场景 `audit_log=true` 时每次 validate 都落表，N=1000 节点 + 多轮 repair → 表会大；P9 archive 策略要覆盖
- **sensitive data 进 schema**：金融数据可能含 PII；error_detail 落表前必须 redact

## 必测项

- 简单 schema valid pass
- schema invalid + `on_invalid=repair` → `output_validation_phase=repairing` → agent 重出 → valid → done
- schema invalid + `on_invalid=fail` → 直接 failed
- 超 `max_repair_rounds` → failed
- 嵌套 schema（数组 / 对象 / enum）
- markdown 包裹的 JSON（strip 模式 vs strict 模式）
- 空输出 / 部分输出 → invalid
- codex JSON mode / app-server `OutputSchema` 能力探测（只能作为优化，必须叠 runtime validate）
- claude tool use 强制结构化（当前 claudecli prompt hint 不算 hard guarantee）
- 金融场景预设模板：`unanimous swarm + additionalProperties=false + parse_mode=strict + schema_draft=2020-12 + audit_log=true/hash-chain` 全链路
- output_validation × verify gate 两层串联（语法 → 语义）
- PII fixture：validation path 对内存原始 parsed value 校验，audit/repair path mask 后不改变 validation 结果

## 输入材料

- README §"P13 JSON 严格输出模式（Strict JSON Output / 金融合规）"
- 用户原话：「JSON 输出模式（金融场景）」（2026-04-25）
- JSON Schema spec（draft 2020-12 推荐）
- codex JSON mode / claude tool use 文档（owner 启动前调研锚点）

## 待办

- **金融场景 PII redaction**（a4 安全 + 多调研共识）：redactor 覆盖 `repair_prompt` / `error_detail` / arbiter input/output / validation 原始输出；drop key whitelist + JSON path mask；落库前调用。
- **audit 必须 append-only + hash chain**（a4 + a5 共识）：`dag_output_validations` 与 `dag_arbiter_calls` / `dag_audit_log` 同样 hash chain；archtest `dag_audit_append_only` 守。
- a8 依赖：provider 抽象当前未暴露 schema 强制入口（`internal/contract/provider.go` + `dream_executor` TODO）；P13 owner 必须在 P8 轻量 LLM 层中并估补齐 codex JSON mode / claude tool use 接口。
- a3 规模：json-schema compile cache 必须控容量上限，避免 N=10000 节点 × hot path 占 RSS；考虑 LRU + max entries 上限。
- a10 成本：audit_log=true 在金融预设下会双写 N=1000 node × 多轮 repair 表行；P9 archive TTL 必须走 7–14 天冷走。

## 金融 JSON 审计硬化契约（需求补全仲裁）

`dag_output_validations` 必须补：`schema_version`、`schema_draft`、`schema_hash_alg`、`prev_hash`、`row_hash`、`hash_alg`、`chain_scope`。`schema_hash` 采用 RFC 8785 JCS canonical JSON：先按本文 “Canonical schema algorithm（冻结）” 完成 internal `$ref` 解析、defaults 缺省合并、递归 `additionalProperties=false` transform，再用 JCS 计算 canonical bytes + sha256；validation row 必须同时记录 hash + version + draft。

金融 preset 默认 strict parse：拒绝 markdown wrapper、json5、jsonpath 容错；非金融可 opt-in strip/lenient。`additionalProperties=false` 默认对所有 object 递归生效，除非 schema 显式 opt-out。

执行顺序分两条路径：validation path 为原始输出 → payload cap → bounded strict parse → 在内存中对原始 parsed value 做 runtime validate（不持久化 raw）；audit/repair path 为 validation 结果、错误摘要、必要片段 → redact/sanitize → audit redacted detail/hash/blob ref → repair_prompt。禁止未 redacted raw 进入 DB 或 repair prompt；v1 `raw_blob_ref` 只能指向 redacted blob。若未来允许 encrypted raw，必须单独设计 KMS、ACL、retention、break-glass audit，不得复用本字段语义。validation pass 后 terminal CAS 必须绑定 `active_turn_id + output_schema_hash/version + validation_id`。
