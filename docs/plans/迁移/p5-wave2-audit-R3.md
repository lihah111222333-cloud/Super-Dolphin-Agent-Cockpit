# P5 波次 2 审查 R3（skill）

## 1-2. 编译+守卫

- `go build ./...`：通过，exit code 0。
- `go test ./internal/archtest/...`：通过，输出 `ok github.com/anthropic-ai/super-agent-v3/internal/archtest 3.467s`。

## 3. 方法完整性（逐一列出）

V3 `internal/module/skill/rpc.go:19-62` 共注册 22 个 handler key。对照基线为：

- V2 core methods 中的 `command/exec`：`go-agent-v2/internal/apiserver/methods.go:157-160`
- V2 `registerSkillMethods`：`go-agent-v2/internal/apiserver/methods.go:229-237`

逐一核对如下：

| Key | V3 证据 | V2 对照 | 结论 |
| --- | --- | --- | --- |
| `command/card/list` | `internal/module/skill/rpc.go:20` | cited V2 baseline 无对应注册 | V3 额外新增 |
| `command/card/get` | `internal/module/skill/rpc.go:21` | cited V2 baseline 无对应注册 | V3 额外新增 |
| `command/card/create` | `internal/module/skill/rpc.go:22-24` | cited V2 baseline 无对应注册 | V3 额外新增 |
| `command/card/update` | `internal/module/skill/rpc.go:25-27` | cited V2 baseline 无对应注册 | V3 额外新增 |
| `command/card/delete` | `internal/module/skill/rpc.go:28` | cited V2 baseline 无对应注册 | V3 额外新增 |
| `command/card/run` | `internal/module/skill/rpc.go:29` | cited V2 baseline 无对应注册 | V3 额外新增 |
| `command/card/versions` | `internal/module/skill/rpc.go:30` | cited V2 baseline 无对应注册 | V3 额外新增 |
| `command/exec` | `internal/module/skill/rpc.go:31-33` | `go-agent-v2/internal/apiserver/methods.go:157-160`, `go-agent-v2/internal/apiserver/methods_command.go:40-92` | 已覆盖 |
| `skills/list` | `internal/module/skill/rpc.go:34-40` | `go-agent-v2/internal/apiserver/methods.go:231` | 已覆盖 |
| `skills/local/read` | `internal/module/skill/rpc.go:41` | `go-agent-v2/internal/apiserver/methods.go:231` | 已覆盖 |
| `skills/local/listFiles` | `internal/module/skill/rpc.go:42` | `go-agent-v2/internal/apiserver/methods.go:231` | 已覆盖 |
| `skills/local/write` | `internal/module/skill/rpc.go:43` | `go-agent-v2/internal/apiserver/methods.go:232` | 已覆盖 |
| `skills/local/importDir` | `internal/module/skill/rpc.go:44` | `go-agent-v2/internal/apiserver/methods.go:232` | 已覆盖 |
| `skills/local/delete` | `internal/module/skill/rpc.go:45` | `go-agent-v2/internal/apiserver/methods.go:232` | 已覆盖 |
| `skills/remote/list` | `internal/module/skill/rpc.go:46` | `go-agent-v2/internal/apiserver/methods.go:233` | 已覆盖 |
| `skills/remote/export` | `internal/module/skill/rpc.go:47-49` | `go-agent-v2/internal/apiserver/methods.go:233` | 已覆盖 |
| `skills/remote/read` | `internal/module/skill/rpc.go:50` | `go-agent-v2/internal/apiserver/methods.go:233` | 已覆盖 |
| `skills/remote/write` | `internal/module/skill/rpc.go:51-53` | `go-agent-v2/internal/apiserver/methods.go:234` | 已覆盖 |
| `skills/config/read` | `internal/module/skill/rpc.go:54` | `go-agent-v2/internal/apiserver/methods.go:234` | 已覆盖 |
| `skills/config/write` | `internal/module/skill/rpc.go:55-57` | `go-agent-v2/internal/apiserver/methods.go:234` | 已覆盖 |
| `skills/summary/write` | `internal/module/skill/rpc.go:58-60` | `go-agent-v2/internal/apiserver/methods.go:235` | 已覆盖 |
| `skills/match/preview` | `internal/module/skill/rpc.go:61` | `go-agent-v2/internal/apiserver/methods.go:235` | 已覆盖 |

