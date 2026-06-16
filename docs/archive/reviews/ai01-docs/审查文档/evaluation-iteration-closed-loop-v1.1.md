# 全栈产品评测与闭环迭代优化通用协议 v1.1

> 适用于任意全栈产品、智能体系统、前端链路追踪 SDK、RUM / Bug 上报包、后端服务、工具插件和多角色协作产品的体验评测、能力度量与持续迭代。
>
> 项目状态提示（2026-06-01 后续同步）：本协议仍可用于评测前端链路追踪 / RUM / Bug 上报 SDK；但当前 `frontend-app` 已不再集成腾讯云 RUM / `aegis-web-sdk`。
>
> 本版本基于 v1.0 的“评审 → 仲裁 → 重构 → 验真 → 迭代”闭环重新编写，并补充：
>
> 1. 指标定义
> 2. Benchmark 规格
> 3. 评分公式 / Rubric
> 4. 失败分级与证据格式

---

## 目录

- [1. 协议目标](#1-协议目标)
- [2. 适用范围](#2-适用范围)
- [3. 第一性原则](#3-第一性原则)
- [4. 关键术语](#4-关键术语)
- [5. 五阶段闭环工作流](#5-五阶段闭环工作流)
- [6. 评审维度](#6-评审维度)
- [7. 指标定义规范](#7-指标定义规范)
- [8. Benchmark 规格](#8-benchmark-规格)
- [9. 评分公式与 Rubric](#9-评分公式与-rubric)
- [10. 失败分级与证据格式](#10-失败分级与证据格式)
- [11. 仲裁与 ADR 机制](#11-仲裁与-adr-机制)
- [12. 质量护栏](#12-质量护栏)
- [13. 迭代终止条件](#13-迭代终止条件)
- [14. 角色提示词模板](#14-角色提示词模板)
- [15. 落地检查清单](#15-落地检查清单)
- [16. 附录：可直接复用的模板](#16-附录可直接复用的模板)

---

## 1. 协议目标

本协议用于建立一套**可量化、可复现、可仲裁、可追溯、防谄媚**的产品评测与闭环迭代机制。

核心目标不是“给产品打一个分”，而是持续回答以下问题：

```text
当前产品状态是否满足目标？
差距在哪里？
该改什么？
谁来裁决？
改完是否真的变好？
是否值得继续迭代？
```

协议的最终产物包括：

- 产品基线指标
- 多维度评审报告
- 缺陷与风险清单
- ADR 架构决策记录
- 重构任务清单
- Benchmark 验真报告
- 迭代收益记录
- 残留 Top-3 瓶颈

---

## 2. 适用范围

本协议适用于包含以下任一结构的产品：

| 产品类型 | 示例 |
|---|---|
| 全栈应用 | Web 应用、SaaS 控制台、管理后台、B2B 产品 |
| 智能体系统 | Agent 工作流、多工具调用系统、自动化执行系统 |
| 前端 SDK | 前端链路追踪、RUM、Bug 上报、埋点、性能监控 SDK |
| 后端服务 | API 服务、网关、任务调度服务、数据服务 |
| 工具 / 插件 | 浏览器插件、IDE 插件、CLI 工具、内部运营工具 |
| 跨层产品 | 同时包含前端、后端、调度、工具调用和数据写入链路的产品 |

不适合直接使用本协议的场景：

- 纯创意内容评审，且无法定义可复现评价标准
- 没有稳定测试环境或数据源的早期概念验证
- 不允许记录指标、日志、失败证据的高保密环境

如必须使用，应先裁剪指标和证据格式。

---

## 3. 第一性原则

### 3.1 评测必须服务于改进

评测不是结论展示，而是闭环控制系统中的“测量器”。每一轮评测都必须输出：

```text
分数 + 证据 + 缺陷 + 可执行改进项
```

没有改进建议的评分无效。

### 3.2 所有结论必须可追溯

每一个评分、失败、风险、改动和裁决必须能追溯到以下至少一种证据：

- 测试结果
- 构建日志
- 性能数据
- Trace / Span / Log
- 截图 / 录屏
- HAR / Profile
- 静态扫描报告
- 代码 diff
- 用户任务复现路径

### 3.3 最小改动优先

每轮迭代应优先选择最小可验证改动。

```text
改动越大，归因越困难；
归因越困难，评测闭环越失真。
```

禁止搭车修改、无关重构和无 ADR 的架构大改。

### 3.4 全局最优高于局部最优

单一维度的提分不能破坏其他维度。例如：

| 局部优化 | 潜在副作用 |
|---|---|
| 增加前端埋点 | JS 体积变大，LCP / INP 退化 |
| 增加后端校验 | API p95 时延上升 |
| 增加自动重试 | 重复写入、链路追踪污染 |
| 增加兜底逻辑 | 掩盖真实错误，降低可观测性 |

发生跨维度冲突时，必须进入仲裁。

### 3.5 防谄媚依赖机制，不依赖态度

禁止使用“体验不错”“整体良好”“基本符合预期”等无证据描述。

评审角色不得为迎合已有方案而提高分数。裁决角色不得因为实现成本低而放行已知缺陷。

所有正面评价都必须绑定指标；所有负面评价都必须绑定证据。

---

## 4. 关键术语

| 术语 | 定义 |
|---|---|
| Benchmark | 一组可复现的场景、输入、环境、运行命令、统计口径和通过阈值 |
| Metric | 可计算、可比较、可归因的指标 |
| Baseline | 当前版本或上一轮版本在相同 Benchmark 下的指标值 |
| Target | 本轮或阶段性目标值 |
| Guardrail | 质量护栏，通常是构建、测试、静态扫描、性能预算、安全检查 |
| ADR | Architecture Decision Record，架构决策记录 |
| P0 / P1 / P2 / P3 | 失败严重等级，P0 最严重 |
| Evidence | 证明结论的证据，包括日志、截图、trace、测试报告、profile 等 |
| Holdout Benchmark | 隐藏或非公开 Benchmark，用于防止针对公开用例刷分 |
| Regression | 相比基线出现退化 |
| Stop Condition | 迭代终止条件，如目标分达成、边际收益不足、成本超限 |

---

## 5. 五阶段闭环工作流

整体流程：

```text
Phase 1 多维评审
    ↓
Phase 2 仲裁落盘
    ↓
Phase 3 重构执行
    ↓
Phase 4 质量验真
    ↓
Phase 5 滚动迭代
    ↺ 回到 Phase 2 或 Phase 1
```

---

### 5.1 Phase 1：多维度并行评审

部署不少于 4 个评审角色，分别覆盖产品关键维度。

每个评审角色必须独立输出：

- 维度评分：0.0–10.0
- 指标明细
- 失败项
- 风险项
- 至少 1 项具体可执行改进建议
- 证据引用

#### Phase 1 输出格式

```md
# Review Report: {维度名称}

- Reviewer: {评审角色}
- Product Version: {版本号 / commit hash}
- Benchmark Version: {benchmark 版本}
- Review Time: {时间}
- Score: {0.0-10.0}
- Status: PASS / FAIL / BLOCKED

## Metric Summary

| Metric | Baseline | Current | Target | Delta | Status | Evidence |
|---|---:|---:|---:|---:|---|---|

## Findings

| ID | Severity | Finding | Evidence | Suggested Fix |
|---|---|---|---|---|

## Top Improvement

{必须给出一个最小可执行改进项。}
```

---

### 5.2 Phase 2：多轮仲裁与设计落盘

汇总所有评审报告后，由独立裁决角色进行仲裁。

裁决角色职责：

1. 拦截过度设计
2. 处理跨维度冲突
3. 合并重复问题
4. 对改动做成本 / 收益 / 风险评估
5. 输出 ADR
6. 定义验真方式和回滚条件

#### 必须进入仲裁的情况

| 场景 | 示例 |
|---|---|
| 指标冲突 | 可观测性提升但性能退化 |
| 方案冲突 | 前端要新增采集逻辑，后端要求减少 header 透传 |
| 复杂度上升 | 引入新框架、新存储、新依赖 |
| 质量红线 | 出现 P0 / P1 失败 |
| 成本争议 | 收益不明确但改动成本高 |

---

### 5.3 Phase 3：重构执行

开发端严格对照 ADR 和评审意见执行改造。

执行纪律：

- 每项改动必须关联 Review Finding ID 或 ADR ID
- 禁止静默降级
- 禁止隐式兜底
- 禁止隐藏错误
- 禁止搭车修改
- 禁止无证据删除告警
- 禁止为了测试通过而削弱测试

#### 改动记录格式

```md
# Change Record

- Change ID: CHG-YYYYMMDD-001
- Related Finding: FIND-001
- Related ADR: ADR-001
- Owner: {负责人}
- Files Changed: {文件列表}
- Change Type: bugfix / refactor / perf / security / observability / test
- Expected Metric Impact: {预期影响}
- Rollback Plan: {回滚方式}
```

---

### 5.4 Phase 4：质量护栏验真

所有改动完成后，必须按相同 Benchmark 重新验证。

最低质量关卡：

| 关卡 | 要求 |
|---|---|
| 构建 | 零 Error；未解决 Warning 必须分级记录 |
| 自动化测试 | 单元测试、集成测试、E2E 测试按预设阈值通过 |
| 静态守卫 | 代码质量、资源体积、安全扫描、依赖扫描通过 |
| 性能基线 | 关键路径不得超过允许退化阈值 |
| 安全与隐私 | 不得新增 P0 / P1 安全或隐私风险 |
| 可观测性 | 不得破坏日志、trace、metrics、错误上报链路 |

性能基线建议使用以下表达：

```text
new_p95_latency <= baseline_p95_latency × 1.05
new_p99_latency <= baseline_p99_latency × 1.10
```

不建议使用“±5%”描述不退化，因为性能提升超过 5% 应视为正向结果，而不是异常。

---

### 5.5 Phase 5：滚动迭代

每轮迭代必须记录：

```text
本轮起始分 → 本轮终止分 → 指标变化 → 残留 Top-3 瓶颈 → 下一轮建议
```

进入下一轮的条件：

- 仍有 P0 / P1 问题
- 总分未达到目标
- 关键业务指标仍未达标
- 残留问题具有明确收益
- 仲裁角色判断继续迭代的收益大于成本

停止迭代的条件见 [13. 迭代终止条件](#13-迭代终止条件)。

---

## 6. 评审维度

默认四维评审如下。具体产品可增减维度，但必须保留每个维度的指标定义、Benchmark 场景和证据格式。

| 评审维度 | 关注指标 |
|---|---|
| 编排 / 调度层 | 任务拓扑执行效率、并发隔离率、状态恢复正确性、人机交互响应延迟 |
| 后端服务层 | API 吞吐稳定性、数据一致性、事务安全性、内部调用效率 |
| 前端交互层 | 渲染流畅度、交互响应时延、资源体积控制、状态同步一致性、界面友好性、界面美学一致性 |
| 工具 / 插件层 | 入参抽象程度、执行超时回收、数据写入原子性、安全边界遵循 |

---

## 7. 指标定义规范

### 7.1 指标必须满足的条件

每个指标必须满足以下条件：

| 条件 | 说明 |
|---|---|
| 可计算 | 能通过公式得到数值或明确 PASS / FAIL |
| 可复现 | 在相同 Benchmark 和环境下可重复测量 |
| 可比较 | 能与 Baseline、Target 或其他版本比较 |
| 可归因 | 指标变化能关联到某类改动或风险 |
| 可阻断 | 达到红线时能触发失败分级 |

禁止使用无法测量的指标名称，例如：

```text
体验很好
代码优雅
架构先进
页面舒服
系统稳定性不错
```

应替换为：

```text
任务完成率
p95 响应时延
错误捕获率
状态一致性断言通过率
E2E 通过率
JS bundle gzip 体积
trace 贯通成功率
```

---

### 7.2 指标定义模板

每个指标必须使用以下模板定义。

```md
## Metric: {metric_name}

- 中文名：{指标中文名}
- 所属维度：编排 / 后端 / 前端 / 工具 / 安全 / 体验 / 可观测性
- 指标类型：success_rate / latency / throughput / error_rate / size / count / boolean / score
- 优化方向：higher_is_better / lower_is_better / boolean_pass
- 定义：{明确描述该指标衡量什么}
- 计算公式：{公式}
- 数据源：{测试、日志、trace、profile、监控、人工验收表等}
- 采样范围：{场景、请求、用户任务或数据集}
- 统计口径：p50 / p95 / p99 / mean / max / ratio / count
- Baseline：{当前基线}
- Target：{目标值}
- Blocking Threshold：{阻断阈值}
- Warning Threshold：{警告阈值}
- 采样次数：{N 次}
- 证据要求：{必须保存哪些证据}
- Owner：{指标负责人}
```

---

### 7.3 通用指标清单

#### 7.3.1 编排 / 调度层指标

| Metric | 定义 | 公式 / 口径 | 建议目标 |
|---|---|---|---|
| task_success_rate | 任务成功率 | 成功任务数 / 总任务数 | >= 99% |
| task_p95_latency_ms | 任务 p95 耗时 | 任务端到端耗时 p95 | 不高于基线 × 1.05 |
| concurrency_isolation_rate | 并发隔离成功率 | 无串扰并发任务数 / 并发任务总数 | >= 99% |
| state_recovery_success_rate | 状态恢复成功率 | 崩溃后成功恢复任务数 / 崩溃注入任务数 | >= 98% |
| scheduling_overhead_ms | 调度开销 | 调度开始至任务执行的耗时 | 不高于目标预算 |
| retry_amplification_rate | 重试放大率 | 实际请求数 / 原始请求数 | <= 预设上限 |

#### 7.3.2 后端服务层指标

| Metric | 定义 | 公式 / 口径 | 建议目标 |
|---|---|---|---|
| api_success_rate | API 成功率 | 2xx / 总请求数，按业务定义修正 | >= 99.9% |
| api_p95_latency_ms | API p95 时延 | 服务端处理耗时 p95 | 不高于基线 × 1.05 |
| api_p99_latency_ms | API p99 时延 | 服务端处理耗时 p99 | 不高于基线 × 1.10 |
| throughput_rps | 吞吐 | 每秒成功请求数 | 不低于基线 × 0.95 |
| transaction_consistency_rate | 事务一致性 | 一致性断言通过数 / 总断言数 | 100% |
| internal_call_count | 内部调用次数 | 单次业务路径内部调用数 | 不高于目标预算 |
| error_rate | 错误率 | 错误请求数 / 总请求数 | <= 预设阈值 |

#### 7.3.3 前端交互层指标

| Metric | 定义 | 公式 / 口径 | 建议目标 |
|---|---|---|---|
| core_user_task_success_rate | 核心用户任务成功率 | 成功完成核心任务数 / 总任务数 | >= 99% |
| lcp_ms | Largest Contentful Paint | 浏览器性能数据 p75 / p95 | 不高于目标预算 |
| inp_ms | Interaction to Next Paint | 交互响应延迟 p75 / p95 | 不高于目标预算 |
| cls | Cumulative Layout Shift | 布局偏移 | <= 0.1 |
| js_bundle_gzip_kb | JS 产物 gzip 体积 | 构建产物 gzip 后大小 | 不高于预算 |
| render_error_count | 渲染异常数 | E2E / 监控捕获的渲染错误 | 0 个 P0/P1 |
| state_sync_assertion_pass_rate | 状态同步断言通过率 | 状态断言通过数 / 总断言数 | 100% |
| accessibility_violation_count | 可访问性违规数 | 自动扫描 + 人工验收 | 不新增 P0/P1 |

#### 7.3.4 前端链路追踪 / Bug 上报 SDK 专项指标

如产品是前端链路追踪、RUM、Bug 上报包，必须额外定义以下指标。

| Metric | 定义 | 公式 / 口径 | 建议目标 |
|---|---|---|---|
| sdk_init_success_rate | SDK 初始化成功率 | 初始化成功次数 / 初始化总次数 | >= 99.9% |
| sdk_init_p95_ms | SDK 初始化 p95 耗时 | 初始化耗时 p95 | 不高于预算 |
| js_error_capture_rate | JS Error 捕获率 | 已捕获 JS Error / 注入 JS Error | >= 99% |
| promise_error_capture_rate | Promise Error 捕获率 | 已捕获 Promise Error / 注入 Promise Error | >= 99% |
| resource_error_capture_rate | 资源错误捕获率 | 已捕获资源错误 / 注入资源错误 | >= 99% |
| api_error_capture_rate | API 错误捕获率 | 已捕获 API 错误 / 注入 API 错误 | >= 99% |
| trace_propagation_success_rate | trace 贯通成功率 | 前端请求在后端成功关联 trace 的数量 / 总请求数 | >= 99% |
| trace_break_rate | 链路断裂率 | 未关联 trace 请求数 / 总请求数 | <= 1% |
| sourcemap_restore_success_rate | Source Map 还原成功率 | 成功还原堆栈数 / 错误堆栈总数 | >= 99% |
| report_loss_rate | 上报丢失率 | 未到达服务端事件数 / 生成事件总数 | <= 0.5% |
| duplicate_report_rate | 重复上报率 | 重复事件数 / 事件总数 | <= 1% |
| pii_leak_count | 敏感信息泄漏数 | 未脱敏敏感字段数量 | 0 |
| sdk_runtime_error_count | SDK 自身运行错误数 | SDK 引起的运行错误 | 0 个 P0/P1 |
| page_perf_regression_rate | 页面性能退化率 | 接入 SDK 后关键性能指标退化比例 | <= 5% |

#### 7.3.5 工具 / 插件层指标

| Metric | 定义 | 公式 / 口径 | 建议目标 |
|---|---|---|---|
| input_schema_validity_rate | 入参合法率 | 合法入参数 / 总入参数 | >= 99% |
| tool_success_rate | 工具执行成功率 | 成功执行数 / 总执行数 | >= 99% |
| timeout_recovery_rate | 超时回收成功率 | 被正确回收的超时任务数 / 超时任务总数 | 100% |
| write_atomicity_pass_rate | 写入原子性通过率 | 原子性断言通过数 / 总断言数 | 100% |
| permission_boundary_violation_count | 权限边界违规数 | 越权行为数量 | 0 |
| idempotency_pass_rate | 幂等性通过率 | 幂等断言通过数 / 总断言数 | 100% |

---

### 7.4 指标归一化规则

为了把不同单位的指标合成为 0–10 分，需要将指标归一化为 0–1。

#### higher_is_better

适用于成功率、通过率、吞吐等指标。

```text
normalized = clamp((current - min_acceptable) / (target - min_acceptable), 0, 1)
```

#### lower_is_better

适用于时延、错误率、体积、失败数等指标。

```text
normalized = clamp((max_acceptable - current) / (max_acceptable - target), 0, 1)
```

#### boolean_pass

适用于安全红线、隐私红线、构建是否通过等指标。

```text
PASS = 1
FAIL = 0
```

#### blocking metric

若指标触发阻断阈值，则不参与普通加权，直接进入失败分级。

```text
if metric_status == BLOCKED:
    dimension_score = min(dimension_score, blocking_cap)
```

建议：

| 失败等级 | 分数上限 |
|---|---:|
| P0 | 0.0 |
| P1 | 4.0 |
| P2 | 7.0 |
| P3 | 9.0 |

---

## 8. Benchmark 规格

### 8.1 Benchmark 的最低构成

一个有效 Benchmark 必须包含：

| 字段 | 说明 |
|---|---|
| benchmark_id | Benchmark 唯一编号 |
| benchmark_version | Benchmark 版本 |
| product_version | 被测产品版本 / commit hash |
| scenario_set | 场景集合 |
| dataset | 输入数据集 |
| environment | 运行环境 |
| commands | 执行命令 |
| warmup | 预热策略 |
| run_count | 运行次数 |
| sampling_strategy | 采样策略 |
| metrics | 采集指标 |
| thresholds | 通过阈值 |
| artifacts | 证据产物 |
| owner | 负责人 |

---

### 8.2 Benchmark Manifest 模板

```yaml
benchmark_id: BENCH-001
benchmark_name: core-product-e2e-benchmark
benchmark_version: 1.0.0
product_name: example-product
product_version: ${COMMIT_SHA}
created_at: YYYY-MM-DD
owner: product-quality-team

scope:
  dimensions:
    - orchestration
    - backend
    - frontend
    - tool
  excluded:
    - experimental_features

environment:
  os: ubuntu-22.04
  browser: chromium-stable
  node: 20.x
  package_manager: pnpm
  network_profile: local / 4g / weak_network
  cpu_profile: standard / throttled_4x
  memory_limit_mb: 4096
  timezone: Asia/Singapore

run_policy:
  warmup_runs: 1
  measured_runs: 5
  timeout_seconds: 300
  retry_policy: no_retry_for_measurement
  random_seed: 42

scenarios:
  - scenario_id: SCN-001
    name: core_user_task_happy_path
    type: e2e
    input: ./datasets/core-task.json
    expected: ./expectations/core-task.expected.json
    metrics:
      - core_user_task_success_rate
      - task_p95_latency_ms
      - api_p95_latency_ms
      - inp_ms

thresholds:
  core_user_task_success_rate: ">= 0.99"
  task_p95_latency_ms: "<= baseline * 1.05"
  api_p95_latency_ms: "<= baseline * 1.05"
  p0_failure_count: "== 0"

artifacts:
  required:
    - junit_report
    - build_log
    - trace_log
    - performance_profile
    - screenshot_on_failure
    - har_on_failure
```

---

### 8.3 Benchmark 场景分类

| 场景类型 | 目的 | 示例 |
|---|---|---|
| Happy Path | 验证核心路径可用 | 用户完成主要任务 |
| Edge Case | 验证边界输入 | 空数据、大数据、异常参数 |
| Failure Injection | 验证失败恢复 | 网络中断、服务 500、超时 |
| Concurrency | 验证并发隔离 | 多用户同时执行同一任务 |
| Regression | 验证历史问题不复发 | 过去缺陷的复现用例 |
| Security | 验证安全边界 | 越权、注入、敏感字段泄漏 |
| Performance | 验证性能预算 | 冷启动、热启动、弱网加载 |
| Observability | 验证可观测性 | 日志、trace、metrics、错误上报 |

---

### 8.4 前端链路追踪 / Bug 上报 SDK Benchmark 场景

| Scenario ID | 场景 | 必测指标 | 证据 |
|---|---|---|---|
| SDK-SCN-001 | SDK 正常初始化 | sdk_init_success_rate, sdk_init_p95_ms | 初始化日志、performance mark |
| SDK-SCN-002 | 注入 JS Error | js_error_capture_rate | 错误事件、服务端入库记录、堆栈 |
| SDK-SCN-003 | 注入 Promise Rejection | promise_error_capture_rate | 错误事件、控制台日志、上报 payload |
| SDK-SCN-004 | 资源加载失败 | resource_error_capture_rate | Network log、错误事件 |
| SDK-SCN-005 | API 500 / 超时 | api_error_capture_rate | HAR、trace、服务端日志 |
| SDK-SCN-006 | trace header 透传 | trace_propagation_success_rate | trace id、前端请求、后端 span |
| SDK-SCN-007 | Source Map 还原 | sourcemap_restore_success_rate | 原始堆栈、还原堆栈、源码位置 |
| SDK-SCN-008 | 弱网批量上报 | report_loss_rate, duplicate_report_rate | 上报队列、重试日志、服务端入库数 |
| SDK-SCN-009 | SDK 对页面性能影响 | page_perf_regression_rate, lcp_ms, inp_ms | Lighthouse / Web Vitals / trace |
| SDK-SCN-010 | 敏感字段脱敏 | pii_leak_count | payload dump、脱敏规则报告 |

---

### 8.5 Benchmark 执行规则

1. 同一轮前后对比必须使用相同 Benchmark 版本。
2. Benchmark 版本变更时，必须记录变更原因。
3. 性能测试至少运行 5 次，取 p50、p95、p99。
4. 失败用例必须保存原始证据。
5. 禁止删除失败样本后重新计算指标。
6. 禁止为通过评测修改 Benchmark 阈值，除非形成 ADR。
7. 每轮至少保留一个 Holdout Benchmark，用于防止针对公开场景刷分。
8. 若环境发生变化，必须重新建立 Baseline。

---

## 9. 评分公式与 Rubric

### 9.1 总体评分结构

总分采用 0.0–10.0。

```text
Total Score = Σ(Dimension Score × Dimension Weight) - Risk Penalty - Complexity Penalty
```

其中：

```text
Dimension Score = Σ(Metric Score × Metric Weight)
Metric Score = normalized_metric_value × 10
```

默认维度权重：

| 维度 | 默认权重 |
|---|---:|
| 编排 / 调度层 | 0.25 |
| 后端服务层 | 0.25 |
| 前端交互层 | 0.25 |
| 工具 / 插件层 | 0.15 |
| 安全 / 隐私 / 可观测性横切项 | 0.10 |

> 如果产品不包含某个维度，应在启动前重新分配权重，并记录在 Benchmark Manifest 中。

---

### 9.2 风险扣分

| 风险类型 | 扣分 |
|---|---:|
| 新增 P0 问题 | 总分直接 0 |
| 新增 P1 问题 | -3.0，且总分上限 4.0 |
| 新增 P2 问题 | 每项 -0.5，最多 -2.0 |
| 新增 P3 问题 | 每项 -0.1，最多 -0.5 |
| 无证据正面评价 | 每项 -0.3 |
| 指标缺失 | 每项 -0.5 |
| Benchmark 不可复现 | 总分上限 6.0 |
| 关键指标无 Baseline | 总分上限 7.0 |

---

### 9.3 复杂度扣分

用于防止过度设计。

| 复杂度变化 | 扣分 |
|---|---:|
| 引入新框架但无 ADR | -2.0 |
| 引入新服务但无收益量化 | -1.5 |
| 新增跨层依赖 | -1.0 |
| 代码体积显著增加但无性能证明 | -1.0 |
| 仅为绕过测试而修改测试 | 总分直接 0 |
| 引入隐式兜底或静默降级 | 总分直接 0 |

---

### 9.4 0–10 分 Rubric

| 分数区间 | 判定 | 定义 |
|---:|---|---|
| 9.0–10.0 | 优秀 | 核心指标全部达标，无 P0/P1/P2；性能无退化；证据完整；复杂度受控 |
| 8.0–8.9 | 良好 | 核心指标达标；存在少量 P3；无关键风险；可发布或进入小范围灰度 |
| 7.0–7.9 | 可用但需修复 | 主要路径可用；存在 P2；不得直接全量发布，需修复计划 |
| 6.0–6.9 | 勉强可用 | 核心路径有明显缺陷；Benchmark 覆盖不足或指标缺失；仅可进入内部验证 |
| 4.0–5.9 | 不合格 | 存在 P1 或多项 P2；关键指标未达标；必须返工 |
| 1.0–3.9 | 严重不合格 | 多个核心路径失败；测试或性能明显退化；不可发布 |
| 0.0 | 阻断 | 构建失败、核心链路不可用、数据损坏、安全红线、隐私泄漏、静默降级、测试造假 |

---

### 9.5 维度评分 Rubric

#### 编排 / 调度层

| 分数 | 标准 |
|---:|---|
| 10 | 任务成功率、并发隔离、状态恢复、调度时延全部达标，无新增重试放大 |
| 8 | 核心任务达标，边缘任务存在轻微缺陷 |
| 6 | 核心任务可用但并发或恢复能力不足 |
| 4 | 多任务执行不稳定或状态经常错乱 |
| 0 | 任务不可执行、状态损坏、调度死锁、关键路径失败 |

#### 后端服务层

| 分数 | 标准 |
|---:|---|
| 10 | API 成功率、p95/p99、事务一致性、安全扫描全部达标 |
| 8 | 主要 API 达标，少量非关键接口需优化 |
| 6 | 核心 API 可用但性能或一致性存在明显问题 |
| 4 | API 失败率高、事务边界不清、吞吐瓶颈明显 |
| 0 | 数据损坏、构建失败、核心 API 不可用、安全红线 |

#### 前端交互层

| 分数 | 标准 |
|---:|---|
| 10 | 核心任务成功率、Web Vitals、状态一致性、资源体积全部达标 |
| 8 | 主要交互达标，少量视觉或边缘状态问题 |
| 6 | 核心路径可用但响应慢、闪烁或状态同步不稳 |
| 4 | 用户任务经常失败或页面性能明显退化 |
| 0 | 页面不可用、白屏、核心操作失败、隐私泄漏 |

#### 工具 / 插件层

| 分数 | 标准 |
|---:|---|
| 10 | 入参校验、超时回收、写入原子性、权限边界全部达标 |
| 8 | 主要工具可用，少量边界输入需补齐 |
| 6 | 工具可执行但错误恢复和幂等性不足 |
| 4 | 工具经常超时、参数歧义、写入风险明显 |
| 0 | 越权、数据破坏、不可恢复挂起、写入非原子 |

---

### 9.6 多评审聚合规则

当多个评审角色对同一维度评分时，使用以下规则：

```text
final_dimension_score = weighted_median(reviewer_scores)
```

推荐使用加权中位数，而不是平均值，原因是平均值容易被异常高分稀释。

若评分分歧超过 2 分：

```text
max_score - min_score >= 2.0
```

必须进入仲裁，并要求每个评审补充证据。

---

### 9.7 提升幅度计算

```text
absolute_improvement = new_score - baseline_score
relative_improvement = (new_score - baseline_score) / max(baseline_score, 0.01)
```

指标级提升：

```text
higher_is_better_delta = new_value - baseline_value
lower_is_better_delta = baseline_value - new_value
```

性能指标必须同时报告绝对变化和相对变化，例如：

```text
api_p95_latency_ms: 240ms → 210ms, delta = -30ms, improvement = 12.5%
```

---

## 10. 失败分级与证据格式

### 10.1 失败分级

| 等级 | 名称 | 定义 | 处理 |
|---|---|---|---|
| P0 | 阻断级 | 构建失败、核心路径不可用、数据损坏、安全漏洞、隐私泄漏、静默降级、测试造假 | 立即阻断；总分 0；必须修复后重新评测 |
| P1 | 严重级 | E2E 核心用例失败、关键性能退化、trace 断链、事务不一致、关键告警新增 | 阻断发布；总分上限 4；必须进入修复分支 |
| P2 | 中等级 | 非核心路径失败、边缘场景错误、非关键性能退化、部分指标缺失 | 不建议发布；需 owner、截止时间和复测计划 |
| P3 | 轻微级 | 文案、低风险 UI 瑕疵、非关键 lint warning、低优先级体验问题 | 记录 backlog；可不阻断本轮 |

---

### 10.2 Fail-Fast 规则

以下问题直接触发 P0：

- 构建失败
- 核心任务无法完成
- 数据损坏或不可逆写入错误
- 权限绕过或越权访问
- 敏感信息明文泄漏
- 引入静默降级
- 删除或削弱测试以通过评测
- 伪造、篡改或选择性隐藏证据
- SDK / 工具导致宿主页面或主流程不可用

---

### 10.3 证据格式总则

每个失败项必须包含以下字段：

```md
## Finding: FIND-YYYYMMDD-001

- Severity: P0 / P1 / P2 / P3
- Dimension: orchestration / backend / frontend / tool / security / observability
- Metric: {metric_name}
- Scenario: {scenario_id}
- Product Version: {commit hash / version}
- Benchmark Version: {benchmark version}
- Expected: {期望结果}
- Actual: {实际结果}
- Reproduction Steps:
  1. {步骤 1}
  2. {步骤 2}
  3. {步骤 3}
- Evidence:
  - Log: {路径或链接}
  - Screenshot: {路径或链接}
  - Trace ID: {trace id}
  - HAR: {路径或链接}
  - Test Report: {路径或链接}
- Impact: {影响范围}
- Suggested Fix: {建议修复方式}
- Owner: {负责人}
- Status: open / fixed / verified / waived
- Waiver Reason: {如豁免，必须填写；P0 不允许豁免}
```

---

### 10.4 不同失败类型的证据要求

| 失败类型 | 必要证据 |
|---|---|
| 构建失败 | 构建命令、完整 build log、错误行、依赖版本 |
| 测试失败 | 测试命令、测试报告、失败用例、复现输入 |
| 性能退化 | baseline/new 对比、运行次数、p50/p95/p99、profile、环境信息 |
| 前端白屏 / 渲染错误 | 截图、console log、source map、浏览器版本、复现路径 |
| API 错误 | 请求 / 响应、状态码、trace id、服务端日志 |
| 数据不一致 | 输入数据、预期数据、实际数据、事务日志 |
| trace 断链 | 前端 trace id、请求 header、后端 span、缺失位置 |
| 上报丢失 | 客户端事件数、服务端入库数、队列日志、网络日志 |
| 重复上报 | event id、重复次数、服务端入库记录 |
| 隐私泄漏 | payload dump、字段名、脱敏规则、触发场景 |
| 安全漏洞 | 扫描报告、复现步骤、影响范围、修复建议 |

---

### 10.5 Waiver 规则

原则上不鼓励豁免。

允许豁免的条件：

- 仅限 P2 / P3
- 有明确 owner
- 有明确修复截止时间
- 有业务负责人和技术负责人共同签署
- 不影响核心路径、安全、隐私、数据一致性

禁止豁免的条件：

- P0
- 安全红线
- 隐私泄漏
- 数据损坏
- 核心路径失败
- 测试造假
- 静默降级

---

## 11. 仲裁与 ADR 机制

### 11.1 ADR 触发条件

以下情况必须输出 ADR：

- 引入新服务、新框架、新存储或新协议
- 修改跨层接口约定
- 修改指标或 Benchmark 阈值
- 对 P0 / P1 失败作重大修复方案选择
- 多个评审角色方案冲突
- 需要牺牲某个指标换取另一个指标提升
- 需要锁定某项“已验证最优”的实现，不再重复优化

---

### 11.2 ADR 模板

```md
# ADR-YYYYMMDD-001: {决策标题}

- Status: proposed / accepted / rejected / superseded
- Date: YYYY-MM-DD
- Owner: {负责人}
- Related Findings: FIND-001, FIND-002
- Related Metrics: {metric_name}
- Related Benchmark: {benchmark_id}

## Context

{背景、问题和约束。}

## Decision Drivers

- {指标目标}
- {性能预算}
- {安全约束}
- {维护成本}
- {上线风险}

## Options

### Option A: {方案 A}

- Benefit: {收益}
- Cost: {成本}
- Risk: {风险}
- Expected Metric Impact: {预期指标变化}

### Option B: {方案 B}

- Benefit: {收益}
- Cost: {成本}
- Risk: {风险}
- Expected Metric Impact: {预期指标变化}

## Decision

{最终选择。}

## Rejected Options

| Option | Rejected Reason |
|---|---|

## Validation Plan

{如何验证该决策有效。}

## Rollback Plan

{失败后如何回滚。}

## Follow-up

{后续事项。}
```

---

## 12. 质量护栏

### 12.1 默认护栏清单

| 护栏 | 命令示例 | 通过标准 |
|---|---|---|
| 安装 | `pnpm install --frozen-lockfile` | 依赖安装成功，无 lockfile 漂移 |
| 构建 | `pnpm build` | 0 Error；Warning 分级记录 |
| 类型检查 | `pnpm typecheck` | 0 Error |
| 单元测试 | `pnpm test:unit` | 通过率达到配置阈值 |
| 集成测试 | `pnpm test:integration` | 核心链路 100% 通过 |
| E2E | `pnpm test:e2e` | 核心场景 100% 通过 |
| Lint | `pnpm lint` | 不新增 P0/P1/P2 |
| 安全扫描 | `pnpm audit` 或专用扫描 | 不新增高危漏洞 |
| Bundle 体积 | `pnpm analyze` | 不超过预算 |
| 性能测试 | `pnpm perf` | 不超过基线退化阈值 |
| 可观测性测试 | `pnpm test:observability` | trace/log/error report 不断链 |

---

### 12.2 质量护栏报告模板

```md
# Guardrail Report

- Product Version: {commit hash}
- Benchmark Version: {benchmark version}
- Run Time: {时间}
- Status: PASS / FAIL / BLOCKED

| Guardrail | Command | Result | Evidence | Severity |
|---|---|---|---|---|
| Build | pnpm build | PASS | build.log | - |
| Unit Test | pnpm test:unit | PASS | junit.xml | - |
| E2E | pnpm test:e2e | FAIL | e2e-report.html | P1 |
| Bundle Size | pnpm analyze | PASS | bundle-report.html | - |
```

---

## 13. 迭代终止条件

### 13.1 正常终止

满足以下全部条件时，可终止当前阶段迭代：

- 总分 >= 目标分
- 无 P0 / P1
- P2 数量低于预设阈值
- 核心指标全部达标
- 性能无不可接受退化
- 安全和隐私扫描通过
- 残留 Top-3 问题均有 owner 和计划

---

### 13.2 边际收益终止

当连续两轮满足以下条件，可停止继续优化：

```text
score_gain < 0.2
and critical_metric_gain < 1%
and estimated_cost > expected_benefit
```

其中：

```text
score_gain = new_total_score - previous_total_score
critical_metric_gain = 核心指标相对提升幅度
```

---

### 13.3 强制停止并返工

以下情况必须停止当前路线，返回 Phase 2 重新仲裁：

- 连续两轮引入 P1 或 P0
- 性能持续退化且无法归因
- 修复一个问题导致多个新问题
- Benchmark 不再可信
- ADR 假设被证伪
- 实现复杂度超过收益

---

## 14. 角色提示词模板

### 14.1 评审角色：Product Review Agent

```text
你是一个不输出恭维、不进行主观美化的产品评测审计器。

你的任务是基于 Benchmark、指标、日志、测试结果和证据，对指定产品维度进行评审。

你必须遵守：
1. 只基于证据评分，不基于印象评分。
2. 每个正面结论必须绑定指标。
3. 每个负面结论必须绑定证据。
4. 每次评分必须给出至少一项具体可执行改进建议。
5. 发现 P0 直接输出 BLOCKED，分数为 0。
6. 发现 P1 时不得给出高于 4.0 的分数。
7. 禁止使用“整体不错”“基本可以”“体验较好”等无证据表达。
8. 禁止以“后续再修”为理由放行阻断问题。

输出必须包含：
- Score
- Status
- Metric Summary
- Findings
- Evidence
- Suggested Fix
```

---

### 14.2 裁决角色：Product Arbitration Agent

```text
你是一个负责全局最优决策的产品技术仲裁器。

你的任务是汇总各评审角色的结论，识别冲突、拦截过度设计、选择最小可验证改动，并输出 ADR。

你必须遵守：
1. 全局最优高于单维度提分。
2. 没有证据的优化建议不得进入执行。
3. 会显著增加复杂度的方案必须给出收益证明。
4. 修改跨层接口、Benchmark 阈值、架构依赖时必须输出 ADR。
5. 出现 P0 / P1 时不得放行发布。
6. 禁止静默降级、隐式兜底和测试削弱。
7. 必须定义验证方式和回滚条件。

输出必须包含：
- Arbitration Summary
- Accepted Findings
- Rejected Findings
- ADR List
- Execution Plan
- Validation Plan
- Rollback Plan
```

---

### 14.3 重构执行角色：Implementation Agent

```text
你是一个严格按 ADR 和 Review Finding 执行的工程实现角色。

你必须遵守：
1. 每项代码改动必须关联 Finding ID 或 ADR ID。
2. 只做当前任务必要的最小改动。
3. 禁止搭车修改。
4. 禁止静默降级。
5. 禁止隐藏错误。
6. 禁止削弱测试。
7. 改动后必须输出 Change Record。
8. 必须运行约定质量护栏。

输出必须包含：
- Changed Files
- Related Finding / ADR
- Implementation Summary
- Test Result
- Risk
- Rollback Plan
```

---

## 15. 落地检查清单

启用本协议时，必须完成以下检查。

### 15.1 协议配置

- [ ] 确定产品边界
- [ ] 确定产品评测维度
- [ ] 为每个维度定义指标
- [ ] 为每个指标定义 Baseline、Target、Blocking Threshold
- [ ] 确定维度权重和指标权重
- [ ] 确定评分公式
- [ ] 确定失败分级规则

### 15.2 Benchmark 配置

- [ ] 建立 Benchmark Manifest
- [ ] 定义核心场景集
- [ ] 定义数据集
- [ ] 固定运行环境
- [ ] 固定运行命令
- [ ] 定义采样次数
- [ ] 定义统计口径
- [ ] 定义产物保存路径
- [ ] 建立至少一个 Holdout Benchmark

### 15.3 质量护栏配置

- [ ] 构建命令
- [ ] 类型检查命令
- [ ] 单元测试命令
- [ ] 集成测试命令
- [ ] E2E 测试命令
- [ ] 性能测试命令
- [ ] 安全扫描命令
- [ ] 资源体积守卫命令
- [ ] 可观测性验证命令

### 15.4 角色与流程配置

- [ ] 部署评审角色
- [ ] 部署裁决角色
- [ ] 部署执行角色
- [ ] 建立 ADR 目录和编号规则
- [ ] 建立 Finding 编号规则
- [ ] 建立证据归档目录
- [ ] 建立 Waiver 审批机制
- [ ] 建立迭代终止条件

### 15.5 首轮执行

- [ ] 完成首轮 Benchmark
- [ ] 记录 Baseline
- [ ] 输出 Review Report
- [ ] 输出 ADR
- [ ] 完成最小改动
- [ ] 运行质量护栏
- [ ] 输出 Validation Report
- [ ] 记录残留 Top-3 瓶颈

---

## 16. 附录：可直接复用的模板

### 16.1 Iteration Report 模板

```md
# Iteration Report: ITER-YYYYMMDD-001

- Product: {产品名}
- Product Version Before: {commit hash}
- Product Version After: {commit hash}
- Benchmark Version: {benchmark version}
- Iteration Owner: {负责人}
- Start Time: {时间}
- End Time: {时间}
- Status: PASS / FAIL / BLOCKED

## Score Summary

| Dimension | Baseline Score | New Score | Delta | Status |
|---|---:|---:|---:|---|
| Orchestration | | | | |
| Backend | | | | |
| Frontend | | | | |
| Tool | | | | |
| Security / Observability | | | | |
| Total | | | | |

## Metric Summary

| Metric | Baseline | New | Target | Delta | Status | Evidence |
|---|---:|---:|---:|---:|---|---|

## Accepted Changes

| Change ID | Related Finding | Related ADR | Summary | Status |
|---|---|---|---|---|

## Failures

| Finding ID | Severity | Summary | Owner | Status |
|---|---|---|---|---|

## Residual Top-3 Bottlenecks

1. {瓶颈 1}
2. {瓶颈 2}
3. {瓶颈 3}

## Next Iteration Recommendation

{是否继续迭代；如果继续，下一轮优先级是什么。}
```

---

### 16.2 Validation Report 模板

```md
# Validation Report

- Product Version: {commit hash}
- Benchmark Version: {benchmark version}
- Run ID: {run id}
- Run Count: {N}
- Environment: {environment id}
- Status: PASS / FAIL / BLOCKED

| Metric | Baseline | New | Delta | Threshold | Status | Evidence |
|---|---:|---:|---:|---:|---|---|
| task_success_rate | 98.5% | 99.2% | +0.7% | >= 99% | PASS | report.json |
| api_p95_latency_ms | 240 | 252 | +5% | <= baseline × 1.05 | PASS | profile.json |
| js_bundle_gzip_kb | 42 | 48 | +6KB | <= 50KB | PASS | bundle.html |
| trace_break_rate | 2.0% | 0.7% | -1.3% | <= 1% | PASS | trace.log |

## Failed Scenarios

| Scenario ID | Failure | Severity | Evidence |
|---|---|---|---|

## Performance Distribution

| Metric | p50 | p95 | p99 | Max |
|---|---:|---:|---:|---:|

## Conclusion

{本轮是否通过；是否允许进入下一轮、灰度或发布。}
```

---

### 16.3 Metric Definition 示例：trace 贯通成功率

```md
## Metric: trace_propagation_success_rate

- 中文名：trace 贯通成功率
- 所属维度：前端链路追踪 / 可观测性
- 指标类型：success_rate
- 优化方向：higher_is_better
- 定义：带 trace context 的前端请求中，能够在后端日志或 APM 中成功关联到同一 trace id 的比例。
- 计算公式：成功关联请求数 / 带 trace context 的请求总数
- 数据源：前端请求日志、HTTP header、后端 span、APM 查询结果
- 采样范围：核心 API 请求、弱网请求、失败请求、重试请求
- 统计口径：ratio
- Baseline：{例如 96.2%}
- Target：>= 99.0%
- Blocking Threshold：< 98.0% 记为 P1
- Warning Threshold：< 99.0% 记为 P2
- 采样次数：每轮至少 3 次，每次不少于 1000 个请求
- 证据要求：trace id 列表、失败请求样本、前端 header、后端 span 查询截图或日志
- Owner：observability-owner
```

---

### 16.4 Metric Definition 示例：SDK 性能退化率

```md
## Metric: page_perf_regression_rate

- 中文名：页面性能退化率
- 所属维度：前端交互层 / SDK 性能影响
- 指标类型：latency / ratio
- 优化方向：lower_is_better
- 定义：接入 SDK 后，页面核心性能指标相对未接入 SDK 或上一版本的退化比例。
- 计算公式：(new_metric - baseline_metric) / baseline_metric
- 数据源：Web Vitals、Lighthouse、Chrome Trace、真实用户性能数据
- 采样范围：核心页面、首屏页面、高频交互页面
- 统计口径：p75 / p95
- Baseline：{上一版本性能指标}
- Target：<= 5%
- Blocking Threshold：> 10% 记为 P1；核心页面不可用记为 P0
- Warning Threshold：> 5% 记为 P2
- 采样次数：每个页面至少 5 次实验室运行，并结合生产采样
- 证据要求：baseline/new 对比表、trace 文件、Web Vitals 报告、运行环境说明
- Owner：frontend-owner
```

---

### 16.5 最小文件目录建议

```text
quality-protocol/
  benchmarks/
    BENCH-001.yaml
    scenarios/
    datasets/
    expectations/
  reports/
    reviews/
    validations/
    iterations/
  adr/
    ADR-YYYYMMDD-001.md
  findings/
    FIND-YYYYMMDD-001.md
  evidence/
    logs/
    traces/
    screenshots/
    har/
    profiles/
  guardrails/
    commands.md
```

---

## 版本说明

| 版本 | 说明 |
|---|---|
| v1.0 | 原始五阶段闭环协议：评审、仲裁、重构、验真、迭代 |
| v1.1 | 重写为工程化 Markdown 协议；补充指标定义、Benchmark 规格、评分公式 / Rubric、失败分级与证据格式 |

---

## 结束语

本协议的核心不是制造更复杂的评审流程，而是建立一个能持续逼近真实产品质量的闭环系统。

```text
没有指标，不评分；
没有 Benchmark，不比较；
没有证据，不裁决；
没有 ADR，不重构；
没有验真，不迭代；
没有收益，不继续。
```
