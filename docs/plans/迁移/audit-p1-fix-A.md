# P1 修复审查 — Agent A

## 1. Skill 修复

### B1 AgentID 回退（逐项验证 + 行号）

- `MatchPreview` 已接收 `agentID + threadID`，并在 `internal/module/skill/skills_match.go:14-17` 先调用 `resolveSkillMatchPreviewThreadID`；`internal/module/skill/skills_match.go:30-35` 实现了“`threadID` 非空优先，否则回退到 `agentID`”。
- RPC 参数与 handler 已透传两者；`skillMatchPreviewParams` 在 `internal/module/skill/rpc_skill_types.go:55-60` 定义了 `ThreadID` 与 `AgentID`，handler 在 `internal/module/skill/rpc.go:61-63` 调用 `svc.MatchPreview(ctx, p.AgentID, p.ThreadID, p.Text, p.Input)`，Service 合约也在 `internal/module/skill/contract.go:25` 对齐为 `MatchPreview(ctx, agentID, threadID, text, input)`。
- Blocker：collector 仍忽略首参。`newSkillsAutoMatchCollector` 在 `internal/module/skill/skills_match.go:43-52` 把解析后的 `resolvedThreadID` 传给 `collectConfiguredAutoMatchedSkills`，但后者在 `internal/module/skill/skills_match.go:56-59` 仅执行 `_ = strings.TrimSpace(threadID)` 后直接 `return nil`。当前 fallback 只影响返回值中的 `thread_id`，不会影响配置型匹配结果。
- TODO 已标注 P7 provider-backed matcher；见 `internal/module/skill/skills_match.go:58`。

### B2 ExecCommand 拆分（逐项验证 + 行号）

