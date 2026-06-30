---
name: 代码审查维度
description: 当在 super-agent-v3 仓库做代码审查、生产风险审计、环形审查、裁决子代理发现或编排修复任务时使用；尤其适用于 Go/Wails/MCP/provider/skill/runtime/store/frontend 变更。
aliases: ["@代码审查维度", "@review-dimensions"]
---

# super-agent-v3 代码审查维度

本技能用于审查 super-agent-v3 已完成代码、生产风险、子代理发现和修复编排。调用时先输出适用维度和本轮优先级，再按证据逐条给出 finding；不要套用其他项目的路径、命令或业务领域。

## 详细模式

详细模式是默认审查口径，适用于全域审查、环形审查、重要 PR、子代理发现裁决、生产风险复核和修复计划拆分。它不是把 18 维机械扫一遍，而是先定边界，再按改动面选择高风险维度深挖。

1. 先看 `git status --short`，记录已有 dirty 文件，后续不得回退或混入 unrelated drift。
2. 路径发现按 README、`docs/doc/codemap/README.md`、相关 codemap、`docs/doc/codemap/ai-index.json`、源码和同包测试顺序。
3. 行为判断以源码和测试为准，再看 `docs/decisions`、`docs/adr`、`docs/契约`；`docs/plans/**`、迁移记录和旧报告只作历史材料。
4. 涉及代码行为、影响面或共享接口时，先读 `docs/internal-notes/LSP系统提示词.md`，再用 LSP 符号、定义、引用、调用层级和诊断确认。
5. 每条 finding 必须有可复核证据：源码行、测试、命令输出、契约文档或生成校验；不能只写感觉或泛化风险。

## 先定边界

1. 确认本轮审查对象：提交、diff、目录、文件、计划项、子代理报告或用户指定问题。
2. 区分事实面和历史面：当前 HEAD、当前 diff、当前测试输出优先；旧计划和旧审查不能覆盖源码事实。
3. 标出不审范围：生成物、外部 worktree、历史归档、未被本轮触碰的 dirty 文件、用户未授权的重构。
4. 如果当前 HEAD 变化或子代理基线过旧，必须重新取证，不沿用 stale PASS。
5. 输出前说明本轮覆盖了哪些维度、哪些维度不适用，以及不适用的理由。

## 18 维详细审查表

