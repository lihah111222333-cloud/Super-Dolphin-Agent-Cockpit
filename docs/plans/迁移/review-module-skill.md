# module/skill 总审查

审查范围：`internal/module/skill/*`

审查方式：只读 LSP 审查 + `go test -cover ./internal/module/skill`

总体结论：`module/skill` 的文件拆分、`fx` 接线、`Service` 闭环、`22` 个 RPC key 注册、`execShell` 内聚、`http.Client` 15s timeout、`prompt.Store` 清理、`provider/` 反向依赖约束，基本都成立；但它仍不是完整的 V2 等价实现。当前最实质的缺口有 4 个：

- `Blocker`：`command/exec` 的协议已经从 V2 的 `argv + env + cwd` 漂移到当前的 `command + args + cwd`，调用方无法再显式 overlay `env`。证据：`internal/module/skill/rpc_types.go:26-30`、`internal/module/skill/contract.go:13`、`internal/module/skill/rpc.go:51-53` 对照 `go-agent-v2/internal/apiserver/methods_command.go:20-24,40-92`。
- `Warning`：skills 写路径丢了 V2 的 `skillWriteWithNotify -> notifySkillsChanged("skills/changed")` 链。当前 `collectChangedSkillNames` 甚至已变成未使用函数。证据：`internal/module/skill/skills_match.go:189-204` 的 references 只有声明自身；对照 V2 `go-agent-v2/internal/apiserver/methods_command.go:165-202,204-215,251-289`。
- `Warning`：`skills/config/read` 和 configured auto-match 仍是 stub。`MatchPreview` 的 wiring 正确，但默认 configured 结果为空，不是 provider/backing-store 语义。证据：`internal/module/skill/skills_fs.go:143-157`、`internal/module/skill/skills_match.go:59-80`。
- `Warning`：测试通过，但包级覆盖率只有 `42.4%`。卡片 CRUD、RPC wrapper、`ReadRemote/WriteRemote/ImportLocalDir/DeleteLocal/WriteSummary` 等大量路径仍是 `0%`。

LSP diagnostics 额外看到两点：

- `internal/module/skill/skills_match.go:189`：`collectChangedSkillNames` 未使用。
- `internal/module/skill/skills_meta.go:201`：只有一个 `stringsseq` 性能提示，不是功能错误。

## 审查维度

### 1. 文件清单与行数

总计 `15` 个文件，`1793` 行。

| 文件 | 行数 |
| --- | ---: |
| `internal/module/skill/cards.go` | 199 |
| `internal/module/skill/contract.go` | 26 |
| `internal/module/skill/exec.go` | 177 |
| `internal/module/skill/exec_test.go` | 122 |
| `internal/module/skill/module.go` | 26 |
| `internal/module/skill/rpc.go` | 102 |
| `internal/module/skill/rpc_skill_types.go` | 60 |
| `internal/module/skill/rpc_types.go` | 30 |
| `internal/module/skill/service.go` | 47 |
| `internal/module/skill/skills_fs.go` | 283 |
| `internal/module/skill/skills_fs_test.go` | 62 |
| `internal/module/skill/skills_match.go` | 204 |
| `internal/module/skill/skills_match_test.go` | 89 |
| `internal/module/skill/skills_meta.go` | 337 |
| `internal/module/skill/types.go` | 29 |

### 2. handler 完整性

`rpc.go` 的 `NewSkillHandlers` 完整注册了 `22` 个 key，没有遗漏。证据：`internal/module/skill/rpc.go:42-87`。

