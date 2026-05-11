# ADR-011：HybridExecutor 拓扑边界与未来扩展

> 状态：📝 Proposed | 日期：2026-05-11 | 决策者：待定 | 相关：F3.1（HybridExecutor v1）、F3.2 / F3.3 / F3.4（v2 拓扑占位）、ADR 0001 §2.6 node_type=hybrid typed schema

## 1. 背景

蓝图 §1 把 DAG 节点收敛为 `agent / automation / hybrid` 三选一；§7 typed schema 里 `hybrid.exec` 已定义为：

```jsonc
"exec": {
  "automation": { ... },
  "verifier": { ... }    // agent 类型
}
```

但当前 F3.1（`HybridExecutor` 串联 automation → agent verifier）**只是一种拓扑**：先跑 automation，失败时 verifier 不跑，成功时 verifier 校验。

返修审查指出：「hybrid」字面意思是「混合多种节点协作」，真实世界还有以下拓扑：
- **agent → automation**：agent 输出落库 / 触发外部 webhook
- **agent A → agent B**：A/B 仲裁（与 F12.1 智能重试不同语义 —— 仲裁是并行多 agent，重试是串行换模型）
- **automation A → automation B**：编排两条命令卡（类 GitHub Actions step）

当前 F3.1 命名 `HybridExecutor` 实际上更像 `AutomationWithVerifier` —— 单向单拓扑。如果不在文档锁死「v1 = AutomationWithVerifier」语义，后续接手者按字面理解 hybrid，会埋实装坑。

## 2. 决策

### 2.1 v1 拓扑锁定

**F3.1 v1 仅实装单一拓扑**：automation → agent verifier
- 顺序：先 automation；automation 成功 → verifier 校验；automation 失败 → verifier 不跑
- 命名：F3.1 代码注释 + 实施计划标题明确写「v1（automation → agent verifier，等同 AutomationWithVerifier 语义）」
- typed schema：`HybridExecConfig` 不动（已含 Automation + Verifier 字段位）

### 2.2 v2 拓扑预留（占位，本 ADR 不实装）

| ID | 拓扑 | 用途 | 子 ADR |
|---|---|---|---|
| F3.2 | agent → automation | agent 输出落库 / 触发 webhook / 写 sharedfile | ADR-011a（待立） |
| F3.3 | agent A → agent B | 并行 A/B 仲裁；与 F12.1 智能重试（串行换模型）正交 | ADR-011b（待立） |
| F3.4 | automation A → automation B | 编排两条命令卡 | ADR-011c（待立） |

每条 v2 拓扑开工前必须：
1. 立独立 ADR-011a/b/c 子文档，描述 typed schema 扩展（`HybridExecConfig` 新增字段位 / discriminator）
2. 守门规则：与 F3.1 v1 schema 向下兼容（旧 hybrid 节点不破）
3. failure class 映射：参照 ADR-008 七类，明确每条新拓扑的 error → class 映射

### 2.3 命名约定

- 实施计划 / 代码注释 / 测试名一律带 `v1` 后缀，表示当前 HybridExecutor = AutomationWithVerifier
- 不重命名 `HybridExecutor` 类型（避免破 typed schema 兼容）
- 未来 v2 实装时按拓扑加 dispatcher 内分支（`exec.agent_then_automation` 等 discriminator），不另立 Executor

## 3. 触发条件

本 ADR 必须在 **F3.1 开工前**拍板。F3.1 依赖 F1 / F2 完成，目前 F1.5 / F2.0 / F2.1 / F2.2 在前置链上。

v2 拓扑（F3.2/F3.3/F3.4）开工无前置 —— 等用户场景驱动 + 独立 ADR-011a/b/c 拍板。

## 4. Open Questions

- **Q1**：`HybridExecutor` 类型名要不要带版本？倾向不带 —— 加 v1/v2 后缀会破坏 nodeexec 包接口。改用拓扑 discriminator（exec.topology = "automation_then_verifier"）。
- **Q2**：v2 拓扑的 typed schema 改动会迫使 ADR 0001 §2.6 升版本吗？倾向是 —— ADR-011a/b/c 落地时同步升 ADR 0001 §2.6 子版本。
- **Q3**：F3.2「agent → automation」与 F1.3「agent 写 sharedfile / node.result」边界？前者是 hybrid 节点内一次完成两步；后者是单 agent 节点输出落地。倾向「agent → automation」专指调外部 webhook / 触发命令卡（automation 维度），不重叠 F1.3。

## 5. 实装登记

| 拓扑 | 状态 | 实装位 | 子 ADR |
|---|---|---|---|
| automation → agent verifier (v1) | ⏳ 待 F3.1 | `executor_hybrid.go` | (无，本 ADR 锁定) |
| agent → automation (v2) | ⛔ 待 ADR-011a 拍板 | (未起) | 待立 |
| agent A → agent B (v2) | ⛔ 待 ADR-011b 拍板 | (未起) | 待立 |
| automation A → automation B (v2) | ⛔ 待 ADR-011c 拍板 | (未起) | 待立 |
