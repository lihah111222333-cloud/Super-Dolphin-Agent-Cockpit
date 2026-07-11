---
name: 代码审查维度
description: 当在 super-agent-v3 仓库做代码审查、生产风险审计、环形审查、裁决子代理发现或编排修复任务时使用；尤其适用于 Go/Wails/MCP/provider/skill/runtime/store/frontend 变更。
aliases: ["@代码审查维度", "@review-dimensions"]
---

# super-agent-v3 代码审查维度

本技能用于审查 super-agent-v3 已完成代码、生产风险、子代理发现和修复编排。调用时先输出适用维度，再按证据逐条给出 finding；不要套用其他项目的路径、命令或业务领域。

## 详细模式

详细模式是默认审查口径，适用于全域审查、环形审查、重要 PR、子代理发现裁决、生产风险复核和修复计划拆分。它不是把 19 维机械扫一遍，而是先定边界，再按改动面选择高风险维度深挖。

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

## 19 维详细审查表

| # | 维度 | 详细审查重点 | 常见证据 |
|---|---|---|---|
| D01 | 架构边界 | `cmd` 只做入口和 bootstrap；`internal/app` 负责组装；`internal/contract` 放跨模块接口和 DTO；`internal/dto` 放传输形状；`internal/module` 保持业务逻辑；`internal/platform` 承接基础设施；`internal/provider` 只做 Claude/Codex/provider 适配；`internal/mcpserver/common` 承接共享 MCP 协议；`internal/store` 只处理持久化访问；`pkg` 只放可复用通用能力。重点查依赖倒置、跨层直连、循环依赖、把运行时状态塞进契约层、前端或 provider 绕过 module/store 边界。 | import 图、codemap、接口定义、调用层级、archtest 输出 |
| D02 | Fail-fast | 配置缺失、字段缺失、provider/tool 错误、空响应、未知枚举、路径不存在时必须返回明确错误并阻断；禁止默认配置、空结构、吞错日志后继续、兼容旧字段掩盖漂移。错误要带上下文，调用方不能把错误改成成功状态。 | 错误分支、测试断言、日志字段、JSON-RPC/MCP error envelope |
| D03 | MCP 协议 | stdio MCP 不能污染 stdout；legacy HTTP 兼容只在真实兼容层处理；tool schema、payload envelope、`_meta`、JSON-RPC id、错误码、取消和超时语义必须一致。新增 tool 要查输入校验、输出 schema、资源泄露和 sidecar stale binary 风险。 | `cmd/mcp-*`、`internal/mcpserver/common`、toolbridge 测试、协议 fixture |
| D04 | LSP 工具 | `cmd/mcp-lsp` 是通用多语言 LSP peer。重点查 workspace root、1-based position、range edit、replace/update 原子性、诊断聚合、多语言边界、旧 sidecar 进程、空结果是否 fail-fast。审查 LSP 行为时不得只靠文本搜索。 | LSP 定义/引用/调用层级、diagnostics、mcp-lsp 测试 |
| D05 | Provider/runtime | Claude/Codex adapter、provider home、skill mirror、toolbridge、session acquire/release、turn/session 生命周期、prompt snapshot、事件流和取消必须一致。重点查 provider 凭据路径泄露、事件乱序、后台任务未托管、provider 错误被改写、session resume/fork 状态丢失。 | `internal/provider/**`、runtime tests、event log、thread/session fixture |
| D06 | Orchestration/DAG/Cron/Wakeup | mcp-orch 是可选编排面，不得把子代理生命周期强制绑定到 DAG。只有真实改动 mcp-orch 时才审 `task_create_dag`、`task_start_dag`、`task_dispatch_node`、`task_update_node`、`task_dag_apply_ops`。Cron/Wakeup 重点查租约、重试、幂等、重复唤醒、任务状态回写和取消。 | `cmd/mcp-orch/**`、cron/wakeup tests、DAG SQL、状态机 fixture |
| D07 | Store/sqlc | SQLite/sqlc 查询、migration、事务、幂等、分页、排序、零值、NULL、baseline 数据和 schema 版本必须一致。重点查手写 SQL 与 sqlc 结构漂移、事务遗漏、读写路径不一致、migration 不可重复、测试数据库与生产路径不一致。 | `internal/store/**`、`internal/platform/db/**`、sqlc query、migration、store tests |
| D08 | Skill/Memory/Prompt/Thread | Skill 事实源是 canonical `.agents/skills`，历史 `.agent/skills` 不作为入口；Memory、Prompt、Thread、Dream/auto-dream、resume/fork、prompt snapshot 要保持读写边界清楚。重点查 mirror 漂移、个人 hub 被当运行时根、prompt 泄露、memory 写入丢字段、thread fork 顺序和 snapshot 不一致。 | skill validator、memory/prompt/thread tests、codemap 11/12、provider mirror diff |
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
| D19 | 唯一真相源（SSOT） | 每个规则、schema、状态或治理清单必须只有一个可写的权威 owner；文档、mirror、index、manifest、缓存和兼容表只能单向派生或只读消费。重点查双写、多处手改、生成物反向成为 owner、冲突时静默选一份、生成或漂移检查未进入强制门禁。 | canonical owner/registry/schema、生成器输入输出、check mode、幂等 diff、drift/fail-fast 测试 |

