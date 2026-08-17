# Skill 子系统重构设计

> 日期: 2026-04-29
> 状态: Draft (待用户审阅)
> 范围: `internal/module/skill`、`internal/module/prompt`、`internal/provider/claudecli`、`internal/provider/codexapp`、`internal/platform/toolbridge`

## 1. 背景与重构目标

### 1.1 现状问题

当前 skill 子系统在 Super-Dolphin 中累积了多套互不正交的复杂度：

- **两套注入路径**：Claude 走 L1 manifest（`SkillCatalogProvider`） + L2 MCP 工具（`skill_mcp_server.go` ~600 行）；Codex 走 `buildSkillPromptInput` 一次性把 body 内联到 prompt 加 DynamicTools 重复实现一份。
- **多层 trust 模型**：`TrustScope` 4 档（user/project/signed/unknown）+ `ArtifactKind` 三粒度（metadata/body/resource）+ 持久化审批 cache 多键索引。
- **多模式 Codex 渲染**：`SkillMode` 三态（Full/Summary/None）+ `SKILL_WRITER_FORMAT` 双格式（legacy/v1）+ `overrideSkillsToSummary` 强制策略。
- **MCP 链路目前不可用**：`skill_expand_body` host RPC schema 报 `unknown field "method"`，实际跑不通。
- **L1 manifest 4 组分类**（Core/Native/Manual/Redacted）+ 字符预算计算 + 三个 revision 缓存键，复杂但收益有限。
- **Codex 全文内联** 严重浪费 prompt token：每次 turn 都背所有 skill body。
- **底层 CLI 自带的 native skill / 工具未做过滤**，与本项目自实现重叠，agent 看到双份占上下文。

合计涉及 ~3000 行 Go 代码 + 39 个测试函数，模型本身已偏离"轻量元数据 + 按需正文"的理想形态。

### 1.2 重构目标

1. **单一事实源 + 单一缓存**：所有 skill 由一处管理，所有底层 CLI 共读一份产物。
2. **Claude 跑 Anthropic 原生发现机制**：删除自研 MCP 链路；agent 用原生 `Read` 读分节文件。
3. **Codex 跑 L1 元数据 + L2 工具按需读**：删除全文内联；不再走 MCP，使用 codex CLI 自身的 `dynamicTools` 协议。
4. **可扩展到未来 CLI**：缓存 CLI 中立；新加底层 CLI 只需一个 adapter。
5. **支持未来 skill 商店**：分层数据模型从一开始就到位（library + sidecar metadata + origin 字段），但功能（安装 UI、签名校验）延后实现。
6. **过滤 native CLI 重复**：屏蔽底层 CLI 自带、与本项目重复或场景用不到的 skill / 工具。
7. **按调用频次动态降级注入详细度**：FBSD 4 层分级，常用 skill 信息丰富，冷僻 skill 名字都不出现。
8. **删除约 1840 行历史代码**，新增 ~1430 行（含 FBSD 与 native filter 新功能），净减约 410 行——但能力面显著扩大（按需取段、频次降级、native 过滤）。详见 §11。

### 1.3 不在本次范围

- 商店 UI 设计（安装/卸载/搜索界面）
- 签名校验、distribution 协议
- 跨用户/跨机器 skill 同步
- skill 内部内容编写规范（这是另一个独立话题）

## 2. 核心抽象

```
[Super-Dolphin 工程师 dev override 源]   .agent/skills/<n>/SKILL.md
                  │
                  │  go:embed → harness 启动时 seed/对账
                  ▼
        ~/.super-dolphin/skills-library/<n>/{SKILL.md, .skill-meta.json}    ← 单一 library
                  │
                  │  forge: H2 拆段 + 数字前缀 + 摘要生成 + 单 skill 原子写
                  ▼
        ~/.super-dolphin/skills-cache/<n>/{SKILL.md, references/<NN-anchor>.md}  ← CLI 中立缓存
                  │
        ┌─────────┴─────────┐
        ▼                   ▼
   Claude adapter       Codex adapter
   整目录 symlink         L1-C 元数据注入 + DynamicTools `skill_read_section`
   `<workspace>/.claude/skills` → 缓存
        │                   │
        ▼                   ▼
   Claude CLI 原生发现     Codex CLI 调工具读缓存文件
   原生 Read 读 references
```

