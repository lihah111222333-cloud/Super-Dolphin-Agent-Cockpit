# Round 018 - 第二梯队：db/store 层兜底

## 来源

Round-002 扫雷 agent 报告：platform/db + store 3 条。

## Findings

### 1. [blocker] platform/db/module.go:305 — rows.Scan 错误静默跳过（已在 round-003 #1 确认）

### 2. [moderate] store/prompt/runtime_assets.go:23 — json.Unmarshal 错误吞掉返回 nil slice

**证据**：prompt assets 的 JSON 解析失败时返回空切片。
**影响**：损坏的 prompt asset 数据被当"无 assets"处理，UI 显示空列表。
**精修**：返回 error，让 RPC 层返回 5xx。

### 3. [moderate] store/commandcard/store.go:61 — timePtr type switch 静默 nil

**证据**：`any` 类型的时间值不匹配已知类型时返回 nil。
**影响**：command card 的时间字段丢失，排序/过滤逻辑异常。
**精修**：default case 返回 error 或 log.Warn。
