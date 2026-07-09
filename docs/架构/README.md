# super-agent-v3 架构骨架文档

本目录记录当前实现采用的框架骨架。它不是历史迁移方案，也不是生成地图；它负责说明哪些框架在当前仓库中承担长期职责，以及这些框架之间如何分工。

源码、测试和 LSP 证据仍是事实来源。`docs/doc/codemap`、project-map 和 capability-contract 用来定位、索引和发现漂移；发现生成物过期时应修生成器或运行刷新入口，不直接手改生成输出。

## 框架清单

| 框架 | 文档 | 主要职责 |
| --- | --- | --- |
| Fx DI | `skeleton-fx.md` | `fx.Module`、constructor、lifecycle hook 和模块装配边界 |
| Runner / run.Group | `skeleton-rungroup.md`、`fx-rungroup-skeleton.md` | 长跑组件注册、退出传播、Fx 与 runner 的分工 |
| JSON-RPC | `skeleton-jrpc2.md` | `internal/platform/rpc` handler 聚合、typed handler、push 和错误语义 |
| Event Surface | `skeleton-event.md` | typed event、bus、前端推送面和 payload 字段守卫 |
| Stateless | `skeleton-stateless.md` | 平台状态机、状态外置存储和事件集成 |
| Code Guard | `skeleton-code-guard.md` | 后端/前端守卫、hook gate、codemap、project-map 和 capability-contract 漂移检查 |

## 阅读规则

1. 先用本 README 确定框架，再打开对应骨架文档。
2. 需要代码路径时，先看 `docs/doc/codemap/README.md` 和相关分卷，再用 LSP 定位源码、引用和诊断。
3. `docs/契约/*.md` 约束具体工程行为；本目录解释框架分工。两者冲突时，以源码/测试/LSP 证据校准并同时修正文档。
4. `docs/plans/**`、`docs/迁移/**`、`docs/superpowers/plans/**` 和旧报告只用于追溯，不作为当前架构入口。

## 验证

文档-only 改动至少运行：

```bash
git diff --check -- docs/契约 docs/架构
```

涉及代码守卫、codemap、project-map 或 capability-contract 的说明变更，追加：

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```