**关键属性**：
- 一个 library 一个 cache，多个 adapter；adapter 不感知库的来源（builtin/marketplace/dev override）。
- forge 是单输入（library）单输出（cache）的纯函数式管道，无状态、可重复运行。
- 触发器：harness 启动全量、install/uninstall 事件增量、dev 模式 fsnotify。
- 删除"内置层 / 用户安装层"的源分层；origin 信息进 sidecar，物理上同住一个 library。

## 3. 数据流详细规格

### 3.1 Library 结构

```
~/.super-dolphin/skills-library/
  <skill-name>/
    SKILL.md             # 源文件（H2 拆段前的完整版）
    .skill-meta.json     # 元数据 sidecar (M1)
    references/          # （可选）skill 自带的额外资源
      asset-foo.md
      script.sh
```

**`.skill-meta.json` schema**：
```json
{
  "name": "测试驱动开发",
  "origin": "builtin | marketplace | local | dev-override",
  "version": "1.0.0",
  "version_hash": "sha256-of-SKILL.md",
  "installed_at": "2026-04-29T10:00:00Z",
  "signature": null,
  "allowed_tools": ["Read", "Edit"],
  "disable_model_invocation": false,
  "pinned": false,
  "disabled": false,
  "replaces_native": {
    "claude": ["security-guidance/security-review"],
    "codex": []
  },
  "section_summaries": {
    "红绿重构循环": "三步循环：先写失败测试 → 实现 → 重构"
  }
}
```

**字段语义**：
- `origin`：决定信任级别 + UI 展示来源
- `version_hash`：harness 升级时判断 builtin 是否需要重写
- `signature`：marketplace 签名占位，本期不校验
- `allowed_tools`：传给底层 CLI 的 auto-approve 列表（实测：写入 `permissions.allow`，跳过审批弹框；非严格白名单——见 appendix-cli-test §3.1 校正）
- `disable_model_invocation`：true 时仅 `/<name>` 显式触发
- `pinned` / `disabled`：FBSD escape hatch，跳过频次降级
- `replaces_native`：F1 native 过滤声明（注意：codex 段当前不可落地——
  spec §8.3 appendix 实测证实 codex-cli 0.121.0 无声明式工具过滤机制，
  `replaces_native["codex"]` 字段保留 parse 但调用方应忽略；待 codex CLI 后续
  版本提供机制后再激活）
- `section_summaries`：覆盖 forge 自动生成的节摘要（P3 手写优先）

### 3.2 Cache 结构

```
~/.super-dolphin/skills-cache/
  <skill-name>/
    SKILL.md             # 瘦身版：frontmatter + 节索引 + 各节摘要
    references/
      01-<H2 标题>.md    # 数字前缀 + 原标题（N1，非法字符清洗）
      02-<H2 标题>.md
      ...
```

**瘦身 SKILL.md 模板**：
```yaml
---
name: 测试驱动开发
description: 实现任何功能或 bug 修复时，在编写实现代码前使用
---

# 测试驱动开发

## 节索引（按需读，勿全量加载）

- 红绿重构循环 — 三步循环：先写失败测试 → 实现 → 重构
  详见 references/01-红绿重构循环.md
- 反模式与诊断信号 — 常见反模式与诊断
  详见 references/02-反模式与诊断信号.md
- 提交前检查清单 — 提交前自检表
  详见 references/03-提交前检查清单.md

> 需要某节时，使用 Read 工具读取对应 references/ 文件，不要整文加载。
```

### 3.3 触发器（Z')

| 触发 | 行为 | 触发方 |
|---|---|---|
| harness 启动 | 全量对账：扫 library 每条 → 与缓存比 hash → 不一致重写 | startup hook |
| install/uninstall 事件 | 增量重写涉及的单个 skill 子目录 | library 管理器发出事件 |
| dev fsnotify | env `SUPER_DOLPHIN_SKILL_DEV=1` 时监听 `.agent/skills/`，change → 重写对应 skill | dev 模式 only |
| 异常恢复 | cache 目录被外部破坏时重建 | startup 对账自然覆盖 |

## 4. forge 详细规格

### 4.1 拆段策略 (P3)

- **粒度**：H2 (`^## `) 一段；H3 不拆，留在所属 H2 文件内
- **覆盖率前置检查**：14 个内置 skill 全部有 ≥5 个 H2，已确认；新 skill install 时若无 H2，forge 报错拒绝（强制源规范）
- **降级**：无 H2 但有正文的 skill → 整 body 视为单段 `references/01-main.md`，瘦身 SKILL.md 只有一条索引（兜底 case，正常不应触发）

