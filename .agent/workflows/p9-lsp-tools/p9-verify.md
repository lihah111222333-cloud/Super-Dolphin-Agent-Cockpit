---
description: Agent V — 全量验证
---

# P9: Agent V — 验证

## 前置条件
- [ ] P0-P8 全部完成

## 验证清单

### 1. 编译验证
- `go build ./cmd/mcp-lsp/...`
- `go build ./cmd/mcp-lsp/...`

### 2. 静态分析
- `go vet ./cmd/mcp-lsp/...`
- `lsp_file(diagnostics)` 全量扫描

### 3. archtest 守卫
- `go test -run TestCodeSizeGuard ./internal/archtest/... -v`
- `go test -run TestMCPLSPDependencyDirection ./internal/archtest/... -v` （新增：依赖方向）
- `go test -run TestMCPLSPExactToolSet ./internal/archtest/... -v` （新增：工具集完整性）
- `go test -run TestMCPFamilyIsolation ./internal/archtest/... -v`

### 4. 全量单元测试
- `go test ./cmd/mcp-lsp/... -v`

### 5. Schema 快照验证
- 9 个工具的参数 schema 与 §3 契约定义一致

### 6. 每工具 happy path 冒烟
- lsp_file: open_file + read_file 单文件 + read_file 批量 + diagnostics
- lsp_inspect: hover + definition + implementation
- lsp_xref: references compact + call_hierarchy
- lsp_grep: text_search + ast_search
- lsp_structure: document_symbol + workspace_symbol
- lsp_edit: replace_range + rename + format
- lsp_completion: compact 模式
- code_run: project_cmd 模式
- code_run_test: go test 模式

### 7. 容错矩阵验证
- T1-T9 截断/预算控制
- F1-F3 大文件保护
- R2-R3 错误恢复
- H1-H3 健康检查

### 8. 修复职责
- V Agent 发现的问题如可快速修复（<10行），直接修
- 复杂问题记录并反馈给对应 Agent

## 完成标准
- [ ] 全部编译通过
- [ ] go vet 零警告
- [ ] archtest 全绿
- [ ] 全量测试通过
- [ ] 冒烟测试通过
