# LSP 工具复现记录

> 本文记录 2026-06-01 对 MCP LSP 工具重新评测时仍可复现的问题。目标是给实现者提供最小复现步骤、实际结果、预期结果和验收标准。

| 项目 | 内容 |
|:---|:---|
| 仓库 | `/Users/mima0000/Desktop/wj/wjboot-v2` |
| 工具来源 | `mcp__lsp` |
| 复测范围 | `file` / `grep` / `structure` / `inspect` / `xref` / `completion` / `edit` |
| 结论 | 核心能力可用，仍有 3 个影响评分的可复现问题 |

## 1. 总览

| 优先级 | 问题 | 状态 | 影响 |
|:---|:---|:---|:---|
| P0 | `grep` 默认排除未生效，仍命中 `.gomodcache` 和 `.tools/gomodcache` | 可复现 | 搜索噪声高，根目录搜索不可信 |
| P1 | `document_symbol` 元字段未统一，缺 `showing/truncated/hint` | 可复现 | 输出契约不一致，截断状态不可判断 |
| P1 | `docs/LSP使用规范.md` 仍提示 `read_file(offset=...)` | 可复现 | 文档与当前 offset 删除后的契约冲突 |

## 2. 复现环境

| 条目 | 值 |
|:---|:---|
| 工作目录 | `/Users/mima0000/Desktop/wj/wjboot-v2` |
| Go 基线 | `1.25.7` |
| JS 样本 | `admin-ui-v2/eslint.config.js` |
| TS 样本 | `admin-ui-v2/src/api/auth.ts` |
| Python 样本 | `qlib2_lite/__init__.py` |
| Rust 样本 | `backend/internal/engine/feed/data/rustagg/src/lib.rs` |

## 3. P0: grep 默认排除未生效

### 3.1 复现调用

| 字段 | 值 |
|:---|:---|
| 工具 | `mcp__lsp.grep` |
| action | `text_search` |
| query | `defineConfig` |
| path | `/Users/mima0000/Desktop/wj/wjboot-v2` |
| glob | `*.js` |
| max_results | `5` |

### 3.2 实际结果

返回结果仍包含以下路径：

| 命中路径 |
|:---|
| `/Users/mima0000/Desktop/wj/wjboot-v2/.gomodcache/github.com/arl/statsviz@v0.8.0/internal/static/vite.config.js` |
| `/Users/mima0000/Desktop/wj/wjboot-v2/.tools/gomodcache/github.com/arl/statsviz@v0.8.0/internal/static/vite.config.js` |
| `/Users/mima0000/Desktop/wj/wjboot-v2/admin-ui-v2/eslint.config.js` |

### 3.3 预期结果

| 规则 | 预期 |
|:---|:---|
| 默认排除 | 根目录搜索默认排除 `.gomodcache`、`.tools/gomodcache`、`node_modules`、`dist`、`cache` |
| 返回结果 | 只返回项目源码结果，例如 `admin-ui-v2/eslint.config.js` |
| total/showing | 统计值不应包含已排除目录内的结果 |

### 3.4 对照调用

当 `path` 缩小到 `/Users/mima0000/Desktop/wj/wjboot-v2/admin-ui-v2` 时，只返回 `admin-ui-v2/eslint.config.js`，说明问题集中在根目录默认排除规则未生效。

### 3.5 验收标准

| 验收项 | 要求 |
|:---|:---|
| 根目录搜索 | 不再出现 `.gomodcache`、`.tools/gomodcache`、`node_modules`、`dist`、`cache` |
| max_results | 即使 `max_results` 很小，也不能先被缓存目录结果占满 |
| 统计字段 | `total/showing/truncated` 仅统计未排除范围内的结果 |

## 4. P1: document_symbol 元字段未统一

### 4.1 复现调用

| 字段 | 值 |
|:---|:---|
| 工具 | `mcp__lsp.structure` |
| action | `document_symbol` |
| file_path | `/Users/mima0000/Desktop/wj/wjboot-v2/backend/pkg/auditlog/file_audit_logger.go` |
| max_results | `2` |