结论：

- 相对 cited V2 baseline，无缺失 key。
- V3 比 cited V2 baseline 多 7 个 `command/card/*` key，全部集中在 `internal/module/skill/rpc.go:20-30`。
- 如果“22 个齐全”的目标是以 V3 当前 `rpc.go` 为准，则 22 个都已注册；如果以 cited V2 baseline 为准，则 V3 是“15 个对齐 + 7 个新增”，不是“22 个全部来自 V2”。

## 4. import 方向（逐文件）

| 文件 | import 列表 | 方向结论 |
| --- | --- | --- |
| `internal/module/skill/cards.go` | `context`, `encoding/json`, `errors`, `fmt`, `regexp`, `strings`, `github.com/jackc/pgx/v5`, `internal/store/commandcard` (`internal/module/skill/cards.go:3-14`) | 无 `provider/*`，无 `platform/rpc` |
| `internal/module/skill/contract.go` | `context` (`internal/module/skill/contract.go:3`) | 合规 |
| `internal/module/skill/exec.go` | `bytes`, `context`, `errors`, `io`, `os/exec`, `path/filepath`, `strings` (`internal/module/skill/exec.go:3-11`) | 合规 |
| `internal/module/skill/module.go` | `go.uber.org/fx` (`internal/module/skill/module.go:3`) | 合规 |
| `internal/module/skill/rpc.go` | `context`, `encoding/json`, `github.com/creachadair/jrpc2/handler`, `internal/platform/rpc` (`internal/module/skill/rpc.go:3-10`) | 唯一命中 `platform/rpc`，且属于允许例外 |
| `internal/module/skill/rpc_skill_types.go` | 无 import (`internal/module/skill/rpc_skill_types.go:1-61`) | 合规 |
| `internal/module/skill/rpc_types.go` | `encoding/json` (`internal/module/skill/rpc_types.go:3`) | 合规 |
| `internal/module/skill/service.go` | `net/http`, `os`, `path/filepath`, `strings`, `internal/store/commandcard`, `internal/store/prompt` (`internal/module/skill/service.go:3-11`) | 无 `provider/*`，无 `platform/rpc` |
| `internal/module/skill/skills_fs.go` | `context`, `errors`, `fmt`, `io`, `net/http`, `os`, `path/filepath`, `sort`, `strings` (`internal/module/skill/skills_fs.go:3-13`) | 合规 |
| `internal/module/skill/skills_match.go` | `context`, `strings` (`internal/module/skill/skills_match.go:3-6`) | 合规 |
| `internal/module/skill/skills_meta.go` | `errors`, `os`, `path/filepath`, `sort`, `strings`, `unicode`, `unicode/utf8` (`internal/module/skill/skills_meta.go:3-10`) | 合规 |
| `internal/module/skill/types.go` | `internal/store/commandcard` (`internal/module/skill/types.go:3`) | 合规 |

补充核对：

- 对 `internal/module/skill/*.go` 做 LSP 文本检索 `provider/|platform/rpc`，唯一命中为 `internal/module/skill/rpc.go:9`；未发现 `provider/*` import。

## 5. 行数（逐文件 + top 3 函数）

逐文件行数：

- `internal/module/skill/cards.go`：200 行
- `internal/module/skill/contract.go`：27 行
- `internal/module/skill/exec.go`：87 行
- `internal/module/skill/module.go`：9 行
- `internal/module/skill/rpc.go`：78 行
- `internal/module/skill/rpc_skill_types.go`：61 行
- `internal/module/skill/rpc_types.go`：31 行
- `internal/module/skill/service.go`：46 行
- `internal/module/skill/skills_fs.go`：269 行
- `internal/module/skill/skills_match.go`：121 行
- `internal/module/skill/skills_meta.go`：338 行
- `internal/module/skill/types.go`：30 行

