# ADR-008：FailureClass 七类的映射规则全集

> 状态：✅ Accepted | 日期：2026-05-11（Proposed）→ 2026-05-12（Accepted，代码引用 ≥5 处 + 行为稳定数轮） | 决策者：项目维护者 | 相关：`docs/adr/0001-dag-v2-contracts.md`（§2 FailureClass 7 类枚举 / S1.2 done）、`cmd/mcp-orch/orchestration/nodeexec/executor_agent.go`（F1.1 已实装 4 处映射）、`docs/plans/dag改造现状与补丁v2.md` §4.5

## 1. 背景

S1.2 在骨架阶段（commit `5e1c731e`）固化了 **FailureClass 7 类枚举**：
- `transient` —— 网络抖动、CLI 启动失败、临时限流
- `quota` —— token 超限、context 过长
- `validation` —— 输出不符合 schema、JSON 解析失败
- `capability` —— 模型能力不够
- `hard` —— 业务层认定不可恢复
- `needs_human` —— 涉及不确定决策
- `infrastructure` —— 外部服务挂了

**但 S1.2 只交付了枚举，没有交付“哪种 error 归哪类”的映射规则**。

F 阶段 F1.1（commit `0f65833b`）已先于 ADR-008 在 `executor_agent.go` 实装映射，具体：
- 4 处硬编码 `FailureClassValidation`（decode error / nil cfg / nil launcher / unmarshal）
- 1 处 `classifyAgentLaunchError` 函数把 launcher error 分类为 `{transient, quota, validation}`
- 详细映射规则散落在 `executor_agent.go` 函数体内

**问题**：F2.1（AutomationExecutor）/ F3.1（HybridExecutor）即将开工。如果不立 ADR-008，三个 Executor 各自实现 error → FailureClass 映射会漂，例如：
- timeout 在 F1.1 是 `transient`，F2.1 可能写成 `infrastructure`
- 解析错误在 F1.1 是 `validation`，F2.1 可能写成 `hard`
- 调用方走 `on_failure.by_class` 策略时行为不一致

## 2. 候选方案

### 方案 A：抽公共映射表 + 各 Executor 注入

新建 `nodeexec/failure_classify.go`：
- 公共 `ClassifyError(err error) FailureClass` 函数 + 公共错误前缀/类型识别表
- 各 Executor（agent / automation / hybrid）调用公共函数，不再各自硬编码
- F1.1 现有 4 处硬编码改为调公共函数

**优点**：单源头，drift 风险低；F2.1/F3.1 直接用
**缺点**：error 来源多样（launcher / command_card / HTTP / shell），公共表难一刀切；需要兼顾各 Executor 特有 error 类型

### 方案 B：各 Executor 自定义 + 报表对账

各 Executor 自己实现映射函数（`classifyAgentLaunchError` / `classifyAutomationError` / `classifyHybridError`），但写一个 `failure_class_report.go` 汇总三家映射规则的覆盖范围 + 单测断言“同语义 error 在三家映射一致”。

**优点**：尊重各 Executor 的 error 模型差异
**缺点**：对账机制脆弱，新增 error 类型时三家容易漏改

### 方案 C：Strategy 模式 + 注册式

定义 `ErrorClassifier interface { Classify(err) FailureClass }`，各 Executor 注册自己的 classifier。`nodeexec` 包提供 default classifier 兜底（覆盖通用 timeout / context cancel / context deadline 等）；Executor classifier 只识别自己专属 error。

**优点**：组合优于继承，扩展友好；新加 Executor 不破现有 classifier
**缺点**：需要重构 F1.1 现有 4 处硬编码 + 注入路径

## 3. 触发条件

本 ADR 必须在 **F2.1 落地前**拍板。F2.1（AutomationExecutor）依赖 S1.4 done（已 done），是 F 阶段下一批可启动项之一。

**前置事实**：F1.1（commit `0f65833b`）已先实装映射，本 ADR 选定方案后**可能需要重构 F1.1 的现有映射代码**（方案 A/C 需要重构；方案 B 不需要）。

## 4. Open Questions

- Q1：方案 A 公共映射表的 input 是什么？go error 没有标准类型分类（除 `errors.Is` / `errors.As`），可能需要：(a) 字符串前缀匹配（脆弱）(b) sentinel error 列表（每种 Executor 注册）(c) error 实现接口 `FailureClassifier`（侵入 launcher / command_card 代码）
- Q2：与 F12.1（智能重试 dispatcher）的关系？F12.1 用 `by_class` 分发策略，本 ADR 决定“如何归类”，F12.1 决定“归类后做什么”。两者独立但有边界。
- Q3：HybridExecutor 的失败归类按哪一段？automation 段失败 = automation 段的分类；verifier 段失败 = agent 段的分类。或者整体归类为 `hybrid_partial`？
- Q4：`needs_human` / `infrastructure` 两类在 F1.1 尚未触发任何映射 —— 暂不实装等真有 case 再补，还是骨架阶段就给出占位规则？

## 5. 决策

⛔ 待定。F2.1 开工前由主线拍板方案 A/B/C 之一。

---

> 命名提示：本 ADR 编号 ADR-008，是 V3 全栈 ADR 命名体系下的第 8 号。本项目另有 `docs/adr/0001-dag-v2-contracts.md` 单独属于 DAG v2 骨架契约的命名体系（与本目录的 ADR-001/002/003/004/... 不互指），勿混。