### 4.2 文件命名 (N1)

```python
def section_filename(index: int, h2_title: str) -> str:
    illegal = r'[/\\:*?"<>|]'
    safe_title = re.sub(illegal, '-', h2_title).strip()
    return f"{index:02d}-{safe_title}.md"
```

- 数字前缀按 H2 出现顺序，从 01 开始
- 中文 / Unicode 保留
- 非法字符 `/ \ : * ? " < > |` 替换为 `-`
- 跨平台前提：现代 macOS / Linux / Windows 均支持中文文件名（NTFS / APFS / ext4）

### 4.3 节摘要生成 (P3)

```python
def section_summary(h2_block: str, sidecar_override: dict, anchor: str) -> str:
    if anchor in sidecar_override.get("section_summaries", {}):
        return sidecar_override["section_summaries"][anchor]
    # 自动抽：去掉空行后取第一段，截到 1-2 句（< 80 chars）
    paragraphs = [p for p in h2_block.split("\n\n") if p.strip() and not p.startswith("#")]
    if not paragraphs:
        return ""
    first = paragraphs[0].replace("\n", " ").strip()
    sentences = re.split(r'[。!?\.\!\?]\s*', first)
    return (sentences[0] + ("。" if sentences[0] else ""))[:80]
```

### 4.4 原子性 (A2)

```python
def write_skill_to_cache(name, snapshot):
    target = cache_dir / name
    tmp = cache_dir / f"{name}.tmp"

    rmtree(tmp)                            # 清残留
    write_files(tmp, snapshot)             # 完整写入 tmp
    rename_atomic(tmp, target)             # POSIX rename / Windows ReplaceFile
```

`rename_atomic` 跨平台抽象：
- POSIX: `os.rename(tmp, target)` — 目录覆盖原子
- Windows: `MoveFileExW(tmp, target, MOVEFILE_REPLACE_EXISTING)` 或 `ReplaceFile`

短暂 rename 窗口期（毫秒级）对 Read 端的处理：
- Claude CLI / Codex 工具实现读到 ENOENT 时**重试一次**（间隔 50ms），仍失败再返回错误

### 4.5 Library → Cache 的对账协议

```python
def reconcile():
    lib_skills = scan_library()
    cache_skills = scan_cache()

    for name, lib_meta in lib_skills.items():
        cache_meta = cache_skills.get(name)
        if cache_meta is None or cache_meta.version_hash != lib_meta.version_hash:
            forge(name)                            # 增量重建

    for name in cache_skills.keys() - lib_skills.keys():
        rmtree(cache_dir / name)                   # library 删了，缓存清理
```

## 5. Library 管理

### 5.1 Origin 类型

| origin | 来源 | 写入时机 | trust |
|---|---|---|---|
| `builtin` | harness binary embed | 启动 seed + harness 升级覆盖 | trusted |
| `marketplace` | 商店下载（本期未实现） | 用户点 install | 待签名校验 |
| `local` | 用户手动放置（如 `cp` 进 library） | 不触发；下次 forge 启动对账时识别 | untrusted（需用户确认） |
| `dev-override` | env `SUPER_DOLPHIN_SKILL_DEV` 指向的 `.agent/skills/` | runtime fsnotify；不进 library 物理目录 | trusted |

### 5.2 Builtin Seed 流程（harness 启动）

```python
def seed_builtins():
    embedded = read_go_embed_skills()   # //go:embed .agent/skills/...
    for name, src in embedded.items():
        lib_path = library_dir / name
        meta = read_meta(lib_path)
        if meta is None:
            # 全新安装
            install_to_library(src, origin="builtin", version=harness_version)
        elif meta.origin == "builtin" and meta.version_hash != src.hash:
            # harness 升级，覆盖 builtin
            install_to_library(src, origin="builtin", version=harness_version)
        # 其他 origin 不动（用户已自定义）
```

### 5.3 Install 流程（marketplace，本期占位）

```python
def install_skill(bundle):
    verify_signature(bundle)            # 占位；本期不校验
    extract_to(library_dir / bundle.name)
    write_meta(origin="marketplace", version=bundle.version, signature=bundle.signature)
    forge(bundle.name)                  # 立即生成缓存
    emit_event("skill_installed", bundle.name)
```

### 5.4 dev override 注入