Top 3 最长函数：

1. `NewSkillHandlers`：52 行，`internal/module/skill/rpc.go:12-63`
2. `(*service).ExecCommand`：34 行，`internal/module/skill/exec.go:27-60`
3. `copySkillDir`：30 行，`internal/module/skill/skills_fs.go:239-268`

## 6. cardHandler 工厂

- 存在局部工厂，但只做了部分消重。`cardByKey` 定义在 `internal/module/skill/rpc.go:13-15`，被 `command/card/get`、`command/card/delete`、`command/card/versions` 复用（`internal/module/skill/rpc.go:21,28,30`）。
- `create`/`update` 仍各自内联 `buildCard(cardPayload(p))`，见 `internal/module/skill/rpc.go:22-27`。
- `run` 仍单独内联，见 `internal/module/skill/rpc.go:29`。
- `list` 仍单独内联，见 `internal/module/skill/rpc.go:20`。
- `namedContent` 也是局部工厂，但服务的是 `skills/remote/export`、`skills/remote/write`、`skills/config/write`，不是 card CRUD，见 `internal/module/skill/rpc.go:16-18,47-57`。

结论：不是“6 个方法各写一遍”，但也不是完整的 card CRUD 工厂化；当前是“key-only handler 做了工厂，create/update/run/list 仍保留重复样板”。

## 7. Service 接口

- `Service` 接口定义在 `internal/module/skill/contract.go:5-25`，共 20 个方法。
- `rpc.go` 的每个 handler 都能在 `Service` 中找到对应调用：
  - card：`ListCards`、`GetCard`、`CreateCard`、`UpdateCard`、`DeleteCard`、`RunCard`、`ListCardVersions`，对应 `internal/module/skill/rpc.go:20-30` 与 `internal/module/skill/contract.go:6-12`
  - exec：`ExecCommand`，对应 `internal/module/skill/rpc.go:31-33` 与 `internal/module/skill/contract.go:13`
  - skill：`ListSkills`、`ReadLocal`、`ListLocalFiles`、`WriteLocal`、`ImportLocalDir`、`DeleteLocal`、`ReadRemote`、`WriteRemote`、`ReadConfig`、`WriteConfig`、`WriteSummary`、`MatchPreview`，对应 `internal/module/skill/rpc.go:34-61` 与 `internal/module/skill/contract.go:14-25`
- `skills/remote/list` 与 `skills/remote/read` 在 RPC 层都复用 `ReadRemote`，因此接口中没有单独的 `ListRemote`，见 `internal/module/skill/rpc.go:46,50` 与 `internal/module/skill/contract.go:20`。

结论：接口覆盖完整，没有“RPC 需要但 Service 未声明”的缺口。

## 8. service 实现

- `service.go` 不是空壳；它定义了状态、构造器和默认根目录逻辑，见 `internal/module/skill/service.go:18-45`。
- card 相关是真实实现，不是 `nil` 占位：
  - `ListCards` / `GetCard` 调 store，见 `internal/module/skill/cards.go:18-32`
  - `CreateCard` / `UpdateCard` 做规范化、校验、归档后 `Upsert`，见 `internal/module/skill/cards.go:34-68`
  - `DeleteCard` 先归档再删，见 `internal/module/skill/cards.go:70-83`
  - `RunCard` 先渲染模板再执行命令，见 `internal/module/skill/cards.go:93-111`
- `command/exec` 也是真实实现，使用 `exec.CommandContext(ctx, name, args...)`，不是 stub，见 `internal/module/skill/exec.go:27-60`；读类命令会注入 LSP 提示，见 `internal/module/skill/exec.go:48-49`。
- 本地/远端/配置/摘要也都是真实实现：
  - 本地读写与导入删除：`internal/module/skill/skills_fs.go:27-109`
  - 远端读取/写回：`internal/module/skill/skills_fs.go:111-141`
  - 配置与摘要：`internal/module/skill/skills_fs.go:143-164`
  - 技能扫描/元信息解析/摘要更新：`internal/module/skill/skills_meta.go:19-337`
