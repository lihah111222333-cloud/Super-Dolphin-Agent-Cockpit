# Round 002 - 30-agent 并行扫雷（兜底/静默/弱契约/Fail-Fast）

## 时间

- 开始：2026-06-01 KST
- 结束：2026-06-01 KST

## 审查方法

30 个 Explore agent 并行扫描 internal/ 全子系统（排除 _test.go 和 .claude/worktrees），每 agent 返回 ≤5 条 finding。汇总后裁决 top-12 精修项。

## 扫描覆盖

| 子系统 | 结果 |
|--------|------|
| module/memory | 5 findings |
| module/insight | clean |
| app/ (fx 装配) | 5 findings |
| module/skill/mirror_* | 5 findings |
| module/cron + notify | clean |
| module/prompt providers | clean |
| module/prompt assembler | 5 findings |
| 全局 unchecked type assert | 2 findings |
| module/turn assembly | (no output) |
| json.Marshal err 丢弃 | 5 findings |
| platform/db + store | 3 findings |
| module/skill service | 3 findings |
| dto/provider | 5 findings |
| provider/claudecli | 5 findings |
| orchestration | clean |
| module/turn lifecycle | 3 findings |
| module/skill approval/rpc | 1 finding |
| provider/codexapp | 5 findings |
| contract/ | 5 findings |
| module/skill fs/import | 5 findings |
| module/dashboard 余下 | 5 findings |
| module/prompt cache | 3 findings |
| module/prompt intent | 5 findings |
| platform/bus+eventsurface | 5 findings |
| module/uistate | 5 findings |
| platform/rpc | clean |
| platform/toolbridge | clean |
| module/turn 核心 | 5 findings |
| module/threadprompt | 1 finding |
| module/feedback | 3 findings |

**总计：~99 条 raw findings，去重后约 70 条独立违例。**

## Top-12 精修裁决（按严重度排序）

### 1. [blocker] platform/db — rows.Scan 错误静默吞掉
- `internal/platform/db/module.go:305`
- 迁移检查循环中 Scan 失败被丢弃，后续逻辑基于脏数据做迁移决策。
- 精修：Scan err → return err，中断迁移。

### 2. [blocker] module/skill — skills_meta.go 越界索引
- `internal/module/skill/skills_meta.go:169`
- `applyMetaLine` 对 `tail[i+1:]` 无 bounds check，畸形 frontmatter 触发 panic。
- 精修：加 `if i+1 > len(tail)` 守卫。

### 3. [blocker] module/prompt — truncateAtRuneBoundary 无 bounds check
- `internal/module/prompt/user_context_builder.go:258`
- `content[cut]` 在 `limit >= len(content)` 时 panic。
- 精修：函数入口 `if limit >= len(content) { return content }`。

### 4. [blocker] platform/eventsurface — Bind 静默返回 nil
- `internal/platform/eventsurface/bind.go:83`
- dispatcher/publish 为 nil 时返回 nil 而非 error，事件链路静默断裂。
- 精修：返回 `fmt.Errorf("eventsurface: dispatcher required")`。

### 5. [blocker] module/uistate — bulkReader.ReadRuntimeConfigs 错误丢弃
- `internal/module/uistate/module.go:117`
- `batchConfigs, _ := bulkReader.ReadRuntimeConfigs(...)` 丢弃 RPC 错误。
- 精修：`if err != nil { return err }`。

### 6. [major] module/skill — mirror_manifest 吞 resolveOwnerIdentity 错误
- `internal/module/skill/mirror_manifest.go:412`
- personal mirrors 静默消失，用户无感知。
- 精修：error 上抛，让 reconcile 报告 conflict。

### 7. [major] module/skill — skills_fs.go requireCWD 错误丢弃 ×2
- `internal/module/skill/skills_fs.go:300, :422`
- `_, _ = requireCWD(ctx)` 丢弃 ErrSkillMissingCWD。
- 精修：`cwd, err := requireCWD(ctx); if err != nil { return err }`。

### 8. [major] module/turn — applyHydration 丢弃 conflict error
- `internal/module/turn/skills.go:323`
- `hydrated, _ := s.applyHydrationWithConflict(...)` 吞 ErrSkillSameNameConflict。
- 精修：删除 `applyHydration` wrapper，直接用 `applyHydrationWithConflict`。

### 9. [major] app/ — thread_orchestration_adapter 静默 noop
- `internal/app/thread_orchestration_adapter.go:23`
- OrchestrationService nil 时所有方法静默返回空值。
- 精修：fx 装配期强制非空，或 adapter 方法返回 error。

### 10. [major] platform/bus — NewLogSink 静默空 sink
- `internal/platform/bus/sink.go:23`
- dispatcher/logger nil → 返回空 sink，事件静默丢失。
- 精修：构造期 panic 或返回 error。

### 11. [major] module/threadprompt — storeListKeyword 逻辑反转
- `internal/module/threadprompt/runtime_catalog.go:183`
- 非空 keyword 被丢弃（`!= "" → return ""`），过滤永远不生效。
- 精修：反转条件 `if keyword == "" { return "" }`。

### 12. [major] provider/codexapp — mustJSON 吞 marshal error
- `internal/provider/codexapp/support.go:35`
- RPC params 构建用 `mustJSON`，marshal 失败返回 nil，下游发空 payload。
- 精修：改为 `([]byte, error)` 签名，caller 上抛。

## 下一步

- Round 003-035：每轮取 2-3 个 finding，深入确认证据（读具体行）、写精修方案、标记是否需要签名变更。
- 精修阶段（用户授权后）：12 agent 并行修复 → 互审 → 集成分支终审。

## 验证命令（待精修后执行）

```bash
./scripts/test_with_guard.sh ./internal/... -count=1
make guard
```
