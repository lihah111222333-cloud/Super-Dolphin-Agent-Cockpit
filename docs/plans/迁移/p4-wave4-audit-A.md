# P4 波次 4 审查 A（T7 侧重）

## 1-3. 基础验证

- `go build ./...`：PASS
- `go vet ./...`：PASS
- `go test ./internal/archtest/... -count=1 -timeout 120s`：PASS

## 4. import 方向

- 审查范围：`internal/module/thread/archive.go`、`command.go`、`contract.go`、`history.go`、`module.go`、`service.go`
- `archive.go` import 仅有 `context`
- `command.go` import `context/errors/fmt/strings/internal/dto/provider`，未 import `internal/provider/*`
- `contract.go` import `context/internal/dto/provider`，未 import `internal/provider/*`
- `history.go` import `context/errors/strconv/strings/time/internal/dto/provider`，未 import `internal/provider/*`
- `module.go` import 仅有 `go.uber.org/fx`
- `service.go` import `context/errors/log/slog/strings/time/internal/contract/internal/store/binding/internal/store/thread`，未 import `internal/provider/*`
- 结论：PASS。`module/thread/*.go` 未出现对 `internal/provider/*` 的直接依赖；`dto/provider` 仅是 DTO 包，不属于 provider 实现层。

## 5. 行数

| 文件 | 行数 | 文件阈值 | 最大函数长度 | 函数阈值 | 结果 |
| --- | ---: | ---: | ---: | ---: | --- |
| `internal/module/thread/archive.go` | 20 | 400 | 9 (`Archive`) | 80 | PASS |
| `internal/module/thread/command.go` | 69 | 400 | 30 (`SendCommand`) | 80 | PASS |
| `internal/module/thread/contract.go` | 26 | 400 | 0 | 80 | PASS |
| `internal/module/thread/history.go` | 92 | 400 | 25 (`parseBeforeCursor`) | 80 | PASS |
| `internal/module/thread/module.go` | 5 | 400 | 0 | 80 | PASS |
| `internal/module/thread/service.go` | 259 | 400 | 17 (`listThreads`) | 80 | PASS |

- 统计命令：`wc -l internal/module/thread/*.go`
- 结论：PASS。所有 thread 文件均低于 400 行，所有函数均低于 80 行。

## 6. 接口完整性

- `internal/module/thread/contract.go:9-21` 定义 `Service` 11 个方法：`List/Get/ReadHistory/ReadMessages/Archive/Unarchive/ListByStatus/ListByCWD/SendCommand/SetName/Delete`
- 实现分布如下：
- `service.go`：`List/Get/ListByStatus/ListByCWD/SetName/Delete`
- `history.go`：`ReadHistory/ReadMessages`
- `archive.go`：`Archive/Unarchive`
- `command.go`：`SendCommand`
- `internal/module/thread/service.go:32` 存在 `var _ Service = (*service)(nil)` 断言
- 结论：PASS。接口与实现一致，且有编译期断言。

## 7. SessionProvider

- `internal/module/thread/service.go:20-23` 定义窄接口 `SessionProvider`，只暴露 `GetSession(agentID string) (contract.Session, error)`
- `internal/module/thread/service.go:180-193` 的 `resolveSession(...)` 先走 `bindingStore.GetByAgentID(...)` 取绑定，再通过 `sessions.GetSession(binding.AgentID)` 取 session
- `internal/module/thread/service.go:225-240` 的 `historyTargetID(...)` 解析顺序为 `ProviderThreadID -> CodexThreadID -> AgentID -> threadID`
- 正向结论：PASS。thread service 通过窄接口取 session，未直接 import `internal/provider/unified`
- 风险结论：当前装配闭环未证实。LSP 搜索下，仓内未找到任何 `GetSession(agentID string)` 的具体实现；`internal/provider/unified/session.go:48-57` 仅提供 `Get(agentID string)`，`internal/provider/unified/module.go:15-21` 也未见 `fx.As(...)` 或适配器暴露 `thread.SessionProvider`
- 影响：`go build` 只能证明类型可编译，不能证明 Fx 运行时图一定能解出 `thread.SessionProvider`

## 8. V2 对照

