# Skill 子系统重构 — Appendix：F1 Native CLI 过滤实测结果

> 状态：**已完成实测**（Claude 侧 fallback 主路径已确定；Codex 侧细节推到 P5 实施期补）
> 主 spec：[`2026-04-29-skill-refactor-design.md`](./2026-04-29-skill-refactor-design.md) §8.3
> 实测日期：2026-04-30
> 实测者：Super-Dolphin agent (`claude --print` 非交互模式)
> Claude CLI 版本：2.1.119 (Claude Code)
> Codex CLI 版本：codex-cli 0.121.0
> OS：Darwin 25.4.0 (macOS 26)

---

## 0. 前置环境

```bash
export F1_TEST_DIR=/tmp/super-dolphin-f1-test
rm -rf "$F1_TEST_DIR"
mkdir -p "$F1_TEST_DIR/.claude/skills/local-keep"
cat > "$F1_TEST_DIR/.claude/skills/local-keep/SKILL.md" <<'EOF'
---
name: local-keep
description: F1 实测保留对照——这个 skill 必须始终可见
---
# local-keep
EOF
```

实测目标 skill：从 baseline 看，本机已装 11 个 skill（`update-config / keybindings-help / simplify / fewer-permission-prompts / loop / schedule / claude-api / local-keep / init / review / security-review`）；spec 假设的 `brainstorming` 在本机不存在。改用 `simplify` 作为屏蔽测试目标。

---

## 1. T1：Claude `permissions.deny: ["Skill(name)"]` 屏蔽（spec 原假设）

### 实测结果

| 变体 | settings.json / flag | 是否生效 |
|---|---|---|
| T1 spec 原文 | `{"permissions":{"deny":["Skill(simplify)"]}}` | ❌ **不生效**（simplify 仍可见 + 可调用） |
| T1d | flag `--disallowed-tools simplify` | ⚠️ 模型 deflect 但未明确说 "blocked" |
| T1e | flag `--disallowed-tools "Skill(simplify)"` | ✅ **生效**（模型回 "NO"） |
| T1g | `{"disallowedTools":["Skill(simplify)"]}`（顶层字段） | ❌ 不生效（YES） |
| T1h | `{"permissions":{"deny":["Skill:simplify"]}}` ← **冒号语法** | ✅ **生效**（模型回 "NO"） |

**结论**：spec §8.3 假设的 `Skill(name)` (圆括号) 在 settings.json 里**不被识别**。实际正确语法是 `Skill:name`（冒号）。CLI flag `--disallowed-tools` 接受圆括号形式，但 settings.json 不接受。

**注意**：deny 是**运行时拦截**——`list skills` 仍会列出被 deny 的 skill 名（system prompt 没改），但实际 `Skill:name` 调用会被 hook 拒绝。模型可能先 attempt 再被拒，效果上达成"该 skill 不可用"。

---

## 2. T2：Claude `permissions.deny: ["Read"]` 屏蔽工具（验证 settings 路径正确）

### 实测结果

`settings.json` = `{"permissions":{"deny":["Read"]}}` → 模型明确说 "The Read tool isn't available in this environment"。

✅ **生效**——同时验证了 `<workspace>/.claude/settings.json` 路径**确实被 Claude Code 读到**。

---

## 3. T3：`enabledPlugins: []` allowlist

未跑（T1h 已确认主路径，T3 属备选档位 2）。如 P5 实施中发现档位 1 不够用，补跑。

---

## 4. T4：同名 skill 优先级（workspace vs user-level plugin）

未跑（T1h 已确认主路径）。当前 baseline 显示 11 个 skill 都来自用户级（`~/.claude/`）+ workspace 自己（`local-keep`），未观察到同名冲突。如 P5 中真出现冲突再补测。

---

## 5. T5：Codex `config.toml [tools] disabled` 字段

### 实测尝试

```bash
codex exec -c features.web_search=false ...
```

`-c` override 被 Codex 接受、不报错；但 `web_search` 在本机非 Codex 内置工具，难以验证 deny 效果。`features.<name>` 段实际控制的是 Codex feature flag（如 `multi_agent` / `apply_patch_freeform`），**不是工具屏蔽机制**。

