Vue 前端约定：优先沿用现有 composable、store、page 组织方式，避免把页面逻辑塞回超大 setup。

检查项：
- 改 UI 行为时同步更新对应 behavior test，尤其是 payload 字段、按钮状态和持久化 preference。
- 运行 `node scripts/size-guard.cjs`；函数超过 250 行、文件超过 800 行、嵌套过深都会被拦。
- `npm run build` 的 chunk size warning 不等于失败，但测试和 size guard 失败都必须修。
- 不要只靠截图判断状态；能用 vitest 锁住的交互优先写测试。
