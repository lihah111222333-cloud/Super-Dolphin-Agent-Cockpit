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