```python
if os.getenv("SUPER_DOLPHIN_SKILL_DEV"):
    src_dir = os.getenv("SUPER_DOLPHIN_SKILL_DEV")
    fsnotify.watch(src_dir, on_change=lambda name: forge_from_path(src_dir / name))
    # forge 时 dev source 优先级最高，覆盖 library 同名条目
```

## 6. Claude 侧 Adapter

### 6.1 Workspace 注入 (B + L1)

每次 harness spawn Claude CLI 子进程前：

```python
def setup_claude_workspace(workspace_dir):
    skills_link = workspace_dir / ".claude" / "skills"
    skills_link.parent.mkdir(parents=True, exist_ok=True)

    target = cache_dir.absolute()

    if platform == "windows":
        try_create_junction(skills_link, target)        # 优先 junction
        if same_drive(skills_link, target):
            os.symlink(target, skills_link)              # dev mode
        else:
            hardlink_copy_tree(target, skills_link)      # L4 fallback
    else:
        os.symlink(target, skills_link)                  # POSIX
```

### 6.2 Native discovery 行为

- Claude CLI 自动扫 `<workspace>/.claude/skills/` 看到我们 forge 出的瘦身 SKILL.md
- description 匹配触发 / `/<name>` 显式触发
- agent 用原生 `Read` 读 `<workspace>/.claude/skills/<name>/references/<NN-anchor>.md`

### 6.3 删除项

- `internal/provider/claudecli/skill_mcp_server.go` (~600 行，整文件)
- `internal/provider/claudecli/skill_mcp_server_test.go`
- `internal/provider/claudecli/skill_inject.go: DetectNativeSkills` (~150 行)
- `internal/module/prompt/skill_catalog_provider.go` 大部 (~400 行；保留极少量给非 skill 类 dynamic section)

## 7. Codex 侧 Adapter

### 7.1 L1-C 注入

base instructions 注入模板（按 FBSD tier 走不同模板，Hot 走 L1-C 完整）：

```
## Skills（按需读，勿全文加载）

可用 skill 列表（仅元数据；正文通过工具按需取）：

[Hot tier — L1-C 完整]
- 测试驱动开发
  实现任何功能或 bug 修复时使用
  节索引：
    - 红绿重构循环 — 三步循环：先写失败测试 → 实现 → 重构
    - 反模式与诊断 — 常见反模式信号
    - 提交前检查清单 — 提交前自检表

[Warm tier — L1-B：节标题列表，无摘要]
- 头脑风暴
  创造性工作前探索意图与设计
  节: [探索项目上下文, 提出澄清问题, 提出方案, 呈现设计, 写设计文档, 规格自审]

[Cold tier — L1-A：仅 name + description]
- 编写技能: 创建新技能、编辑现有技能、部署前验证
- 系统化调试: 遇到 bug、测试失败或意外行为时使用

[Frozen tier 不出现于 L1，可通过 `skill_list_all()` 工具发现]

读取某节正文：调用工具 `skill_read_section(name, anchor)`
枚举所有 skill（包括 Frozen）：调用 `skill_list_all()`
```

### 7.2 DynamicTools (非 MCP)

通过 `internal/platform/toolbridge/handler_host_tools.go: ListToolsForCodex` 注册：

**始终暴露**：
```typescript
skill_read_section(name: string, anchor: string, max_bytes?: number): string
```
实现：toolbridge 进程内直接读 `<cache>/<name>/references/<NN-anchor>.md`，找不到 anchor 时返回所有可用 anchor 列表帮助 agent 修正。

**条件暴露**（FBSD 降级到 L1-A 时才注册）：
```typescript
skill_list_sections(name: string): { anchor: string, summary: string }[]
skill_list_all(): { name: string, description: string, tier: string }[]
```

### 7.3 Budget 兜底降级

```python
def render_codex_l1(skills_with_tier, budget_chars=8192):
    # FBSD 已经 tier 化；这一步是 budget 安全网
    rendered, used = [], 0
    for skill, tier in skills_with_tier:
        block = render_template(skill, tier)
        if used + len(block) <= budget_chars:
            rendered.append(block); used += len(block)
        else:
            # tier 降级再试
            for fallback in [L1_B, L1_A]:
                block = render_template(skill, fallback)
                if used + len(block) <= budget_chars:
                    rendered.append(block); used += len(block); break
            else:
                continue  # 仍超就略过
    return "\n".join(rendered)
```