| RPC key | adapter | service 方法 | 备注 |
| --- | --- | --- | --- |
| `command/card/list` | 直连 `StrictHandler` | `ListCards` | 唯一一个 card surface 直连，无工厂 |
| `command/card/get` | `cardByKeyHandler` | `GetCard` | key-only |
| `command/card/create` | `cardCreateHandler` | `CreateCard` | `cardPayload -> Card` |
| `command/card/update` | `cardUpdateHandler` | `UpdateCard` | `cardPayload -> Card` |
| `command/card/delete` | `cardByKeyHandler` | `DeleteCard` | key-only |
| `command/card/run` | `cardRunHandler` | `RunCard` | `key + args` |
| `command/card/versions` | `cardByKeyHandler` | `ListCardVersions` | key-only |
| `command/exec` | 直连 `StrictHandler` | `ExecCommand` | `execParams` |
| `skills/list` | 直连 `StrictHandler` | `ListSkills` | 包一层 `{"skills": ...}` |
| `skills/local/read` | 直连 `StrictHandler` | `ReadLocal` | `pathParams` |
| `skills/local/listFiles` | 直连 `StrictHandler` | `ListLocalFiles` | `listSkillFilesParams` |
| `skills/local/write` | 直连 `StrictHandler` | `WriteLocal` | `contentParams` |
| `skills/local/importDir` | 直连 `StrictHandler` | `ImportLocalDir` | `importSkillDirParams` |
| `skills/local/delete` | 直连 `StrictHandler` | `DeleteLocal` | `deleteLocalSkillParams` |
| `skills/remote/list` | 直连 `StrictHandler` | `ReadRemote` | 与 `skills/remote/read` 同实现 |
| `skills/remote/export` | `namedContentHandler` | `WriteRemote` | 与 `skills/remote/write` 同实现 |
| `skills/remote/read` | 直连 `StrictHandler` | `ReadRemote` | 与 `skills/remote/list` 同实现 |
| `skills/remote/write` | `namedContentHandler` | `WriteRemote` | 与 `skills/remote/export` 同实现 |
| `skills/config/read` | 直连 `StrictHandler` | `ReadConfig` | stub config read |
| `skills/config/write` | `namedContentHandler` | `WriteSkillContent` | 保留 legacy key |
| `skills/summary/write` | 直连 `StrictHandler` | `WriteSummary` | summary update |
| `skills/match/preview` | 直连 `StrictHandler` | `MatchPreview` | auto-match preview |

补充判断：

- `22` 个 key 并不等于 `22` 个独立 service 方法。`skills/remote/list/read` 共享 `ReadRemote`，`skills/remote/export/write` 共享 `WriteRemote`。
- `NewSkillHandlers` 的 outgoing call hierarchy 已能直接对上 `contract.go` 暴露的 service surface。证据：`internal/module/skill/rpc.go:42-87` 的 call hierarchy。

### 3. V2 对照

#### command 族

| 当前 | V2 | 结论 |
| --- | --- | --- |
| `command/exec` 注册在 `internal/module/skill/rpc.go:51-53`，服务签名是 `ExecCommand(ctx, command string, args []string, cwd string)`，参数结构是 `execParams{command,args,cwd}`。 | `go-agent-v2/internal/apiserver/methods.go:157-166` 注册 `command/exec`；V2 参数是 `commandExecParams{argv,cwd,env}`，逻辑在 `go-agent-v2/internal/apiserver/methods_command.go:20-24,40-92`。 | key 还在，但协议不等价。`argv` 被拆成 `command + args`，`env` 直接消失。timeout/LSP hint/blocklist 基本保留，调用方 env overlay 能力丢失。 |

#### card 族

| 当前 | V2 | 结论 |
| --- | --- | --- |
| 当前公开 surface 是 `command/card/list|get|create|update|delete|run|versions`，全部在 `internal/module/skill/rpc.go:44-50`。 | V2 当前可见的 command card surface 主要是工具层 `command_list` / `command_get`，规格在 `go-agent-v2/pkg/toolsdk/tools/resource_specs.go:116-142`，实现是 `go-agent-v2/pkg/toolsdk/tools/resource.go:253-284`。 | `list/get` 有读面类比，但 `create/update/delete/run/versions` 不是 V2 既有 RPC 面，而是新 surface。不能宣称 card CRUD/run 全量“对齐 V2”；更准确的说法是“V3 新增了 card 写面与执行面”。 |

#### skills 族