## 维度参考要求

参考要求只用于收窄上下文，不要求逐维批量读文件。每轮审查先按改动路径选择适用维度，每个适用维度只取 1 个导航入口和 1 个源码/测试证据；发现冲突、跨模块调用或行为不明时，再补读对应契约或 ADR。涉及行为、调用方或影响面时，按 `docs/internal-notes/LSP系统提示词.md` 做 LSP 定义、引用、调用层级或诊断确认。

| # | 最小参考入口 | 必要证据 |
|---|---|---|
| D01 | 当前改动所属 codemap 分卷或 README 架构图 | import/调用方向、archtest 或当前包边界证据 |
| D02 | 当前错误路径源码；必要时看 fail-fast 契约 | 错误返回、调用方处理、失败测试或缺失测试 |
| D03 | MCP 入口、tool handler 或协议 DTO 所在包 | schema/envelope/error/stdout 证据和协议测试 |
| D04 | `mcp-lsp` 工具入口或 LSP 系统提示词 | position/range/diagnostics 行为和相关测试 |
| D05 | provider/runtime 入口和实际 session/turn 调用链 | provider 错误、事件流、session 生命周期测试 |
| D06 | mcp-orch/cron/wakeup 改动点 | 状态迁移、租约、幂等、重试或 DAG/run 测试 |
| D07 | store/sqlc 改动点 | query/migration/事务/分页排序测试或 schema 证据 |
| D08 | skill、memory、prompt、thread 对应模块入口 | canonical/mirror、snapshot、resume/fork、memory 写入测试 |
| D09 | `frontend-app` 改动点或 Wails bridge | UI 状态、请求/事件、错误提示、lint/test/build 证据 |
| D10 | 不可信输入进入点 | exec/path/log/secret/permission/origin 防护证据 |
| D11 | 产生日志、事件或诊断的代码路径 | 结构化字段、错误码、状态可解释性和关联 id |
| D12 | 本次改动的验证面 | RED/GREEN、guard、受影响包测试或前端/skill 校验 |
| D13 | package/embed/install 改动点 | manifest、embed asset、平台脚本或 release guard 测试 |
| D14 | 热路径、watcher、后台任务或资源扫描点 | 上限、取消、锁/并发、benchmark 或资源 cap 测试 |
| D15 | 真实用户路径对应页面/bridge/service | 状态反馈、失败/取消/恢复路径、截图或手动 QA |
| D16 | 当前 git/workflow 证据 | `git status --short`、staged diff、hook、owned-file 边界 |
| D17 | 生产字段和消费登记点 | 自动枚举、registry/mapper 覆盖、豁免、fail-first 测试 |
| D18 | 重复规则或重复实现出现的位置 | `rg`/LSP 引用、单一事实源候选、同包测试和边界理由 |
| D19 | 当前声称权威的 owner 与所有派生/复制面 | owner 唯一性、单向生成链、只读消费者、幂等生成和 drift 门禁 |

## D01 类型分类

