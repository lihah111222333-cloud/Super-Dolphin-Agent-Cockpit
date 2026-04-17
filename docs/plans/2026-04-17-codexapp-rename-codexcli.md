# 重命名计划：`internal/provider/codexapp` → `internal/provider/codexcli`

> 目标：将 Codex 提供者包名从 `codexapp` 改为 `codexcli`，与 `claudecli` 对称，语义更精确。
>
> 状态：**待审批**

## 背景

当前两个提供者包命名不对称：

```
internal/provider/claudecli/   ← CLI 适配器，命名清晰
internal/provider/codexapp/    ← 也是 CLI 适配器，但命名暗示「应用」语义  ❌
```

`codexapp` 实际职责是通过 stdin/stdout 或 WebSocket 与 Codex CLI 进程通信（transport、session、recovery），本质上是 CLI 适配器，应命名为 `codexcli`。

## 影响面清单

### 1. Go 代码（编译时可验证）

| 类别 | 数量 | 说明 |
|------|------|------|
| 包内文件 | 31 个 `.go` | `package codexapp` → `package codexcli` |
| 子包 | 1 个 (`protocol/`) | 路径 `codexapp/protocol` → `codexcli/protocol` |
| 外部 import 引用文件 | 7 个 | 见下表 |
| 外部 import 引用次数 | 17 处 | sed 自动替换 |

**外部引用方清单**（仅 7 个文件）：

| 文件 | 引用内容 |
|------|---------|
| `internal/app/modules.go` | import `codexapp` |
| `internal/platform/toolbridge/handler.go` | import `codexapp` + `codexapp/protocol` |
| `internal/platform/toolbridge/handler_test.go` | import `codexapp` + **4 个 `go:linkname`** |
| `internal/platform/toolbridge/module.go` | import `codexapp` |
| `internal/provider/e2e/codex_mcp_test.go` | import `codexapp/protocol` |
| `internal/archtest/dependency_direction_test.go` | 路径字符串 |
| `internal/archtest/dependency_direction_wave3_test.go` | 路径字符串 |

### 2. fx module 名称

```go
// internal/provider/codexapp/module.go:22
var Module = fx.Module("provider.codexapp",   // → "provider.codexcli"
```

> **决策**：同步改为 `"provider.codexcli"`。此字符串出现在 Fx 日志中（`[Fx] PROVIDE: ... from module "provider.codexapp"`），改名后日志输出会变化。**无运行时功能影响**，仅影响日志可读性（正向改善）。

### 3. 日志 / 错误前缀字符串（62 处）

包内大量日志和错误消息使用 `"codexapp: ..."` 前缀：

| 文件 | 出现次数 |
|------|---------|
| `recovery.go` | 16 处 |
| `factory.go` | 12 处 |
| `transport_process.go` | 10 处 |
| `support.go` | 6 处 |
| `session_approval.go` | 5 处 |
| `transport_helpers.go` | 5 处 |
| `event_map.go` | 5 处 |
| `session.go` | 3 处 |
| `session_history.go` | 3 处 |
| `driver.go` | 2 处 |
| `transport.go` | 1 处 |
| `history_rollout.go` | 1 处 |

> **⚠️ 风险点 R1**：这些前缀字符串改名不影响编译和功能，但如果有外部日志分析/告警规则 grep `"codexapp:"` 关键字，改名后会丢失匹配。
>
> **收敛措施**：确认当前无外部日志监控系统依赖此前缀。若有，可分两阶段：先改包名/import，日志前缀暂不改或做 alias（`codexcli(codexapp): ...`），观察一周后再统一清理。

> **决策**：**日志前缀同步改为 `"codexcli: ..."`**，保持与包名一致。理由：项目尚无外部日志告警系统，内部一致性优先。

### 4. `go:linkname` 硬编码（4 处）— ⚠️ 最高风险点

