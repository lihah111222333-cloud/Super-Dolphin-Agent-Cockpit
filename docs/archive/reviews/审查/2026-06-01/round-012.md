# Round 012 - 第二梯队：codexapp provider 兜底

## 来源

Round-002 扫雷 agent 报告：provider/codexapp 5 条。

## Findings

### 1. [major] codexapp/support.go:35 — mustJSON 吞 marshal error（已在 round-006 #12 确认）

### 2. [moderate] codexapp/session_approval.go:387 — payload nil 时静默空 map

**证据**：approval payload 为 nil 时返回空 map，跳过 validation。
**影响**：空 approval payload 被当"无参数审批"处理，可能放行不该放行的操作。
**精修**：`if payload == nil { return nil, errors.New("codexapp: approval payload required") }`。

### 3. [moderate] codexapp/session_rollout_events.go:239 — unchecked type assert on content array

**证据**：RPC content 数组元素做 `item.(map[string]any)` 无 comma-ok。
**影响**：非 map 元素触发 panic。
**精修**：comma-ok + skip with log。

### 4. [moderate] codexapp/driver_pool_routing.go:553 — unchecked type assert on sandbox config

**证据**：sandbox config value 做 type assert 无 comma-ok。
**精修**：comma-ok 检查。

### 5. [moderate] codexapp/event_map.go:385 — unchecked type assert on nested payload map

**证据**：嵌套 payload 做 `v.(map[string]any)` 无 comma-ok。
**精修**：comma-ok 检查。
