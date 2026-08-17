# Appendix: spec §8 Native CLI 过滤层实测验证

> **整合说明**（2026-04-30 二支并行实测）：
> 同一日两条独立工作分支（`skill重构` + `feat/skill-refactor-p4`）各自跑了实测，
> 结论局部矛盾。Merge commit 把两份并入：
>
> - **下方 Part A**（来自 `skill重构` 分支）：结构化 T1-T5 验证清单，确认
>   `permissions.deny: ["Skill:<name>"]` 冒号语法生效，圆括号在该实测条件下
>   不识别。errata commit `d1b8ef9f` 把 spec §8.2 / §8.3 / §13 字面修正为冒号。
> - **下方 Part B**（来自 `feat/skill-refactor-p4` 分支）：用 `claude -p` 非交互
>   模式 + debug-file，观察到 `Skill(<name>)` 圆括号也触发 "Skill tool permission
>   denied" debug 日志。可能两种语法都被接受，或 -p 非交互窗口的 deny 来自
>   trust/approval 系统而非 permissions.deny 拦截。
>
> **接线决策**：代码使用 Part A 的冒号语法（更安全 — Part A 给出否定证据，
> Part B 给出肯定证据；同时成立时按"否定证据更可靠"原则选冒号）。Part B
> 的非交互窗口观察作为副产物保留，提示后续工作者注意"两种语法可能都触发某种
> deny，但只有冒号在所有条件下确定生效"。

---

## Part A — F1 Native CLI 过滤实测结果（来源：skill重构 分支）

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

---

## Part B — feat/skill-refactor-p4 worktree 非交互实测（备查）

# Appendix: spec §8.3 Native CLI 过滤层 实测验证

> 实测时间：2026-04-30
> 实测对象：Claude Code 2.1.119（`/Users/mac/.nvm/versions/node/v20.20.2/bin/claude`）
> 沙箱路径：`/tmp/claude-spec8-test/`（已清理）
> 实测者：在 `feat/skill-refactor-p4` 分支落地 P5b 接线后

## 结论

| 候选机制 | 实测结果 | spec §8 假设是否成立 |
|---|---|---|
| `<workspace>/.claude/settings.json` 被 Claude CLI 加载 | ✅ 加载，归到 `localSettings` 域 | 成立 |
| `<workspace>/.claude/settings.local.json` 被加载 | ✅ 加载，同样归到 `localSettings` | spec 未提，但 P5b 选用此路径正确 |
| `permissions.deny: ["Bash"]` 等裸工具名 | ✅ **真硬阻**：工具从模型 toolset 完全消失 | 成立 |
| `permissions.deny: ["Skill(name)"]` | ✅ **真硬阻**：模型尝试调用 native skill 时报 "Skill tool permission denied" | 成立 |
| `permissions.deny: ["WebFetch"]` | ✅ 同上 | 成立 |
| **`permissions.allow: ["Read"]` 是否为严格白名单** | ❌ **不是**——它是 auto-approve 列表，不阻止其他工具调用 | **不成立**，spec §3.1 用词"白名单"不准 |
| `--tools <list>` CLI flag | ✅ **才是真严格白名单**——只有列出的工具被加载到模型 toolset | spec 未提，是 §8 的真正 whitelist 出口 |

## 关键证据

### 1. settings.local.json 被加载

```
[DEBUG] Watching for changes in setting files /Users/mac/.claude/settings.json,
        /private/tmp/claude-spec8-test/.claude/settings.json,
        /private/tmp/claude-spec8-test/.claude/settings.local.json...
[DEBUG] Applying permission update: Adding 1 deny rule(s) to destination 'localSettings': ["WebFetch"]
```

P5b 用 `settings.local.json` 而非 spec §8.2 字面的 `settings.json` 是正确决策——
两者都能被加载，但 `settings.local.json` 不与用户手写的 `settings.json` 冲突。

### 2. `Skill(name)` 语法生效

```jsonc
// settings.local.json
{ "permissions": { "deny": ["Skill(test-skill)"] } }
```

