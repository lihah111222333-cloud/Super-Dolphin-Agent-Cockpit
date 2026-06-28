# AI 项目地图漂移报告

> 状态：**OK**
>
> 已索引文件：5292
>
> 未细分职责文件：483

## 1. 漂移指标

| 指标 | 当前值 |
|---|---:|
| 未细分职责文件数 | 483 |
| 未细分职责占比 | 9.13% |

## 2. 未细分职责分布

| 模块 | 文件数 |
|---|---:|
| `.mypy_cache` | 258 |
| `codex-app 2` | 141 |
| `internal` | 26 |
| `.agent` | 23 |
| `test` | 9 |
| `third_party` | 9 |
| `cmd` | 8 |
| `.codex` | 3 |
| `ai04-docs` | 2 |
| `sql` | 2 |
| `filebeat` | 1 |
| `tests` | 1 |

## 3. 样例文件

- `.agent/.DS_Store`
- `.agent/report/review-2026-06-27/D01-arch-cmd.md`
- `.agent/report/review-2026-06-27/D01-arch-internal.md`
- `.agent/report/review-2026-06-27/D02-fail-fast.md`
- `.agent/report/review-2026-06-27/D03-mcp-protocol.md`
- `.agent/report/review-2026-06-27/D04-lsp-tools.md`
- `.agent/report/review-2026-06-27/D05-provider-claude.md`
- `.agent/report/review-2026-06-27/D05-provider-toolbridge.md`
- `.agent/report/review-2026-06-27/D06-orchestration.md`
- `.agent/report/review-2026-06-27/D07-store-sqlc.md`
- `.agent/report/review-2026-06-27/D08-prompt-thread.md`
- `.agent/report/review-2026-06-27/D08-skill-memory.md`
- `.agent/report/review-2026-06-27/D09-frontend.md`
- `.agent/report/review-2026-06-27/D10-security.md`
- `.agent/report/review-2026-06-27/D11-observability.md`
- `.agent/report/review-2026-06-27/D12-testing.md`
- `.agent/report/review-2026-06-27/D13-release-install.md`
- `.agent/report/review-2026-06-27/D14-performance.md`
- `.agent/report/review-2026-06-27/D15-ux-product.md`
- `.agent/report/review-2026-06-27/D16-git-workflow.md`
- `.agent/report/review-2026-06-27/D17-field-guard.md`
- `.agent/report/review-2026-06-27/FIX-SUMMARY.md`
- `.agent/report/review-2026-06-27/SUMMARY.md`
- `.codex/.gitignore`
- `.codex/config.toml`
- `.codex/hooks.json`
- `.mypy_cache/.DS_Store`
- `.mypy_cache/.gitignore`
- `.mypy_cache/3.14/@plugins_snapshot.json`
- `.mypy_cache/3.14/__future__.data.json`
- `.mypy_cache/3.14/__future__.meta.json`
- `.mypy_cache/3.14/_ast.data.json`
- `.mypy_cache/3.14/_ast.meta.json`
- `.mypy_cache/3.14/_blake2.data.json`
- `.mypy_cache/3.14/_blake2.meta.json`
- `.mypy_cache/3.14/_bz2.data.json`
- `.mypy_cache/3.14/_bz2.meta.json`
- `.mypy_cache/3.14/_codecs.data.json`
- `.mypy_cache/3.14/_codecs.meta.json`
- `.mypy_cache/3.14/_collections_abc.data.json`
- `.mypy_cache/3.14/_collections_abc.meta.json`
- `.mypy_cache/3.14/_contextvars.data.json`
- `.mypy_cache/3.14/_contextvars.meta.json`
- `.mypy_cache/3.14/_decimal.data.json`
- `.mypy_cache/3.14/_decimal.meta.json`
- `.mypy_cache/3.14/_frozen_importlib.data.json`
- `.mypy_cache/3.14/_frozen_importlib.meta.json`
- `.mypy_cache/3.14/_frozen_importlib_external.data.json`
- `.mypy_cache/3.14/_frozen_importlib_external.meta.json`
- `.mypy_cache/3.14/_hashlib.data.json`

## 4. 修复方式

优先在 `scripts/generate_ai_project_map.js` 的 `PURPOSE_RULES` 中补充路径前缀和职责说明，然后重新运行：

```bash
node scripts/generate_ai_project_map.js
```
