# Round 015 - 第二梯队：contract 层弱契约

## 来源

Round-002 扫雷 agent 报告：contract/ 5 条。

## Findings

### 1. [major] contract/approval.go:35 — Approved *bool omitempty 允许 nil=undecided

**证据**：`Approved *bool` 用 `omitempty` 标签，JSON 中缺失该字段时 `Approved == nil`。
**影响**：调用方需要三态判断（approved/rejected/undecided），但 nil 和 "未设置" 语义混淆。如果调用方只做 `if *decision.Approved`，nil 时 panic。
**精修**：要么去掉 omitempty 强制字段存在，要么提供 `IsApproved() bool` helper 做 nil-safe 访问。

### 2. [major] contract/manifest.go:129 — json.Marshal error 触发 panic

**证据**：`panic(fmt.Sprintf("manifest workspace roots must encode as JSON: %v", err))`
**影响**：虽然 string slice 理论上不会 marshal 失败，但 panic 在 library 层违反 Fail-Fast 原则（应返回 error 而非 crash 进程）。
**精修**：改为 `return dto.MCPManifest{}, fmt.Errorf(...)`，签名加 error。

### 3. [moderate] contract/skill.go:122 — ctx.Value type assertion 静默返回零值

**证据**：`value, _ := ctx.Value(SkillCWDContextKey{}).(string)` 失败时返回 ""。
**影响**：这是 Go 标准 context 模式，但 `RequireSkillCWD` 已经做了 fail-fast 包装。单独使用 `SkillCWDFromContext` 的调用方可能拿到空字符串而不知道 context 未设置。
**精修**：文档注释明确"返回空字符串表示未设置，需要 fail-fast 的场景请用 RequireSkillCWD"。

### 4. [moderate] contract/rpc_handler.go:36 — 同模式 ctx.Value type assertion

**证据**：同 #3 模式。
**精修**：同 #3。

### 5. [moderate] contract/toolbridge.go:69 — 同模式 ctx.Value type assertion

**证据**：同 #3 模式。
**精修**：同 #3。
