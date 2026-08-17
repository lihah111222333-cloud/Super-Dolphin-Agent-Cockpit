# Round 001 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 04:09:54 KST
- 结束：2026-05-17 04:43:15 KST
- 持续时间：33 分 21 秒

## 本轮范围

本轮按启动阶段的范围判定，继续把“量化引擎”落到 FBSD 频次量化/分层链路：统计打点、workspace/global 评分合并、Hot/Warm/Cold/Frozen 分层、Codex manifest 渲染和 `skill_read_section` 读取工具。

- `README.md:96-107`
- `.agent/skills/安全工程师/SKILL.md`
- `.agent/skills/Agent工程学/SKILL.md`
- `docs/superpowers/specs/2026-04-29-skill-refactor-design.md:369-385`
- `docs/superpowers/specs/2026-04-29-skill-refactor-design.md:475-505`
- `internal/module/fbsd/score.go`
- `internal/module/fbsd/merge.go`
- `internal/module/fbsd/tier.go`
- `internal/module/fbsd/tracker.go`
- `internal/module/fbsd/store.go`
- `internal/module/fbsd/module.go`
- `internal/module/fbsd/manifest_renderer.go`
- `internal/module/fbsd/*_test.go`
- `internal/module/skillforge/render.go`
- `internal/platform/runner/contract.go`
- `internal/platform/toolbridge/skill_read_section.go`
- `internal/platform/toolbridge/host_tools.go`
- `internal/platform/toolbridge/handler_host_tools.go`
- `internal/platform/toolbridge/*skill_read_section*_test.go`
- `internal/provider/claudecli/module.go`
- `internal/provider/claudecli/fbsd_hook.go`
- `internal/provider/codexapp/driver.go`

## Findings

1. **[major] Tracker 启动失败被吞掉，FBSD 看似启用但统计链路停摆**
   - 证据：`internal/module/fbsd/tracker.go:73-89` 在 `Start()` 中加载 workspace/global stats，遇到 malformed JSON 会返回错误；`internal/module/fbsd/store.go:23-26` 与 `internal/module/fbsd/store_test.go:20-28` 明确锁定 malformed stats 返回错误。生命周期接线在 `internal/module/fbsd/module.go:56` 执行 `_ = w.t.Start()`，把错误丢弃；外层 runner 的 `Worker.Start()` 也没有错误返回通道（`internal/platform/runner/contract.go:13-16`、`internal/platform/runner/contract.go:47-57`）。失败后 `Record()` 只检查 `enabled` 和 name（`internal/module/fbsd/tracker.go:97-107`），而 `Flush()` 对 `!started` 直接 no-op（`internal/module/fbsd/tracker.go:110-115`）。
   - 风险：一个损坏的 `skills-stats.json` 可让 FBSD 在启动阶段静默失效。后续 skill 读取仍会进入 `Record()` 并写入 channel，但没有 worker 消费、没有最终落盘，manifest tier 会长期使用空统计或旧统计。由于 README 说明 FBSD 已 always-on（`README.md:96-107`），运维和用户很难意识到量化引擎已失真。
   - 建议：让 runner 适配层可传播启动错误，或至少在 `trackerWorker.Start()` 记录结构化错误并把 tracker 标记为不可记录；`Record()` 应检查 `started` 或暴露健康状态；损坏 stats 可考虑 quarantine 后以空 stats 恢复，并写明确告警。

2. **[major] workspace stats 只按 hostname 隔离，会跨项目混合量化信号**
   - 证据：`internal/module/fbsd/module.go:28-45` 将全局 stats 放在 `~/.super-dolphin/skills-stats.json`，workspace stats 放在 `~/.super-dolphin/workspaces/<host>/skills-stats.json`；同段注释已承认 “multi-user / multi-project 同主机会混淆”。评分合并在 workspace 调用数达到阈值后直接用 workspace-only（`internal/module/fbsd/merge.go:11-24`），测试也锁定 `ws >= minCalls` 时忽略 global（`internal/module/fbsd/merge_test.go:21-35`）。
   - 风险：同一台机器上的不同仓库会共享一个“workspace”统计桶。项目 A 的 skill 使用频率会改变项目 B 的 Hot/Warm/Cold/Frozen 分层，导致 Codex system prompt 中暴露的 skill 顺序和细节不再反映当前项目。对安全审查任务来说，这既是推荐失准，也可能造成工作流偏好泄露。
   - 建议：workspace ID 至少纳入规范化 cwd hash；迁移时保留旧 hostname stats 作为 global/legacy fallback，并在 disclosure snapshot 中暴露 workspace ID 版本，方便定位混桶问题。

