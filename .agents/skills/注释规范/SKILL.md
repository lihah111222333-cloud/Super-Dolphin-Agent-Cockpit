---
name: 注释规范
description: "仅当用户明确点名 `注释规范` 技能时使用。"
disable_model_invocation: true
aliases: ["@注释规范", "@comments"]
---

# super-agent-v3 注释规范

## 函数级中文注释

函数级中文注释写给维护系统的人看：先说明函数或关键代码块做什么，再补代码本身看不出来的原因、约束和风险。不要逐行复述实现。

必须补注释：

- 导出函数、导出方法、导出类型的关键方法。
- 跨模块入口：provider、store、scheduler、thread、prompt、memory、skill、DAG、MCP、runtime。
- 涉及状态变化、幂等、重试、锁、并发、恢复、fail-fast、权限或持久化边界。
- 私有函数如果较长、分支复杂或嵌套深，需要说明负责什么、不能误改什么。
- React hooks、store slice、service、复杂页面 controller 需要说明数据来源和本地状态边界。

不要求给简单 getter/setter、小型纯映射、小 JSX 片段、测试内直观 helper 机械补注释。

## 风格

- 中文自然简洁，优先 1-3 行。
- 先写“做什么”，必要时再写“为什么/不能乱改/失败时怎样”。
- 少用空泛工程词；除非代码领域名必须保留。
- 错误字符串、协议字段和日志 key 保持英文或源码原名。

## 守卫

函数级注释守卫由 `internal/archtest/guardlib.go` 实现。改 Go 文件后按本仓库命令验证：

```bash
./scripts/test_with_guard.sh <file.go>
make guard
./scripts/test_with_guard.sh ./internal/archtest -count=1
```

## 常见错误

| 错误 | 修正 |
|---|---|
| “处理数据”“执行逻辑”这类空话 | 写清楚处理哪个状态/边界 |
| 注释复述每行代码 | 说明维护约束和风险 |
| 英文模板搬运 | 用中文说明本仓库上下文 |
| 为过守卫给所有小函数灌水 | 只给策略要求的函数补有效注释 |
