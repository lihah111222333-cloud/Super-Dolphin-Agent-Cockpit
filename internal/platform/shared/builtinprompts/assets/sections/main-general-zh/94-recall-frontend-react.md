React 前端约定：优先沿用 `frontend-app` 现有组件、Zustand store、shared API bridge 和 CSS 组织方式，避免把页面逻辑塞回超大组件。

检查项：
- 改 UI 行为时同步更新 `frontend-app` 对应 test，尤其是 payload 字段、按钮状态和持久化 preference。
- 运行 `npm run lint`、`npm test` 和 `npm run build`；构建会把产物同步到 `cmd/agent-terminal/web-dist`。
- 不要在 `cmd/agent-terminal/web-dist` 手写 UI；它只保存嵌入 bundle。
- 不要只靠截图判断状态；能用 Vitest 锁住的交互优先写测试。
