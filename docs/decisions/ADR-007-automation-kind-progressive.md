# ADR-007：automation.kind 多 kind 渐进开通策略

> 状态：✅ Accepted | 日期：2026-05-11（拍板）| 决策者：主线 | 相关：docs/plans/dag改造蓝图v2.md §1 / §7、docs/plans/dag改造修补.md §4b、ADR 0001、F2.0（schema kind 字段位返修）/ F2.1（AutomationExecutor 仅识别 command_card）

## 1. 背景

蓝图 §1 一句话定位：
> 所有"自动化"能力都收敛成 DAG 的一种节点类型，节点是 agent / automation / hybrid 三选一。

但蓝图 §7 typed schema 里 `node_type = "automation"` 的 `exec` **只规定了 `command_ref`** 一种通道：

```jsonc
"exec": {
  "command_ref": "build_app",
  "args": {},
  ...
}
```

`command_ref` 指向 `command_get` 注册表，本质是 Super-Dolphin 自身的命令卡。这与 §1 "所有自动化能力" 之间存在张力：
- webhook（HTTP POST 到外部系统）
- shell（执行任意命令）
- http（GET / POST 任意 URL，含 response 解析）
- 文件操作（cp / mv / 模板渲染）

这些常见自动化基元目前**无法在 DAG 内表达**，要做必须破 typed schema。

修补单 §4b 已把 schema 升级为：
```jsonc
"exec": {
  "kind": "command_card",      // command_card | webhook | shell | http
  "command_ref": "build_app",
  ...
}
```

骨架阶段仅实装 `kind=command_card`，其他 kind 走本 ADR 渐进开通。

## 2. 候选方案

### 方案 A：渐进开通顺序：command_card → webhook → http → shell
- 先开 webhook（POST + 固定 body schema，无 response 解析）
- 再开 http（含 response → node.result 映射 + JSON Path）
- 最后开 shell（高风险，需 sandbox / allowed_paths / timeout 严管）
- 每个 kind 独立 ADR 子节点立项（ADR-007a/b/c）+ 配套 typed schema 子文档 + 守门规则
- 优点：风险最高的 shell 最后开；webhook 最简单先验证多 kind 分发架构
- 缺点：shell 在传统脚本编排中是主力，后置可能拖慎需要 shell 的实际场景（需用户场景证据验证顺序是否合理）

### 方案 B：按需开通，不预设顺序
- 用户提具体场景 → 评估当前缺哪种 kind → 单独 ADR 立项实装
- 优点：每种 kind 都有真实驱动场景
- 缺点：项目层面无规划性；可能多种 kind 同时立项导致 schema 设计冲突

### 方案 C：开 webhook 一种就停（极简）
- 只补 webhook（外部系统触发场景最常见），其他 kind 永不开
- 复杂自动化用 `hybrid` 节点 + agent 写脚本绕过
- 优点：架构面最简；与 §4 原则 1「极简 over 全功能」一致
- 缺点：把"shell 执行"丢给 agent 节点（即 LLM 写脚本），不是所有场景都合适（如纯运维脚本不需要 LLM）

## 3. 守门规则（适用于任何方案选择的 kind）

每新增一种 kind 必须：
1. 独立 typed schema 子文档 + handler-layer 校验（按 ADR-003 A+ 方案：`requireEnum` + 包级 `var` 单源 + 必要时 DB CHECK；不引 jsonschema 库）
2. 独立 `AutomationExecutor` 子分发分支（如 `executor_automation_webhook.go`）
3. 配套失败分类映射（webhook 5xx → transient / 4xx → hard / timeout → transient）
4. 安全审计（shell 必须 sandbox + allowed_paths + timeout + 资源 cgroup；http 必须 allowed_hosts whitelist）
5. ADR 评审 + 受影响范围测试覆盖
6. 在本 ADR-007 文档表里登记 kind / 状态 / 配套 ADR 编号

## 4. 触发条件

本 ADR 必须在 **F2.1 落地前**拍板（F2.1 = AutomationExecutor 解码 `command_ref` 执行）。

修补单 §4b 的 schema 升级**先行**——`kind` 字段位先开，骨架 `AutomationExecutor` 只识别 `kind="command_card"`（默认值），其他 kind 显式拒绝（返回 `unsupported automation.kind` 错误）。

## 5. Open Questions

- Q1 已结 —— kind="" 视为 command_card（向下兼容）：F2.0 的 ParseAutomationConfig 把空 kind 默认填 command_card；非法 kind 走 fail-fast 拒绝（返 unsupported automation.kind 错误）。
- Q2 已结 —— webhook 与 http 保持分开：修补单 §4b schema 已明确拆 `webhook | shell | http` 三种 kind。webhook（外发事件、fire-and-forget）与 http（同步调用 + response 解析）语义不同、错误处理不同，合并会损失 typed schema 的表达力。后续若实现中发现设计重叠，立 ADR-007a 合并补充。
- Q3：shell kind 的 sandbox 实现（容器 / chroot / 用户级 namespace）—— Super-Dolphin 当前没有 sandbox 基建，要不要复用现有 `workspace` 子系统的隔离？
- Q4：每种 kind 的 `inputs / outputs` 与 §7 共享 schema 是否完全适用？webhook 的 outputs 是 HTTP 响应 jsonb，与 `to_node_result` 的"摘要"语义重叠 / 冲突？
- Q5：与 ADR-006 size_cap 交互——webhook/http 响应大小常超 4KB，是否每种 kind 有独立默认 size_cap？

## 6. kind 登记表

| kind | 状态 | 配套 ADR | 实装位 | 备注 |
|---|---|---|---|---|
| command_card | ⏳ 待 F2.0+F2.1 实装（F2.0 = schema kind 字段位返修；F2.1 = AutomationExecutor 解码） | (无，本 ADR) | `executor_automation.go`（F2.1） + `nodeexec/config.go`（F2.0） | 当前 schema 唯一通道 |
| webhook | ⛔ 未开通 | 待立 | (未起) | 等本 ADR 拍板 |
| shell | ⛔ 未开通 | 待立 + sandbox 调研 | (未起) | 高风险，sandbox 是前置 |
| http | ⛔ 未开通 | 待立 | (未起) | 与 webhook 明确分开（见 Q2 结本） |

## 7. 决策

**选方案 A**：渐进开通顺序 `command_card → webhook → http → shell`。

### 实装路径

1. **F2.0（schema kind 字段位返修，先行）**：S5.1 typed schema 已 done 但没加 Kind 字段，本属 drift。F2.0 给 `AutomationExecConfig` 加 `Kind string `json:"kind,omitempty"`` 字段位 + `ParseAutomationConfig` 兜底「未知 kind → fail-fast 拒绝」+ 「空 kind → 默认填 command_card」。实装位 `cmd/mcp-orch/orchestration/nodeexec/config.go`。
2. **F2.1（AutomationExecutor 实装）**：仅识别 `kind="command_card"`，其他 kind 返 `unsupported automation.kind: <kind>` 错误。
3. **后续 kind 渐进开通**：每种 kind 独立 ADR-007a/b/c 子节点 + 守门规则（详 §3）。webhook → http → shell 顺序。