模型尝试调用：

```
> Please invoke the 'test-skill' skill now.

模型输出：I attempted to invoke `test-skill` via the Skill tool, but execution
was blocked by permission rules in this environment.

Debug 日志：[DEBUG] Skill tool permission denied
```

### 3. `permissions.allow` 不是严格白名单

```jsonc
{ "permissions": { "allow": ["Read"] } }   // 仅 allow Read
```

模型仍能成功调用 Bash 输出 "BASHRAN"。Debug 日志：

```
[INFO] [Stall] tool_dispatch_start tool=Bash permissionDecisionMs=4
[INFO] [Stall] tool_dispatch_end tool=Bash outcome=ok durationMs=515
```

`permissions.allow` 的实际语义是 **auto-approve**：列入此名单的工具调用跳过审批弹框；
未列入的工具调用走默认审批策略（在 `--permission-mode default` 下交互模式会弹审批，
非交互 `-p` 模式按默认策略放行）。

### 4. `--tools` CLI flag 才是严格白名单

```
$ claude -p "Run 'echo BASHRAN' via Bash" --tools "Read"
模型输出：I don't have the Bash tool available in this session — only Read is available.
```

## 对 P5b 实现的影响

P5b（commit `3790ba3`）当前把 `AggregateClaude` 算出的 union(skills.AllowedTools)
写到 `<workspace>/.claude/settings.local.json` 的 `permissions.allow` 字段。

按本实测结果，这等于 **"为这些工具跳过审批弹框"**，**不是 spec §3.1 字面的 "工具白名单"**。

两种修正方向：

### 方案 A — 保留 P5b 当前行为，澄清语义

把 `permissions.allow` 重新解读为"skill 声明的常用工具自动批准列表"。
价值：避免用户每次跑该 skill 都要点同意。这本身有用。
代码侧只需更新 `AggregateClaude` 与 `WriteClaudeSettingsLocal` 的 godoc 描述，
把 `Allow` 改名为更准确的 `AutoApprove` 或加注释说明。

### 方案 B — 改成真白名单（`--tools` flag）

把 `AllowedTools` 不写 settings.json，而是收集后通过 `transport_config.go` 的
`--tools` flag 传给 Claude CLI。代码改动：
- `AggregateClaude` 仍输出 `Allow []string`，含义改成"已知工具白名单"
- claudecli driver 在 `prepareSessionStart` 拿到聚合结果后，把 Allow 转成
  逗号分隔的 `--tools` 参数追加到启动 args
- `permissions.allow` 字段不再使用，或保留作为 auto-approve 二级用途

风险：`--tools` 是严格白名单，**漏列任何工具会让该工具不可用**。
当前 spec §3.1 的 skill 字段没有"我需要哪些 builtin 工具默认开启"的概念
（builtin Read/Write/Edit/Bash 等），强行用白名单会让没声明 AllowedTools 的
session 也变成空 toolset。需要默认 fallback 列表（all builtins 或 spec 显式定义的子集）。

### 方案 C — Allow 走 auto-approve, Deny 走真硬阻（推荐）

承认两种语义共存，按用途分配：
- `permissions.deny` ← `Skill(name)` (来自 ReplacesNative.claude) + 显式 disabled_tools
- `permissions.allow` ← skill 声明的 AllowedTools，作为"这些工具我会用、跳过审批"
- 不引入 `--tools` flag（因为没有显式的"完整 builtin tool 集"概念）

P5b 的代码逻辑无需修改；只需在 `nativefilter` 包注释里把 `Allow` 描述从"白名单"
改成"auto-approve 列表"，并在 spec 主文档 §3.1 把 `allowed_tools` 描述措辞改准。

## 推荐路径

走 **方案 C**：纯文档级修正，无代码功能改动；接受 spec §3.1 用词不准的现状，
把语义用"auto-approve"准确描述。

如未来真需要严格白名单（例如某 skill 要严格限制模型只能 Read 不能 Edit），
开 P5c 接 `--tools` flag 时再补默认 fallback 列表（例如所有 file ops + Bash + Grep）。