| 当前 | V2 | 结论 |
| --- | --- | --- |
| `skills/*` 14 个 key 注册在 `internal/module/skill/rpc.go:56-85`。 | V2 `registerSkillMethods` 在 `go-agent-v2/internal/apiserver/methods.go:229-237`，typed handler 在 `go-agent-v2/internal/apiserver/methods_command.go:217-289`。 | key 面基本 1:1 保留，包括 `skills/remote/list` 别名和 `skills/config/write` legacy 语义。 |
| 当前 `MatchPreview` 自己做 `ListSkills + ReadConfig`，configured 路径靠 `ReadConfig` stub 或 `readConfigState` 注入。见 `internal/module/skill/skills_match.go:14-80`。 | V2 `SkillsMatchPreview` 通过 `AutoMatchCollector`，并显式传 `IncludeConfiguredExplicit/Force=true`；桥接发生在 `go-agent-v2/internal/apiserver/methods_command.go:94-113` 与 `go-agent-v2/internal/skills/methods.go:363-386`。 | wiring 方向对，但语义明显收缩。当前不是 provider-backed matcher。 |
| 当前 skills 写路径没有 `skills/changed` notify；`collectChangedSkillNames` 未被消费。 | V2 通过 `skillWriteWithNotify` 做写后事件，见 `go-agent-v2/internal/apiserver/methods_command.go:251-289`。 | V2 的写后 side effect 没迁过来。 |

### 4. Service 接口

`contract.go` 暴露 `20` 个方法，`service` 通过 `var _ Service = (*service)(nil)` 做了编译期覆盖校验。证据：`internal/module/skill/contract.go:5-26`、`internal/module/skill/service.go:27`。

| 接口方法 | 实现位置 |
| --- | --- |
| `ListCards` | `internal/module/skill/cards.go:18-24` |
| `GetCard` | `internal/module/skill/cards.go:26-32` |
| `CreateCard` | `internal/module/skill/cards.go:34-49` |
| `UpdateCard` | `internal/module/skill/cards.go:51-68` |
| `DeleteCard` | `internal/module/skill/cards.go:70-83` |
| `RunCard` | `internal/module/skill/cards.go:93-111` |
| `ListCardVersions` | `internal/module/skill/cards.go:85-91` |
| `ExecCommand` | `internal/module/skill/exec.go:38-40` |
| `ListSkills` | `internal/module/skill/skills_fs.go:15-25` |
| `ReadLocal` | `internal/module/skill/skills_fs.go:27-43` |
| `ListLocalFiles` | `internal/module/skill/skills_fs.go:45-59` |
| `WriteLocal` | `internal/module/skill/skills_fs.go:61-76` |
| `ImportLocalDir` | `internal/module/skill/skills_fs.go:78-98` |
| `DeleteLocal` | `internal/module/skill/skills_fs.go:100-109` |
| `ReadRemote` | `internal/module/skill/skills_fs.go:111-130` |
| `WriteRemote` | `internal/module/skill/skills_fs.go:132-141` |
| `ReadConfig` | `internal/module/skill/skills_fs.go:143-157` |
| `WriteSkillContent` | `internal/module/skill/skills_fs.go:159-168` |
| `WriteSummary` | `internal/module/skill/skills_fs.go:170-179` |
| `MatchPreview` | `internal/module/skill/skills_match.go:14-28` |

判定：`20/20` 覆盖完整，无缺实现。

### 5. card 工厂

结论：`cardByKeyHandler` 不是 `7/7`，也不是单一 mega-factory。

精确状态如下：

- `cardByKeyHandler` 只覆盖 `3/7`：`get/delete/versions`。证据：`internal/module/skill/rpc.go:12-16,45,48,50`，references 也只落这三处。
- `cardCreateHandler` 覆盖 `create`。证据：`internal/module/skill/rpc.go:18-22,46`。
- `cardUpdateHandler` 覆盖 `update`。证据：`internal/module/skill/rpc.go:24-28,47`。
- `cardRunHandler` 覆盖 `run`。证据：`internal/module/skill/rpc.go:30-34,49`。
- `command/card/list` 仍是直连 `StrictHandler`。证据：`internal/module/skill/rpc.go:44`。

