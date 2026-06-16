# LSP 修复后残留问题复现文档

> 本文记录 2026-06-01 修复后重新评估时仍可复现的残留问题。旧的 `grep` 默认排除和 `document_symbol` 截断字段问题已明显改善，本文只记录当前仍影响评分的项。

| 项目 | 内容 |
|:---|:---|
| 复测日期 | 2026-06-01 |
| 目标仓库 | `/Users/mima0000/Desktop/wj/super-agent-v3` |
| 对照仓库 | `/Users/mima0000/Desktop/wj/wjboot-v2` |
| 工具来源 | `mcp__lsp` |
| 复测范围 | `file` / `grep` / `structure` / `inspect` / `xref` / `completion` / `edit` |
| 当前结论 | 工具本体约 `8.9-9.0`，仍有 4 个残留项 |

## 1. 总览

| 优先级 | 问题 | 状态 | 影响 |
|:---|:---|:---|:---|
| P1 | 提示文档仍保留 `read_file(offset=...)` 旧用法 | 可复现 | 文档与工具契约冲突，增加误调用概率 |
| P1 | `grep text_search` 截断结果缺少 `hint` | 可复现 | 截断时缺少下一步提示，输出契约未完全统一 |
| P2 | `xref` 光标误用时错误分类成 `file_not_found` | 可复现 | 误导模型检查路径，而不是调整光标位置 |
| P2 | Rust 无 Cargo 项目临时文件的 LSP 能力不稳定 | 可复现 | 多语言评分受影响，诊断/hover/completion 不完整 |

## 2. 已确认修复项

| 项 | 复测结论 |
|:---|:---|
| `grep` 默认排除 | 根目录搜索 `defineConfig` 不再命中 `.gomodcache` 或 `.tools/gomodcache` |
| `structure document_symbol` 截断字段 | 已返回 `data/total/showing/truncated/hint` |
| `file offset` 工具入口 | 已明确拒绝，并提示使用 `pos="file:line"` |
| `edit replace_range` 纯插入 | 已可用，返回 `matched_by=pure_insertion` |
| `edit no_change` | 已区分 `status=no_change` |
| `edit rename` | 在真实 Go package 中可跨引用改名 |

## 3. P1: 提示文档仍保留 offset 旧用法

### 3.1 复现方式

| 步骤 | 内容 |
|:---|:---|
| 工作目录 | `/Users/mima0000/Desktop/wj/wjboot-v2` |
| 检索命令 | `rg -n "offset=|read_file\\(offset|read_file offset" docs/LSP使用规范.md system-prompt.md` |

### 3.2 实际结果

| 文件 | 行号 | 问题 |
|:---|:---:|:---|
| `/Users/mima0000/Desktop/wj/wjboot-v2/system-prompt.md` | 72 | 仍写 `read_file(offset=func_start, limit=func_end-func_start+1)` |
| `/Users/mima0000/Desktop/wj/wjboot-v2/docs/LSP使用规范.md` | 72 | 仍写 `read_file(offset=func_start, limit=func_end-func_start+1)` |

### 3.3 对照事实

| 调用 | 实际结果 |
|:---|:---|
| `file action=read_file file_path=<go file> offset=104 limit=8` | `success=false` |
| 错误信息 | `offset is removed; use pos="file_path:104" instead` |
| `file action=read_file pos=<go file>:104 limit=8` | 正常返回函数范围，并按 `limit` 截断 |

### 3.4 预期结果

| 项 | 要求 |
|:---|:---|
| 文档示例 | 全部改为 `file action=read_file pos=<file>:<func_start> limit=<func_end-func_start+1>` |
| 搜索验收 | `rg "offset=" docs/LSP使用规范.md system-prompt.md` 不再命中旧入口 |
| 工具契约 | 文档只保留 `pos` 语法，不再引导模型使用已删除参数 |

## 4. P1: grep 截断结果缺少 hint

### 4.1 复现调用

| 字段 | 值 |
|:---|:---|
| 工具 | `mcp__lsp.grep` |
| action | `text_search` |
| query | `defineConfig` |
| path | `/Users/mima0000/Desktop/wj/wjboot-v2` |
| glob | `*.js` |
| max_results | `5` |

### 4.2 实际结果

| 字段 | 实际 |
|:---|:---|
| 返回路径 | 只包含项目路径，未再命中 `.gomodcache` |
| `total` | `12` |
| `showing` | `5` |
| `truncated` | `true` |
| `hint` | 缺失 |

### 4.3 预期结果

| 字段 | 预期 |
|:---|:---|
| `truncated` | `true` |
| `hint` | 必须出现，提示提高 `max_results`、缩小 `path/glob` 或继续更精确搜索 |
| 契约一致性 | 与 `workspace_symbol`、`completion`、`xref references` 的截断反馈保持一致 |

### 4.4 验收标准

| 验收项 | 要求 |
|:---|:---|
| 截断搜索 | 任意 `truncated=true` 的 `grep text_search` 响应都带 `hint` |
| 非截断搜索 | `hint` 可省略，但 `total/showing` 应保持清晰 |

## 5. P2: xref 光标误用时错误分类不准

### 5.1 复现调用