- auto-match 实现在 service 内部：`internal/module/skill/skills_match.go:14-100`。

执行方式补充：

- `ExecCommand` 直接执行目标命令，不经 shell，见 `internal/module/skill/exec.go:41-46`。
- 但 `RunCard` 会把渲染后的模板包装成 `sh -lc <rendered>`，见 `internal/module/skill/cards.go:101-107`。

## 9. store 注入

- `service` 注入了两个 store：`cards commandcardstore.Store`、`prompts promptstore.Store`，见 `internal/module/skill/service.go:18-23`；构造注入见 `internal/module/skill/service.go:27-33`。
- `cards` 的调用与 contract 对齐：
  - V3 调用点：`List` (`internal/module/skill/cards.go:23`)、`Get` (`internal/module/skill/cards.go:31,125`)、`Upsert` (`internal/module/skill/cards.go:48,67`)、`Delete` (`internal/module/skill/cards.go:82`)、`ListVersions` (`internal/module/skill/cards.go:90`)、`InsertVersion` (`internal/module/skill/cards.go:133`)
  - contract：`internal/store/commandcard/contract.go:9-15`
- `prompts` store contract 定义在 `internal/store/prompt/contract.go:9-14`，但 skill 模块实现里没有任何 `s.prompts` 调用；LSP 对 `internal/module/skill/*.go` 做 `s\.prompts|prompts\.` 检索返回 0 命中。

结论：

- `commandcard.Store` 注入和调用是一致的。
- `prompt.Store` 目前是“已注入但未使用”的死依赖。

## 10. skill auto-match

- auto-match 已经从 RPC handler 下沉到 service：RPC 层只做转发，见 `internal/module/skill/rpc.go:61`；真正逻辑在 `internal/module/skill/skills_match.go:14-48`。
- 但 V3 语义与 V2 不等价：
  - V3 `MatchPreview` 只把 `p.ThreadID` 传进 collector，见 `internal/module/skill/skills_match.go:14-24`
  - V3 collector 的第一个字符串参数直接被丢弃为 `_ string`，见 `internal/module/skill/skills_match.go:33-34`
  - V3 只基于 `ListSkills()` 结果和本地字符串匹配做 `force/explicit/trigger` 分类，见 `internal/module/skill/skills_match.go:35-45,62-100`
  - V2 `newSkillsAutoMatchCollector` 位于 API 层，调用 `s.providerAdapter.CollectAutoMatchedSkillMatchesForThread(...)`，并显式传入 `IncludeConfiguredExplicit` / `IncludeConfiguredForce`，见 `go-agent-v2/internal/apiserver/methods_command.go:94-113`
  - V2 `SkillsMatchPreview` 会先通过 `resolveSkillMatchPreviewThreadID` 用 `ThreadID`，缺省时回退到 `AgentID`，见 `go-agent-v2/internal/skills/helpers.go:122-125` 与 `go-agent-v2/internal/skills/methods.go:363-370`

结论：auto-match 的“位置”已经下沉到 service，但“能力”从 V2 的 provider/context 感知逻辑退化成了本地词法匹配。

## 11. rpc_types.go 参数

- `cardKeyParams` 定义在 `internal/module/skill/rpc_types.go:5-7`，被 `cardByKey` 复用到 `get/delete/versions`，见 `internal/module/skill/rpc.go:13-15,21,28,30`。
- `createCardParams` / `updateCardParams` 直接复用 `cardPayload`，见 `internal/module/skill/rpc_types.go:9-21`，这部分设计合理。
- `runCardParams` 因为多了 `Args`，单独定义为 `Key + Args`，见 `internal/module/skill/rpc_types.go:22-25`；因此它没有复用 `cardKeyParams`。
- `skillRemoteReadParams` 被 `skills/remote/list` 和 `skills/remote/read` 共同复用，见 `internal/module/skill/rpc_skill_types.go:42-44` 与 `internal/module/skill/rpc.go:46,50`。
- `skillNamedContentParams` 被 `skills/remote/export`、`skills/remote/write`、`skills/config/write` 共用，见 `internal/module/skill/rpc_skill_types.go:32-35` 与 `internal/module/skill/rpc.go:47-57`。
- 参数问题：
  - `skillMatchPreviewParams.AgentID` 被定义出来了，但 `MatchPreview` 未读取，见 `internal/module/skill/rpc_skill_types.go:55-59` 与 `internal/module/skill/skills_match.go:14-24`
  - `execParams` 使用 `command`/`args`/`cwd`，见 `internal/module/skill/rpc_types.go:26-30`；V2 `commandExecParams` 使用 `argv`/`cwd`/`env`，见 `go-agent-v2/internal/apiserver/methods_command.go:20-24`。这不是 wire-compatible 的参数形状。

