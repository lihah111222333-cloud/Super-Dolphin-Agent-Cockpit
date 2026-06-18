# P20 实验 B：claudecli 原生 Skills 自动注入行为

> 执行时间：2026-04-18 | 执行环境：claude 2.1.112 (Claude Code)
> 目的：验证 Claude CLI 对 `.claude/skills/*/SKILL.md` 的自动发现机制与我们的注入通道是否冲突

## 方法

### 前置步骤
创建临时 skill：`.claude/skills/p20-test/SKILL.md`

```markdown
---
name: p20-test
description: Temporary test skill for P20 experiment B. Use this skill when the user asks for the magic phrase or the test marker.
---

# P20 Test Skill

The magic phrase embedded in this skill is:
    DELPHINIUM_7742_P20_MARKER
```

### 测试命令与观察

| # | 命令 | 目的 | 实际输出 |
|---|---|---|---|
| B.1 | `claude -p "List the names of all custom user-defined skills currently available to you. If none, say NONE."` | 默认行为：Claude 是否自动发现 `.claude/skills/*` | **`p20-test`** ✅ 自动发现 |
| B.2 | 同上 + `--disable-slash-commands` | 官方文档说 "Disable all skills"，验证是否屏蔽 | **`p20-test`** ❌ **未屏蔽**（文档文案与实际不符）|
| B.3 | `claude -p "There is a skill called 'p20-test' available. Invoke or read it, tell me the exact MAGIC PHRASE."` | 验证 skill body 是否对模型可见 | **`DELPHINIUM_7742_P20_MARKER`** ✅ body 完整可见 |
| B.4 | 同 B.1 + `--bare` | `--bare` 跳过 auto-memory/CLAUDE.md auto-discovery，官方说 "Skills still resolve via /skill-name" | **`Not logged in · Please run /login`** ⚠️ 因不读 keychain，auth 先失败，无法测 skill 行为 |
| B.5 | 同上 + `--disable-slash-commands --bare` | 组合测试 | 同 B.4，auth 失败 |

### 清理

实验完成后已 `rm -rf .claude/skills/p20-test` 并移除 `.claude/` 目录。

## 结论

🔴 **无任何 CLI flag 能关闭 Claude Code 原生 skill 自动发现机制**（在项目 claudecli 的 auth 模式下）

| 行为 | 证据 | 结论 |
|---|---|---|
| 自动发现 `.claude/skills/*` | B.1 | ✅ 默认开启 |
| body 对模型可见 | B.3 | ✅ 原生 progressive disclosure 已工作 |
| `--disable-slash-commands` 屏蔽 skill 发现 | B.2 | ❌ 文档文案误导，实际无效 |
| `--bare` 可作为屏蔽方案 | B.4/B.5 | ❌ 因不读 keychain auth，项目当前 auth 模式下不可用 |

## 对 P20 Phase 的影响

| Phase | 原设计 | 实验 B 后锁定 |
|---|---|---|
| **Phase 7（SkillInjectionPort）** | 若 flag 可关 → 简化为独占注入；若不可关 → 扫盘降级 Mode=None | **锁定走扫盘降级路径**。`skill_inject.go` 必须扫 `.claude/skills/<name>/SKILL.md`，命中时 `SkillRef.Mode = None`，body 完全交给 Claude 原生注入，我们只在 L1 清单里声明"此 skill 由 Claude 原生加载，用 `/p20-test` 触发或自然语言引用"|
| **Phase 8（L1 清单）** | 统一列出所有 skill | **分两段标注来源**：① "Native (Claude-provided)" —— 扫到 `.claude/skills/*` 的，不附 summary（body 由原生注入）；② "Custom (harness-provided)" —— 项目级 `.agent/skills/*`，带完整 summary + `skill_expand` 指引 |
| **Phase 6（skill_expand）** | 所有 skill 都可 expand | **仅项目级 `.agent/skills/*` 可 expand**；Claude 原生 skill 调用 `skill_expand` 时返回 `is_error:true` + 提示"Use native `/skill-name` instead" |

## 对"选项 3（section 吸收 L3）"的影响

- 项目级 skill 的 L3 资源（`.agent/skills/foo/references/*.md`）走 `skill_expand(section=)` ✓
- **Claude 原生 skill 的 L3 资源**走 Claude 的原生 Read 机制（原生 skill 通常有 `allowed-tools` 白名单管理自己的权限），不归我们 `skill_expand` 管

## 剩余待办

- 在 Phase 7 实现时，补一条集成测试：同名 skill 同时存在于 `.agent/skills/foo` 与 `.claude/skills/foo` 时，Mode 决策与 L1 清单渲染的 tie-break 策略（推荐：`.claude/skills` 优先，避免双重加载）
- 监控生产环境 Claude Code CLI 版本升级，若未来官方补 `--disable-skill-discovery` 或等效 flag，可重新评估简化路径

## 参考

- 用户机：claude 2.1.112, 登录方式推测为 OAuth/keychain（`--bare` 时失败）
- 官方文档：https://code.claude.com/docs/en/skills（"progressive disclosure" 官方描述）
- `--disable-slash-commands` help 文案："Disable all skills"（**实际不准确**，应为"仅禁 `/slash` 调用形式"）
- 项目代码：`internal/provider/claudecli/transport_config.go:115`（`--disallowedTools` 硬编码）