- 依赖方向越界：`cmd`、`internal/app`、`internal/contract`、`internal/dto`、`internal/module`、`internal/platform`、`internal/provider`、`internal/mcpserver/common`、`internal/store`、`pkg` 互相绕过既有边界。
- 契约层污染：`internal/contract` 承载实现细节、运行时状态、store/provider 专属类型或 UI 形状。
- 组装职责泄漏：Fx module、Wails bootstrap、sidecar bootstrap 内混入业务规则、持久化规则或 provider 行为。
- 可维护性漂移：compat wrapper、noop adapter、旧入口、镜像文件、文档锚点与当前源码职责不一致。

## D01 典型症状/判定场景

- 分层越界场景：frontend、provider、cmd 或 platform 直接访问 store/module 内部实现，绕过公开 contract 或 service。
- 契约污染场景：为了复用方便把具体数据库、provider、UI、runtime 字段塞进通用 DTO 或接口。
- 组装膨胀场景：Fx/Wails/sidecar 入口开始处理业务判断、错误分类、持久化事务或状态迁移。

## D02 类型分类

- 静默兜底：缺配置、缺字段、缺 provider、缺 tool、缺 binary、缺 workspace 时返回默认值、空结构或继续执行。
- 吞错成功：provider/tool/store/event/log 写入失败只记录日志，调用方仍收到成功状态。
- 错误分类丢失：fatal/retry/user error、JSON-RPC/MCP error、permission/error envelope 被泛化或改写。
- 漂移掩盖：兼容旧字段、空 snapshot、baseline 降级或忽略未知枚举，让契约变化不触发失败；标准 MCP `_meta` 兼容必须收敛在共享协议 decode 层，不能扩散成业务兜底。

## D02 典型症状/判定场景

- 缺输入场景：空 id、未知 enum、缺 payload、空 provider response 仍进入主链路或产生持久化副作用。
- 成功误报场景：主操作失败后被 callback、event write、日志或后置 cleanup 覆盖成成功。
- 诊断不足场景：错误没有 method/tool/provider/session/thread 等上下文，调用方无法决定重试、阻断或提示用户。

## D03 类型分类

- stdio 污染：MCP sidecar 在 stdout 输出日志、debug、非 JSON-RPC 帧或混入协议外文本。
- schema/envelope 漂移：tool input/output schema、`_meta`、payload envelope、JSON-RPC id、错误码与客户端期望不一致。
- legacy 兼容误放大：legacy HTTP 或旧 tool 兼容路径绕过统一校验、权限、超时或错误映射。
- sidecar 生命周期错位：stale binary、旧 registry、取消/超时未传播、资源泄露或 tool schema 未随实现更新。

## D03 典型症状/判定场景

- 协议帧场景：stdio 输出看似可读但破坏 MCP 客户端解析，或错误响应不保留 JSON-RPC id。
- schema 场景：handler 接受字段与 manifest/schema 不一致，新增字段没有测试或未知字段被吞掉。
- sidecar 场景：新代码已改但运行中的 mcp-lsp/mcp-orch 仍用旧 binary，导致验证结果假阳性或假阴性。

## D04 类型分类

- 定位语义错误：1-based position、range、line/column、workspace root、语言选择或 file URI 处理不一致。
- 编辑原子性缺口：replace/update/range edit 部分成功、跨文件失败未回滚、诊断未刷新。
- 多语言边界漂移：Go/TS/Markdown/JSON 等语言的 symbol、definition、references 语义混用或退化成文本搜索。
- 影响面缺证据：共享符号修改没有 references/call hierarchy，或 diagnostics 被当成完整测试替代。

## D04 典型症状/判定场景

- 跳转场景：definition/implementation 指到生成物、mirror、旧 worktree 或错误 workspace root。
- 编辑场景：range edit 位置偏移、换行/编码导致误改，或失败后返回成功。
- 审查场景：只靠 `rg` 判断共享接口影响面，没有 LSP 引用、调用层级、diagnostics 和对应测试。

## D05 类型分类

