# P18 Phase 1：记忆存储层

> 预计：2 天 | 依赖：Phase 0

## 目标
实现磁盘记忆的 CRUD + 索引管理 + 路径安全。

> **现状审查结论（2026-04-14）**：V3 已落地 `internal/module/memory/path.go` / `index.go` / `store.go` 的基础 CRUD、`canonical_name` 去重、atomic rewrite 与路径校验；但 `GetAutoMemPath()` 目前还未被 runtime store 主链消费，也还没有 Claude `scanMemoryFiles()` / `parseMemoryFileContent()` 对应的读路径实现。故本 Phase 的剩余工作重点是 **Claude 对齐 + runtime 接线**，不是从零起步。

## 目录结构（默认布局）

```text
~/.super-dolphin/memory/
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
3. 默认 `~/.super-dolphin/memory/projects/<sanitized-root>/memory/`

> **Claude 对照细节**：
> - Claude 先用 `getMemoryBaseDir()` 解析 **base dir**（`CLAUDE_CODE_REMOTE_MEMORY_DIR` → `~/.claude`），再由 `getAutoMemPath()` 解析 **最终 auto-memory 目录**
> - `CLAUDE_COWORK_MEMORY_PATH_OVERRIDE` / trusted settings 覆盖的是**最终 full path**，不是 base dir
> - V3 可以把 base dir 换成 `~/.super-dolphin/memory` / `MULTI_AGENT_MEMORY_DIR`，但文档与实现都要区分“base dir”和“project memory path”两层语义

硬规则：
- **仓库内 project/repo 级配置不得重定向 memory 根目录**
- project key 需先 sanitize；超长时截断并追加短 hash，保证跨平台安全且稳定可复现

### 启停门禁契约
- `IsAutoMemoryEnabled` / `ResolveMemoryMode` 作为统一门禁
- hook 写盘 / `/forget` / prompt 注入 / retrieval 都必须走同一门禁，避免“prompt 已关但仍写盘”

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
- `name/description/type` 必填；`lang/aliases/search_keys` 为 retrieval 辅助字段，可选，**Phase 1 只做原样保留/透传，不参与唯一键、索引生成或核心写入语义**
- 写入时派生 `canonical_name = NFC(name) + Unicode case fold + whitespace collapse`，作为逻辑唯一键；原始 `name` 只负责 display name
- 规范样例：`" Foo\tBar " → "foo bar"`；`"é"` 与 `"e◌́"` 归一后相同；多个 Unicode 空白折叠为单空格；case fold 必须 locale-insensitive
- `feedback` / `project` 正文固定结构：`rule/fact + Why: + How to apply:`
- 写新 memory 前先检查是否存在可更新的旧 memory
- legacy/unknown type 降级处理，不硬失败

> **来源**：`restored-src/src/memdir/memoryTypes.ts:261-270`

## 文件命名与索引更新规则

- topic file 文件名优先由 `name` 生成 **Unicode-safe slug**；若标题不适合安全落盘，则退化为 `mem-<short-hash>.md`
- 逻辑唯一键改为 `canonical_name`（NFC + Unicode case fold + whitespace collapse）；slug 冲突时在文件名后追加短 hash，避免大小写/同名冲突
- 更新已有 memory 时默认**复用原文件路径**；仅在显式迁移脚本中改文件名，不在普通写盘入口中隐式 rename
- `MEMORY.md` 采用**全量重写**策略，按 `canonical_name` 稳定排序；hook 由 `description` 或正文首句生成，不做局部增量 patch
- `legacy/unknown type` 在 scan/search/read 路径继续保留并返回；普通写盘入口不允许新写入 unknown type；迁移脚本仅在显式映射规则下修复 type

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

计量语义说明：
- 若目标是 **Claude 兼容**，`25KB` 计量以“字符串长度”语义为准，而不是 UTF-8 磁盘真实字节数
- 若后续改成按 UTF-8 byte 计，必须显式标注为 **V3 有意偏离 Claude 基线**

读路径语义补充：
- Claude **不会在写 topic file 时截断正文**；`truncateEntrypointContent()` 只用于读/注入 `MEMORY.md` / TeamMem entrypoint
- 主线程路径由 `parseMemoryFileContent()` 在 frontmatter/comment 处理后，对 `AutoMem` / `TeamMem` 执行截断；agent 路径由 `buildMemoryPrompt()` sync 读取 `MEMORY.md` 后复用同一截断器
- `parseMemoryFileContent()` 还会跳过非文本文件、移除 HTML comments、提取 `@include` 路径，并在内容被改写时回传 `rawContent`；这些属于 Phase 4/6 的**读路径语义**，不要混入 Phase 1 的写路径事务里

> **来源**：`restored-src/src/memdir/memdir.ts:34-38, 57-103`
> **来源**：`restored-src/src/utils/claudemd.ts:343-400` (parseMemoryFileContent)

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

## 删除与恢复协议

- `DeleteMemory` 与正常写入共用同一事务锁；删除 topic file 后必须全量重建 `MEMORY.md`
- `MEMORY.md` 缺失/损坏时，允许显式 `RebuildMemoryIndex()` 从现有 topic files 重建索引
- 读取路径发现索引损坏时可走 degrade：fallback scan root + 返回告警；是否自动覆写磁盘只由显式 rebuild 触发
- `skipIndex` 模式结束后，允许由显式 rebuild 恢复索引一致性，不做隐式后台修复

> **来源**：`restored-src/src/memdir/memdir.ts:205-234, 359-365`

## Claude `scanMemoryFiles()` 对照（不要与 index rebuild 扫描混用）
- `scanMemoryFiles(memoryDir, signal)` 递归查找 `.md`，排除 `MEMORY.md`
- 只读取前 30 行 frontmatter（`readFileInRange`），提取 `description/type`，并保留 `mtimeMs`
- 单文件失败不会整体失败：Claude 用 `Promise.allSettled()` 丢弃失败项
- 输出按 `mtime desc` 排序并截到最新 200 个；这是 retrieval / extract 路径的 header scan，不是索引重建排序
- 当前 V3 `index.go:scanMemoryEntries()` 为重建索引而全文件扫描并按 `canonical_name` 排序；保留该行为即可，但要**新增独立 header scan** 才能对齐 Claude retrieval/extract 语义

## 任务清单
- [ ] `memory/store.go`：ReadMemoryIndex / WriteMemoryFile / UpdateMemoryIndex / DeleteMemory / RebuildMemoryIndex
- [ ] `memory/scan.go`：ScanMemoryHeaders（递归 .md，排除 MEMORY.md，前 30 行 frontmatter，最新 200 个）
- [ ] `memory/paths.go`：GetAutoMemPath / IsAutoMemoryEnabled / ResolveMemoryMode / ValidateMemoryRoot / ValidateMemoryWritePath / SanitizePath / FindCanonicalGitRoot
- [ ] `memory/truncate.go`：TruncateEntrypointContent

## 验收
- 单元测试覆盖：CRUD + 索引 + 路径校验 + 截断 + index rebuild
- 仓库契约：文件 ≤ 400 行，函数 ≤ 80 行
