# 测试体系分析

## 1. 本阶段目标

分析测试框架、命令、覆盖范围、CI 流程、风险缺口和补齐路线。

## 2. 已读取文件

- `Makefile`
- `.github/workflows/ci.yml`
- `frontend-app/package.json`
- `cmd/agent-terminal/frontend/package.json`
- `scripts/test_with_guard.sh`
- `scripts/code_size_guard.go`
- `internal/archtest/*`
- `frontend-app/src/*.test.*`
- `internal/sidecar/orch/tools/*_test.go`
- `cmd/mcp-lsp/*_test.go`

## 3. 关键发现

- Go 测试通过 `./scripts/test_with_guard.sh` 与 `make test` 包装，CI 中还执行 `go vet ./...` 和 `go build ./...`。
- 前端新 UI 使用 Vitest、Testing Library、ESLint、Vite build；CI 对 `frontend-app` 独立执行 lint/test/build。
- legacy Vue 前端使用 Vitest/Playwright，并在 prebuild/pretest 执行 size guard。
- 仓库存在约 1291 个 Go/JS 测试文件或测试脚本，覆盖 backend、provider、mcp-orch、mcp-lsp、frontend、scripts、archtest。
- 架构和大小约束由 `internal/archtest`、`scripts/code_size_guard.go`、frontend legacy `size-guard.cjs` 等承担。

## 4. 证据说明

| 结论 | 文件 |
|---|---|
| `make test` 运行 frontend build 后再跑 guarded Go tests | `Makefile` |
| CI 包含 commit guard、Go build/test/lint、frontend-app lint/test/build | `.github/workflows/ci.yml` |
| 新 UI 测试命令是 `vitest run`，lint 是 `eslint .` | `frontend-app/package.json` |
| legacy 前端 pretest/prebuild 运行 size guard | `cmd/agent-terminal/frontend/package.json` |
| mcp-orch 工具和 mcp-lsp 有大量专门测试 | `internal/sidecar/orch/tools/*_test.go`、`cmd/mcp-lsp/*_test.go` |

## 5. 风险与问题

- P1：全量测试依赖前端构建和平台库，Windows 本地可能比 CI 更容易遇到环境差异。
- P1：超大前端文件虽有测试，但局部修改仍可能需要较宽的回归选择。
- P2：文档扫描未运行测试；本次无代码变更，合理跳过。

## 6. 无法判断的信息

- 无法判断测试覆盖率 `67.4%` 是否仍与当前 HEAD 完全一致；本次未执行 coverage。
- 无法判断是否存在真实端到端人工 UAT 记录。

## 7. 下一阶段建议

继续运维部署与可观测性分析，重点看启动脚本、环境变量、日志、trace、metrics、ELK 和发布回滚。
