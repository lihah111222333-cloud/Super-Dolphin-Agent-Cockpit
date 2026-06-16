# 第 07 轮审查结论

## 审查范围

- `internal/platform/difftracker/git_diff.go`（git diff 快照、HEAD/working tree 读取、文件大小/二进制过滤、diff block 拼接）
- `internal/platform/difftracker/resolver.go`（WorkDirResolver 接口）
- `internal/platform/bus/resilient.go`（ResilientSubscribe 兼容 wrapper）
- `internal/platform/bus/sink.go`（LogSink 事件落日志）
- `internal/platform/mcpcontrol/registry.go`（ToolRegistry 主结构、Register/Heartbeat/ShutdownInstance/OnDisconnect）
- `internal/platform/mcpcontrol/peers.go`（jrpc2 Peer 适配、closePeer 工具）

> 与第 01-06 轮已覆盖的 `toolbridge/`、`runner/`、`runtimesafe/`、`runtimeenv/`、`config/`、`mcpserver/common/` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `git_diff.go:44-47` `EmitGitDiff` | 兜底 | `snapshot == nil \|\| snapshot.root == ""` 时返回 `("", nil, nil)` | nil snapshot 是调用方 bug；当作"无 diff"会让 diff fallback 路径以为成功无内容 | nil snapshot 应 panic；root 为空应 error |
| `git_diff.go:74-77` 空 blocks 路径 | 兜底 | `if len(blocks) == 0 { return "", affected, nil }` | 有些情况 affected 非空但 blocks 为空（如所有改动都因 binary/size 被 skip），调用方拿到 `affected=[a,b]` 但 diff text 为空，状态自相矛盾 | 区分"无变动"与"全部被过滤"；后者应有 `ErrAllSkipped` 提示 |
| `git_diff.go:159-171` `readHEADText` | 兜底 | 二进制/超大文件返回 `("", false, nil)`；不传播任何信号 | 调用方无法分辨"文件不存在"与"文件被跳过"；`shouldSkipGitBytes` 把 binary/oversize 当不存在 | 返回 `(content, exists, skipped, err)`；分类清晰 |
| `git_diff.go:173-189` `readWorkingTreeText` | 兜底 | 同上，os.IsNotExist 与 binary skip 走同一条 false 路径 | 同上 | 同上 |
| `git_diff.go:191-209` `shouldSkipGitText/Bytes` | 静默 | 包含 NUL 字节就当 binary 跳过 | 二进制文件用户改了也不会出现在 diff 里；fallback emitter 收到空 diff 会以为"什么都没改" | binary 跳过应至少在 affected 列表里标注 `_skipped_binary` |
| `git_diff.go:215-239` `buildUnifiedDiffBlockWithState` | 静默 | `difflib.GetUnifiedDiffString` 错误返回空 string，被外层当成"无 diff" | difflib 错误（理论不会发生）会让 diff 静悄悄丢失 | 错误必须传播 |
| `resolver.go:5-7` `WorkDirResolver` | 弱契约 | 接口没有 `String()` 或 source 标识；多个实现并存时调用栈难溯源 | 单接口足够，但 ResolveAgentCWD 错误返回时下游靠 message 区分实现 | 在错误 wrap 里加 `resolver_kind` 字段（约定方式） |
| `bus/resilient.go:14-16` `ResilientSubscribe` | 弱契约 | 是 `contract.ResilientSubscribe` 的 thin wrapper；comment 写"new code should use contract directly" | 双入口并存增加心智负担；与 round-04 safego 双实现同根问题 | 短期标 Deprecated；长期删 |
| `bus/sink.go:21-33` `NewLogSink` | 兜底 | `dispatcher == nil \|\| logger == nil` 时返回空 sink；调用方拿到的 sink 是"哑对象" | nil 依赖是装配 bug；当作 noop 使所有日志静悄悄丢失 | 必须 panic |
| `bus/sink.go:35-41` `Close` | 静默 | `s == nil \|\| s.subs == nil` 兜底；`s.subs = nil` 之后再 Close 是 noop | nil receiver 调用是 bug | nil receiver 应 panic |
| `bus/sink.go:94-104` `logEvent` | 兜底 | `dispatcher == nil \|\| logger == nil` 返回 noop func | NewLogSink 已经 nil-guard 过；这里又一层兜底等于双重保险但没有任何实际防护 | 删掉这层 nil-guard，让构造期错误暴露 |
| `bus/sink.go:106-116` `logDebugEvent` | 兜底 | 同上 | 同上 | 同上 |
| `mcpcontrol/registry.go:94-116` `NewRegistry` / `NewToolRegistry` | 弱契约 | `RegistryOptions` 全字段零值兜底为 default；负值/0 都用 default 替换 | 用户配 `HeartbeatInterval = -1*time.Second` 拿到的是 10s default，无任何提示 | 负值返回 error；0 用 default 是合理的 |
| `mcpcontrol/registry.go:262-268` `ShutdownInstance` peer 缺失 | 兜底 | `instance.Peer == nil` 时返回 `errPeerUnavailable` 但**没尝试 evict instance** | peer 为 nil 说明 lease 已部分清理；继续保留会让 list/notify 拿到 zombie | peer=nil 时应同时 evict |
| `mcpcontrol/registry.go:296-308` `OnDisconnect` | 静默 | `_ = r.disconnectLease(...)` 显式忽略 disconnect 错误 | disconnect 失败可能意味着 hook 清理失败、子进程没退；没人知道 | log + metrics |
| `mcpcontrol/registry.go:269-291` peer Callback 失败 | 兜底 | failure 后通过 `notePeerFailure` 累积；达到阈值才 evict | 每次 failure 都返回 errPeerUnavailable，但 instance 留着；客户端反复 retry 期间窗口里其它 RPC 仍尝试用同一坏 peer | failure 即时阈值的设计本身合理，但应在错误返回时附 `try_count` 让 client 退避 |
| `mcpcontrol/registry.go:185-225` `Heartbeat` | 兜底 | lease 不存在时返回 `errLeaseNotFound`；status=disconnected 路径成功返回 `OK: true` 即使 disconnectLease 失败 | disconnected 处理路径中 disconnectLease 错误被吞 | `_ = r.disconnectLease(...)` 改为收集错误并返回 |
| `mcpcontrol/peers.go:23-35` `Callback` | 静默 | `if resp == nil \|\| result == nil { return nil }` | resp == nil 是 jrpc2 bug 或对端断连；当作成功返回会让上游误判 | resp == nil 应返回 errors.New("peer callback returned nil response") |
| `mcpcontrol/peers.go:31-34` UnmarshalResult | 兜底 | `if raw, ok := result.(*json.RawMessage)` 走 raw 分支；其它走通用 | 类型不符是调用方 bug；当前 swallow 了类型断言失败 | 应 panic 或 error；至少 log 类型不符 |
| `mcpcontrol/peers.go:37-42` `Close` | 静默 | `p.server.Stop()` 不返回错误（jrpc2 API），但 nil server 走 noop return nil | nil server 是 bug | nil server panic |
| `mcpcontrol/peers.go:44-48` `closePeer` | 静默 | `_ = peer.Close()` 显式忽略 | close 错误丢失 | log 一行 Warn |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `git_diff.go:44-47` | nil snapshot 当成"无 diff" |
| `git_diff.go:74-77` | 全跳过当成"无变动" |
| `git_diff.go:165-170` | binary/oversize HEAD 转 false |
| `git_diff.go:179-187` | binary/oversize working tree 转 false |
| `git_diff.go:235` | difflib 错误返回空字符串 |
| `bus/sink.go:23-25` | NewLogSink nil 依赖兜底 |
| `bus/sink.go:36-40` | Close nil 兜底 |
| `bus/sink.go:95-96, 107-108` | logEvent/logDebugEvent nil 兜底 |
| `mcpcontrol/registry.go:165-168` | Register 中 disconnectLease 错误忽略 |
| `mcpcontrol/registry.go:203-206` | Heartbeat disconnected 路径忽略 disconnectLease |
| `mcpcontrol/registry.go:279-285` | ShutdownInstance failure 路径忽略 disconnectLease |
| `mcpcontrol/registry.go:304-307` | OnDisconnect 忽略 disconnectLease |
| `mcpcontrol/peers.go:28-30` | resp/result nil 当成成功 |
| `mcpcontrol/peers.go:31-32` | 类型断言失败静默 |
| `mcpcontrol/peers.go:46` | closePeer 忽略 close 错误 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `git_diff.go:14-31` `BeginSnapshot` | path 不做强校验（仅依赖 git 命令探测）；MaxTrackedFiles 用 fmt.Errorf 简单 wrap |
| `git_diff.go:35-42` `EmitCurrentGitDiff` | path 同上 |
| `git_diff.go:44-78` `EmitGitDiff` | snapshot=nil 兜底；MaxTotalDiffBytes 用 fmt.Errorf 简单 wrap |
| `git_diff.go:159-189` HEAD/working tree 读取 | 错误信息不区分 "skipped" vs "missing" |
| `git_diff.go:241-250` `normalizeDiffPath` | 多 case 静默归一为 ""（".", "/", "/dev/null"） |
| `bus/resilient.go:14-16` | thin wrapper，dispatcher/logger/fn 不做 nil 校验（依赖 contract.ResilientSubscribe） |
| `bus/sink.go:21-33` | dispatcher/logger nil 兜底 |
| `mcpcontrol/registry.go:79-84` `RegistryOptions` | 全字段可选；零值/负值都走 default |
| `mcpcontrol/registry.go:118-183` `Register` | 依赖 normalizeRegisterRequest 校验，但 peerFromContext 失败的具体原因不在 error 中暴露 |
| `mcpcontrol/registry.go:228-234` `GetInstance` | 错误返回 (zero, false)；调用方无法区分 "not found" vs "lookup error" |
| `mcpcontrol/registry.go:236-246` `ListInstances` | 直接 RLock 后返回；不区分空 registry 与 nil registry |
| `mcpcontrol/peers.go:15-17` `jrpcPeer` 字段 | server 不做 nil 校验 |
| `mcpcontrol/peers.go:23-35` `Callback` | result 类型断言后默认走 generic 分支，类型不符不 fail |

