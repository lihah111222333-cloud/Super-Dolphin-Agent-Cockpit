# 07A 业务模块层代码地图（读侧）

> 范围：`internal/module/dashboard/`、`internal/module/skill/`，以及 `internal/module/lspgui/` 的现状核对。  
> 关联入口：[07-module.md](07-module.md) / [07-module-write.md](07-module-write.md)。

---

## 1. 读侧总览

读侧模块只做两类事：

1. **聚合已有只读数据**：`dashboard` 把 orchestration + stores + `skillmodule.SkillLister` 窄端口拼成页面化查询面。
2. **暴露技能元数据与按需展开**：`skill` 一边维护技能目录/本地文件，一边保留 `Service` 兼容聚合面，并向 dashboard / prompt / turn / toolbridge 提供窄端口。

```mermaid
flowchart LR
    UI[Frontend / RPC caller] --> DASH[dashboard]
    UI --> SKILL[skill]
    DASH --> ORCH[contract.OrchestrationService]
    DASH --> STORES[(agentstatus/ailog/auditlog/buslog/commandcard/dbquery/prompt/sharedfile/tasktrace)]
    DASH -->|SkillLister| SKILL
    SKILL --> ROOTS[(project .agent/skills\n+ ~/.multi-agent/skills)]
    SKILL --> EVENTS[uidto.SkillsChanged]
    PROMPT[prompt.SkillCatalogProvider] -->|SkillCatalogSource| SKILL
    TURN[turn hydrateSkillRefs] -->|SkillHydrationSource| SKILL
```

### 1.1 跨卷一致性备忘

- `internal/archtest/freeze_registry.go:29-35` 当前显式 freeze 真值仍是 **`internal/module/prompt:27`**；凡是引用 prompt 包文件数的文档都应以 `27` 为准。
- 旧的独立 skill-catalog Fx wiring 已并入 `internal/module/prompt/module.go:14-26`；本卷只引用 `module.go`。
- `internal/module/lspgui/` 当前在仓内不存在；旧文档若仍把它写成真实包，需要按代码真值纠偏。

### 1.2 模块间主线关系（补）

- `dashboard` 站在 UI / RPC 查询面，向下只聚合 orchestration、stores 与 `skillmodule.SkillLister`，本身不持有技能内容。
- `skill.Service` 现在是兼容聚合接口；`prompt` 走 `SkillCatalogSource`，`turn` 走 `SkillHydrationSource`，`dashboard` 走 `SkillLister`，`toolbridge` 走 `SkillHostToolReader`。
- `uistate` 虽不在本卷展开，但它和 `thread` / `bus` 的 projection 链一起构成 dashboard 之外的另一条读侧 UI 面。

### 1.3 依赖图

```mermaid
graph TD
  dash[dashboard] --> orch[orchestration]
  dash --> stores[stores]
  dash -->|SkillLister| skill[skill]
  prompt[prompt.SkillCatalogProvider] -->|SkillCatalogSource| skill
  turn[turn hydrateSkillRefs] -->|SkillHydrationSource| skill
  thread[thread] --> turn
  uistate[uistate] --> thread
  uistate --> bus[bus]
```

---

## 2. dashboard（仪表盘读模型）

### 2.1 角色与边界

`dashboard` 是纯查询聚合层：

- **Fx 装配**：`module.go:19-54`
  - 注入 `contract.OrchestrationService`
  - 注入 `agentstatus / ailog / auditlog / buslog / commandcard / dbquery / prompt / sharedfile / systemlog / tasktrace`
  - 注入 `skillmodule.SkillLister`（不是完整 `skill.Service`）
  - `fx.Provide(NewDashboardHandlers)` 暴露 RPC
- **RPC 面**：`rpc.go:82-147`
  - 页面聚合：`ui/dashboard/get`
  - 细分读取：`dashboard/{agentStatus,taskTraces,commandCards,prompts,sharedFiles,skills,agent/detail,system/info,query,aiLogs,auditLogs,busLogs,dags,dagDetail,logs}`