`internal/platform/toolbridge/handler_test.go` 中通过 `go:linkname` 直接链接 `codexapp` 包的私有符号：

```go
//go:linkname codexNewSession github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp.newSession
//go:linkname codexSessionOnInboundMessage github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp.(*session).onInboundMessage
//go:linkname codexSessionClose github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp.(*session).Close
//go:linkname codexWaitReadLoopStopped github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp.(*session).waitReadLoopStopped
```

> **⚠️ 风险点 R2**：`go:linkname` 中的包路径是**编译器符号链接**，不是普通字符串。如果 sed 替换遗漏或格式错误，会导致：
> - 链接目标消失 → 编译错误（`relocation target not found`）
> - 链接到错误符号 → 运行时 segfault
>
> **收敛措施**：
> 1. sed 替换后，**逐条人工核对** 4 个 linkname 指令的完整路径
> 2. `go build ./internal/platform/toolbridge/...` 确认编译通过
> 3. `go test ./internal/platform/toolbridge/... -count=1` 确认测试通过

### 5. 文档（106 个 md 文件）

| 类别 | 文件数 | 处理方式 |
|------|--------|---------|
| `docs/doc/codemap/ai-index.json` | 1 | `make codemap-refresh` 自动重建 |
| `docs/` 下的 md 文件 | ~105 | sed 批量替换 |
| `migration_checklist.json` | 1 | sed 替换 |

> **⚠️ 风险点 R3**：105 个文档中的 `codexapp` 可能有一些是"概念名"（如 "codex app server"），而非包路径引用。盲目 sed 会损坏文档语义。
>
> **收敛措施**：仅替换 `provider/codexapp` → `provider/codexcli`（带路径前缀的精确替换），而非替换所有裸 `codexapp` 字符串。包内日志前缀（`"codexapp: ..."`）这些由包内 sed 单独处理。

### 6. archtest 架构守卫

`dependency_direction_test.go` 和 `dependency_direction_wave3_test.go` 中有路径字符串：

> **风险**：低。sed 命中后 `go test ./internal/archtest/...` 即可验证。

## 风险汇总与收敛

| ID | 风险 | 级别 | 原因 | 收敛措施 | 验证方法 |
|----|------|------|------|---------|---------|
| R1 | 日志前缀变更 | **低** | 无外部日志监控 | 统一替换为 `codexcli:` | grep 确认一致性 |
| R2 | `go:linkname` 符号断链 | **高** | 编译器级硬编码 | 4 处逐条人工核对 | `go build` + `go test` toolbridge |
| R3 | 文档语义破坏 | **中** | 裸替换命中非路径文本 | 仅带路径前缀精确替换 | 人工抽查 5 份文档 |
| R4 | fx module 名变更 | **低** | 仅影响日志 | 同步改为 `provider.codexcli` | Fx 启动日志确认 |
| R5 | import 遗漏 | **无** | 编译时立即报错 | `go build ./...` | 编译通过 |
| R6 | git blame 断裂 | **低** | 历史追溯困难 | 使用 `git mv` + `git log --follow` | — |

## 执行步骤

### Step 1：移动目录（保留 blame）

```bash
git mv internal/provider/codexapp internal/provider/codexcli
```

### Step 2：替换 package 声明（31 个文件）

```bash
find internal/provider/codexcli -maxdepth 1 -name '*.go' \
  -exec sed -i '' 's/^package codexapp$/package codexcli/' {} +
```

> 子包 `protocol/` 的 package 声明是 `package protocol`，不受影响。

### Step 3：替换 import 路径（全仓 Go 文件）

```bash
find . -name '*.go' -not -path './.git/*' \
  -exec sed -i '' 's|provider/codexapp|provider/codexcli|g' {} +
```

> 此条同时覆盖：
> - 普通 import 路径
> - `go:linkname` 中的完整包路径
> - archtest 中的路径字符串

### Step 4：人工核对 `go:linkname`（R2 收敛）

