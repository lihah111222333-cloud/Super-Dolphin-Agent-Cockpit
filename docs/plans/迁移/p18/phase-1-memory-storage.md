# P18 Phase 1：记忆存储层

> 预计：2 天 | 依赖：Phase 0

## 目标
实现磁盘记忆的 CRUD + 索引管理 + 路径安全。

## 目录结构

```
~/.multi-agent/memory/
├── projects/
│   └── <canonical-git-root>/    # 同仓多 worktree 共享
│       └── memory/
│           ├── MEMORY.md        # 索引文件
│           └── <topic-files>.md # 按语义组织，一条记忆一个文件
└── agent-memory/                # Phase 5 实现
```

> **审查修订**（Agent 3）：
> - `<sanitized>` 取 **canonical git root**，fallback project root
> - 不再画固定的 `user-profile.md` 等文件，topic files 是语义组织
> - `team/` 暂不实现（README 已排除），未来放 `projects/<root>/memory/team/`

## MEMORY.md 索引格式

```markdown
- [Title](file.md) — one-line hook
```

约束：
- 一行一条
- 约 150 字符以内
- **无 frontmatter**
- 只放 hook，不放正文
- 超过 200 行时截断

> **来源**：`restored-src/src/memdir/memdir.ts:199-234`

## Topic File 格式

```markdown
---
name: {{memory name}}
description: {{one-line description}}
type: {{user|feedback|project|reference}}
---

{{memory content}}
```

约束：
- 每条 memory 写到独立文件
- `name/description/type` 必填
- `feedback` / `project` 正文固定结构：`rule/fact + Why: + How to apply:`
- 写新 memory 前先检查是否存在可更新的旧 memory
- legacy/unknown type 降级处理，不硬失败

> **来源**：`restored-src/src/memdir/memoryTypes.ts:261-270`

## 文件命名与索引更新规则

- topic file 文件名由 `name` 生成 `lower-kebab-case` slug，只保留 `[a-z0-9-]`
- `name` 作为逻辑唯一键（大小写不敏感）；slug 冲突时在文件名后追加短 hash，避免大小写/同名冲突
- 更新已有 memory 时默认**复用原文件路径**；仅在显式迁移脚本中改文件名，不在普通 `memory_write` 中隐式 rename
- `MEMORY.md` 采用**全量重写**策略，按 `name` case-insensitive 稳定排序；hook 由 `description` 或正文首句生成，不做局部增量 patch
- `legacy/unknown type` 在 scan/search/read 路径继续保留并返回；`memory_write` 不允许新写入 unknown type；迁移脚本仅在显式映射规则下修复 type

## 路径安全校验

必须拒绝：
- 相对路径
- 根/近根路径
- Windows drive root / UNC path
- null byte
- `~/` 只允许展开到 `$HOME` 的非平凡子路径

Symlink 防护（team memory 路径）：
- `resolve` + `realpathDeepestExisting`
- 校验 real path containment
- dangling symlink / loop → fail-closed

> **来源**：`restored-src/src/memdir/paths.ts:109-150` (validateMemoryPath)
> **来源**：`restored-src/src/memdir/teamMemPaths.ts:109-206` (symlink 校验)

## 截断策略

执行顺序（严格）：
1. `trim()`
2. 按 200 行截断（`MAX_ENTRYPOINT_LINES = 200`）
3. 按 25KB 截断（`MAX_ENTRYPOINT_BYTES = 25_000`，按字符串长度）
4. 触发任一截断 → 追加 warning

> **来源**：`restored-src/src/memdir/memdir.ts:34-38, 57-103`

## 保存协议

Standard 模式两步：
1. 写 topic file
2. 更新 MEMORY.md 索引

附加约束：
- 写入前做服务端敏感信息校验；命中 API key / token / credential / 密码模式时 fail-closed，不只依赖 prompt 规则
- 旧索引项按 `name` 唯一键匹配后重建，不保留重复条目

skipIndex 模式（feature gate 控制）：
- 只写 topic file，不更新索引

> **来源**：`restored-src/src/memdir/memdir.ts:205-234, 359-365`

## 任务清单
- [ ] `memory/store.go`：ReadMemoryIndex / WriteMemoryFile / UpdateMemoryIndex / DeleteMemory
- [ ] `memory/scan.go`：ScanMemoryHeaders（递归 .md，排除 MEMORY.md，前 30 行 frontmatter，最新 200 个）
- [ ] `memory/paths.go`：GetAutoMemPath / ValidateMemoryPath / SanitizePath / FindCanonicalGitRoot
- [ ] `memory/truncate.go`：TruncateEntrypointContent

## 验收
- 单元测试覆盖：CRUD + 索引 + 路径校验 + 截断
- 仓库契约：文件 ≤ 400 行，函数 ≤ 80 行