- **服务核心**：`service.go:35-340`
  - `GetDashboardPage` / `GetAgentDetail` / `GetLogs` / `Query`
  - `GetAgentDetail` 里用 `errgroup` 并发 `Snapshot + GetReport`
- **页内装配**：`ui_page.go:21-202`
  - `DashboardPage` 聚合 `Agents/TaskTraces/Skills/CommandCards/Prompts/Memory`
  - `commands` 页是唯一多 loader 并发页（`commandCards + prompts`）

### 2.2 `ui/dashboard/get` 分发链

`ui/dashboard/get` 只是薄 RPC：`rpc.go:84-86` 直接进 `svc.GetDashboardPage(ctx, p.Page)`，真正的页面分发都在 `ui_page.go`。

```mermaid
sequenceDiagram
    participant C as RPC caller
    participant H as ui/dashboard/get handler
    participant S as Service.GetDashboardPage
    participant P as populateDashboardPage
    participant L as dashboardPageLoaders
    participant G as errgroup
    participant A as listAgents
    participant T as taskTraces.List
    participant K as skills.ListSkills
    participant CC as commandcard.List
    participant PR as prompt.List + dashboard filter
    participant M as sharedfile.List

    C->>H: ui/dashboard/get {page}
    H->>S: GetDashboardPage(ctx, page)
    S->>P: populateDashboardPage(ctx, out, page)
    P->>L: dashboardPageLoaders(out, page)
    L-->>P: loaders[]
    P->>G: errgroup.WithContext(ctx)
    alt page=agents
        G->>A: populateDashboardAgents()
    else page=tasks
        G->>T: populateDashboardTaskTraces()
    else page=skills
        G->>K: populateDashboardSkills()
    else page=commands
        par loader#1
            G->>CC: populateDashboardCommandCards()
        and loader#2
            G->>PR: populateDashboardPrompts()
        end
    else page=memory
        G->>M: populateDashboardMemory()
    else unknown/empty
        L-->>P: nil
    end
    G-->>P: wait()
    P-->>S: DashboardPage
    S-->>H: DashboardPage
    H-->>C: JSON page payload
```

### 2.3 页面到数据源映射

| 页面 | loader | 真正数据源 | 备注 |
|---|---|---|---|
| `agents` | `populateDashboardAgents` | `orchestration.ListAgents` | `service.go:listAgents` |
| `tasks` | `populateDashboardTaskTraces` | `tasktrace.Store.List` | 固定 `Limit=100` |
| `skills` | `populateDashboardSkills` | `skillmodule.SkillLister.ListSkills` | 只读窄端口复用技能扫描层 |
| `commands` | `populateDashboardCommandCards` + `populateDashboardPrompts` | `commandcard.Reader.List` + `prompt.Reader.List` | 两路并发 |
| `memory` | `populateDashboardMemory` | `sharedfile.Reader.List` | 固定 `Limit=500` |

补充：

- `dashboard/commandCards` 只是 `GetDashboardPage("commands")` 后取 `page.CommandCards`。
- `dashboard/prompts` **不是**简单的 page-field wrapper：`rpc.go:102-108` 会先写入 `withDashboardPromptScopeCWD(ctx, p.Cwd)`，再返回 `page.Prompts`。
- `dashboard/sharedFiles` 只是 `GetDashboardPage("memory")` 后取 `page.Memory`，返回 key 为 `files`。
- `dashboard/skills` 也会继承同一份 `{cwd}`：`rpc.go:113-115` → `ui_page.go:158-162` → `skillmodule.SkillLister.ListSkills(skillmodule.WithCWD(...))`。

### 2.4 `dashboard/prompts` 的 `{cwd}` 过滤接线

这条链是 p20.15 补通的重点，当前 prod caller 已由 `lsp_xref(references)` 核实：

- `service.go:86-91` `withDashboardPromptScopeCWD(ctx, cwd)`
- **prod callers 现有 3 个**：`rpc.go:85-87`（`ui/dashboard/get`）、`rpc.go:102-108`（`dashboard/prompts`）、`rpc.go:113-115`（`dashboard/skills`）
- 测试 caller 见 `service_test.go:62`

