# P4 波次 4 审查 B（T8 侧重）

## 1-2. 基础验证

- `go build ./...`：PASS
- `go test ./internal/archtest/... -count=1 -timeout 120s`：PASS
- 守卫输出：`ok github.com/anthropic-ai/super-agent-v3/internal/archtest 1.661s`

## 3. 测试执行结果

- `go test ./internal/provider/unified/... -v -count=1`：PASS
- 包输出：`ok github.com/anthropic-ai/super-agent-v3/internal/provider/unified 0.682s`
- Test 明细：

| Test | 结果 |
| --- | --- |
| `TestClient_StartSession_SelectsCorrectDriver` | PASS |
| `TestClient_StartSession_UnknownProvider` | PASS |
| `TestSessionContract_StartTurn` | PASS |
| `TestSessionContract_Interrupt` | PASS |
| `TestSessionContract_ReadHistory` | PASS |
| `TestSessionContract_ListThreads_Unsupported` | PASS |
| `TestSessionContract_ForkThread_Unsupported` | PASS |
| `TestSessionContract_Configure` | PASS |
| `TestSessionContract_Close` | PASS |
| `TestSessionContract_ForceStop` | PASS |
| `TestBuildManifest_DefaultFamilies` | PASS |
| `TestBuildManifest_WithIDA` | PASS |
| `TestBuildManifest_BinaryPaths` | PASS |
| `TestRegistry_Resolve_Known` | PASS |
| `TestRegistry_Resolve_Unknown` | PASS |
| `TestRegistry_Resolve_NilFactory` | PASS |
| `TestRegistry_Names` | PASS |

## 4. mock Session 完整性

`contract.Session` 定义位于 `internal/contract/provider.go:23-36`，共 10 个方法。`mockSession` 位于 `internal/provider/unified/contract_test.go:30-78`，逐项对照如下：

| 接口方法 | mock 实现 | 结论 |
| --- | --- | --- |
| `ThreadID() string` | `contract_test.go:41` | 已实现 |
| `Capabilities() dto.CapabilitySet` | `contract_test.go:42` | 已实现 |
| `StartTurn(ctx, req)` | `contract_test.go:49-52` | 已实现 |
| `Interrupt(ctx, req)` | `contract_test.go:45-48` | 已实现 |
| `ListThreads(ctx)` | `contract_test.go:53-58` | 已实现 |
| `ForkThread(ctx, req)` | `contract_test.go:59-64` | 已实现 |
| `ReadHistory(ctx, threadID, limit)` | `contract_test.go:65-74` | 已实现 |
| `Configure(ctx, patch)` | `contract_test.go:75-78` | 已实现 |
| `Close(ctx)` | `contract_test.go:43` | 已实现 |
| `ForceStop()` | `contract_test.go:44` | 已实现 |

结论：

- mock 方法集与 `contract.Session` 完全一致。
- `client_test.go:13-24`、`registry_test.go:11-18`、`registry_test.go:37-47` 均把 `*mockSession` 通过 `contract.Driver` / `contract.Session` 路径使用，缺方法时编译不会通过。
- `contract_test.go` 本身没有显式 `var _ contract.Session = (*mockSession)(nil)` 断言，但当前测试包编译链已形成等效约束。

## 5. CapabilityError

- `TestSessionContract_ListThreads_Unsupported` 位于 `internal/provider/unified/contract_test.go:131-137`。
- `TestSessionContract_ForkThread_Unsupported` 位于 `internal/provider/unified/contract_test.go:139-145`。
- 两个测试都使用 `errors.As(err, &capErr)` 验证返回类型为 `*dto.CapabilityError`。
- 两个测试都断言了 `capErr.Capability`，分别为 `dto.CapThreadList` 和 `dto.CapThreadFork`。

结论：

- “不支持能力时返回 `*dto.CapabilityError`” 已有专门测试。
- “针对 Claude capability profile 的专门测试” 不存在。当前用例仅验证“空 capability set / 缺 capability”场景，没有构造 Claude 专属 capability 集合作为前提。

## 6. Registry 覆盖

- `TestRegistry_Resolve_Known`：覆盖 known provider，位于 `internal/provider/unified/registry_test.go:10-19`。
- `TestRegistry_Resolve_Unknown`：覆盖 unknown provider，位于 `internal/provider/unified/registry_test.go:21-26`。
- `TestRegistry_Resolve_NilFactory`：覆盖 factory 返回 `nil`，位于 `internal/provider/unified/registry_test.go:28-35`。
- `TestRegistry_Names`：额外覆盖名称规范化与排序，位于 `internal/provider/unified/registry_test.go:37-48`。