### 7.4 删除项

- `internal/provider/codexapp/module.go: buildSkillPromptInput / renderSkillBlock / overrideSkillsToSummary` (~250 行)
- `dto.SkillRef.Mode` 三态枚举 + 所有分支
- env `SKILL_WRITER_FORMAT` + `legacy/v1` 双格式
- `toolbridge: skill_expand_body / skill_read_resource` 工具注册及实现

## 8. Native CLI 过滤层 (F1)

> **codex 端 deferred**（spec §8.3 appendix 2026-04-30 实测）：codex-cli 0.121.0
> 没有声明式工具过滤机制——`[tools] disabled` 字段静默忽略、无 `--disabled-tools`
> flag、无按 skill name 屏蔽 native skill 的能力。本节 codex 段（`codex.disabled_tools`、
> `replaces_native["codex"]`）字段定义保留作为数据形状占位，待 codex CLI 后续版本
> 提供可声明的工具过滤机制（类似 Claude 的 `permissions.deny` 体系）后再激活；
> Claude 端机制 P5b 已落地，详见 appendix。

### 8.1 配置位置

```
~/.super-dolphin/native-cli-filter.json     # 全局基线
+ 每条 skill 的 .skill-meta.json 的 replaces_native 字段（声明式叠加）
```

`native-cli-filter.json` schema：
```json
{
  "claude": {
    "disabled_skills": ["math-olympiad", "playground", "explanatory-output-style"],
    "disabled_tools": [],
    "allowed_tools": null
  },
  "codex": {
    "disabled_tools": []
  }
}
```

### 8.2 子进程启动前的配置组装

```python
def build_claude_settings(workspace_dir):
    base = read_json("~/.super-dolphin/native-cli-filter.json").get("claude", {})

    extra_disabled_skills = []
    for skill in library.list_active():
        extra_disabled_skills.extend(skill.replaces_native.get("claude", []))

    settings = {
        "permissions": {
            "deny": base.get("disabled_tools", []) + [
                # native skill 屏蔽：Claude Code 2.1.119 实测确认采用冒号语法
                # "Skill:<name>"；原验证表里的圆括号形式 "Skill(<name>)" 实测不生效。
                # 实测详情见 docs/superpowers/specs/2026-04-29-skill-refactor-design-appendix-cli-test.md
                f"Skill:{s}" for s in (base.get("disabled_skills", []) + extra_disabled_skills)
            ],
        }
    }
    write_json(workspace_dir / ".claude" / "settings.json", settings)
```

### 8.3 实测验证清单（Phase 1 实施前必做）

> **2026-04-30 实测完成**。完整结果记在 [`2026-04-29-skill-refactor-design-appendix-cli-test.md`](./2026-04-29-skill-refactor-design-appendix-cli-test.md)。
> 下表保留原始候选机制与预期结果作为设计记录；**实测后的最终语法与主路径以 appendix 为准**。
>
> 关键 errata：原表写作 “`Skill(name)` 圆括号” 实测不生效；Claude Code 2.1.119 实际采用
> 冒号语法 `permissions.deny: ["Skill:name"]`。P5 nativefilter 渲染已按冒号语法落地。

| 候选机制 | 验证方法 | 预期结果 | 实测结论 |
|---|---|---|---|
| `permissions.deny: ["Skill:name"]` | 写配置后启动 Claude CLI，看 `/skills` 输出 | 该 skill 不出现 | ✅ 生效（运行时拦截；list 仍出现但调用被拒）——**P5 主路径** |
| ~~`permissions.deny: ["Skill(name)"]`~~ | 圆括号语法 | 原望屏蔽 skill | ❌ 不生效（spec 写作错误） |
| `permissions.deny: ["Read"]` | 同上 | Read 工具调用被拒 | ✅ 生效 |
| `enabledPlugins: []` allowlist | 写配置 + 启动 | marketplace plugin 全消失 | ⏸️ 未跑（T1h 已确认主路径，本项作为 fallback 备选） |
| 同名 skill 优先级 | `<workspace>/.claude/skills/<n>` 与 `~/.claude/plugins/.../skills/<n>` 同名 | 看 Claude CLI 加载哪一份 | ⏸️ 未跑（当前未观察到同名冲突） |
| Codex `config.toml [tools] disabled = [...]` | 翻 OpenAI codex 文档 + 实测 | 工具被屏蔽 | ❌ codex 0.121.0 未发现等价机制；P5/P6 设计为 stub + TODO |
| Codex `--disabled-tools` flag | 启动参数实测 | 工具被屏蔽 | ❌ codex 0.121.0 仅提供 `--disable <FEATURE>` (feature flag，不是工具屏蔽) |

