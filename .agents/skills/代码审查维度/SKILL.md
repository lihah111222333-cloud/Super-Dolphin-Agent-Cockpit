---
name: 代码审查维度
description: 当在 super-agent-v3 仓库做代码审查、生产风险审计、裁决子代理发现或编排修复任务时使用。
aliases: ["@代码审查维度", "@review-dimensions"]
---

# super-agent-v3 代码审查维度

## 审查边界

1. 记录 `git status --short`、HEAD、review object（worktree、staged tree、commit 或 push range，含可得的 base/head SHA）和不审范围，不回退或混入用户 dirty 文件。
2. 路径发现遵循 README、codemap、AI index/精确目录、LSP、源码和同包测试；旧计划只作历史材料。
3. 源码行为或共享接口必须有定位、理解、影响面、精读、diagnostics 五类 LSP 证据。
4. finding 必须证明风险可达，给精确行号、事实证据和最小修复；不按子代理票数裁决。

## 19 维审查矩阵

对 D01-D19 全量登记 coverage ledger：每维只能标记 `Applied` 或 `N/A + reason`；Applied 维度至少取一个导航入口和一个源码、测试或门禁证据。多 lane 审查还要逐 lane 记录 review object、路径/维度、证据、验证与残余风险；lane PASS 不是 repo PASS。

| 维度 | 关键风险 | 最小证据 |
|---|---|---|
| D01 架构边界 | 违反 `DefaultBackendBoundaryRegistry()` 定义的 owner、依赖方向或例外 | codemap 13、registry rule、调用方向、archtest |
| D02 Fail-fast | 缺配置/字段/依赖、未知枚举、空响应被默认值或吞错掩盖 | 错误分支、调用方、失败测试 |
| D03 MCP 协议 | stdout 污染、schema/envelope/id/error/取消漂移 | 协议 DTO、fixture、测试 |
| D04 LSP 工具 | workspace、1-based position、range edit、多语言、diagnostics 错误 | LSP 导航、mcp-lsp 测试 |
| D05 Provider/runtime | session/turn、resume/fork、事件顺序、snapshot、凭据漂移 | 调用链、runtime fixture |
| D06 Orchestration | DAG/cron/wakeup 状态、版本、租约、幂等、取消错误 | 状态机与测试 |
| D07 Store/sqlc | schema/query/migration/事务/NULL/分页排序错位 | sqlc、migration、store 测试 |
| D08 Skill/Memory/Prompt/Thread | canonical `.agents/skills`、provider mirror、snapshot、fork/resume 漂移 | Skill canonical 用 codemap 07A，Thread 写侧用 codemap 07B，provider mirror 用 codemap 09；Memory/Prompt/Thread 主链用 codemap 11；命中 Dream 时才用 codemap 12；validator、module/provider 测试 |
| D09 Frontend | bridge、取消、stale stream、错误状态、可访问性、embed 漂移 | frontend-app、lint/test/build、QA |
| D10 Security | command/path/origin/permission/secret 越界 | 校验、安全测试、日志字段 |
| D11 Observability | 错误码/关联 id 缺失、失败被成功覆盖 | log/event/diagnostic fixture |
| D12 Testing | 验证错位、无 fail-first、guard 未入门禁、依赖旧 binary/cache | RED/GREEN、测试与门禁 |
| D13 Release/Install | package/embed/manifest/版本/平台/first-run 漂移 | 产物与安装验证 |
| D14 Performance | 无界扫描、goroutine/channel 泄漏、锁与资源上限 | benchmark、取消、cap 测试 |
| D15 UX/Product | 开始到完成各状态误导用户 | UI 状态机、截图或 QA |
| D16 Git/Workflow | owner/worktree/stage/hook/generated drift、分支状态不实 | status、diff、hook、HEAD/remote |
| D17 字段守卫 | 生产字段未动态枚举、消费遗漏、无效豁免、无 fail-first | 自动加载 `字段守卫` 唯一详规 |
| D18 DRY | 重复实现/控制流/转换，或错误抽象跨层隐藏差异 | 重复点、共享边界、差异测试 |
| D19 SSOT | 多个可写 owner、双写、派生物反客为主、生成/mirror 漂移 | owner、单向生成链、drift 门禁 |

## 关键判域

- D17 的生产字段、registry、豁免、roundtrip 和 fail-first 只由 `字段守卫` 维护。
- D18 回答“实现是否重复且值得共享”；不同 provider 语义、权限、错误分类或层级边界可保留重复并用测试说明。
- D19 回答“权威决策能否从多个地方产生”；即使没有复制代码，两个可写 store 也违反 D19。
- 简化不得破坏 D01 架构、D02 fail-fast、D10 安全或 D17 字段守卫。
- Finding 的优先级、修复状态与闭环以 `docs/契约/fix-workflow-convention.md` 为准；返修执行 `Repro -> Root Cause -> RED -> Fix -> GREEN -> Guard -> Residual Retest -> Report`，不得把 `partial` 报成 `fixed`。

## 验证与输出

验证必须绑定已记录的 review object。提交/推送门禁的当前真值只取版本化的 `.githooks/pre-commit`、`.githooks/pre-push`、`.githooks/README.md` 和 `scripts/ai_maintenance_gates.sh` 生成的 gate plan，不得在技能中复制静态命令清单；按变更面补领域专项验证，并把当前 hook 未覆盖项列为 residual。diagnostics 不是测试，旧 binary、空日志或单次 PASS 不是完成证据。

```text
priority | dimension | coverage | reachability | file:line_start-line_end | violated_contract | evidence | fix | bug_locking_test | gate
```

Findings 按 P0-P3 置顶。无 finding 时仍输出完整 coverage ledger、验证和残余风险；工具失败记录 action、work_dir、目标、错误与收窄重试，不得写成 PASS。