## 副产物：P5b 接线已被实测验证有效

- 实测证实 `<workspace>/.claude/settings.local.json` 真被 Claude CLI 拾取
- 实测证实 `permissions.deny: ["Skill(name)"]` + `permissions.deny: ["<ToolName>"]` 真生效
- ⇒ P5b 写出来的 settings.local.json **能真正影响** Claude CLI 子进程行为（在 deny 维度）
- ⇒ spec §11 的 `ArtifactKind` 删除前置已部分满足（deny 路径），allow 路径需按方案 C 重新解读

---

## Codex 端实测（2026-04-30 续）

**实测对象**：codex-cli 0.121.0（`/opt/homebrew/bin/codex`）

### 候选机制实测结果

| spec §8.3 列出的 codex 候选 | 实测结果 | 备注 |
|---|---|---|
| `--disabled-tools` flag | ❌ **不存在** | `codex --help` 无此 flag |
| `--allowed-tools` flag | ❌ 不存在 | 无 |
| `config.toml [tools] disabled = [...]` | ❌ **静默忽略** | `-c tools.disabled=["shell"]` 设置后，shell 工具仍可被模型调用输出 BASHRAN |
| `config.toml [skills] disabled_skills = [...]` | ❌ 静默忽略 | 同上路径无效果 |

### Codex CLI 唯一能用的过滤手段

✅ **`features.<name>=false`**（等价 `--disable <feature>`）：
- 实测 `--disable shell_tool` 真把 shell 工具从 toolset 移除
- 模型自报：`this session exposes no shell/terminal execution tool`
- 但仅对 **stable=true** 且**已注册为 feature flag** 的内置工具生效
- `--disable view_image_tool` / `--disable plan_tool` 报 "Unknown feature flag"——`features list` 显示的 feature 名 ≠ `--disable` 接受的 feature 名（codex CLI 内部子集）

✅ **`[mcp_servers]` 段可注入/屏蔽 MCP server**——但这屏蔽的是 MCP server 整个（不是 server 内某个 tool），且只对 codex 接入的外部 MCP server 有效，不能屏蔽 codex 原生工具。

### 重大结论

**spec §8 在 codex 端 codex-cli 0.121.0 上不可实现**：

1. **没有"按 skill name 屏蔽 native skill"机制**——不像 Claude 的 `permissions.deny: ["Skill(name)"]`
2. **没有"按工具名声明白名单/黑名单"机制**——`disabled_tools` 字段 codex CLI 不识别
3. **唯一过滤维度是 feature flag**——粒度过粗（"禁整个 shell 家族"），且 flag 名集合内部隔离

### 对 P5b-codex 的影响

`internal/module/nativefilter.AggregateCodex` 函数输出当前定义为 `disabled_tools []string`。这个字段在 codex CLI 0.121.0 没有可写入的目标格式：
- 写到 `~/.codex/config.toml` 的 `[tools] disabled` 段→codex 静默忽略
- 写到 `[mcp_servers]` 段→语义不匹配（不是 MCP server 名）
- 通过 `--disable` flag→只接受有限 feature 名集合

**当前没有可行的接线方式**。两种选项：

#### 方案 X — 放弃 codex 端 native filter（推荐）

- `nativefilter.AggregateCodex` 保留作为数据形状占位，标 deprecated 注释
- 不接 codex driver
- spec §8 codex 段标 "deferred until codex CLI exposes declarative filter"
- `SkillMeta.ReplacesNative["codex"]` 字段保留 parse，调用方应当忽略
- 用户接受 codex 端没有 skill 安全过滤的现状（codex CLI 自己有 sandbox + approval policy 兜底）

#### 方案 Y — 改 spec，让 codex 段语义对齐 feature flag

