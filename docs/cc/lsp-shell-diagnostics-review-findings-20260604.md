# mcp-lsp shell diagnostics 文档审查 Findings（2026-06-04）

## 审查范围

- 目标文档：`docs/cc/lsp-shell-diagnostics-root-cause-20260604.md`
- 本文件：`docs/cc/lsp-shell-diagnostics-review-findings-20260604.md`
- 证据范围：当前源码、当前 root-cause 文档与当前 findings 文档；不依赖旧报告、历史计划或不可复核的旧审查过程细节。
- 本次修正版范围：仅修正上述两个文档，未修改源码、测试或构建配置。

## 当前修复决策表

| 拒绝意见 | 当前可复核证据 | 本次处理 |
|---|---|---|
| root-cause 已包含非回归约束，findings 不应继续列为缺失项 | root-cause 的“修复方向（未在本次任务中实施）”第 6 条已要求扩展 shell 支持时保留并扩展既有多语言/打包防护 | findings 改为“已在 root-cause 中保留该边界，后续实现时继续扩展”，不再作为未完成建议 |
| findings 不应用当前 root-cause 行号证明修复前文本 | 当前源码证据可由 `internal/sidecar/lsp/tools/tool_diagnostics.go` 与 `internal/sidecar/lsp/manager/registry.go` 复核；修复前描述只能作为历史状态概述 | 删除“当前文件行号 = 修复前证据”的写法；当前判断改用源码锚点或 root-cause 章节名 |
| findings 不应保留不可稳定复核的旧过程性结论 | 旧审查过程统计无法由当前源码与当前两个文档复核 | 改为当前修复决策表与 supported findings；如需决策，只记录本次文档修复决策 |
| 保持范围边界 | root-cause 仍明确“修复方向（未在本次任务中实施）”；本 worktree 未改源码 | 不声称已经实现 shell diagnostics，不新增源码改动，不扩大到实现方案 |

## 统一 supported findings

### 1. diagnostics 调用顺序已按当前源码校正

修复前版本曾把 `file.diagnostics` 描述为先等待/读取 diagnostics、空结果后再 reactive bootstrap。该历史描述不再作为当前事实，也不再挂当前 root-cause 行号。

当前源码顺序是：URI 收集 → `existingDiagnosticURIs` → `bootstrapDiagnostics/reactiveBootstrap` → `registry.BootstrapDocument` → `waitDiagnosticsWithStartupRecovery/WaitDiagnosticsStable` → `registry.Diagnostics`。

可复核源码锚点：

- `fetchDiagnosticsWithRetry()` 先取 `existingDiagnosticURIs()`，再 `bootstrapDiagnostics()`，再等待稳定，最后 `registry.Diagnostics()`：`internal/sidecar/lsp/tools/tool_diagnostics.go:47-82`
- `collectDiagnosticURIs()` 负责显式目标到 URI 的收集：`internal/sidecar/lsp/tools/tool_diagnostics.go:194-224`
- `existingDiagnosticURIs()` 只保留真实存在、普通文件、非 symlink URI：`internal/sidecar/lsp/tools/tool_diagnostics.go:239-251`
- `reactiveBootstrap()` 对 existing URI 调 `registry.BootstrapDocument(ctx, uri)`：`internal/sidecar/lsp/tools/tool_diagnostics.go:333-352`
- `BootstrapDocument()` 通过 `DetectLanguageID(path)` 进入 manager resolution：`internal/sidecar/lsp/manager/registry.go:300-306`
- `resolveManagerForTarget()` 对未注册语言返回 `ErrUnsupportedLanguage`：`internal/sidecar/lsp/manager/registry.go:147-156`

决策：root-cause 与本 findings 均保留这条当前顺序；它是文档准确性修复，不代表 shell diagnostics 已实现。

### 2. `.sh` 的可达性与防护边界已收窄为当前源码事实

当前事实：

