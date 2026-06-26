# 后端全域注释治理工作流

## 目标

使用 30 个 worker 对后端生产 Go 源码进行注释增强，按 `$注释规范` 补充维护者需要的中文函数级说明、类型说明、关键字段说明、长函数阶段路标和必要的行内解释。

## 范围

- 包含：`cmd/`、`internal/`、`pkg/`、`scripts/`、`test/fixtures/` 下的非生成生产 Go 文件。
- 排除：`frontend-app/`、`cmd/agent-terminal/frontend/`、`third_party/`、`docs/archive/`、`.agent/.agents` provider/skill mirror、worktree/cache/bin、`// Code generated ... DO NOT EDIT.` 文件。
- `_test.go` 本轮不作为 30 分区的主写集；仅在 worker 明确需要补关键测试 helper 注释时另行报告，不做机械刷注释。

## 分区

生产 Go 文件共 1119 个，按排序后的生产文件清单切成 `PARTITIONS/P00.files` 到 `PARTITIONS/P29.files`。每个 worker 只允许修改自己的分区文件和自己的报告文件。

## Gate

- Gate 0：分区文件互斥、工作流文档存在。
- Gate 1：每个 worker 只改授权文件，且每改一个 Go 文件后运行 `./scripts/test_with_guard.sh <file.go>`。
- Gate 2：父任务合并检查 `git diff --check`、变更文件单文件守卫、必要包级测试。
- Gate 3：完成前运行 `make guard` 或说明无法完成的具体 blocker。

## 当前状态

`initial_guard`: 1119 个生产 Go 文件通过单文件守卫，说明本轮目标是按更高注释规范增强可读性，而不是修复既有强制 guard 红项。
