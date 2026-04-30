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