3. **[major] FBSD manifest 的预算只用估算扣减，渲染阶段没有真实长度兜底**
   - 证据：`internal/module/fbsd/tier.go:96-119` 只按 `HotChars/WarmChars/ColdChars` 估算扣减 budget；`internal/module/fbsd/manifest_renderer.go:111-130` 按分层直接写入 rendered block，没有检查 `b.Len()+len(block)` 是否超过 `cfg.Budget`，也没有截断 footer。非 FBSD 路径反而有真实长度检查和截断提示（`internal/module/fbsd/manifest_renderer.go:48-72`）。设计稿要求 FBSD 后仍有 “Budget 兜底降级”（`docs/superpowers/specs/2026-04-29-skill-refactor-design.md:369-385`），并把 Hot/Warm/Cold 长度定义为近似模板长度（`docs/superpowers/specs/2026-04-29-skill-refactor-design.md:475-484`）。同时 description 来自 `SKILL.md` frontmatter，`internal/module/skillforge/render.go:30-33` 原样写入 description，只有 section summary 有 80 rune 提取上限（`internal/module/skillforge/render.go:46-50`）。
   - 风险：一个 description 或 section summary 异常长的 Hot skill，可以让 FBSD manifest 超过 `SKILL_FBSD_BUDGET`。这会直接增加 Codex 启动 prompt 体积，削弱量化分层对上下文成本和可控性的约束。
   - 建议：FBSD 渲染阶段复用非 FBSD 路径的真实长度预算检查；当 Hot block 超预算时尝试 Warm/Cold 降级，仍超预算则 Frozen/省略并写 footer。另应限制 description 的最大展示长度，避免单个 skill 打穿 budget。

4. **[moderate] `skill_read_section` schema 声称服务端有 ceiling，但实现默认无限返回**
   - 证据：`internal/platform/toolbridge/host_tools.go:73-76` 的 schema 描述 `max_bytes` 为可选，并写明 “Server enforces its own ceiling”；实际 `readSection()` 只有在 `a.MaxBytes > 0` 时才截断（`internal/platform/toolbridge/skill_read_section.go:64-80`）。测试明确锁定省略 `max_bytes` 时不截断（`internal/platform/toolbridge/skill_read_section_test.go:104-118`）。host-direct 返回会把 body 包成 JSON 后送入 `ToolCallResult.ContentItems[0].Text`（`internal/platform/toolbridge/handler_host_tools.go:202-214`）。
   - 风险：模型或调用方省略 `max_bytes` 时，可以把超大 references section 原样带回模型上下文。虽然后续 turn output 存储链路有自己的累计上限，但这不等同于 host tool 对模型响应的硬上限，和 schema 对外承诺不一致。
   - 建议：引入服务端默认 hard cap，例如沿用 legacy skill expand 的默认/最大上限语义；保留 `total_bytes/truncated` 元数据，并将 schema 描述改为与实现一致。新增测试覆盖省略 `max_bytes` 时触发默认截断。

## 已排除或暂不确认

- Claude 侧 FBSD 打点不是缺失项：`internal/provider/claudecli/module.go:30-36` 注入 recorder，`internal/provider/claudecli/fbsd_hook.go:31-43` 只对命中的 `Read(.claude/skills/<name>/references/<NN-anchor>.md)` 打点。
- 核心 `Score()` 指数衰减公式本轮未发现偏差：实现位于 `internal/module/fbsd/score.go:24-41`，已有 `score_test.go` 覆盖空 stats、半衰期、frozen window 等路径。
- `EffectiveScore()` 在 “仅 workspace 有数据但低于阈值” 时返回 `weight*ws` 是源码注释明确的设计取舍（`internal/module/fbsd/merge.go:26-32`），本轮不作为 bug。
- `skill_read_section` 对 missing anchor 不打点已有测试覆盖（`internal/platform/toolbridge/skill_read_section_test.go:231-253`）。

## 验证

已在本轮运行并通过：

```bash
./scripts/test_with_guard.sh ./internal/module/fbsd ./internal/platform/toolbridge ./internal/provider/claudecli -count=1
```

结果：guard 通过；`internal/archtest`、`internal/module/fbsd`、`internal/platform/toolbridge`、`internal/provider/claudecli` 均通过。

## 下一轮建议

- Round 002 优先审查 FBSD stats 文件写盘失败路径：`persistAll()` 目前吞掉 `SaveStats` 错误，需确认磁盘只读、权限错误、rename 失败时的可观测性和数据丢失窗口。
- 继续审查 `skill_read_section` 的路径解析、anchor 匹配和大文件读取策略，确认是否存在越权读取或资源放大风险。
- 扩展到 `internal/module/memory/retrieval/` 的相关性评分和 budget 渲染，判断是否属于同一类“量化引擎”风险。
