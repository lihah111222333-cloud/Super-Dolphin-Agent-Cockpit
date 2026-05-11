# ADR-006：outputs.to_node_result 超 size_cap 的处理策略

> ⚠️ **历史快照**：本 ADR 2026-05-11 写于 F1.3 未开工时；F1.3 仍未做，`to_node_result` 仍是 `bool` 未升对象，本文保留；当前详细 follow-up 见 `docs/plans/dag改造现状与补丁v2.md` §4.3。
>
> 状态：⏸ Deferred to F1.3 wiring 闭环 | 日期：2026-05-11（Proposed）→ 2026-05-12（Deferred） | 决策者：项目维护者
>
> 推迟说明（2026-05-12 决议）：W2 worker 本轮未完成 `outputs.to_node_result` 升对象 + `size_cap_bytes` 字段位 enforce —— `nodeexec/config.go:47` 仍 `ToNodeResult bool`、`nodeexec/executor_agent.go:236` 仍 F1.3 留位。本 ADR 内三方案（A validation failure / B truncate+warn / C split-to-sharedfile）继续作设计输入；真正拍板与代码侧 enforce 同步落地在 F1.3 wiring 闭环（届时 ADR 状态再升 Accepted 或重写）。 | 相关：`docs/plans/dag改造蓝图v2.md` §5 关键决策汇总 line 74 / §7 typed schema、`docs/plans/dag改造修补.md` §4a、`docs/adr/0001-dag-v2-contracts.md`（typed schema 锁死契约）

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

⛔ 待定。F1.3 开工前由主线拍板。