- Provider 适配漂移：Claude/Codex adapter、provider home、toolbridge、skill mirror 的 schema、事件和错误语义不一致。
- Session/turn 生命周期缺口：acquire/release、resume/fork、取消、后台任务托管或 prompt snapshot 顺序丢失。
- 事件流与结果错位：stdout、status、tool result、final message、诊断事件乱序或缺少关联 id。
- 凭据与路径泄露：provider home、token、环境变量、用户路径或临时文件被写入日志、事件或 prompt。

## D05 典型症状/判定场景

- Provider 错误场景：adapter 把 provider/tool 错误泛化、改写成成功，或丢失 method、session、thread、turn 上下文。
- 生命周期场景：resume、fork、cancel、release 后仍有 orphan session、重复 stream、未关闭 goroutine 或状态回写丢失。
- Snapshot 场景：发送给 provider 的 prompt、tool schema、memory 注入和本地记录不一致，复现时无法解释同一轮输出。

## D06 类型分类

- 编排面过度绑定：普通子代理工作被强制依赖 mcp-orch，或缺少 mcp-orch 时被误判不可执行。
- DAG 状态迁移缺口：node start/update/dispatch/apply ops 的状态、错误、输出和重试不一致。
- Cron/Wakeup 租约失效：租约刷新、重复唤醒、晚到唤醒、取消传播或任务回写缺少幂等保护。
- 可观测交接漂移：DAG/run/node 证据、agent handoff、workflow artifact 与当前执行事实不一致。

## D06 典型症状/判定场景

- 可选编排场景：单次审查、修复或子代理派发被要求先创建 DAG，工具缺失时直接停止。
- 状态机场景：node 已失败但 run 仍成功、重复 dispatch 覆盖较新结果，或 apply ops 部分成功后无错误暴露。
- 唤醒场景：cron/wakeup 在取消后继续执行、同一任务被多次唤醒，或租约过期后仍写成功状态。

## D07 类型分类

- sqlc/schema 漂移：手写 SQL、sqlc 结构、migration、baseline 数据或 schema 版本不一致。
- 事务与 ctx 边界错误：跨表写入、唯一性校验、读写路径或 repository 调用不在同一事务/ctx 约束下。
- 分页排序语义缺失：limit、cursor、sort、NULL、零值、默认方向和稳定排序没有可验证约束。
- 测试数据库错位：测试用 SQLite、migration 路径、seed 数据或生产路径不一致，导致 GREEN 不代表生产行为。

## D07 典型症状/判定场景

- 查询漂移场景：新增字段、索引、migration 后 sqlc 查询和 DTO 没有同步，运行时才暴露 scan 或 NULL 错误。
- 事务场景：先查再写、跨表状态回写、幂等键登记分散在多个调用里，失败后产生半写入。
- 分页场景：列表 API 或后台扫描没有 limit/cursor，排序不稳定，重复或漏读导致 UI、prompt 或 memory 结果漂移。

## D08 类型分类

- Canonical/mirror 漂移：`.agents/skills`、`.claude/skills` 或个人 skill 根之间事实源不清，或仍把历史 `.agent/skills` 当入口。
- Personal hub 误用：`personal/hub` 被当 runtime root 扫描、同步或镜像，污染运行时技能集合。
- Prompt/Memory 写入丢字段：memory 注入、prompt snapshot、thread message、dream/auto-dream 持久化字段缺失未失败。
- Thread fork/resume 顺序错位：fork、resume、message replay、tool result 和 snapshot 的顺序不一致。

## D08 典型症状/判定场景

- Mirror 场景：只改 `.claude` 或个人镜像，canonical `.agents/skills` 未变；或生成 mirror 与 `.agents/skills` 内容不一致。
- Prompt 场景：memory、skill、tool schema 已参与 provider prompt，但本地 snapshot 缺记录，后续无法复现。
- Thread 场景：fork 后消息、tool result、状态事件或 auto-dream 写入顺序改变，恢复时看到不同上下文。

## D09 类型分类

- Wails bridge 契约漂移：前端调用、后端 bridge、事件流和错误 envelope 字段不一致。
- 请求与缓存取消缺口：query、mutation、stream、轮询和路由切换没有取消、去重或 stale result 防护。
- 表单与状态校验不足：用户输入、空状态、loading、disabled、error recovery 与后端 fail-fast 不一致。
- 布局、可访问性和构建漂移：响应式布局溢出、焦点/键盘/ARIA 缺口、env 或 embed 产物引用旧路径。

