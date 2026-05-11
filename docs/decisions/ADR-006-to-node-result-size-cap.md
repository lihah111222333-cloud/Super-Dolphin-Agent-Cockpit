# ADR-006：outputs.to_node_result 超 size_cap 的处理策略

> ⚠️ **历史快照**：本 ADR 2026-05-11 写于 F1.3 未开工时；2026-05-12 端口收敛 batch 在 AutomationExecutor.finalizeAutomationOutcome 路径上实装 4KB enforce 并升 Accepted，`to_node_result` 维持 `bool` 字段位向后兼容（不升对象），cap 由代码常量 `NodeResultSizeCapBytes` 统一管理。
>
> 状态：✅ Accepted | 日期：2026-05-12（升 Accepted）/ 2026-05-11（首立 Proposed） | 决策者：DAG 改造端口收敛 batch | 相关：`docs/plans/dag改造蓝图v2.md` §5 关键决策汇总 line 74 / §7 typed schema、`docs/plans/dag改造修补.md` §4a、`docs/adr/0001-dag-v2-contracts.md`（typed schema 锁死契约）
>
> 状态修正说明（2026-05-12 round-3 reviewer 决议）：W5 worker 在并行 worktree 内基于 "W2 未实装" 误判把状态降回 Deferred；同步 W2 worker 已落 4KB enforce + Accepted 升级（commit 6d2b6576）。round-3 合并按 W2 实际代码状态保留 Accepted，覆盖 W5 的 Deferred 改动。

## 1. 背景

蓝图 **§5 关键决策汇总 line 74**「result vs sharedfile 边界」决策行明确：
> `to_node_result` 仅适合 < 4KB 摘要；大输出必须走 `to_sharedfile`；F1.3 enforce

（注：多个补丁文档曾误引为「决策表 row 14」——蓝图 §5 关键决策汇总表仅 14 行，这是其中一行，以 line 号引用为准。）

**4KB 阈值依据源**（蓝图 §5 决策行未写出依据，本 ADR 补记便后续调优）：可能源于（a）**下游 LLM context 阈值的经验拍**——jsonb result 取出后装入 prompt，4KB 以上会挥霍中型模型上下文与 cost；（b）**PG jsonb toast 门槛 256KB 的 1/64**，是 toast 之前区间的中位拍；（c）纯经验选值。本 ADR 拍板时主线需指明是哪一条依据（或重选阈值），避免后期看不懂为什么是 4KB 而不是 8KB / 16KB。

但 §7 typed schema `outputs.to_node_result` 原本只是 `bool`，**没有显式 size_cap 字段位**。F1.3 落地时怎么 enforce 4KB？

修补单 §4a 已把字段升级为：
```jsonc
"to_node_result": {
  "enabled": true,
  "size_cap_bytes": 4096
}
```

**未决问题**：当节点 result 超过 `size_cap_bytes` 时怎么办？三种候选行为，本 ADR 拍板。

## 2. 候选方案

### 方案 A：归类为 validation failure
- F1.3 / F2.2 / F3.1 写 node.result 前先校验 size
- 超 cap → executor 返回 `NodeOutcome{Status: failed, FailureClass: validation, Result: "<size_exceeded>"}`
- 优点：与 §8 七类失败的 `validation` 类语义一致；调用方走 on_failure.by_class[validation] 策略（append_error / retry）；AI 节点能在重试时看到"上次输出太大"提示，自行调整为生成摘要
- 缺点：对 automation 节点（命令执行）不够友好——automation 没有"调整输出"的能力，validation 失败只会 retry 同样输出

### 方案 B：强制转 to_sharedfile（如配置了 fallback path）
- node config 增加 `outputs.to_sharedfile_on_overflow.path` 字段位
- 超 cap → result 自动写入指定 sharedfile，node.result 只存 metadata (`{kind: "overflow", sharedfile_path: "..."}`)
- 优点：保留输出可达性；对 automation 节点友好
- 缺点：fallback path 配置心智重；写入 sharedfile 的并发/锁与 §7 `outputs.to_sharedfile.lock_mode` 冲突时需要二次仲裁

> **注**：`outputs.to_sharedfile_on_overflow.path` 字段位**未在修补单 §4a 预加**，仅为方案 B 拍板后的需要进 schema（参 ADR-007 「kind 字段位先加、子 kind 拍板后再加」同款做法判定是否前置）。

### 方案 C：分类型分发 —— agent 走 A、automation/hybrid 走 B
- agent 节点能"看到反馈调整"，超 cap → validation failure
- automation/hybrid 节点不能反馈调整，超 cap → 自动写 sharedfile
- 优点：每类节点用最自然的处理
- 缺点：行为多态心智重；node_type 与 outputs 行为耦合，AI 设计师 / 用户设计节点时需为不同节点类型记同套表现

## 3. 触发条件

本 ADR 必须在 **F1.3 落地前**拍板（F1.3 = AgentExecutor 处理 outputs，写 sharedfile / node.result）。