- 公开 `ExecCommand` 已走严格校验路径；`internal/module/skill/exec.go:27-29` 调用 `s.execCommand(..., false)`，`internal/module/skill/exec.go:40-44` 在 `allowShell=false` 时执行 `validateExecArgs`，而 `internal/module/skill/exec.go:61-67` 明确拒绝 `|;&$`` 等 shell 元字符。
- 内部 shell 路径已拆出且保持 unexported；`internal/module/skill/exec.go:31-35` 的 `execShell` / `execCommand` 都是小写标识符，`execShell` 调用 `s.execCommand(..., true)`，因此不会触发参数元字符校验。
- `RunCard` 已改走内部 shell 路径；`internal/module/skill/cards.go:101-107` 渲染命令后调用 `s.execShell(ctx, rendered, cwd)`。
- Service 接口仍只暴露 `ExecCommand(ctx, command string, args []string, cwd string)`；见 `internal/module/skill/contract.go:13`。

### B3 config 语义（验证 + 行号）

- `ReadConfig` 已显式标注 stub 状态；`internal/module/skill/skills_fs.go:143-156` 的 TODO 说明当前是 placeholder response，并返回 `configured=false`、`binding_count=0`、`binding_source="stub"` 等显式占位字段。
- Blocker：`WriteConfig` 语义仍未真正澄清为 agent-scoped config write。`internal/module/skill/skills_fs.go:159-161` 直接 `return s.WriteRemote(ctx, name, content)`，TODO 明确写着“split named skill content writes from agent-scoped skill config”；RPC 入口仍以 `name, content` 调它，见 `internal/module/skill/rpc.go:55-57`，接口定义也仍是 `WriteConfig(ctx, name, content string)`，见 `internal/module/skill/contract.go:23`。
- 现有测试还把这种别名语义锁死了；`internal/module/skill/skills_fs_test.go:36-62` 断言 `WriteConfig("demo-skill", "# demo")` 会写到 `<skillsRoot>/demo-skill/SKILL.md`，说明当前写路径本质仍是“按 skill name 写内容”，不是 agent config。

### B4 prompt.Store 移除（验证 + 行号）

- `prompt.Store` 已从 service 字段和构造器移除；`internal/module/skill/service.go:17-21` 的字段只剩 `cards/root/http`，`internal/module/skill/service.go:25-30` 的 `NewService` 只接收 `commandcardstore.Store`。
- `module.go` 的 `fx.Provide` 已与新构造器签名对齐；`internal/module/skill/module.go:5-8` 只提供 `NewService` 与 `NewSkillHandlers`。
- 结合 `internal/module/skill/service.go:17-30` 与 `internal/module/skill/module.go:5-8` 的现状，LSP `text_search(path="internal/module/skill", query="prompt.Store"|"promptstore"|"prompts"|"s.prompts")` 均为 0 命中；审查范围内未发现残留 `s.prompts` 调用。

### 回归测试

- `go test ./internal/module/skill/... -count=1 -v` 通过，执行了 `TestMatchPreviewFallsBackToAgentID`、`TestExecCommandRejectsShellMetacharacters`、`TestRunCardAllowsShellSyntaxViaInternalShellPath`、`TestReadConfigReturnsExplicitStubBindingState`、`TestWriteConfigWritesNamedSkillContent`。
- `internal/module/skill/skills_match_test.go:11-38` 只覆盖“空 `threadID` 时回退到 `agentID`”；没有锁住“非空 `threadID` 优先级”、RPC handler 透传、以及 `collectConfiguredAutoMatchedSkills` 真正消费首参的行为。
- `internal/module/skill/exec_test.go:12-19` 与 `internal/module/skill/exec_test.go:21-40` 已锁住 B2 的公开/内部两条执行路径；`internal/module/skill/skills_fs_test.go:10-34` 锁住了 `ReadConfig` stub 语义，`internal/module/skill/skills_fs_test.go:36-62` 锁住了当前 `WriteConfig` 的别名写入行为。

## 2. Workspace 修复

### B11 验参顺序（逐 handler 验证 + 行号）

- `NewWorkspaceHandlers` 当前注册 8 个 handler；见 `internal/module/workspace/rpc.go:13-23`。
- `handleCreateRun` 先执行 `required(p.SourceRoot, "sourceRoot")` 于 `internal/module/workspace/rpc.go:28-30`，后调用 `svc.CreateRun` 于 `internal/module/workspace/rpc.go:31-35`。
- `handleGetRun` 先执行 `required(p.RunKey, "runKey")` 于 `internal/module/workspace/rpc.go:41-43`，后调用 `svc.GetRun` 于 `internal/module/workspace/rpc.go:44-48`。
- `handleListRuns` 没有必填字段校验，直接调用 `svc.ListRuns(ctx, p.Status, p.DagKey, p.Limit)` 于 `internal/module/workspace/rpc.go:52-58`；`limit<=0` 的默认值下沉到 service，见 `internal/module/workspace/service.go:84-87`。
- `handleUpdateRunStatus` 先执行 `required2(p.RunKey, "runKey", p.Status, "status")` 于 `internal/module/workspace/rpc.go:64-66`，后调用 `svc.UpdateRunStatus` 于 `internal/module/workspace/rpc.go:67-71`。
- `handleMergeRun` 先执行 `required(p.RunKey, "runKey")` 于 `internal/module/workspace/rpc.go:77-79`，后调用 `svc.MergeRun` 于 `internal/module/workspace/rpc.go:80-84`。
- `handleAbortRun` 先执行 `required(p.RunKey, "runKey")` 于 `internal/module/workspace/rpc.go:90-92`，后调用 `svc.AbortRun` 与 `svc.GetRun` 于 `internal/module/workspace/rpc.go:93-100`。
- `handleListRunFiles` 先执行 `required(p.RunKey, "runKey")` 于 `internal/module/workspace/rpc.go:106-108`，后调用 `svc.ListRunFiles` 于 `internal/module/workspace/rpc.go:109-113`。
- `handleGetRunFile` 先执行 `required2(p.RunKey, "runKey", p.Path, "path")` 于 `internal/module/workspace/rpc.go:119-121`，后调用 `svc.GetRunFile` 于 `internal/module/workspace/rpc.go:122-126`。

### B5 ListRuns 参数（逐层验证 + 行号）

- `listRunsParams` 已包含 `status/dagKey/limit` 三个字段；见 `internal/module/workspace/rpc_types.go:9-13`。
- `Service.ListRuns` 签名已接收 `status, dagKey string, limit int`；见 `internal/module/workspace/contract.go:14`。
- handler 已把三者透传给 service；`internal/module/workspace/rpc.go:52-58` 调用 `svc.ListRuns(ctx, p.Status, p.DagKey, p.Limit)`。
- service 已把三者传给 `store.ListRuns(ListRunsFilter{...})`；`internal/module/workspace/service.go:84-92` 把 `Status`、`DagKey`、`Limit: int32(limit)` 写入 filter。
- `limit <= 0` 时默认 200；`internal/module/workspace/service.go:21` 定义 `defaultListLimit = 200`，`internal/module/workspace/service.go:85-86` 负责兜底。

### B9 TransitionRunStatus（验证 + 行号）

- `MergeRun` 已改走 `transitionRunStatus`，`FromStatus` 为 `statusActive`，目标状态为 `statusMerged`；见 `internal/module/workspace/service.go:103-105`。
- `AbortRun` 已改走 `transitionRunStatus`，`FromStatus` 同样为 `statusActive`，目标状态为 `statusAborted`；见 `internal/module/workspace/service.go:107-109`。
- `transitionRunStatus` 内部调用 `s.store.TransitionRunStatus`，并显式传入 `FromStatus` 与 `Status`；见 `internal/module/workspace/service.go:123-132`。
- 状态不匹配时会返回明确错误；`internal/module/workspace/service.go:139-146` 在读出当前 run 后返回 `run %q status is %s, expected %s`，缺 run 时返回 `run %q not found` 于 `internal/module/workspace/service.go:141-143`。
- 对 `(*service).UpdateRunStatus` 的 LSP references 只落在声明 `internal/module/workspace/service.go:95-100` 与 RPC update handler `internal/module/workspace/rpc.go:62-71`；审查范围内未发现 `MergeRun` / `AbortRun` 再走 `UpdateRunStatus` 的调用链。

### B10 UpsertFile 路径（验证 + 行号）

- `CreateRunRequest` 已增加 `Files []string`；见 `internal/module/workspace/contract.go:25-35`。`type createRunParams = CreateRunRequest` 于 `internal/module/workspace/rpc_types.go:3` 使该字段直接暴露到 RPC 层。
- `CreateRun` 会在 `UpsertRun` 后遍历 `Files`；`internal/module/workspace/service.go:38-45` 调用 `s.upsertRunFiles(ctx, saved, req.Files)`。
- `upsertRunFiles` 会去重并逐个调用 `s.store.UpsertFile`；去重逻辑在 `internal/module/workspace/service.go:153-162`，store 写入在 `internal/module/workspace/service.go:163-169`。
- 文件路径校验基本合理；`internal/module/workspace/service.go:199-210` 用 `filepath.Clean` 和 `strings.TrimSpace` 标准化路径，禁止空路径、绝对路径和 `..` 逃逸。
- merge 文件持久化 TODO 存在；`internal/module/workspace/service.go:102` 标注 `// TODO: persist merge file state when full merge semantics is implemented.`。
- Warning：`CreateRun` 先 `UpsertRun` 再 `upsertRunFiles`，没有事务或回滚；若某个文件在 `internal/module/workspace/service.go:155-168` 校验/写入失败，run 已在 `internal/module/workspace/service.go:38` 成功落库，可能留下“已创建 run、未完整登记 files”的部分状态。

