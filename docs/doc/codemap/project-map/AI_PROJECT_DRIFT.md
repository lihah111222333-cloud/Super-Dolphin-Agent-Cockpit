# AI 项目地图漂移报告

> 状态：**OK**
>
> 已索引文件：4316
>
> 未细分职责文件：365

## 1. 漂移指标

| 指标 | 当前值 |
|---|---:|
| 未细分职责文件数 | 365 |
| 未细分职责占比 | 8.46% |

## 2. 未细分职责分布

| 模块 | 文件数 |
|---|---:|
| `frontend-app` | 260 |
| `frontend` | 45 |
| `internal` | 24 |
| `test` | 9 |
| `third_party` | 9 |
| `cmd` | 7 |
| `.codex` | 3 |
| `deploy` | 3 |
| `filebeat` | 1 |
| `logstash` | 1 |
| `skills` | 1 |
| `sql` | 1 |
| `tests` | 1 |

## 3. 样例文件

- `.codex/.gitignore`
- `.codex/config.toml`
- `.codex/hooks.json`
- `cmd/super-dolphin-release-manifest/main.go`
- `cmd/super-dolphin-release-manifest/main_test.go`
- `cmd/super-dolphin-updater/detach_darwin.go`
- `cmd/super-dolphin-updater/detach_default.go`
- `cmd/super-dolphin-updater/install.go`
- `cmd/super-dolphin-updater/install_test.go`
- `cmd/super-dolphin-updater/main.go`
- `deploy/elk/README.md`
- `deploy/elk/docker-compose.yml`
- `deploy/elk/logstash/pipeline/super-dolphin.conf`
- `filebeat/filebeat.yml`
- `frontend-app/.env.example`
- `frontend-app/.gitignore`
- `frontend-app/.playwright-cli/console-2026-05-31T11-04-30-522Z.log`
- `frontend-app/.playwright-cli/page-2026-05-31T11-04-30-787Z.yml`
- `frontend-app/README.md`
- `frontend-app/eslint.config.js`
- `frontend-app/index.html`
- `frontend-app/jsconfig.json`
- `frontend-app/package-lock.json`
- `frontend-app/package.json`
- `frontend-app/playwright.desktop.config.js`
- `frontend-app/public/favicon.svg`
- `frontend-app/public/wails/runtime.js`
- `frontend-app/public/wails/runtime.test.js`
- `frontend-app/scripts/desktop-runner-contract.test.mjs`
- `frontend-app/scripts/desktop-smoke.mjs`
- `frontend-app/scripts/desktop-smoke.test.mjs`
- `frontend-app/scripts/desktop-ux-smoke.mjs`
- `frontend-app/scripts/desktop-ux-smoke.test.mjs`
- `frontend-app/scripts/rpc-contract-audit.mjs`
- `frontend-app/scripts/rpc-contract-audit.test.mjs`
- `frontend-app/scripts/sync-frontend-dist.mjs`
- `frontend-app/scripts/sync-frontend-dist.test.mjs`
- `frontend-app/src/App.jsx`
- `frontend-app/src/App.test.jsx`
- `frontend-app/src/AppChrome.css`
- `frontend-app/src/AppShell.css`
- `frontend-app/src/AppShellSidebarPolish.css`
- `frontend-app/src/AppShellSidebarThreadActions.css`
- `frontend-app/src/AppShellWorkbench.css`
- `frontend-app/src/SettingsPage.test.jsx`
- `frontend-app/src/adapters/apiErrorAdapter.js`
- `frontend-app/src/adapters/fileAdapter.js`
- `frontend-app/src/adapters/memoryAdapter.js`
- `frontend-app/src/adapters/observabilityAdapter.js`
- `frontend-app/src/assets/super-dolphin-logo.png`

## 4. 修复方式

优先在 `scripts/generate_ai_project_map.js` 的 `PURPOSE_RULES` 中补充路径前缀和职责说明，然后重新运行：

```bash
node scripts/generate_ai_project_map.js
```