| # | 维度 | 详细审查重点 | 常见证据 |
|---|---|---|---|
| D01 | 架构边界 | `cmd` 只做入口和 bootstrap；`internal/app` 负责组装；`internal/contract` 放跨模块接口和 DTO；`internal/module` 保持业务逻辑；`internal/platform` 承接基础设施；`internal/provider` 只做 Claude/Codex/provider 适配；`internal/store` 只处理持久化访问。重点查依赖倒置、跨层直连、循环依赖、把运行时状态塞进契约层、前端或 provider 绕过 module/store 边界。 | import 图、codemap、接口定义、调用层级、archtest 输出 |
| D02 | Fail-fast | 配置缺失、字段缺失、provider/tool 错误、空响应、未知枚举、路径不存在时必须返回明确错误并阻断；禁止默认配置、空结构、吞错日志后继续、兼容旧字段掩盖漂移。错误要带上下文，调用方不能把错误改成成功状态。 | 错误分支、测试断言、日志字段、JSON-RPC/MCP error envelope |
| D03 | MCP 协议 | stdio MCP 不能污染 stdout；legacy HTTP 兼容只在真实兼容层处理；tool schema、payload envelope、`_meta`、JSON-RPC id、错误码、取消和超时语义必须一致。新增 tool 要查输入校验、输出 schema、资源泄露和 sidecar stale binary 风险。 | `cmd/mcp-*`、`internal/mcpserver/common`、toolbridge 测试、协议 fixture |
| D04 | LSP 工具 | `cmd/mcp-lsp` 是通用多语言 LSP peer。重点查 workspace root、1-based position、range edit、replace/update 原子性、诊断聚合、多语言边界、旧 sidecar 进程、空结果是否 fail-fast。审查 LSP 行为时不得只靠文本搜索。 | LSP 定义/引用/调用层级、diagnostics、mcp-lsp 测试 |
| D05 | Provider/runtime | Claude/Codex adapter、provider home、skill mirror、toolbridge、session acquire/release、turn/session 生命周期、prompt snapshot、事件流和取消必须一致。重点查 provider 凭据路径泄露、事件乱序、后台任务未托管、provider 错误被改写、session resume/fork 状态丢失。 | `internal/provider/**`、runtime tests、event log、thread/session fixture |
| D06 | Orchestration/DAG/Cron/Wakeup | mcp-orch 是可选编排面，不得把子代理生命周期强制绑定到 DAG。只有真实改动 mcp-orch 时才审 `task_create_dag`、`task_start_dag`、`task_dispatch_node`、`task_update_node`、`task_dag_apply_ops`。Cron/Wakeup 重点查租约、重试、幂等、重复唤醒、任务状态回写和取消。 | `cmd/mcp-orch/**`、cron/wakeup tests、DAG SQL、状态机 fixture |
| D07 | Store/sqlc | SQLite/sqlc 查询、migration、事务、幂等、分页、排序、零值、NULL、baseline 数据和 schema 版本必须一致。重点查手写 SQL 与 sqlc 结构漂移、事务遗漏、读写路径不一致、migration 不可重复、测试数据库与生产路径不一致。 | `internal/store/**`、`internal/platform/db/**`、sqlc query、migration、store tests |
| D08 | Skill/Memory/Prompt/Thread | Skill 事实源是 canonical `.agent/skills`，provider mirror 只作生成目标；Memory、Prompt、Thread、Dream/auto-dream、resume/fork、prompt snapshot 要保持读写边界清楚。重点查 mirror 漂移、个人 hub 被当运行时根、prompt 泄露、memory 写入丢字段、thread fork 顺序和 snapshot 不一致。 | skill validator、memory/prompt/thread tests、codemap 11/12、provider mirror diff |
| D09 | Frontend | 当前 UI 只在 `frontend-app` React/Vite；`cmd/agent-terminal/web-dist` 是 embed 产物。重点查 Wails bridge、状态 store、请求取消、错误提示、表单校验、事件流、可访问性、布局溢出、构建 env 和旧前端路径引用。 | `frontend-app` 源码、lint/test/build、Playwright 或截图证据 |
| D10 | Security | secrets、命令注入、路径穿越、shell quoting、权限审批、WebSocket/HTTP origin、日志泄露、临时文件、用户路径和 provider home 都要按不可信输入处理。tool 执行必须保留审批和最小权限边界。 | 输入校验、exec 调用、日志字段、权限分支、安全测试 |
| D11 | Observability | 结构化日志、错误码、状态字段、诊断输出和用户可见状态必须能解释失败原因。禁止只打 debug 后吞证据，禁止用成功事件覆盖失败，禁止丢 trace/span/session/thread 关联。 | slog 字段、event stream、diagnostic payload、trace/log fixture |
| D12 | Testing | 审查测试是否覆盖本次风险，而不是只看有无测试。Go 改动优先单包 guard，再扩大到受影响包；前端改动跑 lint/test/build；SQL/codemap/skill 改动跑对应校验。skill 文档改动跑 `python3 scripts/validate_super_agent_skills.py` 和 `git diff --check`。 | 测试名、失败先证据、通过命令、CI/pre-push 门禁 |
| D13 | Release/Install | package/embed、manifest、安装/更新、平台差异、签名、版本、资源路径和 first-run 副作用要可重复。重点查本地 dev 路径混入发布包、embed 产物过期、安装脚本默默跳过失败。 | Makefile、install scripts、embed assets、manifest、release tests |
| D14 | Performance | 热路径、轮询、watcher、streaming、文件扫描、provider mirror、大目录遍历、goroutine/channel 泄漏、锁粒度、后台任务托管和资源上限。重点查无界扫描、忙等、重复索引、长任务阻塞 UI 或 provider turn。 | benchmark、pprof、watcher tests、context cancellation、resource cap tests |
| D15 | UX/Product | 真实用户路径必须可理解：开始、运行中、失败、取消、恢复、重试、完成都有明确状态。重点查隐藏阻塞、按钮可点但无效、失败提示泛化、终端输出和 UI 状态冲突、工作流让用户误以为已完成。 | UI 状态机、交互截图、错误文案、event stream、manual QA |
| D16 | Git/Workflow | owned files、atomic commit、分支/worktree、stage 边界、hook、pre-push、generated drift 都要清楚。禁止 `git add .`、混入无关 dirty、回退用户修改、把 docs-only 和生产修复揉在一个不透明提交里。 | `git status --short`、diff、staged diff、hook 输出、workflow artifacts |
| D17 | 字段守卫 | 生产字段新增后，消费侧未登记必须有测试失败；检查事实源自校验、枚举、注册表、豁免、fail-first、CI/pre-push 和禁止兜底。重点查手工字段清单被当事实源、mapper/select/snapshot/DTO 漏字段、无效豁免、降低 baseline 或删 snapshot 绕过。 | 反射/AST/schema 枚举、registry、roundtrip tests、fail-first 证据 |
| D18 | DRY | 重复实现、重复事实源和复制粘贴后局部改名都要审。重点查 provider/tool/schema/DTO/mapper/prompt/UI 状态机/错误处理/脚本是否散落相同规则；相同规则应集中到单一事实源、registry、小型 helper 或明确的共享测试。DRY 不能破坏 D01 架构边界，不能为了抽象跨层穿透、隐藏真实差异或引入过度泛化。 | `rg` 重复片段、调用层级、registry/helper、同类测试、diff 对照 |

## 优先级规律