```mermaid
sequenceDiagram
    participant FE as SystemPromptPage
    participant RPC as dashboard/prompts handler
    participant CTX as withDashboardPromptScopeCWD
    participant PAGE as GetDashboardPage(commands)
    participant LIST as listDashboardPrompts
    participant STORE as prompt.Reader.List
    participant FILT as filterDashboardPromptsByCWD

    FE->>RPC: dashboard/prompts {cwd}
    RPC->>CTX: ctx = withDashboardPromptScopeCWD(ctx, cwd)
    RPC->>PAGE: GetDashboardPage(ctx, "commands")
    PAGE->>LIST: populateDashboardPrompts(ctx)
    LIST->>LIST: cwd = dashboardPromptScopeCWDFromContext(ctx)
    LIST->>STORE: prompts.List(ListFilter{CWD: cwd, Limit:100})
    STORE-->>LIST: PromptTemplate[]
    LIST->>FILT: filterDashboardPromptsByCWD(items, cwd)
    FILT-->>LIST: keep global + scope.cwd:<cwd>
    LIST-->>PAGE: page.Prompts
    PAGE-->>RPC: DashboardPage{Prompts}
    RPC-->>FE: {"prompts": [...]} 
```

关键细节：

1. **`dashboard/prompts` handler 已不再走旧 `dashboardPageField("commands", ...)` 包装**：`rpc.go:102-108` 直接返回 `{"prompts": page.Prompts}`。
2. **ctx 传递链不是测试专用 helper，而是三条读侧入口共享的 prod 入口**：`uiDashboardGetParams{Cwd}` / `dashboardPromptsParams{Cwd}` → `withDashboardPromptScopeCWD` → `dashboardPromptScopeCWDFromContext`。
3. **最终生效过滤仍在 dashboard 模块内**：`ui_page.go:149-196`
   - 先把 `cwd` 带进 `promptstore.ListFilter`
   - 再按 tag `scope.cwd:<value>` 做 `filterDashboardPromptsByCWD`
4. **store contract 与实现存在一层“名义支持 / 实际忽略”的落差**：
   - `internal/store/prompt/contract.go:24-29` 有 `ListFilter.CWD`
   - 但 `internal/store/prompt/store.go:61-75` 当前只下发 `AgentKey/Keyword/Limit`，未真正用到 `CWD`
   - 所以今天的可见性仍由 `dashboardPromptVisibleForCWD()` 控制，而不是 prompt store SQL 过滤
5. 测试锚点：`dashboard/service_test.go:36-126` 已覆盖 `repo-a/repo-b` 作用域与 `prompts` 顶层 key。

### 2.5 其它查询面

- **Agent 详情**：`service.go:114-158`
  - `orchestration.Snapshot` 与 `orchestration.GetReport` 并发
  - `LastReport` 以 `GetReport` 结果优先，缺失时回退 snapshot 内缓存
- **统一日志**：`service.go:173-198` + `logs.go`
  - `resolveLogSource` 把 `all/system/ai` 归一成组合读取模式
  - `appendSystemLogs` / `appendAILogs` 合流后统一排序与裁剪
- **DAG 查询**：`rpc.go:132-136` / `detail.go`
  - 直接透传 `contract.OrchestrationService` 的 list/detail 能力
- **任意 DB 查询**：`rpc.go:120-122` → `service.Query()` → `dbquery.Store.Query`

### 2.6 依赖图

```mermaid
flowchart TD
    DASHMOD[dashboard.Module] --> DASHSVC[NewService]
    DASHMOD --> DASHRPC[NewDashboardHandlers]
    DASHRPC --> UIPAGE[ui/dashboard/get]
    DASHRPC --> PROMPTSRPC[dashboard/prompts]
    UIPAGE --> PAGE[GetDashboardPage]
    PAGE --> AG[orchestration.ListAgents]
    PAGE --> TT[tasktrace.Store.List]
    PAGE --> SK[skillmodule.SkillLister.ListSkills]
    PAGE --> CC[commandcard.Reader.List]
    PAGE --> PR[prompt.Reader.List]
    PAGE --> SF[sharedfile.Reader.List]
    PROMPTSRPC --> SCOPE[withDashboardPromptScopeCWD]
    SCOPE --> PAGE
    PAGE --> TAGFILT[scope.cwd tag filter]
```