## 3. 编译守卫

- `go test ./internal/module/skill/... -count=1 -v` 通过。
- `go build ./...` 通过。
- `go test ./internal/archtest/... -count=1` 通过。

## 结论

### Blocker（修复不完整或引入新问题）

- B1 未完全收口：`collectConfiguredAutoMatchedSkills` 仍忽略首参，导致 `MatchPreview` 的 `agentID/threadID` plumbing 还没有进入配置型 matcher 分支；见 `internal/module/skill/skills_match.go:43-59`。
- B3 未完全收口：`WriteConfig` 仍是 `WriteRemote` 的别名，接口与 RPC 仍以 `name, content` 暴露，语义没有真正从“技能内容写入”切到“agent config 写入”；见 `internal/module/skill/skills_fs.go:159-161`、`internal/module/skill/rpc.go:55-57`、`internal/module/skill/contract.go:23`。

### Warning（建议改进）

- `CreateRun` 的 run/file 两阶段写入没有事务保护，文件登记失败会留下部分状态；见 `internal/module/workspace/service.go:38-45` 与 `internal/module/workspace/service.go:149-169`。
- `skill` 回归测试没有覆盖“非空 `threadID` 优先级”“RPC handler 透传 agentID/threadID”“configured matcher 真正消费首参”；现有覆盖仅见 `internal/module/skill/skills_match_test.go:11-38`。
- `workspace` 目录下 LSP `text_search(path="internal/module/workspace", glob="**/*_test.go", query="func Test")` 返回 0 命中，B5/B9/B10/B11 当前主要依赖编译与 `archtest` 间接守卫。

### OK（确认修复正确）

- B2 的公开 `ExecCommand` 与内部 shell 执行已成功拆分，且 `RunCard` 已切到内部 shell 路径；见 `internal/module/skill/exec.go:27-45`、`internal/module/skill/cards.go:101-107`、`internal/module/skill/contract.go:13`。
- B4 的 `prompt.Store` 死依赖已从 service 与构造器中移除，`fx.Provide` 也已对齐；见 `internal/module/skill/service.go:17-30` 与 `internal/module/skill/module.go:5-8`。
- B5、B9、B11 的 workspace 修复项实现到位；见 `internal/module/workspace/rpc_types.go:9-13`、`internal/module/workspace/contract.go:14`、`internal/module/workspace/service.go:84-147`、`internal/module/workspace/rpc.go:26-126`。