修补单 §4a 的 schema 升级**先行**——字段位先开，可以无副作用入主文档；enforce 行为待本 ADR 拍板后随 F1.3 实装。

## 4. Open Questions

- Q1：`size_cap_bytes` 默认值 4096 是否要给 schema 校验 lower/upper bound？太小（< 256B）可能拒绝合理输出，太大（> 64KB）违反决策初衷。
- Q2：方案 B 的 `to_sharedfile_on_overflow` 与 §7 主 `to_sharedfile` 是否同字段复用？语义上两者都是"输出去向 sharedfile"，但前者是 fallback，后者是主路径。
- Q3：result jsonb 列 PG 层有无硬上限？现 task_dag_nodes.result jsonb 无 CHECK，理论上 PG 单 jsonb 上限 ~ 1GB，但实践 > 256KB 会触发 toast 表，查询性能下滑。size_cap 应不仅是"语义阈值"还要考虑 PG 性能。
- Q4：与 F12.1 智能重试 strategy dispatcher 的交互——validation failure 走 by_class[validation]=append_error，是否需要给 AI 注入"上次输出 X bytes，超出 cap Y bytes" 的具体诊断？

## 5. 决策

✅ **方案 A（validation failure）+ 主路径建议 to_sharedfile**。2026-05-12 端口收敛 batch 实装。

### 5.1 实装位置

- 常量：`cmd/mcp-orch/orchestration/nodeexec/executor_automation.go::NodeResultSizeCapBytes = 4096`；
- 守卫函数：`enforceNodeResultSizeCap(payload []byte) *NodeOutcome`——返回非 nil 即拦截；
- 触发点：`finalizeAutomationOutcome`，在 `shouldEmitNodeResult` 已判定要写 `node.result` 之后、`outcome.Result = payload` 之前；
- 行为：`len(payload) > 4096` → `NodeOutcome{Status: failed, FailureClass: validation, ErrorSummary: "result exceeds 4KB size cap (N > 4096 bytes), configure outputs.to_sharedfile (ADR-006)"}`；
- 边界：`len(payload) <= 4096` 视为 OK（蓝图 §5 的「< 4KB 摘要」严格读是「不超」即 `<=`，便于运营者按 4KB 整数边界设计 payload）。

### 5.2 维持字段位为 bool 而非升对象

修补单 §4a 曾建议升级为 `{"enabled": true, "size_cap_bytes": 4096}` 对象形态。本 batch 决定**暂不升对象**：

- 阈值由代码常量统一，不需要 schema 字段位；
- 现网 / 测试 DAG 中已有的 `"to_node_result": true` 配置无需迁移；
- 待真实运营需求出现"按节点定制 cap"时再升对象，等价于「字段位先开、cap 个性化拍板后再升」（同 ADR-007 §4 渐进策略）。

### 5.3 与 ADR-008 / FailureClass 映射的协同

`size cap 超阈` 归类为 `validation`，原因：

- AI 节点（agent）见到 validation 失败可在 retry prompt 上读到「ErrorSummary 报超阈」，自行调整成生成摘要；
- automation 节点见到 validation 失败也是合理的——它的命令卡输出形状失配 outputs 约定，应在 command_card 端修，而非 retry；
- 与方案 A 原本"AI 能反馈调整"的论证一致。

### 5.4 未走方案 B/C 的原因

- 方案 B（自动转 sharedfile）：需要在 schema 加 `to_sharedfile_on_overflow.path` 字段位，心智重；当前 `outputs.to_sharedfile` 已是显式 sharedfile 主路径，运营者应直接走 explicit；
- 方案 C（按节点类型分发）：行为多态，AI 设计师 / 用户在设计节点时需要为不同 node_type 记两种 outputs 行为，违反"DAG 是统一抽象"。

## 6. Open Questions 落定

- Q1 `size_cap_bytes` 默认值是否要 schema bound？→ 不入 schema，由代码常量管理；不再需要 bound 校验。
- Q2 fallback vs 主路径 sharedfile 同字段复用？→ 不复用，方案 A 不需要 fallback。
- Q3 PG jsonb toast 性能？→ 4KB 远低于 256KB toast 门槛，无 PG 层影响；ADR 仍提示运营者大输出走 sharedfile。
- Q4 智能重试 by_class[validation] 注入诊断？→ ErrorSummary 已含 「N > 4096」具体数字，retry prompt 可直接读。

## 7. 单测覆盖

- `TestEnforceNodeResultSizeCap_Boundary`：4096 byte 通过、4097 byte 拒绝、错误消息含 outputs.to_sharedfile 提示；
- `TestEnforceNodeResultSizeCap_FivekBytes`：5000 byte 拒绝、消息含具体数字；
- `TestAutomationExecutor_Outputs_OversizeResultRejected`：端到端 5000 byte stdout + to_node_result=true → validation 拒绝，Result 未写；
- `TestAutomationExecutor_Outputs_OversizeViaSharedfile_OK`：大输出 + to_sharedfile（不勾 to_node_result）→ 走 sharedfile 主路径，不触发 cap rejection（ADR-006 主推修复路径）。