结论：card 系列只做了“部分参数复用”；`cardKeyParams` 复用范围有限，`skillMatchPreviewParams.AgentID` 是死字段，`execParams` 则明显偏离 V2。

## 12. V2 对照

选取 `methods_command.go` 中 2 个代表性方法对照：

### 12.1 `commandExecTyped`（V2） vs `(*service).ExecCommand`（V3）

V2：

- 参数：`argv` / `cwd` / `env`，见 `go-agent-v2/internal/apiserver/methods_command.go:20-24`
- 30 秒超时，见 `go-agent-v2/internal/apiserver/methods_command.go:55-56`
- `cwd` 为空时回退 `CurrentProjectCwd(s)`，见 `go-agent-v2/internal/apiserver/methods_command.go:58-62`
- 允许白名单 env 注入，见 `go-agent-v2/internal/apiserver/methods_command.go:63-67`
- 读命令注入 LSP hint，见 `go-agent-v2/internal/apiserver/methods_command.go:84-91`

V3：

- 参数变成 `command` / `args` / `cwd`，见 `internal/module/skill/rpc_types.go:26-30`
- 直接 `exec.CommandContext(ctx, name, args...)`，没有内部 `WithTimeout`，见 `internal/module/skill/exec.go:41`
- `cwd` 仅取请求值，没有 project cwd fallback，见 `internal/module/skill/exec.go:42`
- 没有 env 注入路径，见 `internal/module/skill/exec.go:27-60`
- 读命令仍注入 LSP hint，见 `internal/module/skill/exec.go:48-49`

结论：仅“阻断危险命令 + 读命令提示”保持同方向；参数协议、超时、cwd fallback、env 注入都已变化，不能判定为等价迁移。

### 12.2 `newSkillsAutoMatchCollector`（V2） vs `(*service).newSkillsAutoMatchCollector`（V3）

V2：

- 使用 `providerAdapter.CollectAutoMatchedSkillMatchesForThread(...)`，见 `go-agent-v2/internal/apiserver/methods_command.go:98-109`
- 由 `SkillsMatchPreview` 传入 `threadID`，并开启 `IncludeConfiguredExplicit` / `IncludeConfiguredForce`，见 `go-agent-v2/internal/skills/methods.go:363-370`
- `threadID` 由 `resolveSkillMatchPreviewThreadID` 解析，支持 `ThreadID` 为空时回退 `AgentID`，见 `go-agent-v2/internal/skills/helpers.go:122-125`

V3：

- collector 签名的第一个字符串参数被忽略，见 `internal/module/skill/skills_match.go:33-34`
- 数据来源仅是 `s.ListSkills(ctx)`，见 `internal/module/skill/skills_match.go:35`
- 匹配算法只做本地 `force` / `explicit` / `trigger` 字符串判断，见 `internal/module/skill/skills_match.go:39-45,62-100`
- `MatchPreview` 仅把 `p.ThreadID` 传进去，不处理 `AgentID` fallback，见 `internal/module/skill/skills_match.go:14-24`

结论：V3 确实把逻辑放进了 service，但功能语义不再等价于 V2 的 provider/context 感知版本。

## 结论（Blocker / Improvement）

### Blocker

