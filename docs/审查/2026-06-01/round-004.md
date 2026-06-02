# Round 004 - 深入确认 blocker #4~#5 + major #6

## Findings 确认

### 4. [blocker] platform/eventsurface/bind.go:82-85 — 静默返回 nil

```go
func Bind(dispatcher *event.Dispatcher, logger *pkglogger.Logger, publish PublishFunc) []context.CancelFunc {
    if dispatcher == nil || publish == nil {
        return nil  // ← 整个事件绑定链路静默断裂
    }
```

**影响**：fx 装配期如果 dispatcher 未注入，Bind 返回 nil，所有 UI 实时推送（agent 状态、task 进度、cron 运行）静默失效。前端显示"无事件"但实际是管道断了。

**精修方案**：
```go
func Bind(dispatcher *event.Dispatcher, logger *pkglogger.Logger, publish PublishFunc) ([]context.CancelFunc, error) {
    if dispatcher == nil {
        return nil, errors.New("eventsurface: dispatcher required")
    }
    if publish == nil {
        return nil, errors.New("eventsurface: publish func required")
    }
```
签名变更，所有 caller 需适配。

---

### 5. [blocker] module/uistate/module.go:117 — RPC 错误丢弃

```go
batchConfigs, _ := bulkReader.ReadRuntimeConfigs(ctx, threadIDs)
return batchConfigs
```

**影响**：RPC 失败时 `batchConfigs == nil`，下游 `enrichFromDB` 走 fallback 路径（line 142-143）逐个读取。如果 fallback 也失败（line 143 同样 `cfg, _ =`），所有 thread 的 runtime config 为 nil，UI 显示空配置。

**精修方案**：
```go
batchConfigs, err := bulkReader.ReadRuntimeConfigs(ctx, threadIDs)
if err != nil {
    return nil, fmt.Errorf("uistate: batch config read: %w", err)
}
```
同时 line 143 的 fallback 也需改为 `if err != nil { return err }`。

---

### 6. [major] module/skill/mirror_manifest.go:410-412 — 吞 resolveOwnerIdentity 错误

```go
owner, err := resolveOwnerIdentity(superHome, defaultOwnerOSUID(), defaultAppProfile())
if err != nil {
    return nil  // ← personal mirrors 静默消失
}
```

**影响**：用户 home 目录权限异常 / OS UID 解析失败时，personal skill mirrors 全部消失。reconcile 报告中不会出现任何 conflict/error，用户以为"没有 personal skills"。

**精修方案**：
```go
func (s *service) defaultPersonalMirrorTargets() ([]SkillMirrorTarget, error) {
    ...
    if err != nil {
        return nil, fmt.Errorf("skill mirror: resolve owner identity: %w", err)
    }
```
签名变更，caller `reconcileTargets` 需处理 error。
