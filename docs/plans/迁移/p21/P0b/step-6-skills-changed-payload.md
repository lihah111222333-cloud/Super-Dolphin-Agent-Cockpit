# P0b Step 6：SkillsChanged 扩展 Scope/Cwd

## 目标

为 `internal/dto/ui/event.go:1` 现有 `SkillsChanged` 事件追加 `Scope` 与 `Cwd` 字段，让下游订阅者能区分 project / system scope 变更，并在 project scope 下定位到具体 cwd。向后兼容：旧字段全部保留，旧订阅者忽略新字段不受影响。

## 前置依赖

- 现有 `SkillsChanged` 定义（`internal/dto/ui/event.go` 中 `SkillsDir/Name/Action/Actions/Count` 五字段，**不含** Scope/Cwd）。
- Step 5 已交付：`Service.CreateSkill` / `WriteLocal` 在 approve 通过后被调用；本步只需在事件发布点填字段。

## 文件清单

### 修改

| 路径 | 说明 |
|---|---|
| `internal/dto/ui/event.go` | `SkillsChanged` struct 加 `Scope` / `Cwd` 字段（`omitempty`）。 |
| `internal/module/skill/service.go` | 所有发出 `SkillsChanged` 的位置（`WriteLocal` / `CreateSkill` / `ImportLocalDir` / `DeleteLocal` / `WriteSkillContent` / `WriteSummary` 等）按实际 scope / cwd 填充字段。 |

### 新建

无。本步仅修改。

## 契约

```go
// internal/dto/ui/event.go

// SkillsChanged reports local skill inventory mutations.
type SkillsChanged struct {
    shared.EventHeader
    SkillsDir string   `json:"skillsDir,omitempty"`
    Name      string   `json:"name,omitempty"`
    Action    string   `json:"action,omitempty"`
    Actions   []string `json:"actions,omitempty"`
    Count     int      `json:"count,omitempty"`
    Scope     string   `json:"scope,omitempty"` // NEW: "project" | "system"
    Cwd       string   `json:"cwd,omitempty"`   // NEW: project root（scope=project 时必填）
}
```

`Type()` 方法保持不变（`shared.EventTypeUISkillsChanged`）。

## 实施约束

- **向后兼容**：保留所有原字段；旧订阅者不读 `Scope` / `Cwd` 不影响行为（P0 §"关键实现约束"：`SkillsChanged` 事件当前不携带完整 scope / cwd 语义，扩展 payload 时保持兼容）。
- service 写入路径（`WriteLocal` / `CreateSkill` / 其他写盘点）发事件时**按实际 scope** 填充：
    - `WriteLocal(..., scope="project")` / `CreateSkill(...)` → `Scope="project"`、`Cwd=<入参 cwd>`。
    - `WriteLocal(..., scope="system")` / `WriteSkillContent` / `WriteSummary` → `Scope="system"`、`Cwd=""`。
- `scope="project"` 时 **必须**填 `Cwd`；如果调用方未提供 cwd，service 必须先返回 `ErrMissingCWD`（P0a 现有逻辑），不会走到事件发布。
- `scope="system"` 时 `Cwd` 可空；订阅者必须容忍空串。
- 不要为兼容性补全 `Scope=""` 默认值——`omitempty` 会让旧订阅者保持原行为，新订阅者按是否非空判断。
- 事件发布点的批量变更（`Actions` 多元素）也要附 `Scope` / `Cwd`：本期约定一次 `SkillsChanged` 内的 `Actions` 必须全部属于同一 `(Scope, Cwd)`，跨 scope 批量必须拆事件（防止下游订阅者无法归属）。

## 验收标准

### 必测项（建议测试名）

- `TestSkillsChanged_RoundTripWithScopeCwd`：`json.Marshal` + `json.Unmarshal` `SkillsChanged{Scope:"project", Cwd:"D:/x"}`，字段无丢失。
- `TestSkillsChanged_BackwardCompatibleEmptyScope`：旧 payload（不含 `scope` / `cwd`）反序列化后 `Scope=""` `Cwd=""`，其余字段正常。
- `TestService_EmitsScopedSkillsChanged_Project`：`WriteLocal(ctx_with_cwd, ..., "project")` 发出事件 `Scope="project"` 且 `Cwd` 等于入参 cwd。
- `TestService_EmitsScopedSkillsChanged_System`：`WriteLocal(ctx, ..., "system")` 发出事件 `Scope="system"` 且 `Cwd=""`。
- `TestService_CreateSkillEmitsProjectScope`：调 `CreateSkill` 后事件 `Scope="project"` + `Cwd` 非空。
- `TestService_BatchActionsSameScope`：`ImportLocalDir` 一次 import 多个 skill，发出的事件中 `Actions` 多元素但 `Scope` / `Cwd` 一致；如果跨 scope 必须拆成多事件。
- 集成验证：candidate approve 落盘后下游 UI 订阅者能读到 `Scope=project` + `Cwd=<project root>`。

### 命令

```bash
go test ./internal/dto/ui/...
go test ./internal/module/skill/ -run "SkillsChanged|EmitsScoped"
```

### 集成验证

- 启动整套 fx app；触发一次 `skills/candidate/approve`（沿用 Step 5 集成路径）；订阅 `EventTypeUISkillsChanged` 的 sink 收到事件含 `Scope="project"` + `Cwd` 非空。

## 已知风险 / 反模式

- **改字段名不兼容老 UI**：`Scope` / `Cwd` 是新增字段，必须 `omitempty`；不要顺手把旧 `SkillsDir` 改名或大小写调整。
- **System scope 也填 Cwd**：会让下游误把 system 变更归到某个项目；`scope="system"` 时 `Cwd` 必须空。
- **混合 scope 的批量事件**：`Actions` 多元素时若 scope 不同，订阅者无法判断；本期要求拆事件。
- **依赖 Scope 字段做权威鉴权**：事件是通知用途，鉴权仍由 service 层完成；订阅者不得仅凭 `Scope` 字段做策略决定。
- **删除写入路径上某个事件发布点而忘记同步 Scope**：在 service 中所有 `bus.Publish(SkillsChanged{...})` 调用必须 grep 一遍，不能漏点。