**Fallback 链**（按优先级）：
1. `permissions.deny` 字段（声明式，最理想）
2. allowlist 模式 `enabledPlugins: []` + 显式枚举要保留的
3. 物理隔离：harness 给子进程指定干净 `CLAUDE_CONFIG_DIR`，只放我们想要的内容

实测结果记入 `docs/superpowers/specs/2026-04-29-skill-refactor-design-appendix-cli-test.md`，方案随结果调整。

## 9. FBSD 频次降级

### 9.1 Tier 定义

| Tier | 触发条件 | L1 注入形态 | 模板长度 |
|---|---|---|---|
| Hot | score > 0 且能塞进 budget 的 Hot 配额 | 完整 (L1-C) | ~600 chars |
| Warm | score > 0 但 Hot 配额已满 | 节标题列表 (L1-B) | ~200 chars |
| Cold | 90 天内有调用但 Warm 配额已满 | 仅 name + description (L1-A) | ~80 chars |
| Frozen | 90 天零调用 或 budget 已耗尽 | 不进 L1，仅通过 `skill_list_all()` 发现 | 0 |

### 9.2 Score 算法（指数衰减）

```python
def score(skill, now):
    half_life = timedelta(days=int(env("SKILL_FBSD_HALF_LIFE_DAYS", 7)))
    cutoff = now - timedelta(days=int(env("SKILL_FBSD_FROZEN_DAYS", 90)))
    return sum(
        2 ** (-(now - t).total_seconds() / half_life.total_seconds())
        for t in skill.calls if t >= cutoff
    )
```

### 9.3 G3 双层数据合并

```python
def effective_score(skill, workspace_id, now):
    ws = workspace_stats.get(workspace_id, {}).get(skill.name)
    ws_total = len(ws.calls) if ws else 0

    if ws_total >= 10:
        return ws.score(now)
    glob = global_stats.get(skill.name)
    if glob is None:
        return 0
    if ws is None:
        return glob.score(now)
    return 0.3 * ws.score(now) + 0.7 * glob.score(now)
```

### 9.4 Tier 分配（budget-driven 贪心）

```python
def assign_tiers(skills, workspace_id, budget=8192, now=datetime.now()):
    grace = timedelta(days=int(env("SKILL_FBSD_GRACE_DAYS", 7)))
    h_chars = int(env("SKILL_FBSD_HOT_CHARS", 600))
    w_chars = int(env("SKILL_FBSD_WARM_CHARS", 200))
    c_chars = int(env("SKILL_FBSD_COLD_CHARS", 80))

    decorated = []
    for s in skills:
        if s.disabled: continue
        if s.pinned:
            decorated.append((s, math.inf, "Hot"))      # pinned 永远 Hot 优先
        elif now - s.installed_at < grace:
            decorated.append((s, math.inf - 1, "Hot"))  # grace 期 Hot 但优先级低于 pinned
        else:
            es = effective_score(s, workspace_id, now)
            decorated.append((s, es, None))

    decorated.sort(key=lambda x: x[1], reverse=True)

    remaining, result = budget, []
    for s, sc, forced_tier in decorated:
        if sc == 0 and forced_tier is None:
            result.append((s, "Frozen")); continue
        if remaining >= h_chars:
            result.append((s, "Hot")); remaining -= h_chars
        elif remaining >= w_chars:
            result.append((s, "Warm")); remaining -= w_chars
        elif remaining >= c_chars:
            result.append((s, "Cold")); remaining -= c_chars
        else:
            result.append((s, "Frozen"))
    return result
```

### 9.5 打点 Hook

**Claude 侧**：
- 在 workspace symlink target 上挂 fs access tracker（OS 级）—— 不可行；改为
- 监听 Claude CLI 输出中的 `Read("<workspace>/.claude/skills/<name>/references/...")` 事件，捕获 skill name + section anchor
- 实现位置：`claudecli/` 的 turn output parser 加 hook

**Codex 侧**：
- `skill_read_section` 工具实现内打点（最直接）
- 实现位置：`toolbridge/handler_host_tools.go` 的 tool implementation

### 9.6 Stats 持久化

