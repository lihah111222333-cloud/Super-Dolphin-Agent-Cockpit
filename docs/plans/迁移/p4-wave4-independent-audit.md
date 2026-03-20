# P4 波次 4 前置修补独立重审

> 审查日期：2026-03-20

## 1. 基础验证

- `go build ./...`：通过。
- `go vet ./...`：通过。
- `go test ./internal/archtest/... -count=1 -timeout 120s`：通过，`ok github.com/anthropic-ai/super-agent-v3/internal/archtest 1.220s`。
- 附加验证：`go test ./... -count=1 -timeout 180s` 未全绿。失败点为 `scripts` 包中的 `TestExtractJSONRPCMethodsScript_EmitsKnownMethods`，报错显示脚本仍在扫描已不存在的 `internal/apiserver`、`internal/dashrpc`、`internal/skills` 目录。该失败不属于本次 provider 波次 4 前置修补范围，但当前仓库并非全量单测全绿。

## 2. B1 ReadHistory

### 接口

- `internal/contract/provider.go:23-35` 的 `contract.Session` 已包含 `ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error)`。
- 接口位置与现有 `ListThreads`、`ForkThread` 同层，属于 session 读能力扩展，不影响 `TurnHandle` 或 `Driver` 契约。

### Message 类型

- `internal/dto/provider/message.go:5-10` 已存在 `Message` 类型。
- 字段完整：`Role string`、`Content string`、`Timestamp time.Time`、`Metadata map[string]any`。
- 依赖仅为标准库 `time`，无 `fx`、RPC、存储、平台层依赖，满足零框架依赖要求。

### Claude 实现

- `internal/provider/claudecli/session_history.go:12-26` 已实现 `(*session).ReadHistory`。
- 默认线程解析逻辑正确：优先使用入参 `threadID`，为空时回退到 `s.ThreadID()`。
- 实现真实委托 `historyBackend`：`internal/provider/claudecli/session_history.go:20` 调用 `s.history.ReadHistory(ctx, target)`，不是空壳实现。
- 返回类型正确：`trimClaudeHistory` 后经 `toProviderHistory` 转为 `[]dto.Message` 返回。
- 后端注入真实存在：`internal/provider/claudecli/driver.go:102-118` 初始化 session 时注入 `history: &historyBackend{sessionDir: spec.historyDir}`。
- 后端读取真实存在：`internal/provider/claudecli/history.go:18-47` 读取 Claude 本地 `.jsonl` 历史并解析为本地 `[]Message`。

### Codex 实现

- `internal/provider/codexapp/session_history.go:13-26` 已实现 `(*session).ReadHistory`。
- 默认线程解析逻辑正确：优先使用入参 `threadID`，为空时回退到 `s.threadID`，空值会直接报错。
- 实现真实委托 `rolloutReader`：`internal/provider/codexapp/session_history.go:21` 调用 `s.history.ReadHistory(ctx, target, limit)`。
- 返回类型正确：调用 `toProviderHistory`，映射为 `[]dto.Message`；同时保留 `Metadata` 解码逻辑，见 `internal/provider/codexapp/session_history.go:28-50`。
- 后端注入真实存在：`internal/provider/codexapp/session.go:74-85` 初始化 session 时注入 `history: &rolloutReader{transport: transport}`。
- 后端读取真实存在：`internal/provider/codexapp/history.go:19-39` 优先读本地 rollout，失败或空结果时回退到 RPC `thread/read`。

### 编译期断言

- `internal/provider/claudecli/session.go:393` 仍保留 `var _ contract.Session = (*session)(nil)`。
- `internal/provider/codexapp/session.go:32` 仍保留 `var _ contract.Session = (*session)(nil)`。
- `go build ./...` 已通过，说明接口扩展后的编译期约束已真实生效。

## 3. 全面回归

### import 方向

- 使用 LSP 逐文件核对 `internal/provider/unified/*.go`、`internal/provider/claudecli/*.go`、`internal/provider/codexapp/*.go`、`internal/dto/provider/*.go`、`internal/contract/provider.go`、`internal/module/turn/*.go` 的 import。
- `internal/provider/unified/*` 仅依赖 `internal/contract`、`internal/dto/provider` 与外部库。
- `internal/provider/claudecli/*` 仅依赖 `internal/contract`、`internal/dto/{agent,provider,shared,tool,turn}`、`internal/platform/shared`、`internal/provider/unified` 与标准库/外部库。
- `internal/provider/codexapp/*` 仅依赖 `internal/contract`、`internal/dto/{agent,provider,shared,tool,turn}`、`internal/platform/shared`、`internal/provider/unified` 与标准库/外部库。
- `internal/module/turn/*` 仅依赖 `internal/contract`、`internal/dto/{provider,shared}` 与外部库。
- `internal/dto/provider/*` 仅依赖标准库；其中 `internal/dto/provider/turn.go` 额外依赖 `internal/dto/shared`。
- 未发现 provider 层反向依赖 `internal/module/*`、`internal/app`、`internal/store` 或其他更高层包。`go test ./internal/archtest/...` 通过与该结论一致。