### 4.2 实际结果

返回结构只包含：

| 字段 | 实际 |
|:---|:---|
| `items` | 有 |
| `total` | `2` |
| `showing` | 缺失 |
| `truncated` | 缺失 |
| `hint` | 缺失 |

问题点：该文件实际符号数量明显超过 2，但 `total=2`，表现为“返回数量”而不是“总数量”。模型无法判断结果是否被截断。

### 4.3 预期结果

| 字段 | 预期 |
|:---|:---|
| `total` | 原始符号总数 |
| `showing` | 实际返回数量 |
| `truncated` | 当 `showing < total` 时为 `true` |
| `hint` | 截断时提示提高 `max_results` 或缩小文件/查询范围 |

### 4.4 对照结果

`workspace_symbol` 和 `completion` 已返回统一字段，例如 `total/showing/truncated/hint`，说明 `document_symbol` 是残留未统一项。

### 4.5 验收标准

| 验收项 | 要求 |
|:---|:---|
| document_symbol 截断 | `max_results=2` 时返回 `showing=2`、`total>2`、`truncated=true` |
| hint | 截断时给出可执行提示 |
| 未截断 | `truncated` 可省略或为 `false`，但 `showing` 应与返回数量一致 |

## 5. P1: LSP 使用规范仍引用 offset

### 5.1 复现位置

| 文件 | 行号 | 问题 |
|:---|:---|:---|
| `/Users/mima0000/Desktop/wj/wjboot-v2/docs/LSP使用规范.md` | L72 | 仍写 `read_file(offset=func_start, limit=func_end-func_start+1)` |

### 5.2 实际工具契约

`file` 工具当前已删除 `offset`。传入 `offset` 时会明确报错：

| 字段 | 实际 |
|:---|:---|
| success | `false` |
| error | `offset is removed; use pos="file_path:104" instead` |

### 5.3 预期文档写法

| 旧写法 | 新写法 |
|:---|:---|
| `read_file(offset=func_start, limit=func_end-func_start+1)` | `file action=read_file pos=<file>:<func_start> limit=<func_end-func_start+1>` |

### 5.4 验收标准

| 验收项 | 要求 |
|:---|:---|
| 文档搜索 | `rg "offset=" docs/LSP使用规范.md` 不再命中旧入口 |
| 示例一致性 | 所有 read_file 示例统一使用 `pos=<file>:<line>` |

## 6. 已确认通过项

| 工具 | 通过项 |
|:---|:---|
| `file` | `pos+limit` 保持函数模式 cap；`scope=lines` 显式行窗口；`offset` 明确拒绝 |
| `grep` | 返回 `total/showing/truncated/hint`，但默认排除仍需修 |
| `xref` | references 返回 `total/showing/truncated/hint`，hint 已使用 `pos` |
| `completion` | 返回 `total/showing/truncated/hint` |
| `diagnostics` | 返回 `total/showing/hint` |
| `edit` | 纯插入 patch 已可用，返回 `matched_by=pure_insertion` |

## 7. 评分影响

| 维度 | 当前影响 |
|:---|:---|
| 认知负荷 | `grep` 噪声和 `document_symbol` 字段不一致会增加二次判断成本 |
| 功能 | 核心功能可用；主要问题是默认过滤和结构化元信息 |
| 可用性 | 从仓库根搜索时容易被缓存目录污染，影响实际工作流 |

修复以上 3 项后，预计整体评分可从约 `8.8` 回到 `9.1` 左右。

## 8. 建议修复顺序

| 顺序 | 任务 | 原因 |
|:---|:---|:---|
| 1 | 修复 `grep` 默认排除 | P0，直接影响搜索可信度 |
| 2 | 统一 `document_symbol` 元字段 | P1，输出契约收口 |
| 3 | 更新 `docs/LSP使用规范.md` | P1，避免文档误导模型继续使用 `offset` |