## 修复优先级

### P0（必须本周修）
1. `git_diff.go:44-47` nil snapshot 必须 panic / error，不能静默成功
2. `mcpcontrol/peers.go:23-35` Callback resp/result==nil 必须返回 error
3. `mcpcontrol/registry.go:262-268` ShutdownInstance peer==nil 时 evict instance，避免 zombie lease
4. `mcpcontrol/registry.go:296-308` OnDisconnect 的 `_ = disconnectLease(...)` 改为 log + metrics
5. `bus/sink.go:21-33` NewLogSink nil 依赖必须 panic
6. `git_diff.go:159-189` HEAD/working tree 读取的 binary/oversize 路径要在 affected 列表里标注

### P1（本月）
7. `mcpcontrol/registry.go:165-168, 203-206, 279-285` 所有 `_ = r.disconnectLease(...)` 改为 errors.Join 上抛
8. `mcpcontrol/peers.go:31-34` Callback 类型断言失败应 error 或 panic
9. `mcpcontrol/registry.go:228-234` GetInstance 区分 not found / lookup error
10. `git_diff.go:215-239` difflib 错误必须传播
11. `mcpcontrol/registry.go:94-116` RegistryOptions 负值返回 error
12. `bus/sink.go:35-41` Close nil receiver panic

### P2（下个 sprint）
13. `bus/resilient.go` 整个文件标 Deprecated；逐步迁到 contract.ResilientSubscribe
14. `bus/sink.go:94-116` 删除 logEvent/logDebugEvent 中的 nil-guard（构造期已防）
15. `mcpcontrol/peers.go:44-48` closePeer 加 Warn log
16. `mcpcontrol/peers.go:37-42` jrpcPeer.Close nil server 改 panic
17. `git_diff.go:74-77` 区分"无变动"与"全部跳过"；返回 `ErrAllSkipped` sentinel
18. `git_diff.go:241-250` normalizeDiffPath 非法输入返 error 而非 ""