### 2.7 文件地图

| 文件 | 作用 |
|---|---|
| `module.go` | Fx 注入 orchestration / stores / `skillmodule.SkillLister`，并提供 RPC handlers。 |
| `rpc.go` | `ui/dashboard/get` 与各细分 `dashboard/*` 路由。 |
| `service.go` | `GetDashboard` / `GetAgentDetail` / `GetLogs` / `Query` / cwd-scope ctx helper。 |
| `ui_page.go` | `DashboardPage`、loader switch、页内并发装配、prompt tag 过滤。 |
| `detail.go` | DAG list/detail 与 Agent turn history 辅助逻辑。 |
| `logs.go` / `ai_logs.go` / `factory.go` | 日志统一包装、过滤与 DTO 组装。 |
| `agent_status.go` / `types.go` / `contract.go` | DTO 与查询接口定义。 |

---

## 3. lspgui（历史章节，当前代码缺席）

### 3.1 当前代码真值

截至 2026-04-20，本仓 **没有** `internal/module/lspgui/`：

- `find internal/module -maxdepth 2 -type d | grep lsp` → 空
- `lsp_grep path=. query="package lspgui"` → 0 命中
- `lsp_grep path=. query="lsp/gui_" glob="**/*.go"` → 0 命中

### 3.2 对 codemap 的影响

- 旧版 `07-module.md` 把 `lspgui` 写成真实存在的 GUI-LSP 包，现已与代码失真。
- 本次拆卷后只保留 **现状说明**，不再捏造文件地图 / RPC 列表 / stub 能力。
- 若未来重新引入该包，应在本节补回：`module.go / rpc.go / service.go / stubs.go` 等真实锚点后再展开。

---

## 4. skill（技能系统 / 渐进披露）

### 4.1 角色与边界

`skill` 同时承担四类职责：

1. **技能目录扫描**：系统根 + 项目根双根模型。
2. **渐进披露**：给 host 暴露 `skill/list` / `skill/expand`；给 toolbridge host tools 暴露 `skill_expand_body` / `skill_read_resource`，底层走 `SkillHostToolReader`。
3. **legacy 技能文件面**：`skills/local/*`、`skills/remote/*`、`skills/config/*`、`skills/match/preview` 继续保留。
4. **受限命令执行与事件**：`command/exec` + `uidto.SkillsChanged` debounce 发布。

Fx 装配见 `module.go:15-30`：

- `newService(cfg, dispatcher)` 从 `platform/config.Config.ProjectRoot` 注入构造期 project root
- 若底层 `Service` 是具体 `*service`，再 `bindDispatcher(dispatcher)` 开启 `SkillsChanged` 事件发射器
- `fx.Provide(ProvideSkillLister / ProvideSkillCatalogSource / ProvideSkillHydrationSource / ProvideSkillHostToolReader)` 暴露跨模块窄端口
- `fx.Provide(NewSkillHandlers)` 暴露 host RPC

### 4.2 根目录与 `cwd` 作用域

`skill.Service` 兼容聚合面及其窄端口的作用域不是全局常量，而是 **构造期 projectRoot + 请求期 cwd** 的叠加：

- `contract.go:12-25`：`WithCWD(ctx, cwd)` / `cwdFromContext(ctx)`
- `service.go:97-123`：`skillRoots(cwd)` / `projectSkillsRootForCWD(cwd)`
- `skills_meta.go:22-45`：`scanSkills(cwd)` 顺序扫描 roots

当前 root 规则：