## D09 典型症状/判定场景

- Bridge 场景：后端返回字段变更后前端继续显示默认值、空状态或旧缓存，没有 contract 或组件测试失败。
- Stream 场景：切换线程、会话或页面后旧事件仍写入当前 store，状态显示与终端输出冲突。
- UI 场景：按钮可点击但无效、错误提示泛化、文本溢出或 build env 缺失时仍生成看似可用的页面。

## D10 类型分类

- 命令与 shell 注入：exec 参数拼接、shell quoting、环境变量、approval 命令或脚本路径把用户输入当可信。
- 路径与 provider home 穿越：workspace、provider home、临时目录、附件路径或 source-file 读取越界。
- Secret 与日志泄露：token、API key、用户路径、prompt、trace、环境变量进入日志、事件、diagnostics 或 UI。
- 权限与 origin 边界缺口：tool approval、WebSocket/HTTP origin、文件权限、跨线程访问或 provider 权限未最小化。

## D10 典型症状/判定场景

- Exec 场景：用字符串拼 shell 命令，未区分 argv、cwd、env、approval 和用户输入边界。
- 路径场景：相对路径、软链、`..`、provider home 或下载文件能读写 workspace 外敏感位置。
- 泄露场景：错误、debug log、event stream、snapshot 或前端状态暴露 secret、完整 prompt、绝对用户路径。

## D11 类型分类

- 结构化字段不足：日志、事件、diagnostic 缺 method、tool、provider、session、thread、trace/span 或 stage。
- 成功事件覆盖失败：主路径失败后 callback、event write、cleanup、status update 继续写成功状态。
- 诊断 payload 不可解释：错误码、kind、retryability、用户动作、阻断原因和上下游关联丢失。
- 观测噪声与分级错误：debug/info/error 分级混乱，导致真实失败被淹没或用户看不到关键原因。

## D11 典型症状/判定场景

- 关联场景：用户只有“failed”或“unknown”提示，无法从日志串起 provider、tool、thread、session 和 trace。
- 覆盖场景：失败已发生但最后一个 status/event 显示 complete，UI 或子代理误以为任务成功。
- 诊断场景：diagnostic payload 缺错误分类和修复方向，只能靠人工翻源码判断是否重试、阻断或提示。

## D12 类型分类

- 验证面不匹配：只跑无关测试、只看 lint、只看 agent 自报，未覆盖本次改动风险。
- Fail-first 缺失：字段守卫、contract、skill 文档、migration 或错误路径新增检查没有 RED 证据。
- Guard/CI 漏项：本地脚本通过但未进入 guard、pre-push、CI 或仓库强制门禁。
- 测试隔离错误：测试依赖本地状态、旧 sidecar、缓存、生成物或外部服务，导致结果不可复现。

## D12 典型症状/判定场景

- 命令错位场景：skill 文档变更未跑 `python3 scripts/validate_super_agent_skills.py` 和 `git diff --check`。
- RED/GREEN 场景：新增守卫只证明 GREEN，没有临时破坏后的失败摘要，不能证明能拦住漂移。
- 环境场景：测试通过依赖运行中的旧 binary、缓存目录或本机配置，换 workspace 后无法复现。

## D13 类型分类

- Embed/manifest 漂移：package、embed asset、manifest、版本、签名或资源路径与源码不一致。
- Dev 路径混入发布：本地绝对路径、dev server、debug flag、未构建 web-dist 或测试资源进入发布包。
- 安装脚本静默跳过：install/update/first-run 脚本遇到缺依赖、缺权限、平台差异时继续成功。
- 平台差异未覆盖：macOS/Linux、arm64/x64、权限、路径分隔、可执行位和 quarantine 行为未验证。

## D13 典型症状/判定场景

- Embed 场景：前端或模板已改，但 embed 产物未更新，发布包仍加载旧 UI 或旧 manifest。
- Install 场景：安装脚本缺文件、权限不足或命令失败后仍 exit 0，用户以为安装完成。
- 平台场景：只在本机路径验证，通过依赖绝对路径、shell 特性或本机已有二进制。