### 行数

- 目标范围内文件全部 `<= 400` 行。
- 最大文件为 `internal/provider/claudecli/session.go`，`393` 行。
- 其后为 `internal/provider/codexapp/session.go`，`337` 行；`internal/provider/codexapp/transport.go`，`322` 行。
- 使用 LSP 对含函数文件逐文件扫描 `^func `，未发现任何函数 `> 80` 行。
- 最大函数为 `internal/provider/codexapp/event_map.go:76-112`，共 `37` 行。
- 次大函数为 `internal/provider/claudecli/session.go:266-301`，共 `36` 行；`internal/provider/codexapp/event_map.go:114-148`，共 `35` 行。

### 并发安全

- Claude:
  - `internal/provider/claudecli/session_history.go:13-25` 仅执行只读逻辑。
  - `s.history` 在 `internal/provider/claudecli/driver.go:102-118` 初始化后不再改写。
  - 默认线程回退使用 `s.ThreadID()`；`internal/provider/claudecli/session.go:77-81` 对 `threadID` 读取加锁。
  - Claude 流事件会在 `internal/provider/claudecli/session_events.go:46-53` 更新 `threadID`，同样受 `s.mu` 保护。
  - `historyBackend` 本身仅包含不可变 `sessionDir string`，`internal/provider/claudecli/history.go:18-47` 每次调用只使用局部变量和独立文件句柄；并发读安全。
- Codex:
  - `internal/provider/codexapp/session_history.go:14-25` 仅执行只读逻辑。
  - `s.history` 在 `internal/provider/codexapp/session.go:74-85` 初始化后不再改写。
  - `s.threadID` 在 `internal/provider/codexapp/driver.go:75-90` 与 `93-108` 内赋值后返回给调用方，后续未发现再写路径。
  - `rolloutReader.ReadHistory` 最终通过 `transport.Call` 访问 RPC；`internal/provider/codexapp/transport.go:55-61`、`79-99`、`192-200`、`271-293` 通过 `writeMu`、`sync.Map`、`atomic`、`stateMu` 保护并发调用。
- 结论：新增 history 读路径未引入共享可变状态竞争；当前实现满足并发只读访问要求，不需要为该方法再额外加锁。

### CapabilityError

- `internal/dto/provider/capability.go:7-15` 现有 capability 常量中不存在 `ReadHistory` 对应 capability；本次新增能力未被接入 capability gate。
- 全范围检索 `NewCapabilityError`，仅命中 `internal/provider/claudecli/session.go:229-235`。
- Claude 对不支持的方法仍返回 `CapabilityError`：
  - `ListThreads` -> `dto.NewCapabilityError(dto.CapThreadList, "claude")`
  - `ForkThread` -> `dto.NewCapabilityError(dto.CapThreadFork, "claude")`
- `ReadHistory` 在 `internal/provider/claudecli/session_history.go:12-26` 与 `internal/provider/codexapp/session_history.go:13-26` 都直接调用后端，不经过 `caps.Has(...)`，也不返回 `CapabilityError`。

## 4. 结论

### 波次 4 可开工判定

- 判定：可开工。
- 依据：
  - 波次 4 前置修补已真实落地，不是接口空挂。
  - `contract.Session` 扩展、`dto.Message` 新类型、Claude/Codex 双实现、编译期断言均已到位。
  - `go build`、`go vet`、`archtest` 全通过。
  - provider 层 import 方向、文件长度、函数长度、并发安全、CapabilityError 语义均未发现回归。

### 需修正项（如有）

- 本次审查范围内未发现阻塞波次 4 开工的问题。
- 非阻塞仓库级问题：`go test ./... -count=1 -timeout 180s` 失败于 `scripts` 包 guard test。若目标是全仓测试全绿，需要单独修复该脚本测试对已删除目录的陈旧扫描配置。
