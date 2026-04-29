# Skill 子系统重构 — Appendix：F1 Native CLI 过滤实测清单

> 状态：**待跑**（结果由实测者填入下方"实测结果"段）
> 主 spec：[`2026-04-29-skill-refactor-design.md`](./2026-04-29-skill-refactor-design.md) §8.3
> 创建日期：2026-04-29
> 用途：决定 P5 nativefilter 的 fallback 链选哪一档作为主路径

---

## 0. 前置环境

```bash
# 1. 验证 CLI 已安装
claude --version          # 期望: claude 1.x 或 2.x
codex --version           # 期望: codex 0.x

# 2. 准备实测 workspace（下面所有测试都用同一个）
export F1_TEST_DIR=/tmp/super-dolphin-f1-test
rm -rf "$F1_TEST_DIR"
mkdir -p "$F1_TEST_DIR/.claude/skills"

# 3. 准备 1 个本地 skill 用作"我们要保留的"对照
mkdir -p "$F1_TEST_DIR/.claude/skills/local-keep"
cat > "$F1_TEST_DIR/.claude/skills/local-keep/SKILL.md" <<'EOF'
---
name: local-keep
description: F1 实测保留对照——这个 skill 必须始终可见
---

# local-keep

测试用占位 skill，验证我们配置的过滤不会误伤它。
EOF

# 4. 进入 workspace
cd "$F1_TEST_DIR"
```

执行人请确保上面 4 步都跑通后，再开始下面的 6 项实测。

---

## 1. T1：Claude CLI `permissions.deny: ["Skill(name)"]` 屏蔽 native skill

**候选机制**：用 `permissions.deny` 字段直接拒绝指定 skill 名。

### 复现步骤

```bash
# 1.1 写 settings.json，明确 deny 一个 Claude 自带 native skill
cat > "$F1_TEST_DIR/.claude/settings.json" <<'EOF'
{
  "permissions": {
    "deny": ["Skill(brainstorming)"]
  }
}
EOF

# 1.2 启动 Claude CLI 在该 workspace
cd "$F1_TEST_DIR"
claude
```

### 在 CLI 内验证

输入：

```
/skills
```

观察：`brainstorming` 是否仍然出现在 skill 列表里。

### 结果填写

```
T1 实测结果：
  brainstorming 出现：[ ] 是 / [ ] 否
  实际表现描述：（粘贴关键 CLI 输出，最多 3 行）
  推断：permissions.deny "Skill(name)" [ ] 生效 / [ ] 不生效 / [ ] 部分生效
```

---

## 2. T2：Claude CLI `permissions.deny: ["Read"]` 屏蔽工具

**候选机制**：用同一字段拒绝普通工具（非 skill）。

### 复现步骤

```bash
cat > "$F1_TEST_DIR/.claude/settings.json" <<'EOF'
{
  "permissions": {
    "deny": ["Read"]
  }
}
EOF
cd "$F1_TEST_DIR"
claude
```

### 在 CLI 内验证

输入：

```
请读取本目录下的 SKILL.md 文件
```

或（更明确）让模型显式调 `Read` 工具读 `.claude/skills/local-keep/SKILL.md`。

观察：Read 调用是否被拒（错误信息里是否提"permission denied" / "deny rule" / 类似字样）。

### 结果填写

```
T2 实测结果：
  Read 调用被拒：[ ] 是 / [ ] 否
  实际错误信息：（一句话）
  推断：permissions.deny "Read" [ ] 生效 / [ ] 不生效
```

---

## 3. T3：Claude CLI `enabledPlugins: []` allowlist

**候选机制**：用 allowlist 模式，只列允许加载的 plugin。

### 复现步骤

```bash
cat > "$F1_TEST_DIR/.claude/settings.json" <<'EOF'
{
  "enabledPlugins": []
}
EOF
cd "$F1_TEST_DIR"
claude
```

### 在 CLI 内验证

输入：

```
/skills
/plugins
```

观察：marketplace plugin（如有安装过）是否全消失，只剩 workspace 自己的 `local-keep`。

### 结果填写

```
T3 实测结果：
  marketplace plugin 消失：[ ] 是（全消失） / [ ] 否（仍出现） / [ ] N/A（本机未装 plugin）
  workspace 的 local-keep 是否仍可见：[ ] 是 / [ ] 否
  实际 /skills 输出（前 5 行）：
  推断：enabledPlugins allowlist [ ] 生效 / [ ] 不生效 / [ ] 仅过滤 plugin 不过滤 native skill
```

---

## 4. T4：同名 skill 优先级（workspace vs user-level plugin）

**候选机制**：把 workspace `.claude/skills/<name>/` 与用户级 `~/.claude/plugins/.../skills/<name>/` 同名，看 Claude CLI 加载哪一份。

### 复现步骤

```bash
# 4.1 在 user-level 放一个"假冒"同名 skill
mkdir -p ~/.claude/plugins/test-shadow/skills/local-keep
cat > ~/.claude/plugins/test-shadow/skills/local-keep/SKILL.md <<'EOF'
---
name: local-keep
description: SHADOW 版本——如果这条描述出现，说明 user-level 优先
---

# local-keep (SHADOW from user-level)

如果模型在 /skills 看到这条描述，说明 user-level plugin 优先。
EOF

# 4.2 删 settings.json 用默认配置
rm -f "$F1_TEST_DIR/.claude/settings.json"

# 4.3 启动
cd "$F1_TEST_DIR"
claude
```