## D14 类型分类

- 无界扫描与索引：workspace、skills、provider mirror、memory、logs、watcher 或 source-file 遍历缺上限。
- 轮询/streaming 忙等：dashboard、event stream、watcher、retry loop 或 background worker 无退避、取消或去重。
- Goroutine/channel 泄漏：后台任务未托管、ctx 未传播、channel 未关闭、cancel 后仍持有资源。
- 热路径重复构造：prompt assembly、schema encoding、decoder、buffer、regexp、JSON marshal 在高频路径重复分配。

## D14 典型症状/判定场景

- 大目录场景：扫描 `.worktrees`、mirror、logs、node_modules、dist 或生成报告时无 cap，UI 或 provider turn 被阻塞。
- 取消场景：用户取消、切换线程或 session 结束后，watcher/stream/worker 仍运行并写状态。
- 热路径场景：benchmark、pprof、trace 或代码结构显示循环内重复构造对象，P99、alloc 或吞吐回退。

## D15 类型分类

- 阻塞状态隐藏：等待审批、缺 tool、缺 provider、验证失败或子代理 blocked 没有清晰用户状态。
- 无效动作可点击：运行中、取消中、失败后、权限不足或输入不完整时按钮仍可触发副作用。
- 状态来源冲突：终端、event stream、thread status、toast、dashboard 展示不同完成/失败状态。
- 恢复路径缺失：取消、重试、resume、fork、重新授权或失败后清理没有可理解入口。

## D15 典型症状/判定场景

- 误导完成场景：任务仍 blocked 或验证失败，但 UI、终端或报告显示 complete。
- 操作场景：用户能重复点击启动、提交、取消、批准，导致重复任务或状态互相覆盖。
- 恢复场景：失败提示只有泛化文案，用户不知道需要补配置、授权、重跑验证还是清理 stale sidecar。

## D16 类型分类

- Dirty 边界不清：已有用户修改、生成物、工作流文件、计划文档和本轮改动混在一起。
- 原子提交破坏：docs-only、生产修复、验证脚本、mirror 生成物或 unrelated guard 变更揉成不透明提交。
- Worktree/branch 误用：从错误 base、旧 HEAD、stale worktree 或未同步 main 执行审查/修复。
- Hook/门禁绕过：pre-commit、pre-push、diff check、owned-file 或 generated drift 被跳过或结果未记录。

## D16 典型症状/判定场景

- Stage 场景：使用 `git add .` 或提交前未看 `git status --short`，把 unrelated dirty 一起带入。
- Base 场景：子代理或 worktree 基于旧提交，当前 HEAD 已移动但仍沿用 stale PASS。
- Hook 场景：hook 失败后只改提交信息或脚本绕过，没有保留失败输出和最终通过证据。

## D17 类型分类

- 生产字段不可枚举：结构体、JSON schema、tool schema、registry 或 AST 没有自动反查生产字段。
- 消费登记漏项：mapper、select、snapshot、DTO、contract registry、前端类型或 allowlist 未覆盖新增字段。
- 豁免失效：豁免缺 Field、Direction、Reason，或用“暂时不用”“以后再加”掩盖缺口。
- Baseline/snapshot 滥用：降低 baseline、删除 golden、更新 snapshot 或注释测试让守卫变绿。

## D17 典型症状/判定场景

- 字段漂移场景：生产字段新增后，消费侧 mapper/DTO/select 无测试失败，运行时才出现空值或缺字段。
- 守卫场景：字段清单靠手写数组维护，无法证明生产结构的每个字段都被登记或豁免。
- 豁免场景：豁免表没有原因、方向或 fail-first 证据，导致字段长期脱离契约覆盖。

## D18 类型分类

- 重复事实源：schema、tool manifest、DTO、mapper、prompt rule、UI state、错误码或脚本规则多处手写维护。
- 复制粘贴后局部改名：provider/tool/handler/service/hook/component 逻辑只差名称、label、字段或 envelope。
- 重复解析与转换：status、kind、permission、path、JSON envelope、message、diagnostic 在多处分支重复实现。
- 过度抽象风险：为了 DRY 跨层穿透、隐藏真实差异、弱化 fail-fast 或绕过字段守卫。