```
~/.super-dolphin/skills-stats.json    (全局)
{
  "测试驱动开发": {
    "calls": [1714389000, 1714389050, 1714390000],
    "installed_at": 1714000000,
    "section_calls": { "红绿重构循环": 12, "反模式": 3 }
  }
}

~/.super-dolphin/workspaces/<workspace-id>/skills-stats.json    (workspace)
（同 schema）
```

写入策略：每次工具调用后异步写入（non-blocking）；harness 退出时 flush。

### 9.7 默认参数表

```
SKILL_FBSD_BUDGET=8192
SKILL_FBSD_HALF_LIFE_DAYS=7
SKILL_FBSD_FROZEN_DAYS=90
SKILL_FBSD_GRACE_DAYS=7
SKILL_FBSD_HOT_CHARS=600
SKILL_FBSD_WARM_CHARS=200
SKILL_FBSD_COLD_CHARS=80
SKILL_FBSD_WS_MIN_CALLS=10           # workspace 数据足够阈值
SKILL_FBSD_WS_WEIGHT=0.3             # workspace 数据权重（混合模式）
```

所有参数支持 env 覆盖，写进 spec 等于初始值；正式跑起来后根据数据再调。

## 10. Trust / 安全

本期**仅做架构占位**，功能延后：

| 字段 | 现期行为 | 未来扩展 |
|---|---|---|
| `origin` | 区分 builtin/marketplace/local/dev-override | UI 标识来源；marketplace 联动签名 |
| `signature` | 写入但不校验 | marketplace 上线时启用 ED25519 签名校验 |
| `installed_at` | 用于 grace period | 用于"近期 install 提示" |
| `version_hash` | builtin 升级判断 | marketplace 安全更新提示 |

删除项：`internal/module/skill/approval.go` 中 `ArtifactKind` 三粒度（metadata/body/resource）+ 多键审批 cache。整体审批 cache 也删除（git 跟踪 = 信任 + builtin 自动信任）。

## 11. 删除清单

| 模块 | 行数估 | 处理 |
|---|---|---|
| `claudecli/skill_mcp_server.go` + `_test.go` | 600 | 删 |
| `claudecli/skill_inject.go: DetectNativeSkills` | 150 | 删 |
| `prompt/skill_catalog_provider.go` 大部 | 400 | 删 |
| `codexapp/module.go: buildSkillPromptInput / renderSkillBlock / overrideSkillsToSummary` | 250 | 重写为 `buildSkillManifest` (~150 行) |
| `dto.SkillRef.Mode` + 所有分支 | 80 | 删 |
| env `SKILL_WRITER_FORMAT` + 双格式 | 50 | 删 |
| `module/skill/approval.go` 中 ArtifactKind 分支 | 150 | 删；保留单粒度 manifest 字段占位 |
| `module/skill/rpc.go` expand/read RPC | 100 | 删 |
| `toolbridge: skill_expand_body / skill_read_resource` 注册 + 实现 | 60 | 删 |

**新增**：
| 模块 | 行数估 |
|---|---|
| `internal/module/skillforge/` 包（parse / render / atomic write / fsnotify） | 400 |
| Library 管理（seed / install / uninstall / sidecar 读写） | 200 |
| `toolbridge: skill_read_section + skill_list_all + skill_list_sections` | 80 |
| `internal/module/nativefilter/` 包（settings 写入器） | 250 |
| FBSD（打点 + score + tier 分配 + 持久化 + 渲染按 tier） | 500 |

**净删**：~1840 - ~1430 = **~410 行净减**（不含测试增减）。

> 注：之前估算 ~1000 行净减是不计算 FBSD 和 native filter 新增的版本。加上这两块新功能后净减更少，但功能远超之前。

## 12. 老代码删除分阶段策略

| Phase | 内容 | 风险 | 可独立 ship | 顺序约束 |
|---|---|---|---|---|
| P1. 建基础设施 | skillforge 包 + library + cache + sidecar；不接调用方 | 🟢 低 | ✓ | — |
| P2. 切 Claude | workspace symlink；删 mcp server / DetectNativeSkills；缩 catalog provider | 🟡 中 | ✓ | 依赖 P1 |
| P3. 切 Codex | 改 buildSkillPromptInput → buildSkillManifest；toolbridge 替换工具 | 🟡 中 | ✓ | 依赖 P1，可与 P2 并行 |
| P4. 清共享老代码 | 删 SkillRef.Mode、SKILL_WRITER_FORMAT、ArtifactKind、RPC expand/read | 🟢 低 | ✓ | 依赖 P2 + P3 |
| P5. 加 F1 native filter | nativefilter 包；写 workspace settings；处理 replaces_native | 🟢 低 | ✓ | 可与 P3 并行；F1 实测验证后再 implement |
| P6. 加 FBSD | 打点 hooks；stats 持久化；tier 算法；按 tier 渲染 L1 | 🟢 低 | ✓ | 依赖 P3（Codex 工具调用打点） |