1. `skills/match/preview` 丢失了 V2 的 `AgentID` 回退和 provider/configured-skill 感知能力。V3 参数里仍保留 `AgentID`（`internal/module/skill/rpc_skill_types.go:55-59`），但 `MatchPreview` 只传 `p.ThreadID`（`internal/module/skill/skills_match.go:14-24`），collector 还直接忽略首参 `_ string`（`internal/module/skill/skills_match.go:33-34`）。V2 则先用 `resolveSkillMatchPreviewThreadID` 做 `ThreadID -> AgentID` 回退（`go-agent-v2/internal/skills/helpers.go:122-125`），再调用 provider-backed auto matcher 并启用 `IncludeConfiguredExplicit` / `IncludeConfiguredForce`（`go-agent-v2/internal/skills/methods.go:363-370`, `go-agent-v2/internal/apiserver/methods_command.go:98-109`）。这会直接改变 preview 结果。

2. `RunCard` 与 `ExecCommand` 的参数守卫互相冲突，导致 shell 卡片无法承载常见 shell 语法。`RunCard` 把渲染结果作为 `sh -lc <rendered>` 传给 `ExecCommand`（`internal/module/skill/cards.go:101-107`），但 `ExecCommand` 会拒绝任何包含元字符 `|`、`;`、`&`、`$`、反引号的参数（`internal/module/skill/exec.go:36-39`）。命令卡模板本身又明确是 shell 文本渲染（`internal/module/skill/cards.go:190-199`）。这意味着包含管道、变量、串联执行的卡片会在进入 shell 之前被自己拦掉。

### Improvement

1. `command/exec` 与 V2 不等价：V2 参数是 `argv/cwd/env`（`go-agent-v2/internal/apiserver/methods_command.go:20-24`），有 30 秒超时（`go-agent-v2/internal/apiserver/methods_command.go:55-56`）、project cwd fallback（`go-agent-v2/internal/apiserver/methods_command.go:58-62`）和 env 白名单注入（`go-agent-v2/internal/apiserver/methods_command.go:63-67`）；V3 改成 `command/args/cwd`（`internal/module/skill/rpc_types.go:26-30`），直接用外部 ctx 执行（`internal/module/skill/exec.go:41-42`），缺少这些行为。若迁移目标包含 RPC 兼容性，这里需要明确补齐或声明断裂。

2. `promptstore.Store` 是死依赖。它被注入到 `service` 结构和构造器中（`internal/module/skill/service.go:18-23,27-33`），但 skill 模块中没有任何 `s.prompts` 调用；LSP 文本检索 `s\.prompts|prompts\.` 返回 0 命中。当前只有 `commandcard.Store` 真正被使用，且其调用与 contract 对齐（`internal/module/skill/cards.go:23,31,48,67,82,90,133`; `internal/store/commandcard/contract.go:9-15`）。

3. 远端技能读取缺少内建超时。V3 在构造时使用 `&http.Client{}`（`internal/module/skill/service.go:27-33`），`ReadRemote` 直接 `s.http.Do(req)`（`internal/module/skill/skills_fs.go:111-130`）；V2 则使用 `&http.Client{Timeout: 15 * time.Second}`（`go-agent-v2/internal/skills/methods.go:437-459`）。如果调用链上没有上游 deadline，这里会把网络阻塞风险直接暴露给 RPC。

## 互辩：批判 R4 + R5

### 对 R4（workspace）的批判

1. R4 在 `docs/plans/迁移/p5-wave2-audit-R4.md:25,57` 把“当前 8 个 key 本身齐全 / contract 对 rpc.go 全覆盖”说得过于平滑，但漏掉了更直接的 handler 缺陷：V3 多个 workspace handler 都是在调用 service 之后才做必填校验。`workspace/run/get` 先 `svc.GetRun(ctx, p.RunKey)` 再 `required(...)`（`internal/module/workspace/rpc.go:19-21`），`workspace/run/status/update`、`workspace/run/merge`、`workspace/run/files/list`、`workspace/run/file/get` 也都是同模式（`internal/module/workspace/rpc.go:27-29,31-33,45-47,49-51`）。V2 则先解码并校验，再调用 manager，例如 `workspaceRunGet` 在 `runKey` 为空时直接返回错误（`go-agent-v2/internal/apiserver/workspace_methods.go:69-78`），`workspaceRunMerge` 和 `workspaceRunAbort` 也是先验参（`go-agent-v2/internal/apiserver/workspace_methods.go:120-127,143-154`）。R4 没抓到这个运行时问题。

