# ADR-003：MCP 工具 input enum 校验落 handler 层（A+ 方案）

> 状态：✅ Accepted | 日期：2026-05-11 | 决策者：主线 | 相关：ADR 0001 §2.10、P13、P19、P23、`docs/契约/mcp-service-convention.md` §5.1、`docs/契约/onion-architecture-convention.md` §2.4

## 1. 背景

`cmd/mcp-orch` 暴露的 MCP 工具用 `tools.ObjectSchema` + `tools.EnumStringSchema`
声明 input_schema，包含若干字符串 enum 字段（`task_list_runs.status` /
`task_start_dag.trigger_source` / `task_update_node.status` /
`orchestration_launch_agent.provider` 等）。但 MCP server 框架本身不按
input_schema 验证入参 —— `tools.makeHandler` 只做 `requireDependency` +
`shared.DecodeInput`，schema 仅作为「描述/文档」对外暴露。

后果是 schema 写一份 enum，handler 写一份字面量，DB 又写一份 CHECK，
任何一处漏改都会出现 drift：

- 调用方传非法 enum 值 → 框架不拒 → service 层走 default 分支 / store 层
  unmarshal 异常 / DB 层（若有 CHECK）才报错；错误信息往往是技术栈底
  unfriendly 的英文，且离用户最远（P19 错误约定要求中英双语 + 业务态）。
- 历史代码确实出现过 silent Warn 后放行的反模式（`runtime.UpdateRuntime`
  对 provider 的处理，ADR-003 同批 fix-D commit 已根治）。

替代方案 P13 曾设想引 jsonschema 库做框架级校验，但被砍掉：
- 引依赖（`xeipuuv/gojsonschema` 量级）+ wire-breaking 70+ tool 的
  `additionalProperties` 兼容性 audit 成本高；
- 当前痛点只是少数 enum 字段，handler 兜底 + DB CHECK 已能覆盖。

## 2. 决策

采用 **A+ 方案**：
- **handler 层** 用 `tools.requireEnum(value, field, allowed)` 显式校验
  enum 字符串字段；
- **schema 与 handler 共用** 同一份 enum 切片（提到包级 `var`），靠**单测**
  断言两处一致（`TestEnumValidation_SchemaHandlerSingleSource`）；
- **DB 层** 加 CHECK 兜底（migration 0080/0081/0082），形成 schema +
  handler + DB 三层互锁；
- 错误消息中英双语，列出 allowed 候选；
- 必填字段（如 `task_update_node.status`）`requireEnum` 内置必填检查；
  可选字段（`task_list_runs.status` 等）handler 内手动放行空串。

不引 jsonschema 库；不改 `makeHandler`；不动 70+ tool 的 schema 形状。

## 3. 后果

### 受益

- enum 字段 drift 风险降到最低：schema 改了字面量不同步切片 →
  `TestEnumValidation_SchemaHandlerSingleSource` 当场红；切片改了
  handler 不接 `requireEnum` → 任何非法值在 handler 单测一抓即中。
- 用户/调用方拿到一致的中英双语错误，离调用面更近，diagnostic 更短。
- DB CHECK 兜底防止任何走 store 直写绕过 handler 的脏值。
- 无依赖、无 wire breaking，对历史 70+ tool 零侵入。

### 成本

- handler 单测面变大（每个 enum 字段 4 case：合法/非法/空/空白）。
- 包级 `var` 命名约定 `<tool>_<field>_Enum` 需要 review 卡控（已在
  `docs/契约/mcp-service-convention.md` §5.1 落地）。

### 不处理的范围

- 非 enum 字段（如 path / json blob）的 schema validation 不在本 ADR
  范围；如未来决定走 jsonschema 库，需立 ADR-004。
- `memory_scope` drift（5 个来源 + 3 个独立决策点）单独立 follow-up，
  详见 `docs/plans/dag改造实施计划.md` §10。

## 4. 实施

| Commit | 改动 |
|---|---|
| `feat(tools): 加 EnumValues + requireEnum helper ...` | types.go EnumValues + factory.go requireEnum + 单测 |
| `feat(tools): 4 个 MCP enum 字段接入 requireEnum 兜底 ...` | listRunsStatusEnum / startDAGTriggerEnum / updateNodeStatusEnum / launchAgentProviderEnum 提到包级 var + 4 handler 接入 + 5 类单测 |
| `feat(migrations): 0082 task_dag_runs.trigger_source CHECK 枚举 ...` | DB 层 CHECK 兜底，对齐 ADR 0001 §2.10 baseline |
| `fix(orch/runtime): provider 非法值 silent Warn → fail-fast error ...` | 根治历史 silent-Warn 反模式 |
| 本 commit | ADR-003 落地 + 契约同步 + §10.61 + memory_scope follow-up |

## 5. 状态与引用

- 状态：Accepted（2026-05-11）
- 引用：
  - `docs/契约/onion-architecture-convention.md` §2.4 handler/service/store 三层职责
  - `docs/契约/mcp-service-convention.md` §5.1 Input enum 校验（本 ADR 同批新增）
  - ADR 0001 §2.10 DB 不变量基线
  - P23 README §默认值安全（错就拒，不能让 default 背锅）
  - P19 错误中英双语规约

---

# ADR-003 (English summary)

**Status: Accepted (2026-05-11)**

MCP server does not enforce input_schema's `enum` fields. To prevent drift
between schema declarations, handler logic, and the database, we adopt the
**A+ approach**:

1. Handler layer validates enum string fields via `tools.requireEnum`.
2. The `enum` slice is hoisted to a package-level `var` shared by the
   schema builder (`EnumStringSchema(..., enumVar...)`) and the handler.
3. A unit test
   (`TestEnumValidation_SchemaHandlerSingleSource`) pins the two to the
   same slice.
4. Migrations add `CHECK` constraints (0080 status, 0081 dag.trigger, 0082
   run.trigger_source) so the database is the final backstop.
5. Errors are bilingual (Chinese + English) and list the allowed
   candidates, matching `translateStartDAGError` style and P19.

We explicitly do **not** import a jsonschema library (P13 rejected the
breaking-change cost on 70+ tools) and do **not** modify `makeHandler`.

`memory_scope` drift is tracked as a follow-up in
`docs/plans/dag改造实施计划.md` §10 because it touches 5 sources and 3
independent decision points (team scope retention / empty-string
normalization / `service_test.go:111` nature).
