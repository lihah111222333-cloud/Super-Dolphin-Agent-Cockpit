# Round 002 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 04:53:52 KST
- 结束：2026-05-17 05:18:05 KST
- 持续时间：24 分 13 秒
- 说明：本轮原按 30 分钟计时执行；用户在 2026-05-17 05:18:05 KST 更新指令为“不需要等满，直接写入后启动下一轮”，因此从本轮起按最新指令执行。

## 本轮范围

本轮继续审查 FBSD 统计持久化、`skill_read_section` host-direct 读取路径和旧 skill expand 安全边界的差异。

- `internal/module/fbsd/tracker.go`
- `internal/module/fbsd/store.go`
- `internal/module/fbsd/types.go`
- `internal/module/fbsd/score.go`
- `internal/module/fbsd/tracker_test.go`
- `internal/module/skilllibrary/section.go`
- `internal/module/skilllibrary/store.go`
- `internal/module/skilllibrary/reconcile.go`
- `internal/module/skilllibrary/section_test.go`
- `internal/module/skillforge/atomic.go`
- `internal/module/skillforge/forge.go`
- `internal/module/skill/trust.go`
- `internal/module/skill/skills_expand.go`
- `internal/module/skill/skills_fs.go`
- `internal/platform/toolbridge/skill_read_section.go`
- `internal/platform/toolbridge/host_tools.go`
- `internal/platform/toolbridge/handler_host_tools.go`

## Findings

1. **[major] FBSD 写盘失败被吞掉，统计文件可长期停在旧状态**
   - 证据：`internal/module/fbsd/tracker.go:141-159` 的 worker 在 dirty 后调用 `persistAll()`，`internal/module/fbsd/tracker.go:164-175` 在 stop drain 后也调用 `persistAll()`；但 `persistAll()` 对 workspace/global 两次 `SaveStats` 都用 `_ =` 丢弃错误（`internal/module/fbsd/tracker.go:196-204`）。`SaveStats()` 本身会对空 path、mkdir、write tmp、rename 返回明确错误（`internal/module/fbsd/store.go:35-58`），但这些错误不会传到 `Flush()`，也不会被记录。
   - 风险：磁盘只读、权限变化、目录被替换、rename 失败等情况下，内存中的量化结果继续变化，但文件持久层停留在旧状态。下一次进程启动后会加载旧 stats，Hot/Warm/Cold/Frozen 分层回退到过期数据，且没有日志或错误让调用方知道本轮统计丢失。
   - 建议：让 `persistAll()` 返回合并错误，tick 路径至少记录错误并保持 dirty；stop 路径应让 `Flush()` 返回错误。对 workspace/global 分别记录成功与失败，避免一个文件失败掩盖另一个文件成功。

2. **[major] `skill_read_section` 直接用模型传入的 name 拼路径，缺少 skill 名白名单校验**
   - 证据：host tool schema 只声明 `name` 是 string（`internal/platform/toolbridge/host_tools.go:60-82`），`CallHostTool()` 解码后直接调用 `r.tool.readSection(args)`（`internal/platform/toolbridge/host_tools.go:120-146`）。`readSection()` 又把 `a.Name` 原样传给 reader 并在成功后用同一个 name 打点（`internal/platform/toolbridge/skill_read_section.go:64-80`）。生产 reader `ReadSection()` 只检查空值，然后用 `filepath.Join(cacheDir, name, "references")` 读目录（`internal/module/skilllibrary/section.go:50-68`），没有拒绝 `/`、`\`、`..`、绝对路径、控制字符或过长 name。相邻的旧 `Service.Expand` 路径已有 `validateSkillName()`，明确拒绝 `/`、`\`、`..` 和危险字符（`internal/module/skill/trust.go:19-49`）。
   - 风险：`skill_read_section` 是模型可见 host-direct 工具。只要 cacheDir 附近存在形如 `<外部路径>/references/<NN-anchor>.md` 的目录结构，恶意或失控模型参数就可能尝试越过技能 cache 边界读取非目标目录中的 markdown；即使未成功读取，也会把未经校验的 name 写入 FBSD stats，污染量化数据。
   - 建议：在 `decodeSkillReadSectionArgs()` 或 `readSection()` 入口复用/迁移 `validateSkillName()` 的白名单；`ReadSection()` 也应在模块边界做 defense-in-depth，清理后确认最终 refDir 仍在 cacheDir 内。

3. **[moderate] FBSD Calls 历史永久追加，没有过期裁剪或聚合压缩**
   - 证据：`SkillStats.Calls` 注释为“所有调用时间戳”（`internal/module/fbsd/types.go:31-40`）；`Score()` 只在计算时跳过 frozen window 之前的调用（`internal/module/fbsd/score.go:24-42`）；`applyEvent()` 每次对 workspace/global 都 `append` 新时间戳（`internal/module/fbsd/tracker.go:180-193`）；`persistAll()` 克隆并保存完整 stats（`internal/module/fbsd/tracker.go:196-204`）。本轮未发现 prune、compact 或 max history 逻辑。
   - 风险：长期运行或高频 skill 调用会让 `skills-stats.json` 无界增长。启动 `LoadStats()`、每轮 `Snapshot()`、每次 `Score()` 都会处理越来越多的历史调用，即使很多调用已在 frozen window 外不再影响分数。这会把“量化引擎”的成本从近似当前窗口变成历史总量。
   - 建议：在 apply 或 persist 前按 `FrozenDuration` 裁剪 Calls，或把过期历史压缩为分桶计数；同时给单 skill 和单文件设置保守上限，超过上限时保留最近窗口并记录一次可观测事件。

## 已排除或暂不确认

- `skill_read_section` 省略 `max_bytes` 默认无限返回已在 Round 001 记录，本轮只引用旧 `skill_expand` 的默认/hard cap 作为对照，不重复作为新 finding。
- 旧 `skill_expand` 的资源路径有 symlink 解析和 `ContainsPath` 防逃逸（`internal/module/skill/skills_expand.go:116-134`、`internal/module/skill/skills_fs.go:161-189`），本轮未发现同类逃逸问题。
- `ReadSection()` unknown anchor 会返回可用 anchor 列表，测试覆盖在 `internal/module/skilllibrary/section_test.go:80-101`；这本身不是问题。

## 验证

已在本轮运行并通过：

```bash
./scripts/test_with_guard.sh ./internal/module/fbsd ./internal/module/skilllibrary ./internal/platform/toolbridge -count=1
```

结果：guard 通过；`internal/archtest`、`internal/module/fbsd`、`internal/module/skilllibrary`、`internal/platform/toolbridge` 均通过。

## 下一轮建议

- Round 003 审查 memory retrieval 的评分、截断和排序路径：`internal/module/memory/retrieval/`。
- 对比 memory 相关性分数和 FBSD 分数在“旧历史无界增长、预算截断、排序稳定性”上的共同风险。
- 检查 `internal/module/thread/router_resolve.go` 是否也存在类似的路由/评分输入未裁剪问题。
