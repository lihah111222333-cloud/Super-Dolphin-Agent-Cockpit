# Round 021 - 第二梯队：dashboard 余下兜底

## 来源

Round-002 扫雷 agent 报告：dashboard 余下 5 条。

## Findings

### 1. [major] dashboard/dag_snapshot.go:448 — dashboardOptionalTime 吞 parse error

**证据**：时间字符串解析失败时返回 nil，不报错。
**影响**：DAG run 的时间字段丢失，排序/展示异常。
**精修**：返回 `(time.Time, error)`，caller 决定是否降级。

### 2. [major] dashboard/factory.go:242 — readStoredLogFields default 返回零 struct

**证据**：未知 log field type 时返回零值 struct。
**影响**：日志字段解析失败时该条日志的 metadata 全部丢失。
**精修**：default case 返回 error。

### 3. [moderate] dashboard/ui_page.go:194 — json.Unmarshal error 返回 false

**证据**：判断 prompt 是否 system-managed 时，tags unmarshal 失败返回 false。
**影响**：损坏的 tags → 不被识别为 system → 系统模板泄露给 UI（已在 round-001 #5 确认）。

### 4. [moderate] dashboard/factory.go:63 — safeList 返回 items alongside swallowed error

**证据**：`safeList` 在 query 返回 error 时仍返回 `items`（可能为 nil → 被替换为 `[]T{}`）。
**影响**：error 被上层 `populateXxx` 处理，但 items 可能是 partial 数据。
**精修**：error 时 items 应为 nil，不做 `[]T{}` 替换。

### 5. [moderate] dashboard/ui_page.go:204 — ErrSkillSameNameConflict 吞掉（已在 round-001 #6 确认）