逐条核对 `internal/platform/toolbridge/handler_test.go` 中 4 个 linkname 指令：

```bash
grep -n 'go:linkname' internal/platform/toolbridge/handler_test.go
```

预期结果（每条路径均含 `codexcli`）：

```
23://go:linkname codexNewSession github.com/anthropic-ai/super-agent-v3/internal/provider/codexcli.newSession
26://go:linkname codexSessionOnInboundMessage github.com/anthropic-ai/super-agent-v3/internal/provider/codexcli.(*session).onInboundMessage
29://go:linkname codexSessionClose github.com/anthropic-ai/super-agent-v3/internal/provider/codexcli.(*session).Close
32://go:linkname codexWaitReadLoopStopped github.com/anthropic-ai/super-agent-v3/internal/provider/codexcli.(*session).waitReadLoopStopped
```

### Step 5：替换 fx module 名

```bash
sed -i '' 's/"provider\.codexapp"/"provider.codexcli"/' \
  internal/provider/codexcli/module.go
```

### Step 6：替换日志/错误前缀字符串（62 处）

```bash
find internal/provider/codexcli -name '*.go' \
  -exec sed -i '' 's/"codexapp:/"codexcli:/g; s/"codexapp /"codexcli /g' {} +
```

### Step 7：精确替换文档路径

```bash
# 精确替换：仅替换 provider/codexapp 路径引用
find docs -name '*.md' \
  -exec sed -i '' 's|provider/codexapp|provider/codexcli|g' {} +

# migration_checklist.json
sed -i '' 's|provider/codexapp|provider/codexcli|g' migration_checklist.json

# ai-index.json 自动重建
make codemap-refresh
```

### Step 8：编译验证

```bash
go build ./...
go vet ./...
```

### Step 9：测试验证

```bash
# 高风险：toolbridge linkname 测试
go test ./internal/platform/toolbridge/... -count=1 -v

# 架构守卫
go test ./internal/archtest/... -count=1

# codexcli 自身测试
go test -p 1 ./internal/provider/codexcli/... -count=1

# 全量
go test -p 1 ./... -count=1
```

### Step 10：残留检查

```bash
# Go 文件中不应有任何 codexapp 残留
grep -rn 'codexapp' --include='*.go' . | grep -v '.git/'
# 预期：0 结果

# 文档路径残留（允许本计划文档保留历史）
grep -rn 'provider/codexapp' --include='*.md' --include='*.json' . | grep -v '.git/' | grep -v '本计划文档'
# 预期：0 结果（除本文档外）
```

## 提交策略

**单 commit**（改动纯机械，原子性强）：

```
refactor(provider): rename codexapp to codexcli for semantic consistency with claudecli

- git mv internal/provider/codexapp → internal/provider/codexcli
- package declaration: codexapp → codexcli
- import paths: 7 external files, 17 occurrences
- go:linkname symbols: 4 directives in toolbridge/handler_test.go
- fx module name: "provider.codexapp" → "provider.codexcli"
- log/error prefixes: 62 occurrences across 12 files
- docs: ~105 markdown files path references
```

## 工作量估算

| 步骤 | 耗时 |
|------|------|
| Step 1-3（git mv + sed） | 2 分钟 |
| Step 4（linkname 人工核对） | 3 分钟 |
| Step 5-7（fx + 日志 + 文档） | 5 分钟 |
| Step 8-10（编译 + 测试 + 残留检查） | 10 分钟 |
| **总计** | **~20 分钟** |

## 不改项

以下有意**不改**：

1. **`handler_test.go` 中 linkname 的本地函数名**：`codexNewSession`、`codexSessionOnInboundMessage` 等。这些是测试文件内部的本地别名，语义仍然有效（指代 codex 提供者），且它们不影响链接目标解析。如需一致性可后续 follow-up。
2. **历史计划文档中的叙述性文本**：如 "codex app server" 等非路径引用，保留原文不动。
