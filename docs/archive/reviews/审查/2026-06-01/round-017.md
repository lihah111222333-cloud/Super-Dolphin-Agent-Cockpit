# Round 017 - 第二梯队：uistate 模块兜底

## 来源

Round-002 扫雷 agent 报告：module/uistate 5 条。

## Findings

### 1. [blocker] uistate/module.go:117 — bulkReader.ReadRuntimeConfigs 错误丢弃（已在 round-004 #5 确认）

### 2. [major] uistate/module.go:143 — ReadRuntimeConfig 错误丢弃

**证据**：`cfg, _ = s.runtimeConfig.ReadRuntimeConfig(ctx, threadID)` fallback 路径同样吞错误。
**精修**：`if err != nil { return nil, err }`。

### 3. [major] uistate/config_rpc.go:190 — decodePreferenceValue 静默 type assert

**证据**：`decodePreferenceValue(raw).(string)` 失败时返回 ""。
**影响**：preference 值类型不匹配时静默降级为空字符串，UI 显示空配置。
**精修**：comma-ok + 返回 typed error。

### 4. [moderate] uistate/config_rpc.go:205 — cfg[key].(string) nil-tolerant

**证据**：runtime config map 中 value 非 string 时静默返回 ""。
**精修**：comma-ok + log.Warn。

### 5. [moderate] uistate/patch.go:335 — text, _ := value.(string) 静默

**证据**：patch field 非 string 时静默跳过。
**精修**：返回 validation error。