结论：要求的 3 个 Resolve 场景齐全，另有 `Names()` 附加覆盖。

## 7. Manifest 覆盖

`BuildManifest` 位于 `internal/dto/provider/manifest.go:28-42`，通过内部 `families` 列表生成 `Binaries`。

- `TestBuildManifest_DefaultFamilies`：覆盖默认分支，断言输出为 2 个默认 binary 名称，位于 `internal/provider/unified/manifest_test.go:9-14`。
- `TestBuildManifest_WithIDA`：覆盖带 `ida` capability 时追加第三个 binary，位于 `internal/provider/unified/manifest_test.go:16-21`。
- `TestBuildManifest_BinaryPaths`：覆盖 `BinaryDir` 拼接命令路径，位于 `internal/provider/unified/manifest_test.go:23-31`。

结论：

- 默认 families、含 IDA、`BinaryDir` 拼接 3 个场景都已覆盖。
- 默认分支只校验了 binary 名称和数量，没有校验 `BinaryDir == ""` 时的 `Command` 值；该分支的命令路径仍有未显式断言的空白。

## 8. Client 覆盖

- `TestClient_StartSession_SelectsCorrectDriver` 位于 `internal/provider/unified/client_test.go:12-26`，验证：
  - provider 选路到正确 driver。
  - session 被注册到 `SessionManager`。
  - 目标 driver 调用计数为 1，非目标 driver 为 0。
- `TestClient_StartSession_UnknownProvider` 位于 `internal/provider/unified/client_test.go:28-33`，验证 unknown provider error 非空。

结论：

- “选路正确” 与 “unknown provider error” 两个要求都满足。
- `ResumeSession` 没有对应测试；当前 `Client` 的 `open()` 共用路径虽被 `StartSession` 间接覆盖，但恢复会话入口仍缺单测。

## 9. 测试质量

基于 `internal/provider/unified` 范围内 LSP 检索与逐文件核对：

- 未发现 `TODO`。
- 未发现 `_ = ...` 形式的伪断言或伪消费结果。
- 未发现 `t.Skip`。
- 各测试均包含显式失败条件，使用 `t.Fatal` / `t.Fatalf` 做断言。
- 未发现“仅调用函数、不校验结果”的空断言测试。

补充观察：

- `TestClient_StartSession_SelectsCorrectDriver` 会输出一条默认 logger 的 `INFO` 日志，不影响通过性，但会增加 `-v` 输出噪声。

## 10. ReadHistory

- `TestSessionContract_ReadHistory` 位于 `internal/provider/unified/contract_test.go:120-129`。
- 该测试直接调用 `mockSession.ReadHistory(...)`，验证了：
  - 正确返回历史消息。
  - `limit=1` 时返回尾部一条消息。
- `internal/module/thread/history.go:13-19` 与 `history.go:22-45` 都通过 `session.ReadHistory(...)` 走统一会话接口。

结论：

- mock session 的 `ReadHistory` 已被直接测试。
- 未覆盖的边界包括：线程 ID 不匹配返回 `nil`、`limit <= 0` 语义、`ReadMessages` 的分页前置过滤与 mock 行为组合。

## 11-12. T7 快扫

- `go build ./internal/module/thread/...`：PASS
- `thread` 包总行数：477

| 文件 | 行数 |
| --- | ---: |
| `internal/module/thread/archive.go` | 21 |
| `internal/module/thread/command.go` | 70 |
| `internal/module/thread/contract.go` | 27 |
| `internal/module/thread/history.go` | 93 |
| `internal/module/thread/module.go` | 6 |
| `internal/module/thread/service.go` | 260 |
| 合计 | 477 |

## 结论

- 本次要求的编译、守卫、`unified` 测试、`thread` 包编译均通过。
- T8 重点项中，mock Session 完整性、CapabilityError 返回类型、Registry 三场景、Manifest 三场景、Client 选路/unknown provider、ReadHistory 直测均已具备。
- 非阻塞缺口有 4 项：
  - CapabilityError 用例不是 Claude capability profile 专测，只是通用“缺能力”测试。
  - Manifest 默认分支未断言 `Command` 值。
  - Client 缺 `ResumeSession` 测试。
  - Contract 级测试未直接覆盖 `ThreadID()`、`Capabilities()` 以及 `ListThreads` / `ForkThread` 支持路径。