## 边界条件

1. **`git_diff.go` 的 binary 跳过设计是有意的**：避免把 PNG/二进制文件当成 diff 文本注入到 LLM context。修复时不要"反过来把 binary 也 emit"——只是要让 affected 列表能区分"未改" vs "改了但跳过"。
2. **`MaxTrackedFiles` / `MaxTotalDiffBytes` 都是硬上限**：超过直接报错。这条已经是 fail-fast 风格，本轮没改动它。但要注意 fallback 路径（`EmitCurrentGitDiff`）用了空 `beforeFiles`，所有文件都视为"新增"，diff 大小可能爆炸；需要确认 limit 仍能保护。
3. **`mcpcontrol/peers.go:23-35` Callback resp==nil 改 error 的影响**：jrpc2 在某些 notification-style 调用里会返回 nil resp。要先确认 mcpcontrol 的 Callback 是否被用作 notification（应该不是，因为有 result 参数）；如果有就要拆分 Callback 与 Notify 的实现。
4. **`mcpcontrol/registry.go:165-168` Register 路径里的 disconnectLease**：这是替换旧 lease 的清理，旧 lease 已经被 evict locked，这里失败不影响新 lease 注册成功。改 errors.Join 后调用方需要决定"新 lease 成功 + 旧 lease 清理失败"是否算成功——建议算成功但带 warning。
5. **`bus/sink.go` 的 nil-guard 是为测试方便**：很多测试用例在不提供 dispatcher 的情况下构造 LogSink。改 panic 后要同步更新测试或提供 fake dispatcher。
6. **`difftracker.WorkDirResolver` 接口很简单**：本轮没找到致命问题；但下游（toolbridge/diff_fallback、handler_host_tools 的 cwd 解析）已经在第 4、6 轮指出了"resolver 错误被合并成 ErrSkillMissingCWD"。修这两处时一起加 source 区分。
7. **`mcpcontrol/registry.go` 的 lock 顺序**：Register 中 `r.mu.Lock()` 期间调用 `r.evictLocked` 是合理的（同 lock 内）；`disconnectLease` 在 unlock 之后才调用——这是对的，因为 disconnectLease 内部可能调 close peer 阻塞。修 errors.Join 改造时不要把 disconnectLease 移回 lock 内。
8. **`bus/resilient.go` 的 Deprecated 标记**：grep 一下被多少处调用，决定是直接删还是保留 alias。本轮已确认实现是 thin wrapper，迁移工作量集中在调用点。

---

下一轮范围建议：
- `internal/platform/mcpcontrol/sweeper.go` + `sweeper_runner.go`（lease 扫描清理）
- `internal/platform/mcpcontrol/handlers.go` + `handlers_hooks.go`（jrpc2 method 处理器）
- `internal/platform/mcpcontrol/router.go` + `resolution.go`（路由与 scope 解析）
- `internal/platform/bus/subscription.go` + `subscribers.go`（订阅生命周期）
