# P18 Phase 1：记忆存储层

> 预计：2 天 | 依赖：Phase 0

## 目标
实现磁盘记忆的 CRUD + 索引管理 + 路径安全。

## 目录结构（默认布局）

```text
~/.multi-agent/memory/
├── projects/
│   └── <canonical-git-root>/    # 同仓多 worktree 共享
│       └── memory/
│           ├── MEMORY.md        # 索引文件
│           └── <topic-files>.md # 按语义组织，一条记忆一个文件
└── agent-memory/                # Phase 5 实现
```

> **审查修订**（Agent 3/4）：
> - `<canonical-git-root>` 取 **canonical git root**，fallback project root
> - 上图是**默认布局**，实际路径需经过 override / trusted settings / default 解析
> - 不再画固定的 `user-profile.md` 等文件，topic files 是语义组织
> - `team/` 暂不实现（README 已排除），未来放 `projects/<root>/memory/team/`

### 路径解析优先级
1. 显式 full-path / env override
2. 受信配置（policy/local/user 级）
3. 默认 `~/.multi-agent/memory/projects/<sanitized-root>/memory/`

硬规则：
- **仓库内 project/repo 级配置不得重定向 memory 根目录**
- project key 需先 sanitize；超长时截断并追加短 hash，保证跨平台安全且稳定可复现

### 启停门禁契约
- `IsAutoMemoryEnabled` / `ResolveMemoryMode` 作为统一门禁
- `memory_write` / `memory_delete` / prompt 注入 / retrieval 都必须走同一门禁，避免“prompt 已关但仍写盘”

## MEMORY.md 索引格式

```markdown
- [Title](file.md) — one-line hook
```

约束：
- 一行一条
- **软约束**：约 150 字符以内，便于模型维护
- **无 frontmatter**
- 只放 hook，不放正文
- **硬加载上限**：读取时最多 200 行 / 25KB

> **来源**：`restored-src/src/memdir/memdir.ts:199-234`

## Topic File 格式

```markdown
---
name: {{display name}}
description: {{one-line description}}
type: {{user|feedback|project|reference}}
lang: {{optional BCP-47}}
aliases: [{{optional aliases...}}]
search_keys: [{{optional retrieval hints...}}]
---

{{memory content}}
```

约束：
- 每条 memory 写到独立文件
- `name/description/type` 必填；`lang/aliases/search_keys` 为 retrieval 辅助字段，可选
- 写入时派生 `canonical_name = NFC(name) + Unicode case fold + whitespace collapse`，作为逻辑唯一键；原始 `name` 只负责 display name
- `feedback` / `project` 正文固定结构：`rule/fact + Why: + How to apply:`
- 写新 memory 前先检查是否存在可更新的旧 memory
- legacy/unknown type 降级处理，不硬失败

> **来源**：`restored-src/src/memdir/memoryTypes.ts:261-270`

## 文件命名与索引更新规则

- topic file 文件名优先由 `name` 生成 **Unicode-safe slug**；若标题不适合安全落盘，则退化为 `mem-<short-hash>.md`
- 逻辑唯一键改为 `canonical_name`（NFC + Unicode case fold + whitespace collapse）；slug 冲突时在文件名后追加短 hash，避免大小写/同名冲突
- 更新已有 memory 时默认**复用原文件路径**；仅在显式迁移脚本中改文件名，不在普通 `memory_write` 中隐式 rename
- `MEMORY.md` 采用**全量重写**策略，按 `canonical_name` 稳定排序；hook 由 `description` 或正文首句生成，不做局部增量 patch
- `legacy/unknown type` 在 scan/search/read 路径继续保留并返回；`memory_write` 不允许新写入 unknown type；迁移脚本仅在显式映射规则下修复 type

## 路径安全校验

建议拆成两个 API：
- `ValidateMemoryRoot(raw string)`：校验配置得到的 memory 根目录
- `ValidateMemoryWritePath(root, file string)`：真实写盘前做 containment / symlink / dangling symlink 校验

`ValidateMemoryRoot` 成功后的返回契约：
- 输入为空时返回空值（不是 hard error）
- 先标准化，再做 Unicode NFC 归一化
- 去掉原有尾部分隔符后再校验
- 返回值永远带**恰好一个** trailing separator

必须拒绝：
- 相对路径
- 根/近根路径
- Windows drive root / UNC path
- null byte
- `~/` 只允许展开到 `$HOME` 的非平凡子路径

`ValidateMemoryWritePath` 额外要求：
- `resolve` + `realpathDeepestExisting`
- 校验 real path containment
- dangling symlink / loop → fail-closed

> **来源**：`restored-src/src/memdir/paths.ts:109-150` (validateMemoryPath)
> **来源**：`restored-src/src/memdir/teamMemPaths.ts:109-206` (symlink 校验)

## 截断策略

执行顺序（严格）：
1. `trim()`
2. 先基于**原始内容**统计 `lineCount` / `byteCount`
3. 按 200 行截断（`MAX_ENTRYPOINT_LINES = 200`）
4. 若仍超 25KB，再优先截到上限前最后一个 `\n`；找不到换行时才退化为硬截断
5. 基于原始统计分别计算 `wasLineTruncated` / `wasByteTruncated`
6. 触发任一截断 → 追加区分原因的 warning（只超行 / 只超字节 / 两者都超）

> **来源**：`restored-src/src/memdir/memdir.ts:34-38, 57-103`

## 保存协议

Standard 模式两阶段：
1. 写 topic file
2. 重建并更新 `MEMORY.md` 索引

附加约束：
- 写入前做服务端敏感信息校验；命中 API key / token / credential / 密码模式时 fail-closed，不只依赖 prompt 规则
- 旧索引项按 `canonical_name` 唯一键匹配后重建，不保留重复条目
- 并发写保护：同一 memory root 下的 `topic file + MEMORY.md` 更新必须串行化（进程内 keyed mutex；跨进程优先 file lock）
- 锁必须覆盖完整事务：读取当前磁盘视图 → 判定 name/slug 冲突 → 写 topic temp → rename topic → 重新 scan root → 全量重建 `MEMORY.md.tmp` → rename `MEMORY.md`
- 建议维护 root revision/digest；writer 进入临界区后重新确认当前 revision，避免 stale writer 覆盖新索引
- 落盘顺序：先写临时文件，再 `rename` 原子替换；任一步失败都保留旧 `MEMORY.md`，不留下半写状态
- 错误语义应区分 `topic_written` 与 `index_updated`；若正文已落盘但索引失败，返回显式 `memory_index_update_failed`，允许后续 `RebuildMemoryIndex()` 修复

skipIndex 模式（feature gate 控制）：
- 只写 topic file，不更新索引

> **来源**：`restored-src/src/memdir/memdir.ts:205-234, 359-365`

## 任务清单
- [ ] `memory/store.go`：ReadMemoryIndex / WriteMemoryFile / UpdateMemoryIndex / DeleteMemory / RebuildMemoryIndex
- [ ] `memory/scan.go`：ScanMemoryHeaders（递归 .md，排除 MEMORY.md，前 30 行 frontmatter，最新 200 个）
- [ ] `memory/paths.go`：GetAutoMemPath / IsAutoMemoryEnabled / ResolveMemoryMode / ValidateMemoryRoot / ValidateMemoryWritePath / SanitizePath / FindCanonicalGitRoot
- [ ] `memory/truncate.go`：TruncateEntrypointContent

## 验收
- 单元测试覆盖：CRUD + 索引 + 路径校验 + 截断
- 仓库契约：文件 ≤ 400 行，函数 ≤ 80 行
