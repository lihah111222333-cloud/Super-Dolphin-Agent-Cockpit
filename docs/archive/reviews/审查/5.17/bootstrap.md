# Bootstrap - 量化引擎风险审查范围识别

## 时间

- 时间：2026-05-17 04:06:33 KST
- 说明：这是启动审查和范围识别，不计入正式轮次；用户最新目标已调整为 300 轮。

## 本次范围

- `README.md`
- `docs/doc/codemap/README.md`
- `docs/doc/codemap/02-mcp-orch.md`
- `.agent/skills/安全工程师/SKILL.md`
- `.agent/skills/Agent工程学/SKILL.md`
- `internal/module/fbsd/score.go`
- `internal/module/fbsd/merge.go`
- `internal/module/fbsd/tier.go`
- `internal/module/fbsd/tracker.go`
- `internal/module/fbsd/store.go`
- `internal/module/fbsd/module.go`
- `internal/module/fbsd/manifest_renderer.go`
- `internal/platform/toolbridge/skill_read_section.go`
- `internal/module/fbsd/*_test.go`

## 范围判定

当前仓库未发现字面命名的“量化引擎”。按源码证据，正式审查先从 FBSD 频次量化/分层引擎开始：

- 频次统计：`internal/module/fbsd/tracker.go`
- 指数衰减评分：`internal/module/fbsd/score.go`
- workspace/global 合并：`internal/module/fbsd/merge.go`
- Hot/Warm/Cold/Frozen 分层：`internal/module/fbsd/tier.go`
- manifest 渲染：`internal/module/fbsd/manifest_renderer.go`
- 调用打点接入：`internal/platform/toolbridge/skill_read_section.go`

后续正式轮次再扩展到 memory retrieval、thread router、DAG/cron 调度排序等评分/排名链路。

## 风险种子

这些是启动审查识别出的风险种子，正式轮次需要重新验证、补充测试证据或排除误报后再作为正式 finding 记录。

1. `internal/module/fbsd/module.go:28-45`：workspace stats 当前按 hostname 隔离，可能跨项目混淆。
2. `internal/module/fbsd/tracker.go:196-203`、`internal/module/fbsd/module.go:56`：stats 持久化和 Start 错误被静默忽略。
3. `internal/module/fbsd/tracker.go:25-27`、`internal/module/fbsd/tracker.go:97-107`：Flush 后 Record 仍可写入但无人消费。
4. `internal/module/fbsd/tier.go:96-119`、`internal/module/fbsd/manifest_renderer.go:111-130`：FBSD 渲染阶段缺少真实长度预算兜底。

## 下一步

正式 Round 001 应至少持续 30 分钟，重新审查上述风险种子，确认 severity、复现路径、已有测试覆盖和推荐修复方案。
