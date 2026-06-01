# Round 005 - 深入确认 major #7~#9

## Findings 确认

### 7. [major] module/skill/skills_fs.go:300, :422 — requireCWD 错误丢弃

**Line 300**（WriteLocal 成功路径末尾）：
```go
result := map[string]any{"ok": true, "path": path, ...}
cwd, _ := requireCWD(ctx)  // ← ErrSkillMissingCWD 被丢弃
return attachMirrorPublish(result, s.publishWriteTimeMirrors(ctx, cwd, scope, personalType, name)), nil
```

**Line 422**（deletePersonalLocal 成功路径末尾）：
```go
cwd, _ := requireCWD(ctx)  // ← 同上
return attachMirrorPublish(result, s.publishWriteTimeMirrors(ctx, cwd, scope, personalType, name)), nil
```

**影响**：CWD 缺失时 `cwd == ""`，`publishWriteTimeMirrors` 拿到空 cwd 可能写入错误的 mirror 路径或静默跳过 publish。写入/删除操作本身已成功，但 mirror 同步静默失败，provider-native 侧看不到最新 skill。

**精修方案**：
```go
cwd, err := requireCWD(ctx)
if err != nil {
    // 写入已成功，mirror publish 是 best-effort 但必须记录
    s.logger.Error("skill mirror publish skipped: missing CWD", "error", err)
}
```
或者更严格：在函数入口就 requireCWD，失败则整个操作 fail-fast（但这会改变已有行为，需评估）。

---

### 8. [major] module/turn/skills.go:323 — applyHydration 丢弃 conflict error

```go
func (s *service) applyHydration(ctx context.Context, ref dto.SkillRef, index map[string]contract.SkillInfo, policy skillHydrationPolicy) dto.SkillRef {
    hydrated, _ := s.applyHydrationWithConflict(ctx, ref, index, policy)
    return hydrated
}
```

**影响**：`ErrSkillSameNameConflict` 被丢弃。此函数目前无生产调用方（`hydrateSkillRefsFromIndex` 直接调 `applyHydrationWithConflict`），但作为 exported-equivalent 的 helper 留存，未来调用方会踩坑。

**精修方案**：删除 `applyHydration` wrapper（dead code），只保留 `applyHydrationWithConflict`。

---

### 9. [major] app/thread_orchestration_adapter.go:23-27 — 静默 noop facade

```go
func newThreadOrchestrationFacade(p threadOrchestrationParams) thread.OrchestrationFacade {
    if p.Service == nil {
        return noopThreadOrchestrationFacade{}  // ← 所有 LaunchAgent/StopAgent 静默空操作
    }
    return threadOrchestrationAdapter{svc: p.Service}
}
```

**影响**：fx 装配期 OrchestrationService 未注入时，thread 模块拿到 noop facade。用户发起 LaunchAgent 请求 → 返回 nil error → 前端显示"启动成功"→ 实际无 agent 被创建。

**精修方案**：
- 方案 A（推荐）：fx 装配期 `fx.Validate` 强制 OrchestrationService 非空。
- 方案 B：noop facade 的方法返回 `errors.New("orchestration: service not configured")`。