spec §8.3 假设的 `[tools] disabled = [...]` **未在本机 Codex 0.121.0 找到对应配置语义**。Codex 配置目录 `~/.codex/` 包含 `skills/ / plugins/ / config.toml`，但 `config.toml` 现有段是 `[features]` / `[mcp_servers]`，没看到 `[tools]`。

### 暂定结论

P5 当前阶段对 Codex 工具屏蔽**无强需求**——spec §8.1 schema 里 `codex.disabled_tools` 默认空数组，本项目当前没明确要屏蔽哪个 Codex native 工具。**P5 实施 nativefilter 时只需把 schema 字段留好但 enforcement 走 stub**（写一句 TODO 留 P5.x 或 P6 补真实测）。

如未来要真屏蔽 Codex 工具，需额外做：
1. 翻 codex-cli 0.x 源码 / 文档找正确的 `[tools] disabled` 字段语法（或等价物）
2. 验证 `-c tools.<x>=false` 是否生效
3. 物理隔离 fallback：给子进程指定干净 `~/.codex/`（覆盖 `XDG_CONFIG_HOME` 或类似）

---

## 6. T6：Codex `--disabled-tools` flag

### 实测尝试

`codex --help` 中**没有** `--disabled-tools` flag，等效物是 `--disable <FEATURE>`（多次重复）= `-c features.<name>=false`。同 T5 限制：`features` 是 Codex feature flag，不是工具屏蔽。

### 结论

✗ Codex 0.121.0 不提供启动参数级工具屏蔽（仅 feature flag 级）。

---

## 7. Fallback 链选定

按 spec §8.3：

### Claude 侧

| 档位 | 候选机制 | 实测结论 |
|---|---|---|
| **1** | **`permissions.deny` 字段（声明式，最理想）** | ✅ **选定主路径** —— 用 `Skill:name` 冒号语法（不是 spec 假设的圆括号） |
| 2 | allowlist 模式 `enabledPlugins: []` + 显式枚举 | ⏸️ 备选；档位 1 已验证生效，未跑 T3 |
| 3 | 物理隔离 `CLAUDE_CONFIG_DIR` | ⏸️ 备选；当前未观察到同名冲突 |

### Codex 侧

| 档位 | 候选机制 | 实测结论 |
|---|---|---|
| 1 | `config.toml [tools] disabled` | ❌ 未在 0.121.0 找到对应字段 |
| 2 | `--disabled-tools` flag | ❌ 不存在 |
| 3 | 物理隔离干净 `~/.codex/` | ⏸️ 备选；本期 P5 不主推 |

**Codex 侧暂时按"schema 留字段、enforcement stub"**，P5 实施时记 TODO，未来真有需求再做实测。

---

## 8. P5 实施前必须知道的额外信息

- ✅ Claude `permissions.deny` 实际匹配语法：`["Skill:<name>", "Read", "Bash(...)", ...]`（`Skill:<name>` 是冒号，不是圆括号）
- ✅ Settings 文件路径：`<workspace>/.claude/settings.json`，Claude Code 会自动读取
- ⚠️ Skill 屏蔽是**运行时拦截**而非"完全隐藏"——模型仍知道 skill 存在；如要完全隐藏，需档位 3（物理隔离 `CLAUDE_CONFIG_DIR`）
- ⚠️ 同名 skill 优先级未实测；P5 实施时若 workspace `<workspace>/.claude/skills/<name>` 与 user-level `~/.claude/plugins/.../skills/<name>` 同名，行为待定
- ❓ Codex 工具屏蔽语法待 P5.x 实测；P5 主线 schema 字段留空 / TODO

---

## 9. 后续行动

1. ✅ 本 appendix 已 commit 到 `feat/skill-refactor-p5` 分支
2. 下一步：基于"档位 1 + Skill: 冒号语法"写 P5 implementation plan（`docs/superpowers/plans/2026-04-29-skill-refactor-p5-nativefilter.md`）
3. P5 spec 字段：`SkillMeta.replaces_native.claude` 在写入 settings.json 时聚合到 `permissions.deny`，每条用 `Skill:<name>` 格式