| 层级 | 规则 |
|---|---|
| 项目根 | `<cwd>/.agent/skills`；若请求没带 cwd，则回退构造期 `projectRoot/.agent/skills` |
| 系统根 | `$SKILLS_ROOT` 或 `~/.multi-agent/skills` |
| scoped 系统根 | 当请求带 `cwd` 时，系统根会落到 `~/.multi-agent/skills/<ProjectKeyFromCwd(cwd)>` |
| 空 `cwd` | 保留 legacy 全局行为：继续使用构造期 project root + 原系统根 |

`cwd_scope_test.go:14-119` 已验证：

- 带 `cwd` 时可隔离同名 skill（`projectA` 与 `projectB`）
- 空 `cwd` 时仍保留旧的全局列表行为
- `ExpandBody` 也会按 `cwd` 选中对应 project-key 目录下的同名 skill

### 4.3 新增 host 渐进披露 RPC

`skillCoreHandlers@rpc.go:92-101` 把两条 host-facing 新键与 legacy `skills/list` 一起挂进 `newSkillHandlers()`：

| RPC | 入参/返回 | 真实流程 |
|---|---|---|
| `skill/list` | `skillListParams{cwd}` → `skillListResult{skills[]}` | `skillListHandler@rpc.go:147-159` → `scopedSkillContext@rpc.go:269-274` → `ListSkills` → `scanSkills` → 只返回瘦身 DTO |
| `skill/expand` | `skillExpandParams` → `skillExpandResult` | `skillExpandHandler@rpc.go:175-187` → `scopedSkillContext` → `expandSkillWithApproval@rpc.go:276-280` → `prepareSkillExpand` / `ensureExpandApproved` |

DTO / caller 链补充：

- `rpc_skill_types.go:80-95` 当前显式定义 `skillListResult` / `skillExpandParams`，host 返回结构与 legacy `skills/list` 已分流。
- `expandSkillWithApproval@rpc.go:315-320` 在 `svc` 不是具体 `*service` 时，会回落到兼容聚合接口的 `Service.Expand(ctx, p)`；跨模块消费者不应依赖这条 legacy expand 面。

与 legacy `skills/list` 的区别：

- `skill/list` 只暴露 `name/summary/description/trust/content_hash/disable_model_invocation`
- `skills/list` 继续返回完整 `SkillInfo` 数组（原形态不变）

`skill/list` 的 prod caller / 消费者可由 `lsp_xref(references)` 追到：

- `dashboard/ui_page.go:158-162`：技能页通过 `skillmodule.SkillLister.ListSkills` 读取元数据
- `prompt/module.go:122-149` + `skill_catalog_provider.go:147-155`：skill catalog 通过 `skillpkg.SkillCatalogSource` 读取 `ListSkills`，并复用 approval / revision
- `turn/service.go:80-95` + `turn/skills.go:201-241,326-339`：hydrate 手动 skill ref 通过 `skillpkg.SkillHydrationSource` 调 `ListSkills` / `ReadLocal`
- `skills_match.go:43-50`：`skills/match/preview` 内部先列全量 skills 再做 local matcher

### 4.4 `skill/list` 读取链

```mermaid
sequenceDiagram
    participant C as RPC caller
    participant H as skill/list handler
    participant S as SkillLister.ListSkills
    participant SCAN as scanSkills
    participant ROOT as skillRoots(cwd)
    participant PARSE as parseSkillRecord/parseSkillInfo

    C->>H: skill/list {cwd}
    H->>H: ctx = scopedSkillContext(ctx, cwd)
    H->>S: ListSkills(ctx)
    S->>SCAN: scanSkills(cwdFromContext(ctx))
    SCAN->>ROOT: resolve project + system roots
    ROOT-->>SCAN: roots[]
    loop every SKILL.md
        SCAN->>PARSE: parseSkillRecord(root, path, defaultTrust)
        PARSE-->>SCAN: SkillInfo{Name/Summary/Trust/...}
    end
    SCAN-->>S: []skillRecord
    S-->>H: []SkillInfo
    H-->>C: skillListResult{skills:[slim dto]}
```