所以更准确的说法是：

- `cardByKeyHandler = 3/7`
- `card adapter helpers 合计 = 6/7`
- 剩余 `1/7` 的 `list` 为直连

### 6. exec 安全

结论：当前实现具备 timeout/cwd/env/metachar/blocklist 基础护栏，但与 V2 的 env contract 不等价。

| 项目 | 当前实现 | 判定 |
| --- | --- | --- |
| timeout | `internal/module/skill/exec.go:57-59` 用 `platformconfig.WithRPCRequestTimeout(ctx)`；常量在 `internal/platform/config/timeouts.go:15,19-20` 为 `30s` | 通过 |
| cwd fallback | `internal/module/skill/exec.go:56,107-111` 空 `cwd` 回退到 `s.projectRoot` | 通过 |
| blocked command | `internal/module/skill/exec.go:16-21,62-73` | 通过 |
| shell 元字符守卫 | `internal/module/skill/exec.go:51-55,75-82` 对非 shell 路径禁止 `|;&$\`` | 通过 |
| env 构造 | `internal/module/skill/exec.go:28-34,114-150` 只注入基础环境 + `PWD` + 前缀白名单 | 通过，但语义收缩 |
| 调用方 `env` overlay | 当前 RPC / Service 都没有该字段 | 不通过，非 V2 等价 |

补充：

- `ExecCommand` 的 shell 语法默认不可用，只有内部 `allowShell=true` 路径才能放行，这是正确的分层。
- `runExecCommand` 对 read commands 注入 LSP hint，延续了 V2 的导向。证据：`internal/module/skill/exec.go:93-95` 对照 `go-agent-v2/internal/apiserver/methods_command.go:84-90`。

### 7. execShell

结论：内部 shell 执行入口仍是 unexported，仅由卡片执行路径消费。

证据：

- `execShell` 定义为 `func (s *service) execShell(...)`，未出现在 `Service` 接口和 `rpc.go` surface 中。见 `internal/module/skill/exec.go:42-44`、`internal/module/skill/contract.go:5-26`、`internal/module/skill/rpc.go:42-87`。
- references 只有声明自身和 `RunCard` 内部调用。见 `internal/module/skill/cards.go:106` 与 `internal/module/skill/exec.go:42` 的 references。
- `exec_test.go:75-94` 明确验证了 shell syntax 只经 `RunCard -> execShell` 内部路径放行。

### 8. skills_match

结论：`AgentID -> threadID` fallback 和 `collectConfiguredAutoMatchedSkills` 的首参消费都对；但 configured 语义仍受 `ReadConfig` stub 限制。

证据链：

- fallback：`internal/module/skill/skills_match.go:15,30-35`。`threadID` 为空时回退到 trim 后的 `agentID`。
- 首参消费：`internal/module/skill/skills_match.go:15-17,43-57,59-74`。`MatchPreview` 先解出 `resolvedThreadID`，再把它传给 collector，collector 再把它传给 `collectConfiguredAutoMatchedSkills(ctx, resolvedID)`。
- 回读配置：`internal/module/skill/skills_match.go:76-80` 先走注入的 `readConfigState`，否则才落到 `ReadConfig`。
- 测试：
  - `internal/module/skill/skills_match_test.go:11-38` 覆盖 agent fallback。
  - `internal/module/skill/skills_match_test.go:40-71` 覆盖 configured collector 消费 resolvedID。

风险边界：

- 当前 configured match 的真实来源并不是 provider/backing-store，而是 `ReadConfig` stub 或测试注入。证据：`internal/module/skill/skills_fs.go:143-157`。
- V2 在 `SkillsMatchPreview` 里会传 `IncludeConfiguredExplicit/IncludeConfiguredForce=true` 给 collector；当前实现没有这层 option contract。对照 `go-agent-v2/internal/skills/methods.go:363-370`、`go-agent-v2/internal/skills/methods_test.go:83-143`。

