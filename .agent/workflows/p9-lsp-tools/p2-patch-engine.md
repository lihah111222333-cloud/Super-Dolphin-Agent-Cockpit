---
description: Agent B — Patch 引擎 (patchparse+patchmatch+seeksequence+replaceutil)
---

# P2: Agent B — Patch引擎 (~776行, 4文件)

## 前置条件
- 无（第一批并行）

## 任务范围

### 需要创建的文件
- `cmd/mcp-lsp/edit/patchparse.go` (~230) — Parse + ParseMulti
- `cmd/mcp-lsp/edit/patchmatch.go` (~190) — 多 hunk 匹配 + 歧义检测
- `cmd/mcp-lsp/edit/seeksequence.go` (~146) — 4-pass: exact→trimRight→trimBoth→unicodeNorm
- `cmd/mcp-lsp/edit/replaceutil.go` (~210) — offset→行号 + 替换预览

### 禁止触碰的文件 ⚠️
- `protocol/`, `format/`, `gopls/`, `tools/`, `search/`, `exec/`, `middleware/`

## 关键共识
- 共识#5: patch 三件套完整实现
- 共识#6: seek_sequence 4-pass 全量（exact/trimRight/trimBoth/unicodeNorm）

## 完成标准
- [ ] `go build ./cmd/mcp-lsp/edit/...` 通过
- [ ] 4-pass seek_sequence 全量实现 + 测试
- [ ] ParseMulti 支持多 hunk

## 验证命令
```bash
go build ./cmd/mcp-lsp/edit/...
go test ./cmd/mcp-lsp/edit/... -v
```