2. R4 在 `docs/plans/迁移/p5-wave2-audit-R4.md:81-86` 对 store 对齐的判断偏轻，漏掉了一个更硬的事实：`workspace/run/files/list` 和 `workspace/run/file/get` 在 V3 有读面，没有写面。RPC 已暴露这两个方法（`internal/module/workspace/rpc.go:45-52`），service 也直接转到 `ListFiles/GetFile`（`internal/module/workspace/service.go:83-92`），但整个 `internal/module/workspace/` 内没有任何 `UpsertFile` 调用；LSP 检索 `UpsertFile(` 只命中 store contract 与 store 实现（`internal/store/workspace/contract.go:15`, `internal/store/workspace/store.go:73-89`）。R4 提到了 `UpsertFile` 未使用，却没有继续推到“文件查询接口当前没有内部数据生产路径”这一层。

3. R4 忽略了 workspace 与 orchestration/task DAG 的交叉依赖检查。当前 V3 workspace 模块对 `taskdag|orchestration|event|notify` 的 LSP 检索是 0 命中，而仓库里已经存在完整的 DAG wakeup 能力：`taskdag.Store` 暴露 `EnqueueWakeup/ClaimDueWakeups/MarkWakeupSent/BindWakeupTurn/...`（`internal/store/taskdag/contract.go:26-38,97-104`），store 实现也已落地（`internal/store/taskdag/store.go:152-178`），并且有 `TaskWakeupDispatched/TaskWakeupCompleted` 事件类型（`internal/dto/task/event.go:23-41`）。R4 的报告没有检查 `workspace/run/merge` 之后是否推进 DAG 或唤醒 agent，只停在 workspace 自身 CRUD 层，视角过窄。

4. R4 在 `docs/plans/迁移/p5-wave2-audit-R4.md:70-77` 说 service “不是空壳，但核心行为骨架化/退化”，方向没错，但力度仍然不够，因为它漏掉了状态机守卫被直接拆掉。当前 store contract 已提供 `TransitionRunStatus` 和 `TransitionRunStatusInput{FromStatus,...}`（`internal/store/workspace/contract.go:13-18,33-39`），但 V3 `MergeRun`/`AbortRun` 只是走 `UpdateRunStatus`（`internal/module/workspace/service.go:67-80`）。V2 `MergeRun` 会先 `TryTransitionRunStatus(active -> merging)`，失败时显式报状态不符（`go-agent-v2/internal/service/workspace.go:283-304,306-315`）。这不是单纯“薄封装”，而是生命周期合法性检查被移除。

5. R4 的 store 对齐审查还漏了一处参数收窄：`ListFilesFilter` 除了 `RunKey` 还有 `State`/`Limit`（`internal/store/workspace/contract.go:41-45`），但 V3 service 只固定传 `RunKey + Limit`（`internal/module/workspace/service.go:83-87`），RPC 侧更只收 `runKeyParams`（`internal/module/workspace/rpc.go:45-47`）。R4 提到了 `ListRunsFilter.Status/DagKey` 被吞掉，却没继续追到 `ListFilesFilter.State` 这一半的 contract 也被吞掉，store 对齐判断不完整。

### 对 R5（orchestration）的批判

1. R5 在 `docs/plans/迁移/p5-wave2-audit-R5.md:49` 说 `launchParams` “对当前 V3 LaunchAgent contract 而言，类型是对齐的”，这不成立。`LaunchRequest` 明明定义了 `ParentID` 和 `Env`（`internal/sidecar/orch/orchestration/contract.go:32-39`），service 也确实消费这两个字段：`agentForLaunchLocked` 会写入 `agent.parentID` 和 `agent.env`（`internal/sidecar/orch/orchestration/helpers.go:39-43`），`startProcessLocked` 会把 `agent.env` 拼进 `cmd.Env`（`internal/sidecar/orch/orchestration/service.go:211-215`）。但 RPC `launchParams` 只有 `AgentID/Name/CWD/Command`（`internal/sidecar/orch/orchestration/rpc_types.go:5-10`），handler 也只填这四项（`internal/sidecar/orch/orchestration/rpc.go:13-19`）。这不是“对齐”，而是 RPC 面已经丢掉了 contract 可承载的字段。