### 9. skills_fs

聚焦 `ReadConfig / WriteSkillContent / ReadRemote`：

| 方法 | 当前语义 | 判定 |
| --- | --- | --- |
| `ReadConfig` | 要求非空 `agent_id`，返回 `skills=[]`、`session_bound=false`、`configured=false`、`binding_count=0`、`binding_source="stub"`。见 `internal/module/skill/skills_fs.go:143-157` | 结构正确，但语义是明确 stub |
| `WriteSkillContent` | 要求非空 `name`，最终调用 `writeSkill(name, content)`，写入 `root/skillSlug(name)/SKILL.md`。见 `internal/module/skill/skills_fs.go:159-168`、`internal/module/skill/skills_meta.go:259-270,314-337` | 当前模块内语义自洽，但比 V2 `SkillService.WriteSkillContent` 更弱，没有 staged write / index / alias 解析保证 |
| `ReadRemote` | 用 `ctx` 建 `GET` 请求，经共享 `s.http` 执行；非 2xx 读最多 `8KB` 错误体；正文限 `1MB`。见 `internal/module/skill/skills_fs.go:111-130` | 通过 |

和 V2 的差异：

- `ReadConfig`：V2 也是 stub，但只返回 `agent_id/skills/session_bound`。当前多了 `configured/binding_count/binding_source`，仍然不是持久绑定语义。对照 `go-agent-v2/internal/skills/methods.go:388-394`。
- `WriteSkillContent`：V2 `SkillService.WriteSkillContent` 采用 staged dir + activate 方式，具备更强原子性和 ID/index 语义。对照 `go-agent-v2/internal/service/skills_core.go:407-438,470-479`。当前 `writeSkill` 是直接 `MkdirAll + WriteFile`，语义更轻。
- `ReadRemote`：15s timeout、1MB body limit、非 2xx 错误体读取，这几项与 V2 基本一致。对照 `go-agent-v2/internal/skills/methods.go:437-459`。

### 10. http timeout

结论：通过。

证据：

- `internal/module/skill/service.go:29-35` 在构造时固定 `http: &http.Client{Timeout: 15 * time.Second}`。
- `internal/module/skill/exec_test.go:60-73` 显式断言 `impl.http.Timeout == 15*time.Second`。

### 11. prompt.Store 移除

结论：在审查范围内确认零残留。

证据：

- 文本搜索 `internal/module/skill` 下 `prompt.Store / PromptStore / PromptTemplateStore / promptstore` 均无命中。
- `service` 当前只持有 `cards/root/projectRoot/http/readConfigState`。见 `internal/module/skill/service.go:19-25`。
- `module.go` 只接 `commandcardstore.Store` 与 `platformconfig.Config`。见 `internal/module/skill/module.go:20-25`。

### 12. import 方向

结论：通过，未发现 `provider/` 反向依赖。

证据：

- 对 `internal/module/skill` 全量文本搜索 `provider/` 无命中。
- 当前 import 主要指向 `internal/store/commandcard`、`internal/platform/config`、`internal/platform/rpc`，没有 `internal/provider/*`。

### 13. fx 注册

结论：`module.go` wiring 简洁且闭合。

证据：

- `internal/module/skill/module.go:15-18`：`fx.Module("skill", fx.Provide(newService), fx.Provide(NewSkillHandlers))`
- `internal/module/skill/module.go:20-25`：`newService(cards, cfg)` 从 `platformconfig.Config.ProjectRoot` 取项目根，再调 `NewService(cards, projectRoot)`。
- `NewService` 的 references 只落在 `module.go` 和 `exec_test.go`。说明构造路径集中，无旁路构造。证据：`internal/module/skill/service.go:29-35` 的 references。

### 14. 函数复杂度

按 document symbol 的起止行统计，当前范围内最长的 3 个函数如下：

