# Child task principal issuer candidate

Status: Local candidate only — production disabled

本切片收口“父 task 已验证身份 → child reservation/incarnation/current binding → 独立 child identity issuer”的最小边界。它只验证一个未来 issuer 返回的 `common.TaskSessionIdentity`，不负责签发 principal、生成 bearer、写 SQLite、启动 provider、建立 HTTP/stdio/ctl 连接，亦不注册 `unified_v2` 或 Room 工具。

## 代码边界

`cmd/mcp-orch/orchestration/childissuer` 只依赖两个窄端口：

- `ParentResolver`：必须返回 `taskprincipal.TaskPrincipal`，其实现负责重读 reservation/incarnation/current binding；
- `IdentityIssuer`：未来受独立认证边界保护的 issuer，只返回完整 task identity，不返回 raw bearer 或 provider secret。

`Request` 要求 owner 显式提供已激活、命令驱动且属于 `managed_agent` 的 child reservation、不可变 incarnation、active current binding、`managed_runtime|provider_bridge` issuer binding 和小写 SHA-256 nonce digest。`native_subagent` 当前直接拒绝：独立 zero-effect/adoption proof 尚未存在，不能把 command-backed launch/recover/replace 冒充 native child。reservation 的 `parent_agent_id`、caller principal、scope 必须分别与已验证 parent 的 task、principal 和 scope 逐字相等；child 与 parent 不能复用 agent、principal、auth context 或同一 `IssuerBinding`。观察类 reservation（`provider_discovery|a2a_observation`）、operator profile/ref、stale/revoked/future schema、foreign fence、issuer generation/digest 漂移在 delegate 前拒绝。

`CandidateIssuer` 在 delegate 前以进程内 mutex 线性化稳定 child key（child reservation/incarnation/binding fence）；同一 child 即使 parent 代际、issuer generation 或 nonce 改变也只能得到 `ErrChildAlreadyIssued`，不会再次调用 delegate。parent fence 是每次尝试的 expected CAS/二次读校验，而不是可绕过防重放的 key；replacement 必须建立新的 reservation/incarnation。nonce digest 随结果显式绑定到 child/issuer tuple，因此不同 child 不会因进程内 nonce 相同而互相误拒。delegate 返回错误、context 取消或 identity 校验失败时该 child 尝试仍不释放，这是故意的 fail-closed 语义，因为本地候选无法证明外部 issuer 没有副作用；跨进程/跨 daemon replay authority、原 operation 查询和 durable receipt 仍未实现。

delegate 返回 identity 后，候选会再次通过 `ParentResolver` 读取 parent，并逐字段比较 principal（含 expiry）、attribution 和 current binding；期间 parent rollover/revoke/expiry 会丢弃 child identity，不能把旧 parent fence 带入结果。

传给 delegate 的 context 只保留取消/截止时间，隐藏 parent transport values；parent/child 的非秘密 refs、fence、scope、kind、epoch、command/idempotency 与 auth-context digest 通过 typed `IssueRequest` 传递。

结果 `Candidate` 显式保留 parent/child execution fence、child reservation/incarnation id、issuer binding generation/digest 和 nonce digest；session identity 存于私有字段，只能经 `SessionIdentity()` 交给未来 credential activation owner。JSON/String/GoString 均隐藏 parent/child auth context，避免日志或序列化泄漏。

`DecodeRequest` 使用递归 duplicate/unknown/null/trailing/深度/成员/总字节检查，并要求与 Go canonical JSON 字节逐字相等；它是候选输入守卫，不是已批准 wire schema。

## 验证与缺口

`contract_test.go` 覆盖 parent authenticated pair、双端 fence/issuer/audience/epoch/nonce、同 nonce 并发单赢家、同 child 换 nonce 仍单赢家、不同 child 可使用同 nonce、delegate 后 parent rollover、operator/foreign/cross-scope/observation/stale rejection、delegate error 后禁止重发、identity reuse、严格 JSON 和脱敏。`architecture_test.go` 阻止 provider、DB、Fx、managedruntime、unified/Room 依赖及 production wiring；`field_guard_test.go` 动态枚举公开 JSON 字段并拒绝 bearer/token/credential 字段。

本机普通、race、vet、code-size 和 LSP 结果只能标记 `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED`。退出生产候选仍需要：

1. managed/native runtime 的独立 issuer、connection/nonce replay registry、credential pending→active 与真实 token digest；
2. IdentityRepository 的跨事务 current snapshot read-back、reservation/incarnation source epoch 和 revoke/rollover 证明；
3. OUT/RESOURCE/CONTENT/TRUST/custody 对 child launch/fork 的完整 command、permit、receipt、cleanup、unknown/reconcile 链；
4. provider-specific process-start/authority successor、跨 daemon crash/restart 证据和独立发布验证。

当前 candidate 明确没有以下生产语义：`IssuerNonceDigest` 不是 signed carrier/JTI/connection nonce，也没有 issuer receipt、expiry/security-time 或跨重启 anti-replay；`usedChildren` 是无界进程内防护 map，重启/新副本不保留状态，重复请求不会返回原 candidate/receipt 或提供 operation query。child reservation/incarnation/current binding 是调用方提供的 owner-issued snapshot，candidate 不执行 IdentityRepository read-back，非空 receipt ref/合法 digest 不能证明 active-process、measurement 或 authority。parent-child 仅做 parent task/principal/scope 和 stable fence 关系校验，尚无 delegated grant、workspace/run subset、lineage/depth/fan-out、resource admission 或 CONTENT/OUT join。`ExternalIdentityRef`/`ExternalEpoch` 也不是 provider/runtime/session profile；provider-originated discovery/adoption、zero-effect proof 和独立 child connection/token 均未实现。`standalone/managedruntime` 的 endpoint/process issuer 是另一条候选 seam，不能当作此处的 CallerPrincipal 或 child credential owner。

在这些依赖完成前，该包不得进入 Fx、tool registry、provider bridge 或任何 public task/Room surface。
