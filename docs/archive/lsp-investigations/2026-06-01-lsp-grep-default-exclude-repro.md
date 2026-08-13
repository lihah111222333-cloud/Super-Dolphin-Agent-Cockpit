# LSP grep 默认排除未生效复现文档

> 本文记录 `mcp__lsp.grep` 在默认搜索配置下仍命中缓存目录的问题，用于实现侧复现、修复和验收。

| 项目 | 内容 |
|:---|:---|
| 日期 | 2026-06-01 |
| 复现仓库 | `/Users/mima0000/Desktop/wj/wjboot-v2` |
| 目标工具 | `mcp__lsp.grep` |
| 问题类型 | 默认排除规则未生效 |
| 当前结论 | 可稳定复现 |

## 1. 问题摘要

当 `mcp__lsp.grep` 使用较宽的 `path` 搜索 Go 文件时，默认排除规则没有过滤 `backend/.gomodcache`。结果会优先返回依赖缓存目录中的命中项，导致 Agent 在仓库级检索时拿到噪声结果。

该问题不是搜索功能不可用，而是默认搜索边界不符合 Agent 工作习惯。

## 2. 最小复现调用

| 参数 | 值 |
|:---|:---|
| 工具 | `mcp__lsp.grep` |
| `action` | `text_search` |
| `query` | `package main` |
| `path` | `backend` |
| `glob` | `*.go` |
| `max_results` | `5` |
| `work_dir` | `/Users/mima0000/Desktop/wj/wjboot-v2` |

## 3. 实际结果

本次复现返回的前 5 个结果全部来自 `backend/.gomodcache`。

| 序号 | 命中路径 | 行号 | 内容 |
|:---:|:---|:---:|:---|
| 1 | `/Users/mima0000/Desktop/wj/wjboot-v2/backend/.gomodcache/github.com/c-bata/goptuna@v0.9.0/_benchmarks/goptuna_solver/main.go` | 1 | `package main` |
| 2 | `/Users/mima0000/Desktop/wj/wjboot-v2/backend/.gomodcache/github.com/c-bata/goptuna@v0.9.0/_benchmarks/himmelblau_problem/main.go` | 1 | `package main` |
| 3 | `/Users/mima0000/Desktop/wj/wjboot-v2/backend/.gomodcache/github.com/c-bata/goptuna@v0.9.0/_benchmarks/rastrigin_problem/main.go` | 1 | `package main` |
| 4 | `/Users/mima0000/Desktop/wj/wjboot-v2/backend/.gomodcache/github.com/c-bata/goptuna@v0.9.0/_benchmarks/rosenbrock_problem/main.go` | 1 | `package main` |
| 5 | `/Users/mima0000/Desktop/wj/wjboot-v2/backend/.gomodcache/github.com/c-bata/goptuna@v0.9.0/_examples/cmaes/blackhole/main.go` | 1 | `package main` |

返回统计：

| 字段 | 值 |
|:---|:---|
| `total` | `1697` |
| `showing` | `5` |
| `truncated` | `true` |

## 4. 预期结果

同一调用在默认配置下不应返回缓存、构建产物或依赖目录中的结果。

| 目录模式 | 默认行为 |
|:---|:---|
| `.gomodcache` | 排除 |
| `.tools/gomodcache` | 排除 |
| `node_modules` | 排除 |
| `dist` | 排除 |
| `.cache` / `cache` | 排除 |
| `.git` | 排除 |

修复后，`total/showing/truncated` 也应只统计未排除目录内的结果。

## 5. 影响

| 维度 | 影响 |
|:---|:---|
| 认知负荷 | Agent 需要人工识别并丢弃缓存目录结果 |
| 可用性 | `max_results` 较小时，真实源码结果可能被缓存命中挤掉 |
| 可靠性 | 仓库级搜索的首屏结果不可直接信任 |
| 多语言场景 | Go、JS、TS、Python 等宽路径搜索都可能被依赖或构建目录污染 |

## 6. 验收标准

| 验收项 | 标准 |
|:---|:---|
| 默认搜索 | 上述最小复现调用不再返回包含 `/.gomodcache/` 的路径 |
| 结果统计 | `total` 不包含被排除目录内的命中数量 |
| 截断行为 | `max_results=5` 时，首屏结果不能被默认排除目录占满 |
| 可配置性 | 如果实现支持显式 include/exclude，默认排除仍应在未显式覆盖时生效 |

## 7. 建议修复方向

| 优先级 | 建议 |
|:---|:---|
| P0 | 在 `grep` 默认搜索层统一追加排除规则，而不是依赖调用方手动传入 |
| P0 | 排除规则应在计算 `total/showing/truncated` 前生效 |
| P1 | 在响应 `hint` 中说明默认排除已启用；仅当用户显式扩大搜索范围时提示可能包含缓存目录 |
