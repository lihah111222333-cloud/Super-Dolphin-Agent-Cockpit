# 验证：skill 1:1 对齐修复（exec/card）

已读取：

- `docs/plans/迁移/align-skill-exec.md`
- `docs/plans/迁移/align-skill-card.md`

本次仅验证当前代码是否已经补齐目标点，不重复评估全部 V2/V3 语义差异。

## 结论总览

| 检查项 | 结论 | 依据 | 说明 |
| --- | --- | --- | --- |
| exec 参数协议 V2 兼容层（`argv/env` `UnmarshalJSON`） | ✅ | `internal/module/skill/rpc_types.go:30-60` `internal/module/skill/rpc.go:51-53` `internal/module/skill/contract.go:13` | 已有兼容层：`execParamsWire` 同时接 `command/args/cwd` 与 legacy `argv/env`；`UnmarshalJSON` 会在 `command` 为空时消费 `argv`，并保留 `env` 到 `ExecCommand`。 |
| exec timeout `30s` + cwd fallback + env 白名单 | ✅ | `internal/module/skill/exec.go:56-60` `internal/module/skill/exec.go:107-120` `internal/module/skill/exec.go:144-185` `internal/platform/config/timeouts.go:17` `internal/platform/config/timeouts.go:29-30` `internal/module/skill/exec_test.go:22-37` `internal/module/skill/exec_test.go:39-84` | `execCommand` 已统一走 `WithRPCRequestTimeout`，常量是 `30s`；空 `cwd` 回退到 `projectRoot`；环境变量由基础键 + allowlist 前缀组成，overlay 仅放行 `PWD` 或白名单键。 |
| card 工厂 7/7 覆盖 | ✅ | `internal/module/skill/rpc.go:12-33` `internal/module/skill/rpc.go:42-50` `internal/module/skill/contract.go:6-12` `internal/module/skill/cards.go:18-24` `internal/module/skill/cards.go:26-32` `internal/module/skill/cards.go:34-49` `internal/module/skill/cards.go:51-68` `internal/module/skill/cards.go:70-83` `internal/module/skill/cards.go:85-91` `internal/module/skill/cards.go:93-111` | 当前 `NewSkillHandlers` 已注册 `list/get/create/update/delete/run/versions` 7 个 `command/card/*` handler；参数适配由 `cardByKeyHandler` / `cardCreateHandler` / `cardUpdateHandler` / `cardRunHandler` 承担，`list` 为零参直接 handler。按对外 handler 覆盖口径，7/7 已齐。 |
| `WriteSkillContent` 语义清晰 | ✅ | `internal/module/skill/rpc.go:77-80` `internal/module/skill/skills_fs.go:159-168` `internal/module/skill/skills_meta.go:259-270` `internal/module/skill/skills_fs_test.go:36-62` | 语义已经明确收敛为“通过 legacy `skills/config/write` 写命名 skill 的主 `SKILL.md` 内容”。RPC 注释、service 名称、底层落盘路径和测试都一致。 |
| match `AgentID` 回退 + `collectConfiguredAutoMatchedSkills` 首参消费 | ✅ | `internal/module/skill/rpc_skill_types.go:55-60` `internal/module/skill/skills_match.go:14-18` `internal/module/skill/skills_match.go:30-35` `internal/module/skill/skills_match.go:43-50` `internal/module/skill/skills_match.go:59-64` `internal/module/skill/skills_match_test.go:11-38` `internal/module/skill/skills_match_test.go:40-71` | `threadId` 为空时回退到 `agent_id`；`resolvedID` 经过 collector 继续传入 `collectConfiguredAutoMatchedSkills`，最终被 `readConfiguredSkillState` 消费。对应测试已锁住这两条链路。 |

## 备注

- `align-skill-exec.md` 里关于 “V3 已丢失 `env` / 只剩 `command+args+cwd`” 的结论已经过时；当前代码已补回 `env` 和 legacy `argv` 兼容层，见 `internal/module/skill/rpc_types.go:37-60` 与 `internal/module/skill/exec.go:42-60`。
- `align-skill-card.md` 里对 card 面的旧判断也需要按当前实现更新：V3 现在确实存在完整的 `command/card/*` 7 个对外 handler，见 `internal/module/skill/rpc.go:42-50`。
- 本文件没有把 “configured auto-match 是否已恢复 V2 explicit/force provider 语义” 作为通过条件；该问题仍可见于 `internal/module/skill/skills_match.go:68-72` 的 TODO。此次验证只覆盖你列出的 “回退 + 首参消费” 两点。
