# AI 项目地图漂移报告

> 状态：**OK**
>
> 已索引文件：4896
>
> 未细分职责文件：380

## 1. 漂移指标

| 指标 | 当前值 |
|---|---:|
| 未细分职责文件数 | 380 |
| 未细分职责占比 | 7.76% |

## 2. 未细分职责分布

| 模块 | 文件数 |
|---|---:|
| `frontend-app` | 260 |
| `frontend` | 45 |
| `internal` | 24 |
| `test` | 9 |
| `third_party` | 9 |
| `.codex-run` | 7 |
| `cmd` | 7 |
| `.superpowers` | 6 |
| `.codex` | 5 |
| `deploy` | 3 |
| `filebeat` | 1 |
| `logstash` | 1 |
| `skills` | 1 |
| `sql` | 1 |
| `tests` | 1 |

## 3. 样例文件

- `.codex-run/chat-layout-check.png`
- `.codex-run/launcher.err.log`
- `.codex-run/launcher.log`
- `.codex-run/manual-bash-launcher.err.log`
- `.codex-run/manual-bash-launcher.log`
- `.codex-run/manual-launcher.err.log`
- `.codex-run/manual-launcher.log`
- `.codex/.gitignore`
- `.codex/config.toml`
- `.codex/hooks.json`
- `.codex/vite-ui-refactor.err.log`
- `.codex/vite-ui-refactor.out.log`
- `.superpowers/brainstorm/1243779-1780164281/content/ready.html`
- `.superpowers/brainstorm/1243779-1780164281/content/visual-direction.html`
- `.superpowers/brainstorm/1243779-1780164281/content/waiting-done.html`
- `.superpowers/brainstorm/1243779-1780164281/state/server-stopped`
- `.superpowers/brainstorm/1243779-1780164281/state/server.log`
- `.superpowers/brainstorm/1243779-1780164281/state/server.pid`
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

## 4. 修复方式

优先在 `scripts/generate_ai_project_map.js` 的 `PURPOSE_RULES` 中补充路径前缀和职责说明，然后重新运行：

```bash
node scripts/generate_ai_project_map.js
```