1. Provider、toolbridge、turn/session、prompt、thread、memory 改动：优先 D02、D05、D08、D10、D11、D12。
2. MCP sidecar、LSP、orchestration、cron/wakeup 改动：优先 D03、D04、D06、D11、D14、D16。
3. Store、migration、sqlc、持久化改动：优先 D02、D07、D12、D17，再看 D01。
4. Frontend/Wails 改动：优先 D09、D15、D02、D10、D12；涉及 event stream 时补 D05、D11。
5. Skill、provider mirror、repo workflow 改动：优先 D08、D12、D16、D17；确认 canonical 与 mirror 一致。
6. 发布、安装、embed、平台脚本改动：优先 D13、D02、D10、D12、D16。
7. 性能或后台任务改动：优先 D14、D11、D02；涉及并发状态时补 D06 或 D05。
8. 多 provider、多 tool、多 mapper、多 prompt 或前后端状态规则重复出现时：补 D18；先找单一事实源，只有差异真实存在时才允许保留重复，并要求测试覆盖差异。

## 使用方式

### 环形审查

多个 agent 并发审查时，先把 18 维分配到不同 agent，但每个 agent 都必须先读本技能和任务边界。汇总时按源码证据裁决，不按票数直接合并；冲突 finding 必须回到当前 HEAD 复核。

### 单任务快审

对单个 diff、单个目录或单个计划项，先列出适用维度和跳过维度；只输出真实 finding。没有问题时说明已检查的维度、验证命令和残余风险。

### 维度裁决

子代理报告、旧审查或自动扫描结果进入修复清单前，必须确认风险真实可达、文件行号仍在当前 HEAD、测试或契约能支持判断。证据不足时标为待复核，不改写成 PASS。

### 修复编排

把 finding 拆成最小修复任务时，保持 owner、文件边界、验证命令和阻塞条件清楚。mcp-orch 可用于持久 DAG、租约、重试或跨代理交接记录，但不是子代理工作的强制前置。

## 字段守卫详细要求

出现“生产字段 -> 消费侧”映射时，审查必须确认：

1. 生产字段必须由反射、AST、类型系统或 schema 从生产结构自动枚举并自校验；不得把手工字段数组、硬编码白名单或复制粘贴的字段常量当事实源。
2. 硬编码 mapper、select、snapshot、DTO 字段清单只能作为消费侧登记对象，必须被自动枚举出的生产字段反查覆盖；缺项、重名、未知字段或无效豁免都必须 fail。
3. 每个生产字段都在 mapper、select、snapshot、DTO 或 contract registry 中显式登记，或在豁免表中写明 `Field`、`Direction`、`Reason`；空原因、暂时不用、以后再加、不知道用途都按无效豁免处理。
4. 新增字段未登记时至少一个自动化测试 fail；不得用默认值、空结构、吞错或兼容旧字段掩盖漂移。
5. 单向 mapper 标明方向；双向 mapper 做 roundtrip；map、slice、pointer 字段按需验证深拷贝。
6. 新增守卫必须有 fail-first 证据：测试名、临时破坏后的失败摘要、恢复后的通过命令。
7. 守卫必须进入 CI、pre-push 或仓库强制门禁；仅本地可运行不算通过，靠降低 baseline、删除 snapshot、注释测试通过的修改按 P1 处理。

## Skill 文档审查补充

审查 repo-local skill 变更时，还要确认 canonical `.agent/skills` 是事实源，`.agents/skills` 与 `.claude/skills` mirror 没有漂移；若 `.agent/skills/.super-dolphin-skill-policy.json` 已登记该 skill，必须同步对应 sha256 hash。技能文本必须通过 `python3 scripts/validate_super_agent_skills.py`，并确认没有旧项目词、错误命令、错误触发路由或过期 mirror 证据。

## 严重度标准

| 级别 | 判定条件 |
|---|---|
| `P0` | 数据损坏、secret 泄露、核心路径不可用、误导系统继续错误执行、可达的权限绕过或远程执行风险。 |
| `P1` | 发布阻塞、契约破坏、fail-fast 破坏、测试/工具链结果不可信、可达状态丢失或 provider/session 主链路错误。 |
| `P2` | 边界错误、诊断不准、默认体验退化、重要测试缺口、可维护性风险或局部性能退化。 |
| `P3` | 文档、命名、注释、非阻塞清理；不能掩盖 P1/P2 事实。 |

## 输出格式

审查 finding 使用：

```text
severity | dimension | file:line_start-line_end | problem | evidence | fix
```

输出要求：

1. Findings 放在最前面，按严重度排序。
2. 每条 finding 必须有精确文件行号；若是跨文件问题，给最小可定位入口行。
3. `evidence` 写清楚源码、测试、命令输出或契约依据；不要写“可能”“感觉”“建议关注”当 finding。
4. 没有 finding 时，明确说未发现问题，并列出已跑或未能跑的验证。
5. 输出修复任务时，附 owner 边界、验证命令和不应触碰的 unrelated 文件。