要点：

- `skills_meta.go:105-143` 会把 frontmatter 的 `name/description/summary/trigger_words/force_words/trust/allowed_tools/disable_model_invocation` 规范化进 `SkillInfo`。
- 若没写 `summary`，会从正文自动抽取摘要；`TriggerWords` 默认补 `@name` 与 `[skill:name]`。
- `ContentHash` 是整份 `SKILL.md` 的 SHA-256，全文件任一变动都会触发重新审批。

### 4.5 `skill/expand` 展开链与审批

`skill/expand` 不是简单读文件，而是 `prepare + approval` 两段式：

1. `skillExpandHandler@rpc.go:175-187` → `expandSkillWithApproval@rpc.go:276-280`
2. `service.go:134-178`：`expandWithApproval` 先 `prepareSkillExpand`，再 `ensureExpandApproved`
3. `skills_fs.go:61-95` + `121-169`：根据 `Section` 分流
   - 空 `section`：整份 `SKILL.md`
   - `#` 开头：按 H2/H3 heading 切片
   - 其它：当作 resource 相对路径
4. `service.go:145-178`：未受信任 skill 触发 approval requester；可缓存到 session/project scope

```mermaid
flowchart TD
    EXP[skill/expand RPC] --> PREP[prepareSkillExpand]
    PREP --> REC[resolveSkillRecordByName + cwd]
    PREP --> CASE{section}
    CASE -->|empty| FILE[expandSkillFile]
    CASE -->|# H2/H3| SEC[expandSkillSection]
    CASE -->|relative path| RES[expandSkillResource]
    FILE --> HASH[buildSkillExpandResult]
    SEC --> HASH
    RES --> HASH
    HASH --> APPROVAL{trusted? / cache hit?}
    APPROVAL -->|yes| OUT[result]
    APPROVAL -->|no| REQ[approvalRequester.RequestApproval]
    REQ -->|approved| CACHE[persist approval if cacheable]
    CACHE --> OUT
```

实现细节：

- `skills_fs.go:149-159` 明确限制 `section` 只接受 **H2/H3** heading；资源分支再走 `expandSkillResource()` 的相对路径校验。
- `skills_expand.go:72-213` 的 `ExpandBody` / `ReadResource` 是更细粒度的底层原语；mcp-orch 会把它们映射成工具 `skill_expand_body` / `skill_read_resource`。
- `service.go:215-248` 构造 approval payload 时会带上 `name/section/content_hash/trust/approval_scope/skills_dir/project_root/agentId/threadId/sessionId/turnId`。

### 4.6 legacy RPC 共存面

`newSkillHandlers()` 当前通过 `mergeSkillHandlerMaps()` 同时暴露 host 新键与 legacy 老键；legacy 入口主要分布在 `skillLocalHandlers / skillRemoteHandlers / skillPreviewHandlers` 与 `skillsListHandler`：

| 分组 | 现存键 | 说明 |
|---|---|---|
| 旧列表/匹配 | `skills/list`、`skills/match/preview` | 老客户端继续可用；`match/preview` 仍走 local matcher + configured state |
| 本地 FS | `skills/local/read`、`skills/local/listFiles`、`skills/local/write`、`skills/local/importDir`、`skills/local/delete` | 统一走 `resolveSkillPath` / `writeSkill` / import/delete 逻辑 |
| 远端/配置 | `skills/remote/{list,read,write,export}`、`skills/config/{read,write}`、`skills/summary/write` | `config/read` 仍偏 stub，`config/write` 还是 legacy 主 skill 文件写口 |
| 细粒度展开 | `skills/expandBody`、`skills/readResource` | 内部服务名仍挂在 `skills/*`，但对 MCP 工具暴露为 `skill_expand_body` / `skill_read_resource` |
| 命令执行 | `command/exec` | 独立于渐进披露，但同属 skill module |

### 4.7 事件与副作用

- `events.go:27-70`
  - `publishSkillsChanged(action, name)` 在写入/导入/删除后触发
  - 100ms debounce 合并 burst，最终发出 `uidto.SkillsChanged`
