# ADR-010：DAG 上下文与 token budget 兜底阈值

> 状态：✅ Accepted | 日期：2026-05-11；2026-05-23 状态回写 | 决策者：主线 | 相关：H7（inputs.summarization）/ H8（token budget）原"按需触发不预排"、ADR-006（to_node_result.size_cap）、F15.1（dispatch/retry metric 前置）、M3 端到端验收

## 1. 背景

蓝图 §4 H 阶段把 H7/H8 列为「按需触发不预排」：
- H7：inputs.summarization 真实实现
- H8：token budget enforcement

返修审查指出：DAG ≥ 10 节点 + 每节点 result 较大时，agent 节点输入会先于 M3 端到端用例炸。「按需触发」意味着等用户报"上下文爆了"才动 —— 这违反三层架构「上层调度看得见下层执行」原则。

ADR-006 已规定 `outputs.to_node_result.size_cap` 默认 4KB，但只解决了单节点 result 落地大小，没解决：
- 多节点 result 累积到下游节点 input 时的总量
- 单 run 累计 token 消耗的兜底
- M3 验收用例的硬阈值标准

## 2. 候选方案

### 方案 A：硬阈值 + 触发动作 + M3 验收锚点（推荐）

把 H7/H8 拆成「硬阈值 + 触发动作」两部分；硬阈值文档化，触发动作走 H 阶段实装。

| 维度 | 硬阈值（占位，待 Q1 调） | 触发动作 | 归属 |
|---|---|---|---|
| 单节点 result | > ADR-006 拍板的 `size_cap_bytes`（默认 4KB占位） | **依 ADR-006 拍板结果**：方案 A 走 validation 报错 / 方案 B 落 sharedfile + hash+path / 方案 C 截断。本ADR 不锁死具体路径 | H7（实装）/ ADR-006（拍板后决定） |
| DAG 节点数 | ≥ 10（占位） | 启用 input window 裁剪（上游范围待 Q2 拍板） | H7（实装） |
| 单 run 累计 token | > 100K（占位） | 告警 + 告警阈值 ×2 强制降级到 sonnet；sonnet 还不够 → fail-fast quota 错（Q3） | H8（实装） |

**依赖顺序记录**：本 ADR §2 第 1 行（单节点 result）不得先于 ADR-006 拍板。ADR-006 已在 2026-05-12 升 Accepted，当前单节点 result 超 cap 的动作固定为 validation failure + 建议 `outputs.to_sharedfile`。

M3 验收增加（项目本身：数字以 ADR-006 拍板后为准）：
- 用例必须覆盖 DAG ≥ 10 节点
- 用例必须覆盖单节点 result 超 ADR-006 size_cap_bytes 场景（验证 ADR-006 拍板的动作被触发）
- 用例必须能从 F15.1 metric 端点读取 `dispatch_failed_total` / `retry_count_per_node` 计数

### 方案 B：完全留 H 阶段（现状）

- 优点：极简
- 缺点：M3 通过不等于生产可用；用户驱动反馈才动

### 方案 C：实装提前到 F 阶段

- 优点：M3 直接可生产
- 缺点：F 阶段已 31 个任务，再加 H7/H8 实装会拖 M3

## 3. 决策

**选方案 A**。理由：
- 「按需触发」必然失序（用户报错才知道阈值），文档化硬阈值能让 M3 验收有判定标准
- 实装仍留 H 阶段，避免 F 拖延
- **与 ADR-006 分工，不锁路径**：单节点 result 超 size_cap 的具体动作依 ADR-006 拍板结果（本ADR 不领头决）；本 ADR 负责 DAG 维度额外维度（多节点累积 / 单 run 累计 token / 节点数）

## 4. 触发条件

本 ADR 必须在 **M3 端到端验收用例设计前**拍板（M3 验收要引用本 ADR 的硬阈值数字）。

H7/H8 实装无前置 —— 仍按需触发，但触发时不需要再做阈值决策（直接按本 ADR 数字落实）。

2026-05-13 backend dogfood 已按本 ADR 的 M3 硬阈值完成后端验收；H7/H8 的真实 summarization / token budget enforcement 仍留 H 阶段按需触发。

## 5. Open Questions

- **Q1（占位数字待调）**：100K token / 10 节点 / 4KB 是占位值，缺生产数据。实装前应跑一次代表性 DAG（≥ 15 节点 + 多类 agent）实测，按 P95 拟合。
- **Q2**：input window 裁剪策略 —— 只取上游直接父节点 result，还是按 BFS N 跳？倾向「直接父节点 result + 全局 sharedfile 引用列表」组合方案。
- **Q3**：单 run 累计 token 触发降级到 sonnet 后，如果 sonnet 也撑不住怎么办？倾向 fail-fast 抛 quota 类错误（与 ADR-008 FailureClass.quota 对齐）。

## 6. 验收锚点（M3）

M3 端到端用例必须包含以下三条断言（与实施计划 §3 M3 验收硬阈值三档同步）：

1. **节点数断言**：构造 ≥ 10 节点的 DAG，跑完成，sum(node.result.size) > 单节点 `size_cap_bytes` × 5。“10 节点”为本ADR Q1 占位，拍板前允许以生产实测 P95 调整。
2. **大 result 断言**：构造 1 节点输出超 ADR-006 `size_cap_bytes` （拍板后代入实数），验证 **ADR-006 拍板的动作被正确触发**。本ADR 不锁“落 sharedfile + hash+path”路径——如 ADR-006 选方案 A（validation 报错）则本断言改为验证 validation 错路径。
3. **metric 断言**：用例必须能在 F15.1 metric 端点（`/metrics` 或同类）读到 `dispatch_failed_total` / `retry_count_per_node` 计数。节点 retry ≥ 3 时告警 webhook 能被捕获。