- 抽读文件：
- `go-agent-v2/pkg/agentsdk/service/history/thread_history_core.go` 前 50 行
- `go-agent-v2/pkg/agentsdk/service/archive/thread_archive_core.go` 前 50 行
- `go-agent-v2/pkg/agentsdk/service/listing/thread_listing_core.go` 前 50 行
- `thread_history_core.go` 前 50 行暴露的是 backend/timeout/context 级抽象；符号表继续显示 `ResolveProviderThreadCandidates`、`ThreadExistsInHistory`。V3 当前已覆盖“通过 session 读取历史”这条主链，但未覆盖 V2 的 backend fallback、rollout/artifact 搜索、history existence probe
- `thread_archive_core.go` 前 50 行暴露的是 archive manifest/file/restore 结构与 restore 依赖；V3 当前 `Archive/Unarchive` 仅更新 thread status + binding archived 标记，不覆盖 manifest/restore/inspect 语义
- `thread_listing_core.go` 前 50 行暴露的是分页、列表项和 alias 相关结构；符号表继续显示 `BuildThreadList`、`AppendArchivedThreads`、`PersistThreadAlias` 等。V3 当前已覆盖 `List/Get/ListByStatus/ListByCWD/SetName`，但未覆盖 cursor 分页、alias 存储、archive/status 聚合列表
- 结论：PARTIAL。V3 覆盖了 T7 的基础 thread service 面，但未达到 V2 三个 core 文件的完整能力面
- 说明：`docs/plans/迁移/p4-wave4-review.md:321-325` 已明确 B2 只注入 `thread + binding` store，并推迟 `agentstatus/uipreference`，因此 listing 能力缩减与决策一致；archive manifest/restore 能力仍未见 V3 对应承接点

## 9. 工厂模式

- `history.go`、`archive.go`、`command.go` 已拆成独立文件
- `service.go` 负责构造、列表/查询/删除、状态更新、binding 解析、session 解析、公共 helper，并非“只编排”的薄工厂文件
- 共享逻辑已集中在 `resolveSession`、`resolveBinding`、`historyTargetID`、`normalizeThreadID`，未看到明显重复代码
- 结论：PARTIAL。文件拆分方向正确，但 `service.go` 仍承担较多业务辅助逻辑，属于“构造 + 公共编排/辅助”而非纯工厂

## 10. store 依赖

- `internal/module/thread/service.go:25-30` 注入依赖为：
- `threadstore.Store`
- `bindingstore.Store`
- `SessionProvider`
- 未注入 `store/agentstatus`、`store/uipreference`
- 与 `docs/plans/迁移/p4-wave4-review.md:321-325` 的 B2 决策一致：store 面只保留 `thread + binding`
- 差异点：B2 文本写的是“通过 `unified.SessionManager` 获取 `contract.Session`”，实际落地是更窄的 `SessionProvider` 抽象；方向更好，但当前缺少已证实的 concrete adapter
- 结论：PASS（store 面），RISK（session provider 装配面）

## 11-12. T8 快扫

- `internal/provider/unified/*_test.go` 存在以下文件：
- `client_test.go`
- `contract_test.go`
- `manifest_test.go`
- `registry_test.go`
- `go test ./internal/provider/unified/... -v -count=1`：PASS
- 通过测试 17 项：
- `client_test.go` 2 项
- `contract_test.go` 8 项
- `manifest_test.go` 3 项
- `registry_test.go` 4 项

## 结论

- 基础验证全部通过：`build`、`vet`、`archtest`、`unified` 测试均 PASS
- T7 结构约束整体通过：`module/thread` 未反向 import provider，实现文件和函数体量均在阈值内，`Service` 接口完整且有断言
- 当前最主要的技术风险有 2 个：
- `SessionProvider` 抽象已建立，但仓内未找到 `GetSession(...)` 的 concrete 实现或 Fx 适配绑定；运行时装配存在未闭环风险
- V3 thread service 仅部分覆盖 V2 `history/archive/listing` core 能力，尤其缺少 archive manifest/restore 与 richer listing 聚合能力
- 审查结论：可判定 T7 当前实现“结构方向正确、基础质量通过”，但若目标是完成波次 4 的线程服务迁移闭环，仍需补证 `SessionProvider` 装配，并明确 V2 能力缩减是否为接受中的范围决策