## D18 DRY 要求

- 同一规则、字段清单、解析流程、错误分类、状态机、表单渲染、payload 构造或脚本检查出现 2 处以上同步维护点时，必须标记 DRY 风险。
- DRY 简化必须保留 D01 架构边界、D02 fail-fast、D10 安全边界和 D17 字段守卫；不得用反射、泛型或兜底吞掉真实差异。
- 简化思路要指出可替代结构，例如单一 schema/registry、小型 helper、typed fact、value object、builder、共享测试或领域命名边界对象。
- 若重复代码承载不同 provider 语义、权限边界、错误分类、用户体验或测试隔离，应保留重复并用测试说明差异。

## D18 典型症状/判定场景

- 字段/schema 场景：同一字段在 JSON schema、tool schema、DTO、mapper、前端类型、allowlist、测试 fixture 中重复维护。
- Handler/tool 场景：多个 tool 或 endpoint 重复鉴权、参数绑定、日志、错误分类、响应 envelope 和测试断言。
- Prompt/UI 场景：prompt 规则、表单项、状态机、toast、thread card、tool result panel 或 session status 只差 label、字段名或单位。
- 脚本/守卫场景：多个脚本重复路径过滤、stale token、diff check、hook 逻辑，修改一处不能同步约束其他入口。

## D19 类型分类

- 权威 owner 缺失：同一概念没有明确 canonical 位置，消费者只能根据路径、时间或调用顺序猜测哪份有效。
- 双事实源/双写：registry、schema、store、配置、文档或 manifest 中有两个以上可独立写入的权威副本。
- 派生物反客为主：mirror、缓存、生成文档、index、baseline 或兼容层被手改，或反向覆盖 canonical owner。
- 漂移门禁缺口：生成链不确定、不幂等、无 check mode，或冲突时默认值、缓存、旧字段和 fallback 掩盖漂移。

## D19 唯一真相源要求

1. 对每个被审规则、schema、状态、治理清单或可持久化业务事实，必须能指出唯一 canonical owner 及其写入边界；“多份都算”或依赖读取顺序不算有 owner。
2. 其他表示必须是从 owner 单向生成、投影、缓存或镜像的只读消费面；不得让派生面独立接受业务写入或反向覆盖 owner。
3. 更改必须先修改 owner，再由确定性、幂等生成器刷新派生物；手改生成文档、mirror、index、manifest 或 cache 按漂移处理。
4. 迁移和兼容期可以暂时双读，但必须明确 owner、单向同步方向、冲突时的 fail-fast 规则与删除旧面的退出条件；禁止无期限双写。
5. 消费者发现 owner 与派生面冲突、生成输入不完整或无法判定新旧时，必须明确报错并阻断；禁止按默认值、时间戳、搜索顺序或 fallback 静默选一份。
6. 唯一性和漂移检查必须进入自动化测试及 CI、pre-push 或仓库强制门禁；至少证明第二 owner、手改派生物或生成漂移会 fail。
7. D18 回答“实现是否重复”，D19 回答“权威决策能否从多个地方产生”。即使没有复制代码，两个可写 store 也违反 D19；必要的 adapter 重复若只读同一 owner，则不因 D19 单独判错。

## D19 典型症状/判定场景

- Registry/文档场景：Go registry、JSON/YAML manifest 和 Markdown 规则表都可手改，无法证明哪个生成另外两个。
- Schema/类型场景：数据库 schema、sqlc DTO、API/tool schema、前端类型和 mapper 各自定义字段，新增字段时任一副本不会自动报错。
- 运行时状态场景：memory、store、provider session、thread event 和 UI store 都能改写同一状态，冲突后靠最后写入或时间戳决定结果。
- Skill/生成物场景：`.agents/skills`、provider mirror、codemap/index、README marker 或 embed 产物被独立修改，校验器不能拦截 drift。

## 使用方式

### 环形审查

多个 agent 并发审查时，先把 19 维分配到不同 agent，但每个 agent 都必须先读本技能和任务边界。汇总时按源码证据裁决，不按票数直接合并；冲突 finding 必须回到当前 HEAD 复核。

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
