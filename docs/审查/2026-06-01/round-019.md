# Round 019 - 第二梯队：json.Marshal 错误丢弃全局扫描

## 来源

Round-002 扫雷 agent 报告：marshal err 丢弃 5 条。

## Findings

### 1. [blocker] module/skill/service.go:92 — sha256 hash 基于 nil marshal 输出（已在 round-002 top-12 确认）

### 2. [major] module/insight/flusher.go:182 — DB 行 skillsJSON 在 marshal 失败时为 nil

**证据**：持久化到数据库的 skills 字段在 marshal 失败时写入 NULL。
**影响**：insight 查询时该行的 skills 数据永久丢失。
**精修**：marshal 失败 → abort flush，返回 error。

### 3. [major] module/memory/index.go:344 — formatStringList 返回 "null" 字符串

**证据**：`json.Marshal(nil)` 返回 `"null"` 字节，被当字符串写入 index。
**影响**：memory index 搜索时 "null" 字符串污染结果。
**精修**：空 slice 时返回 `"[]"`，marshal 失败时返回 error。

### 4. [major] provider/codexapp/support.go:35 — mustJSON（已在 round-006 #12 确认）

### 5. [moderate] mcpserver/common/bootstrap/env.go:256 — bootstrap snapshot 静默零值

**证据**：bootstrap JSON 解析失败时返回零值 snapshot。
**影响**：MCP server 启动时拿到空 bootstrap，所有配置为默认值。
**精修**：返回 error，让 MCP server 启动失败。