- `SkillMeta.ReplacesNative["codex"]` 改成"要禁的 codex feature flag 名"
- 例如：skill X 声明 `replaces_native.codex: ["shell_tool"]` = 该 skill 加载时关 codex shell 工具
- AggregateCodex 输出改成 `disabled_features map[string]bool`
- driver 启动前 codex 子进程时把这些 feature 设为 false（通过 `-c features.<name>=false` 或修改 `~/.codex/config.toml`）
- 风险：feature flag 集合是 codex CLI 内部约定，可能在 codex 升级时改名/消失，强耦合

### 推荐路径

走 **方案 X**：把 codex 端 native filter 标 deferred，等 codex CLI 提供可声明的工具过滤机制后再开 P5d-codex。当前代码层只需：

1. `nativefilter.AggregateCodex` godoc 加 deprecated 标记 + 实测背景
2. 不在 codex driver 接线（已经如此，本来就没接）
3. spec §8 主文档加一段说明 codex 段当前不可落地的原因

### 副产物：spec §8 现状梳理

| spec §8 字段/机制 | Claude 端 | Codex 端 |
|---|---|---|
| `permissions.deny: [Skill(name)]` | ✅ 实测生效 | ❌ codex CLI 无对应概念 |
| `permissions.deny: [<ToolName>]` | ✅ 实测生效 | ❌ codex CLI 无对应概念 |
| `permissions.allow: [...]` (auto-approve) | ✅ 实测生效（非严格白名单） | ❌ codex CLI 无对应概念 |
| `--tools` 严格白名单 | ✅ 实测生效（CLI flag） | ❌ 无 |
| `features.<name>=false` 粗粒度禁 | ❌ 无（builtin 工具靠 settings） | ✅ 实测生效（仅 stable feature） |


---

## Known scope limitation: P5b 只覆盖 user-level skill (2026-04-30)

真 desktop session smoke (commit `34ed147f` 集成测试 + 启动 binary 实跑) 揭示
P5b 的 `applyNativeFilter` 当前只扫 user-level `~/.super-dolphin/skills-library/`，
**不扫项目级 `<cwd>/.agent/skills`**。

### 数据源错位的真因

仓里两条 skill scan 路径互不交叉：

| 包 | 扫描路径 | 元数据来源 |
|---|---|---|
| `internal/module/skill` | user `~/.super-dolphin/skills/` + project `<cwd>/.agent/skills` + cwd | SKILL.md frontmatter 直接解析 |
| `internal/module/skilllibrary` | user `~/.super-dolphin/skills-library/`（builtin seed / marketplace） | sidecar `.skill-meta.json` |

P5b 当前用 `*skilllibrary.Store`（第二条），看不到项目 skill。

### 决策三问 + 团队当前回答（2026-04-30）

P5e 修复方案：把 P5b 的数据源从 `skilllibrary.Store` 切到 `*skill.Service`，
后者扫两条路径，能拿到项目 skill 的 SKILL.md frontmatter 解析结果。

是否做 P5e 的判据：

| 问 | 内容 | 当前回答 |
|---|---|---|
| Q1 | 是否打算让人在项目 skill SKILL.md frontmatter 写 `allowed_tools` | 否 |
| Q2 | 是否需要 untrusted 项目 skill 自动被工具限制（spec §10 安全模型） | 否 |
| Q3 | 是否会用 `replaces_native: {claude: [...]}` 声明屏蔽 native skill | 否 |

三问全否 → P5e 不做，标 known limitation 留 follow-up。

### 重启 P5e 的触发条件

任意一条改变：
- 团队规范开始要求项目 skill 声明 `allowed_tools` 收紧权限
- 安全模型升级到强制 untrusted 项目 skill 工具白名单
- marketplace 引入第三方 skill，spec §10 必须真生效

到时按 Q1/Q2/Q3 重新评估，必做就做 P5e。

### 影响

- 当前 P5b 对 builtin seed / marketplace skill 的 allowed_tools / replaces_native 生效
- 对 project skill 的对应字段无效（即便用户填了也读不到）
- 用户当前的 14 个项目 skill 都没填这俩字段，所以"无效"在事实上等于"无影响"