- `skills_match.go:14-187`
  - `skills/match/preview` 先算 configured skills，再做 local `force / explicit / trigger` 匹配
  - 显式 `@skill` 与 `[skill:skill]` 优先级高于普通 trigger words
- `exec.go` / `exec_tokenizer_safety.go`
  - `command/exec` 会拒绝 shell 解释器、危险包装器与 shell metacharacters

### 4.8 prompt / turn / dashboard 的消费关系

```mermaid
flowchart LR
    SK[skill module\nService aggregate + narrow ports]
    DASH[dashboard skills page] -->|SkillLister.ListSkills| SK
    PROMPT[prompt.SkillCatalogProvider] -->|SkillCatalogSource.ListSkills + approval/revision| SK
    TURN[turn hydrateSkillRefs] -->|SkillHydrationSource.ListSkills/ReadLocal| SK
    MATCH[skills/match/preview] -->|internal ListSkills + configured state| SK
    TOOLS[toolbridge host tools] -->|SkillHostToolReader.ExpandBody/ReadResource| SK
    HOST[skill/list / skill/expand] -->|Service aggregate host RPC| SK
    EVENTS[SkillsChanged event bus] <-->|publish| SK
```

注意：

- `prompt.SkillCatalogProvider` 不是独立缓存源：`prompt/module.go:122-149` 注入 `skillpkg.SkillCatalogSource`，`skill_catalog_provider.go:147-155` 的 `Resolve()` 真实调用 `ListSkills(skillpkg.WithCWD(ctx, input.BuildCtx.CWD))`；dashboard 技能页（`ui_page.go:158-162` + `rpc.go:113-115`）与 turn hydration（`turn/skills.go:201-241`）共用这条扫描真值。
- `prompt` 侧的 progressive disclosure 现在通过 `internal/module/prompt/module.go:14-26` 直接注册 `NewCompositeNativeSkillDetector` / `NewSkillCatalogProviderFx` / `RegisterSkillCatalogProviderIfEnabled`；**不再存在单独的 skill-catalog Fx wiring 文件**。
- `turn` 只消费 `SkillHydrationSource.ListSkills/ReadLocal` 做 hydration，不会直接走 `skill/expand` RPC，也不依赖完整 `skill.Service`。
- `dashboard` 仅把 `SkillLister.ListSkills` 当作只读列表来源，不参与 approval / resource read。

### 4.9 文件地图

| 文件 | 作用 |
|---|---|
| `module.go` | Fx 装配、dispatcher 绑定。 |
| `contract.go` | `WithCWD`、`Service` 兼容聚合接口，以及 `SkillCommandExecutor` / `SkillLister` / `SkillCatalogSource` / `SkillHydrationSource` / `SkillHostToolReader` / `SkillRevisionSource` / `TrustRevisionSource` 窄端口。 |
| `rpc.go` | 新旧 RPC 共存入口。 |
| `service.go` | roots、approval cache、approval requester、cwd-scoped root 决策。 |
| `skills_meta.go` | 扫描 `SKILL.md`、frontmatter 解析、默认 trust / content hash。 |
| `skills_fs.go` | `ListSkills`、`Expand`、本地读写/导入/删除主流程。 |
| `skills_expand.go` | `ExpandBody` / `ReadResource`、Markdown 锚点切片。 |
| `skills_match.go` | `skills/match/preview`、configured + local matcher。 |
| `events.go` | `SkillsChanged` debounce 事件。 |
| `exec*.go` | `command/exec` 安全执行与 token 化检查。 |
| `approval*.go` / `trust.go` | 审批缓存、trust scope。 |
| `rollout_markers.go` | rollout marker 文件。 |

---

## 5. 本卷需要牢记的代码真值