**约束**：
- 每个 Phase 独立 PR、独立 ship、main 永远绿
- P4 必须在 P2 + P3 之后（删除的东西要先有替代）
- P5 在 F1 候选机制实测后再开始（验证清单见 §8.3）
- 具体 task 拆分（每个 Phase 内部分多少步、每步做什么）由"编写计划"技能在拿到本 spec 后产出，spec 不到那个粒度

## 13. 风险与未决项 (Deferred)

1. **Native CLI 屏蔽机制实测** — ✅ 已于 2026-04-30 实测完成。Claude 侧 `permissions.deny: ["Skill:name"]`（冒号）生效；圆括号语法不生效（spec 原文已 errata）。Codex 侧 0.121.0 未发现等价工具屏蔽机制，P5 nativefilter 设计为 `disabled_tools` schema 留字段但 enforcement 走 stub，未来实测后补。详见 [appendix](./2026-04-29-skill-refactor-design-appendix-cli-test.md)。
2. **FBSD tier 阈值参数**：`SKILL_FBSD_*` 一系列默认值是初始拍脑袋值，正式跑起来后需根据真实数据 tune；spec 定义参数化结构，不锁死值。
3. **Windows symlink fallback**：junction 在跨盘场景失败 → 退化到 hardlink-copy。具体 Go 抽象 IO 接口在 P1 期内提交时再细化。
4. **商店 trust 模型**：`signature` 字段语义、签名算法、CA 链等留作未来 spec。
5. **节摘要质量**：自动抽第一段第一句的质量取决于 skill 作者的写作风格；frontmatter `section_summaries` 覆盖给了 escape hatch，但需要观察实际生效情况，可能后续要加 LLM 辅助生成。
6. **打点 hook 的 Claude 侧实现**：通过 turn output 解析 Read 调用是否稳定，需实测。如不稳定，退化为"按 skill 触发计数"（不到 section 级别），FBSD 算法仍可工作。
7. **dev override 路径冲突**：`SUPER_DOLPHIN_SKILL_DEV` 指向 `.agent/skills/` 时如果工程师本地多个 skill 同名，覆盖优先级行为需明确。

## 14. 测试策略概要

新增组件的覆盖建议（具体测试用例 list 由"编写计划"产出）：

- `skillforge` 包：H2 拆段、文件命名、摘要抽取、原子写、fsnotify hook
- Library 管理：seed builtin、覆盖逻辑、install/uninstall 事件
- 双端 adapter：Claude symlink 创建（含 Windows fallback mock）、Codex L1 渲染按 tier
- nativefilter：settings 文件生成、replaces_native 聚合
- FBSD：score 衰减、G3 合并、tier 分配 budget 满足、grace period
- e2e：full pipeline `.agent/skills/` → cache → workspace 注入 → agent 调工具读节

保留并迁移的现有测试：library 元数据 parse、frontmatter parse、文件路径安全（防逃逸）。

---

## Self-review 结果

- [x] 占位符扫描：无 TBD/TODO/FIXME。
- [x] 内部一致性：§1.2 LoC 估算与 §11 已对齐；触发器与 forge reconcile 对齐；删除清单与各侧 adapter 章节对齐。
- [x] 范围检查：本 spec 落地需 6 个 Phase（§12），单 spec 范围合适，无需再拆。具体 task 拆分由后续"编写计划"产出。
- [x] 歧义检查：FBSD tier 阈值参数化避免硬编码歧义；native filter 候选机制以"实测后选定"明确收口；其他章节关键词与 §2 标识符体系（B/M1/P3/N1/A2/L1/ξ1/F1/FBSD/G3）一一对应。

## 用户审阅门槛

规格已写入 `docs/superpowers/specs/2026-04-29-skill-refactor-design.md`。请审阅；如开始编写实现计划前还想修改，告知调整点。