- 显式传入且真实存在的 `.sh` 会被收集为 URI，并因存在且是普通非 symlink 文件进入 bootstrap：`internal/sidecar/lsp/tools/tool_diagnostics.go:194-224`，`internal/sidecar/lsp/tools/tool_diagnostics.go:239-251`
- `.sh` 没有扩展映射时 fallback 为 `sh`：`internal/sidecar/lsp/manager/registry.go:250-259`
- `sh` 未注册 manager 时在 `resolveManagerForTarget()` 返回 unsupported：`internal/sidecar/lsp/manager/registry.go:147-156`
- `BootstrapDocument()` 发生在读取 diagnostics 之前，因此显式存在的 `.sh` 会先在 bootstrap 的 manager resolution 失败：`internal/sidecar/lsp/manager/registry.go:300-306`，`internal/sidecar/lsp/tools/tool_diagnostics.go:47-82`，`internal/sidecar/lsp/tools/tool_diagnostics.go:333-352`
- 无显式目标时没有 URI 可 bootstrap；显式目标不存在时不会进入 `existingDiagnosticURIs()` 返回的 bootstrap 列表：`internal/sidecar/lsp/tools/tool_diagnostics.go:194-224`，`internal/sidecar/lsp/tools/tool_diagnostics.go:239-251`
- 下游 `groupURIsByManager()` 对 unsupported URI 的 skip 不是这个上层失败路径的有效防护，因为显式存在目标已先在 `BootstrapDocument()` 失败：`internal/sidecar/lsp/manager/registry.go:377-391`

决策：该边界已在 root-cause 的 diagnostics 调用链、根因细节和可达性段落中保留；findings 不再扩大为“所有 shell 目标都会 bootstrap”或“下游 skip 已防住”。

### 3. 非回归约束已在 root-cause 中保留，不再作为未完成文档缺口

当前 root-cause 的“修复方向（未在本次任务中实施）”第 6 条已经写明：扩展 shell 支持时，应保留并扩展现有多语言/打包防护，避免破坏 Go、JSTS、Python、Rust、Java、CSS 和文档 fallback 既有链路。

这意味着：

- 非回归约束是后续实现 shell 支持时必须继续扩展的边界。
- 它不是当前运行时已经发生的回归。
- 它也不是当前 root-cause 文档仍缺失的建议项。

决策：findings 仅把该项记录为“已保留边界，后续实现时继续扩展”，不再要求在 root-cause 中再次补同一条约束。

### 4. 当前文档仍保持“未实施 shell diagnostics”的边界

当前两个文档只记录根因、可达性、证据和后续实现边界；没有声称已经接入 shell language adapter、installer、bundle manifest 或 shellcheck/shell LSP 后端。

决策：本次修复不改源码、不新增测试、不改变运行时行为；后续如果要真正支持 shell diagnostics，需要另行实施并补回归测试。

## 已确认但不需要修改的结论

- shell diagnostics 当前失败点在 manager resolution / bootstrap 链路，不是 shell 语法诊断结果为空。
- 非打包 installer、打包 bundle 与 runtime language 映射未接入 shell；当前文档将其作为根因证据，而不是已完成修复。
- 下游 unsupported skip 只能说明 diagnostics 分组阶段会跳过 unsupported URI，不能抵消显式存在 `.sh` 在更早 bootstrap 阶段的失败。
- 当前 findings 不再引用旧报告编号或不可在当前文档/源码中复核的过程性结论。

## 建议的最小后续改动（仅未完成边界/维护性）

- 后续真正实现 shell diagnostics 时，扩展非回归测试/guard，覆盖新增 shell 语言链路同时保护现有 Go、JSTS、Python、Rust、Java、CSS 和文档 fallback。
- 可选维护性改进：若后续维护者需要更低定位成本，可继续给 root-cause 的 adapter、installer、bundle 段落补更细源码行号锚点。
- 继续保留“修复方向未在本次任务中实施”的边界，避免把根因文档误读为已完成实现。
