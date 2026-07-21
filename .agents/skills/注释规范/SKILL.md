---
name: 注释规范
description: 当在 super-agent-v3 中编写或审查 Go 函数级中文注释、React hooks/service/controller 注释，或处理注释守卫失败时使用。
aliases: ["@注释规范", "@comments"]
---

# super-agent-v3 注释规范

注释说明代码本身看不出的职责、原因、约束和风险，不逐行复述实现。中文自然简洁，通常 1-3 行；协议字段、错误文本和日志 key 保持源码原名。

## 机器守卫

`internal/archtest/guardlib.go` 检查所有导出函数/方法和达到复杂度阈值的私有函数；阈值以守卫源码和同包测试为事实源，不在技能中复制。测试文件不进入该函数注释守卫。确需跳过时使用 `// archguard:ignore func_comment -- 具体原因`；空理由或为过守卫灌水不合格。

## 人工规范

跨模块入口、状态迁移、幂等/重试、锁/并发、恢复、权限、持久化边界，以及复杂 React hook/store/service，应说明 owner、失败语义或不能误改的约束。简单 getter/setter、纯映射、小 JSX 和直观测试 helper 不机械补注释。

## 验证

- 普通 Go 文件：`./scripts/test_with_guard.sh <file.go>`。
- 修改守卫：`./scripts/test_with_guard.sh ./internal/archtest -count=1`。
- 跨面或提交前：`make guard`。

“处理数据”“执行逻辑”、英文模板和逐行翻译代码均为无效注释，应改写为具体状态、边界、原因或风险。