2. R5 对 `agent.list` 的验证不够到位。它在 `docs/plans/迁移/p5-wave2-audit-R5.md:68-70` 只讨论了 service 返回 `[]AgentSnapshot`，但没检查 JSON 结果形状。`AgentSnapshot` 没有任何 `json` tag（`internal/sidecar/orch/orchestration/contract.go:41-57`），模块内也没有 `MarshalJSON` 之类自定义序列化；LSP 对 `internal/sidecar/orch/orchestration/*.go` 检索 `MarshalJSON|json:"` 只命中 `rpc_types.go`，不命中 `AgentSnapshot`。这意味着 `agent.list` / `agent.snapshot` 默认会输出 Go 字段名。V2 的 schema 守卫却明确要求 `agent.list` 返回项键为 `cwd,id,last_report,name,parent_id,port,provider,state,thread_id`（`go-agent-v2/internal/apiserver/methods_schema_contract_a_m_test.go:279-281`），对应的 `runner.AgentInfo` 也带了 snake_case json tag（`go-agent-v2/internal/runner/manager.go:147-157`）。R5 漏掉了这个 wire-shape 断裂。

3. R5 也没有检查 `agent.list` 的输出顺序稳定性。V3 `ListAgents` 直接遍历 `s.agents` map 组装切片（`internal/sidecar/orch/orchestration/service.go:161-169`），没有任何排序；V2 `AgentManager.List` 在返回前用 `sort.SliceStable` 依 `ID/Name/Port` 排序（`go-agent-v2/internal/runner/manager.go:303-311`）。如果外部有快照测试、golden 或前端按稳定顺序消费，V3 当前行为是可见退化，R5 没提。

4. R5 对 `orchestration/report` 的批判仍然过浅。它在 `docs/plans/迁移/p5-wave2-audit-R5.md:14-15,80-89` 主要把问题压缩成“V3 缺 `agent.getReport` / report key 未实现”，但 V2 的报告流远不止一个 getter：注册面包括 `agent.getReport`、`agent.rememberReportRequest`、`agent.reportEvent`（`go-agent-v2/internal/apiserver/methods_orchestration.go:20-23`），实现面包括 `agentGetReportTyped`、`agentRememberReportRequestTyped`、`agentReportEventTyped`（`go-agent-v2/internal/apiserver/methods_orchestration.go:122-209`），以及独立的 `orchestration_report.go` 137 行逻辑，负责 requester 归并、完成摘要提取和自动回传（`go-agent-v2/internal/apiserver/orchestration_report.go:23-137`）。V3 orchestration 模块里与 report/requester 相关的代码只有一个 `newNotImplementedHandler` 注册点（`internal/sidecar/orch/orchestration/rpc.go:34,38-41`）。R5 点到了“没实现”，但没把缺失范围说全。

5. R5 的参数类型审查过度聚焦 `launchParams/agentIDParams`，漏掉了当前 V3 DAG 面自身也已经出现参数回退。`task/dag/list` 在 RPC 层直接用 `struct{}`（`internal/sidecar/orch/orchestration/rpc.go:32`），而底层 `taskdag.Store` 已经定义了 `ListDAGsFilter{Status, Keyword, Limit}`（`internal/store/taskdag/contract.go:41-45`）。`rpc_types.go` 为 create/get/update 都建了 DTO（`internal/sidecar/orch/orchestration/rpc_types.go:16-48`），唯独 list 没有 params。R5 在 `docs/plans/迁移/p5-wave2-audit-R5.md:47-53` 的参数结论没有覆盖这一点，审查不完整。