| 字段 | 值 |
|:---|:---|
| 工具 | `mcp__lsp.xref` |
| action | `call_hierarchy` |
| direction | `outgoing` |
| pos | `/Users/mima0000/Desktop/wj/super-agent-v3/cmd/mcp-lsp/tools/tool_file.go:131:21` |
| max_results | `8` |

### 5.2 实际结果

| 字段 | 实际 |
|:---|:---|
| success | `false` |
| error | `identifier not found` |
| code | `file_not_found` |
| hint | `Verify file_path is under the trusted workspace and exists on disk.` |

问题点：文件真实存在，错误原因是光标没有落在可解析标识符上。`file_not_found` 和路径校验提示会误导模型检查文件路径。

### 5.3 对照调用

| pos | 结果 |
|:---|:---|
| `/Users/mima0000/Desktop/wj/super-agent-v3/cmd/mcp-lsp/tools/tool_file.go:131:25` | `call_hierarchy` 正常返回 `handleFile` 的 outgoing 调用 |

### 5.4 预期结果

| 字段 | 预期 |
|:---|:---|
| code | `identifier_not_found` 或 `invalid_position` |
| hint | 提示把光标移动到函数名、类型名、变量名等标识符内部 |
| meta | 可选返回 `file_path`、`line`、`column`，但不应归类为文件不存在 |

### 5.5 验收标准

| 验收项 | 要求 |
|:---|:---|
| 文件存在但光标无标识符 | 不返回 `file_not_found` |
| hint | 明确指导调整 `pos` 列号 |
| 对照调用 | 光标移到同一行真实标识符内部后正常成功 |

## 6. P2: Rust 临时文件 LSP 能力不稳定

### 6.1 复现准备

| 步骤 | 内容 |
|:---|:---|
| 工作目录 | `/Users/mima0000/Desktop/wj/super-agent-v3` |
| 临时文件 | `/Users/mima0000/Desktop/wj/super-agent-v3/docs/li/lsp_probe_eval.rs` |
| 文件内容 | 一个 `struct` 加一个接收该结构体引用并返回字符串的函数 |
| 清理要求 | 复现后删除该临时文件 |

### 6.2 复现调用与实际结果

| 工具调用 | 实际结果 |
|:---|:---|
| `file action=open_file pos=<temp rs file>` | 成功 |
| `file action=read_file pos=<temp rs file>:6 limit=30` | 成功，返回 `[scope=function describe_user L6-L8]` |
| `structure action=document_symbol file_path=<temp rs file>` | 成功，返回 `ProbeUser` 和 `describe_user` |
| `file action=diagnostics pos=<temp rs file>` | 失败，`diagnostics not ready`，`code=lsp_unavailable`，`retryable=true` |
| `inspect action=hover pos=<temp rs file>:6:8` | `no hover info available` |
| `completion pos=<temp rs file>:7:10` | 空结果 |

### 6.3 预期结果

| 场景 | 预期 |
|:---|:---|
| 无 Cargo 项目临时文件 | 如果语言服务器无法完整支持，应返回稳定、明确的 `unsupported_workspace` 或 `lsp_unavailable` 说明 |
| diagnostics 未发布 | `hint` 应说明 Rust 文件可能需要 Cargo workspace，或建议在真实 Rust crate 内复测 |
| hover/completion 为空 | 返回原因应可区分“确实无结果”和“语言服务能力未初始化/无项目上下文” |

### 6.4 验收标准

| 验收项 | 要求 |
|:---|:---|
| 临时 Rust 文件 | `read_file` 和 `document_symbol` 可继续成功 |
| 诊断失败 | 错误信息说明是否因无 Cargo workspace 或 rust-analyzer 未就绪 |
| hover/completion | 空结果带 `meta.message`，说明空结果原因 |
| 多语言评分 | Rust 真实 crate 与临时文件场景分开计分 |

## 7. 评分影响

| 维度 | 影响 |
|:---|:---|
| 认知负荷 | 旧 `offset` 文档和错误分类会让模型走错修复路径 |
| 功能 | 核心功能已接近完整，主要剩余在边界场景和提示一致性 |
| 可用性 | 截断无 `hint`、Rust 空结果无原因，会降低连续操作效率 |

## 8. 建议修复顺序

| 顺序 | 问题 | 理由 |
|:---:|:---|:---|
| 1 | 更新 `system-prompt.md` 与 `docs/LSP使用规范.md` 的 `offset` 旧用法 | 成本最低，直接降低误调用 |
| 2 | 给所有 `grep truncated=true` 响应补 `hint` | 输出契约收口，收益稳定 |
| 3 | 修正 `xref identifier not found` 的错误分类 | 避免误导模型检查路径 |
| 4 | 明确 Rust 无项目上下文时的诊断/hover/completion 反馈 | 提升多语言边界可解释性 |

## 9. 当前评分参考

| 项 | 分数 |
|:---|:---:|
| 工具本体综合 | `8.9-9.0` |
| Go | `9.2` |
| JS | `8.9` |
| TS | `9.1` |
| Python | `8.8` |
| Rust | `7.8` |

修复以上残留后，工具本体综合评分预计可稳定到 `9.1` 左右；Rust 评分取决于是否将“无 Cargo 项目临时文件”定义为支持场景。
