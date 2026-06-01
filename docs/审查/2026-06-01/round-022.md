# Round 022 - 第二梯队：skill service 核心兜底

## 来源

Round-002 扫雷 agent 报告：skill service 3 条 + skill approval/rpc 1 条。

## Findings

### 1. [major] skill/service.go:92 — hashResolutionEnvelope 丢弃 marshal error（已在 round-002 top-12 确认）

### 2. [moderate] skill/service.go:134 — NewApprovalCache error 吞掉，静默 nil fallback

**证据**：approval cache 构造失败时 `s.approval = nil`，后续 `LookupArtifactApproval` 返回 `(false, nil)`。
**影响**：审批缓存初始化失败 → 所有 artifact 被判"未批准" → 可能触发不必要的重新审批流程。
**精修**：构造失败 → service 构造失败（返回 error）。

### 3. [blocker] skill/scope_model.go:224 — unchecked type assertion in sort closure

**证据**：`files[i]["name"].(string)` 无 comma-ok，非 string 时 panic。
**影响**：skill 文件列表排序时如果 metadata 损坏直接 crash。
**精修**：comma-ok 检查，或在构建 files 时保证类型。

### 4. [moderate] skill/skills_match.go:92 — unchecked type assert on non-map config

**证据**：config 值非 map 时静默返回 nil。
**影响**：skill match 配置损坏时匹配逻辑静默失效。
**精修**：comma-ok + log.Warn。