| 排名 | 函数 | 行数 | 位置 |
| --- | --- | ---: | --- |
| 1 | `copySkillDir` | 30 | `internal/module/skill/skills_fs.go:254-283` |
| 2 | `upsertSkillSummary` | 28 | `internal/module/skill/skills_meta.go:285-312` |
| 3 | `parseSkillInfo` | 27 | `internal/module/skill/skills_meta.go:81-107` |

判断：

- top 3 都没有失控，最大也只有 `30` 行。
- 复杂度风险不在“单函数过长”，而在“行为语义是否维持 V2 等价”，尤其是 `command/exec`、skills notify、configured matcher、技能文件写路径。

### 15. 测试覆盖

`go test -cover ./internal/module/skill` 已通过，包级覆盖率 `42.4%`。

#### `skills_match_test`

已覆盖：

- `AgentID` fallback 到 `thread_id`
- `collectConfiguredAutoMatchedSkills` 消费 resolved ID

未覆盖：

- `classifySkillMatch` 的 `force/explicit/trigger` 三分支组合
- `joinMatchText` 对 `input` 多字段拼接
- `configuredSkillNames` 多类型输入分支
- `dedupeAutoMatchedSkills`
- 与 V2 对齐的 collector option contract

#### `exec_test`

已覆盖：

- shell metachar guard
- 空 `cwd` 回退 `projectRoot`
- 前缀白名单 env 注入
- `NewService` 的 `projectRoot/http timeout`
- `RunCard -> execShell` 内部 shell 路径

未覆盖：

- `blockedCommands` 的拒绝分支
- `validateExecCommand` 空命令分支
- read command 的 LSP hint 注入
- `runExecCommand` 的非零 exit code 分支
- `shellQuote`

#### `skills_fs_test`

已覆盖：

- `ReadConfig` stub 字段
- `WriteSkillContent` 写入路径与内容

未覆盖：

- `ReadRemote`
- `WriteRemote`
- `ReadLocal`
- `ListLocalFiles`
- `WriteLocal`
- `ImportLocalDir`
- `DeleteLocal`
- `WriteSummary`

#### 覆盖率细化

从 `go tool cover -func` 看，当前最显著的盲区是：

- `cards.go`：`ListCards/CreateCard/UpdateCard/DeleteCard/ListCardVersions/lookupCard/archiveCard/normalizeCard/validateCard/mergeCard` 全部 `0%`
- `rpc.go`：`cardByKeyHandler/cardCreateHandler/cardUpdateHandler/cardRunHandler/namedContentHandler/NewSkillHandlers/buildCard` 全部 `0%`
- `skills_fs.go`：`ReadLocal/ListLocalFiles/WriteLocal/ImportLocalDir/DeleteLocal/ReadRemote/WriteRemote/WriteSummary/listSkillFiles/collectImportSources/importSources/importSource/copySkillDir` 全部 `0%`
- `skills_match.go`：`collectChangedSkillNames` 为 `0%`，且 LSP 诊断提示未使用

这意味着当前测试更像“局部语义 smoke test”，不是“模块级回归保护网”。

## 最终判断

`module/skill` 目前可以判定为“结构闭合、行为部分收敛，但尚未达到完整 V2 等价”的状态。

成立的部分：

- `15` 文件拆分清晰，`20` 个 `Service` 方法实现齐全。
- `22` 个 RPC key 全部注册，`fx` wiring 简洁。
- `ExecCommand` 的 timeout/cwd/blocklist/metachar/env allowlist 基础护栏齐全。
- `execShell` 没有泄露到公开 surface。
- `http.Client` 15s timeout、`prompt.Store` 清理、`provider/` import 方向都成立。

未完全成立的部分：

- `command/exec` 还不是 V2 contract 等价面。
- skills 写后 notify side effect 丢失。
- configured auto-match 仍是 stub 驱动，不是 provider/backing-store 驱动。
- card helper 工厂仍不是 `7/7`。
- 测试覆盖偏浅，尤其是 card CRUD / RPC wrapper / skills FS 大量路径没有回归保护。
