# AI 项目地图漂移报告

> 状态：**OK**
>
> 已索引文件：2880
>
> 未细分职责文件：91

## 1. 漂移指标

| 指标 | 当前值 |
|---|---:|
| 未细分职责文件数 | 91 |
| 未细分职责占比 | 3.16% |

## 2. 未细分职责分布

| 模块 | 文件数 |
|---|---:|
| `internal` | 68 |
| `cmd` | 7 |
| `third_party` | 7 |
| `.project-map` | 5 |
| `.codex` | 3 |
| `sql` | 1 |

## 3. 样例文件

- `.codex/logs/stop-gate-20260616200625.log`
- `.codex/logs/stop-gate-20260616200852.log`
- `.codex/logs/stop-gate-20260616200947.log`
- `.project-map/PROJECT_MAP.md`
- `.project-map/imports.tsv`
- `.project-map/packages.tsv`
- `.project-map/project-map.json`
- `.project-map/symbols.tsv`
- `cmd/.DS_Store`
- `cmd/sqlitepackagesmoke/main.go`
- `cmd/super-dolphin-release-manifest/main.go`
- `cmd/super-dolphin-updater/detach_darwin.go`
- `cmd/super-dolphin-updater/detach_default.go`
- `cmd/super-dolphin-updater/install.go`
- `cmd/super-dolphin-updater/main.go`
- `internal/guards/guard_manifest.json`
- `internal/guards/refactor_baseline.json`
- `internal/sidecar/lsp/edit/doc.go`
- `internal/sidecar/lsp/edit/patchmatch.go`
- `internal/sidecar/lsp/edit/patchparse.go`
- `internal/sidecar/lsp/edit/patchparse_test.go`
- `internal/sidecar/lsp/edit/replaceutil.go`
- `internal/sidecar/lsp/edit/seeksequence.go`
- `internal/sidecar/lsp/format/compact.go`
- `internal/sidecar/lsp/format/display.go`
- `internal/sidecar/lsp/format/doc.go`
- `internal/sidecar/lsp/format/funcrange.go`
- `internal/sidecar/lsp/format/render.go`
- `internal/sidecar/lsp/installer/doc.go`
- `internal/sidecar/lsp/installer/installer.go`
- `internal/sidecar/lsp/installer/installer_test.go`
- `internal/sidecar/lsp/internal/hiddenexec/doc.go`
- `internal/sidecar/lsp/internal/hiddenexec/process.go`
- `internal/sidecar/lsp/internal/hiddenexec/process_default.go`
- `internal/sidecar/lsp/internal/hiddenexec/process_windows.go`
- `internal/sidecar/lsp/manager/doc.go`
- `internal/sidecar/lsp/manager/manager.go`
- `internal/sidecar/lsp/manager/registry.go`
- `internal/sidecar/lsp/manager/scope.go`
- `internal/sidecar/lsp/middleware/budget.go`
- `internal/sidecar/lsp/middleware/budget_hints.go`
- `internal/sidecar/lsp/middleware/doc.go`
- `internal/sidecar/lsp/middleware/logging.go`
- `internal/sidecar/lsp/middleware/recovery.go`
- `internal/sidecar/lsp/middleware/timeout.go`
- `internal/sidecar/lsp/protocol/codec.go`
- `internal/sidecar/lsp/protocol/codec_test.go`
- `internal/sidecar/lsp/protocol/doc.go`
- `internal/sidecar/lsp/protocol/ext.go`
- `internal/sidecar/lsp/protocol/methods.go`

## 4. 修复方式

优先在 `scripts/generate_ai_project_map.js` 的 `PURPOSE_RULES` 中补充路径前缀和职责说明，然后重新运行：

```bash
node scripts/generate_ai_project_map.js
```
