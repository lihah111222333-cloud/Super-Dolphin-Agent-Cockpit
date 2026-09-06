# Task Principal 与 Canonical Task Event Source 候选

Status: Active — Local Candidate Only

日期：2026-09-02；上级：[ID/事件切片](identity-event-next-slice.md)与[Access/Wait切片](access-wait-next-slice.md)。

## 当前定位

本切片新增task attribution、task-only HTTP session认证、内存credential owner、current task principal resolver和canonical task event source候选，用于把服务端已认证的调用归因固定到当前durable identity binding。task event producer（含 split 与 transaction-bound 两个候选）、tasktransport Authorization、childissuer、childhandoff 与 `unified_v2` list/wait/send 也仅有未注册的本地候选；它们不发布工具，不启用跨任务发现、等待、发送或Room。

生产边界仍是fail-closed：没有transport auth binder时不能由metadata、tool参数、thread ID、cwd或local operator principal推导task身份；没有真实event producer时不能把source/cursor构造器声明为事件链已接通。

## 已实现的候选链

```text
transport-authenticated caller principal
  + server-bound CallerAttribution
  + task-only SessionCredentialDigest / TaskSessionAuthenticator
  -> taskprincipal.Resolver
       -> reservation
       -> incarnation
       -> current binding
  -> verified TaskPrincipal
  -> taskeventsource.Source
       -> fixed task/{agent_id} source
       -> incarnation source epoch
       -> selector / canonical cursor
```

### CallerAttribution

`internal/mcpserver/common.CallerAttribution`是transport-private context snapshot：

- 固定schema v1、subject kind `task`和audience `mcp-orch/unified_v2`；
- task ID必须等于source agent ID；source携reservation/incarnation/binding/source fence完整代际；
- issuer kind只允许`provider_bridge|managed_runtime|a2a_gateway`并绑定issuer generation/digest；
-只能由服务端context binder一次性写入，已有值拒绝覆盖；不接受客户端metadata补全。

这只是DTO/context seam。当前没有真实transport认证边界把provider/runtime/A2A身份签发并绑定到每个production调用。

### Task-only HTTP session 与 credential owner

`internal/mcpserver/common`已新增与operator endpoint严格互斥的task session模式：transport只把严格Bearer的SHA-256 digest交给`TaskSessionAuthenticator`，initialize冻结digest与`CallerPrincipal+CallerAttribution`，后续POST/DELETE每次在session锁外重新认证并逐字段比较，匹配后才touch、dispatch或删除。raw bearer不进入context、日志、错误或数据库。

`standalone/taskcredential.Registry`提供两阶段内存候选：`IssuePending`只把唯一raw bearer交给一次性holder，pending不能认证；`Activate`在锁外回验current identity后CAS为active；每次`Authenticate`再次回验current，revoke/expiry/close立即fail-closed。registry只保存digest、状态、代际、expiry与非秘密identity snapshot，不保存raw bearer。

这些仍未进入standalone Fx或managed provider启动链。现operator ready endpoint保持operator-only；第二个task-only listener、production current-identity verifier adapter、credential pending→active生命周期和provider配置注入均未实现。childissuer、managedruntime `BindingCandidate`、childhandoff 与 `unified_v2` 候选同样未注册。

### Current Task Principal

`taskprincipal.Resolver.RequireCurrent`按固定顺序重读reservation、incarnation和current binding，并同时比较`CallerPrincipal`与`CallerAttribution`：

- reservation必须activated且principal/scope、agent、generation、source fence一致；
- incarnation必须属于同agent/reservation且generation/source fence一致；
- current binding必须active，完整execution fence逐字段等于attribution source；
- 缺归因/owner读取失败返回unavailable，任一current fence漂移返回fence error，不回退到operator、thread或latest。

### Canonical Task Event Source

`taskeventsource.FromCurrentBinding`只接受schema v1 active binding，固定：

- `source_id = task/{agent_id}`；
- `source_epoch = incarnation_id`，recover后不自动跟随新incarnation；
- binding version/digest与source fence ref；
- accesswait selector与EventStore canonical cursor都携同一source/epoch/lane。

该组件只构造受fence约束的source/selector/cursor，是未注册的本地候选；不写EventStore、不生产provider/task事件，也不授予read/send权限。

`taskeventsource.Producer` 在进入 appender 前还会拒绝 reservation/incarnation
快照的未知 schema（当前只接受 schema 1）。这条守卫只保证不支持的 durable
身份不会触达事件写入口；它不提供 schema 迁移、序列分配或 runtime producer
接线，也不改变本切片的 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` 状态。

`taskeventsource.RuntimeEventTxCandidate` 进一步要求 identity snapshot、source
watermark 与 appender 在同一 transaction-bound `DBTX` 内完成，并在 gap 或 commit
unknown 时停止重放。它仍没有 owner/Fx/runtime 调用方，不能替代完整 SourceSequenceOwner。

`orchestration/tasktransport` 的 Authorization candidate 只做 discover/read/send
的 parent/grant/revocation/cursor 形状和 zero-effect 预检；`orchestration/childissuer`
只验证 parent→child identity 链，均不签发 token、不持久化、不接 provider。

task-only HTTP profile 现在仅在已绑定的 task attribution context 中把
`Attribution.TaskID` 投影为内部 `ToolScope.AgentID`；operator profile 仍保持空
`AgentID`，客户端 metadata 仍被拒绝。该投影修复 tools/list 与 tools/call 的一致性，
不签发身份、不扩大 grant，也不把 legacy `send_message` 变成 canonical cross-task。

## 当前证据

raw `taskprincipal`、`taskeventsource`、`eventstore`和`internal/mcpserver/common`测试PASS；direct code/entry guard与相关build/vet PASS，统一 wrapper 在 production-ratchet 25 项阶段 fail-fast，尚未进入 freeze baseline drift 阶段。结果仍为`LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`，只证明本地候选字段、拒绝路径和静态边界。

## 尚未实现

1. 第二个task-only loopback listener、production current-identity verifier adapter及standalone Fx lifecycle；
2. managed Codex `bearer_token_env_var`与Claude私有临时MCP配置注入、失败清理、pending→active顺序和真实CLI eager-connect证据；
3. 真实canonical event producer：provider observation、task lifecycle、OUT/content/resource/Room事件写入dense EventStore；split 与 transaction-bound task event producer 目前都只是未注册本地候选；
4. `unified_v2` public `list/wait/send` schema、tool registry、授权、Fx/runtime和客户端E2E；当前对应处理器与 tasktransport Authorization 仍是未注册本地候选；
5. childissuer/childhandoff与child/task principal跨daemon重建、old-incarnation follow拒绝的真实进程证据；当前 childissuer 只验证外部 identity，managedruntime `BindingCandidate` 只验证 endpoint/process receipt，均未注册；
6. 与CONTENT/RESOURCE/RecoveryCustody/OUT accepted/receipt/permit/nonce全链接线。

legacy `send_message`与migration124 direct-peer仍是旧局部路径；它们没有task attribution、current task principal、CONTENT/RESOURCE/custody和OUT terminal chain，不能升级表述为canonical cross-task send。managed provider、Room与release继续NO-GO；总计划保持Active，十四份完整规格保持Draft，cross-task send与Room保持NO-GO。