1. `dashboard/prompts` 的 `{cwd}` 作用域已经接到 **prod handler**；`withDashboardPromptScopeCWD` 不再只是测试 helper。
2. `promptstore.ListFilter.CWD` 只在 contract 层露出；当前 store 实现未真正下推过滤，实际筛选仍在 `dashboard/ui_page.go`。
3. `skill/list` / `skill/expand` 是新增 host-facing 渐进披露口；legacy `skills/*` 族没有删除。
4. 旧的独立 skill-catalog Fx wiring 已并入 `internal/module/prompt/module.go`；prompt/turn/dashboard/toolbridge 均应按 skill 窄端口理解，不再按完整 `skill.Service` 消费。
5. `lspgui` 在当前仓内 **无源码目录**；旧 codemap 对它的实现级描述已过时。

---

## 6. 测试入口 + how-to 补遗

### 6.1 测试入口

> freeze 口径：本卷直接涉及的 `dashboard / skill / uistate` 当前无独立 freeze 项；相邻 `prompt` 真值仍以 §1.1 的 `27` 为准。

| 包 | 测试文件 | 核心 Test* | 锁定点 |
|---|---|---|---|
| `dashboard` | `service_test.go` | `TestGetDashboardPageFiltersPromptsByScopedCWD` / `TestDashboardPromptsHandlerScopesByCWDAndReturnsPromptsKey` | 锁定 `dashboard/prompts` 的 `{cwd}` 透传、`prompts` 顶层 key 与 scope 过滤。 |
| `skill` | `cwd_scope_test.go` | `TestListSkillsScopesByRequestCWD` / `TestAllSkillServiceMethodsRequireCWD` | 锁定 roots 按请求 `cwd` 隔离，以及 `ListSkills` / `Expand` / `ExpandBody` / `ReadResource` 都要求 cwd。 |
| `skill` | `rpc_types_test.go` | `TestSkillListHostRPCResponseHidesLegacyFields` / `TestSkillExpandHostRPCScopesByCWD` | 锁定 host `skill/list` 的瘦身返回与 `skill/expand` 的 cwd-scoped 展开。 |
| `prompt` | `skill_catalog_provider_test.go` | `TestSkillCatalogProvider_EmptySkillsReturnsNil` | 锁定 `SkillCatalogProvider` 走 `ListSkills` 后的空列表容错。 |
| `uistate` | `phase2_stats_patch_pending_test.go` / `sidebar_test.go` | `TestActivityStats_CommandIncrementsCommands` / `TestProjectionSubscriptionsUpdateSidebarFromLifecycleAndOutputEvents` | 锁定 sidebar / activity stats 的 projection 读侧更新。 |
| `uistate/timeline` | `timeline/timeline_test.go` | `TestAppendAndGetByThread` | 锁定 timeline 读模型 append/get 基线。 |

### 6.2 how-to 三条

1. **dashboard 页**
   - 触发：新增只读聚合 page / tab。
   - 步骤：`DashboardPage` 扩字段 → `dashboardPageLoaders@dashboard/ui_page.go:66-92` 挂 loader → `NewDashboardHandlers@dashboard/rpc.go:83-150` 接 `ui/dashboard/get` 或专门 `dashboard/*` 路由。
   - 验证：优先补 `dashboard/service_test.go`。
2. **skill RPC**
   - 触发：新增 `skill/*` 或 `skills/*` 且需要 `cwd` 作用域。
   - 步骤：先补 service/helper，再接 `skillCoreHandlers / skillPreviewHandlers / skillLocalHandlers / skillRemoteHandlers@skill/rpc.go`；host 口保持 `skillListResult` / `skillExpandParams` 这类单独 DTO，并通过 `scopedSkillContext@skill/rpc.go:269-274` 统一写入 `WithCWD`。
   - 验证：`cwd_scope_test.go` + `rpc_types_test.go`。
3. **uistate**
   - 触发：线程 / 回合事件要进 sidebar、timeline 或 stats。
   - 步骤：先有事件 emit，再由 `registerProjections@uistate/module.go:46-66` 挂生命周期，最后在 `registerProjectionSubscriptions@uistate/projector.go:17-60` 订阅并落读模型。
   - 验证：`sidebar_test.go` / `timeline/timeline_test.go`。