### 在 CLI 内验证

```
/skills
```

看 `local-keep` 的 description 是 workspace 版（"F1 实测保留对照"）还是 user-level 版（"SHADOW 版本"）。

### 清理

```bash
rm -rf ~/.claude/plugins/test-shadow
```

### 结果填写

```
T4 实测结果：
  哪一份生效：[ ] workspace / [ ] user-level / [ ] 两份都列出（duplicate） / [ ] 报错
  description 实际值：
  推断：workspace 是否优先 [ ] 是 / [ ] 否；
       Fallback 3（物理隔离 CLAUDE_CONFIG_DIR）是否必要 [ ] 必要 / [ ] 可选
```

---

## 5. T5：Codex `config.toml [tools] disabled = [...]`

**候选机制**：在 Codex 配置文件里声明禁用工具。

### 复现步骤

```bash
# 5.1 找到 Codex 配置文件路径（不同版本可能不一样；先 grep 帮助文档）
codex --help 2>&1 | grep -i "config\|toml" | head -5

# 5.2 假设默认 ~/.codex/config.toml；备份原文件
[ -f ~/.codex/config.toml ] && cp ~/.codex/config.toml ~/.codex/config.toml.bak.$(date +%s) || true

# 5.3 写禁用配置（实测 disable Codex 的 web_search 工具）
mkdir -p ~/.codex
cat > ~/.codex/config.toml <<'EOF'
[tools]
disabled = ["web_search"]
EOF

# 5.4 启动 Codex
cd "$F1_TEST_DIR"
codex
```

### 在 CLI 内验证

让模型尝试调用 `web_search` 工具：

```
请使用 web_search 工具搜"hello world"
```

观察：是否被拒 / 是否模型直接说"我没有 web_search 这个工具"。

### 清理

```bash
# 5.5 恢复原配置（如果有备份）
ls ~/.codex/config.toml.bak.* 2>/dev/null | head -1 | xargs -I{} mv {} ~/.codex/config.toml
```

### 结果填写

```
T5 实测结果：
  web_search 被拒/不可见：[ ] 是 / [ ] 否
  config.toml [tools] disabled 字段语法是否被 Codex 接受：[ ] 是 / [ ] 否（报错） / [ ] 字段名不对（请改）
  实际报错或工具列表：
  推断：Codex config.toml [tools] disabled [ ] 生效 / [ ] 不生效
```

---

## 6. T6：Codex `--disabled-tools` flag

**候选机制**：启动参数禁用工具，比 config.toml 更直接。

### 复现步骤

```bash
codex --help 2>&1 | grep -i "disabl" | head -5
# 如果找到类似 --disabled-tools / --no-tool / --disable-tool 字样的 flag
# 用它来启动（替换 <flag> 为实际名字）：
cd "$F1_TEST_DIR"
codex <flag> web_search
```

### 在 CLI 内验证

同 T5：让模型调 `web_search` 看是否被拒。

### 结果填写

```
T6 实测结果：
  --disabled-tools flag 实际名称：（如果不存在则填 "N/A"）
  web_search 被拒/不可见：[ ] 是 / [ ] 否 / [ ] flag 不存在
  推断：Codex CLI 是否提供启动参数级屏蔽 [ ] 提供 / [ ] 不提供
```

---

## 7. 实测后的 Fallback 链选定

按 spec §8.3 的 fallback 链优先级：

| 档位 | 候选机制 | 适用条件 |
|---|---|---|
| 1 | `permissions.deny` 字段（声明式，最理想） | T1 + T2 至少一个 ✓ |
| 2 | allowlist 模式 `enabledPlugins: []` + 显式枚举要保留的 | T3 ✓ |
| 3 | 物理隔离：harness 给子进程指定干净 `CLAUDE_CONFIG_DIR`，只放我们想要的内容 | T1-T3 全 ✗ 时兜底 |

**Codex 侧**优先级：

| 档位 | 候选机制 | 适用条件 |
|---|---|---|
| 1 | `config.toml [tools] disabled` | T5 ✓ |
| 2 | `--disabled-tools` flag | T6 ✓ |
| 3 | 物理隔离干净 codex 配置目录 | T5 + T6 全 ✗ 时兜底 |

### 实测后填写最终决策

```
最终 Fallback 选择：
  Claude 侧主路径：[ ] 档位 1 / [ ] 档位 2 / [ ] 档位 3
  Codex 侧主路径：[ ] 档位 1 / [ ] 档位 2 / [ ] 档位 3
  
P5 实施前必须知道的额外信息：
  - Claude `permissions.deny` 实际匹配语法是什么（"Skill(name)" 还是别的）：
  - Codex disabled 字段实际配置语法：
  - 同名 skill 优先级是否需要物理隔离 CLAUDE_CONFIG_DIR：[ ] 是 / [ ] 否
```

---

## 8. 元信息

- 实测者：（请填）
- 实测日期：（请填）
- Claude CLI 版本：（请填）
- Codex CLI 版本：（请填）
- 操作系统：（请填）

实测完成后，把整个文件 commit 到分支 `feat/skill-refactor-p5`。下一步基于结果写 P5 implementation plan。
