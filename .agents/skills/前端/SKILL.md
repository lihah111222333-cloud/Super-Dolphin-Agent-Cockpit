---
name: 前端
description: 当在 super-agent-v3 中开发、审查或修复当前 React/Vite 前端 UI 时使用；前端源码只在 frontend-app。
aliases: ["@前端", "@frontend"]
---

# super-agent-v3 前端规范

## 路由

| 用户目标 | 默认路径 |
|---|---|
| 当前新 UI、React、Vite、桌面主界面、聊天/线程页面 | `frontend-app` |

`run-new-ui-desktop.sh` 使用 `frontend-app` Vite dev server；无 dev proxy 时，桌面宿主使用 `frontend-app` 构建同步到 `cmd/agent-terminal/web-dist` 的嵌入 bundle。

## React/Vite 当前 UI

常见路径：

- 页面：`frontend-app/src/pages`
- 实体状态：`frontend-app/src/entities`
- 共享 API：`frontend-app/src/shared/api`
- 全局样式：`frontend-app/src/styles.css`

原则：

1. 优先遵循现有 React 组件、store、API bridge 和 CSS 模式。
2. 不默认引入 Tailwind、shadcn 或新 UI 框架；只有仓库已有依赖或用户明确要求时才使用。
3. UI 变更要考虑 desktop 和移动宽度，不让文本和控件重叠。
4. Wails/backend bridge 错误必须 fail-fast 可见，不要静默吞掉。

验证：

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

## 常见错误

| 错误 | 修正 |
|---|---|
| 把前端源码定位到 `cmd/agent-terminal` | 当前页面、状态和 bridge 默认看 `frontend-app` |
| 为单个控件引入新 UI 框架 | 先用现有组件和 CSS |
| 只跑 build 不跑 lint/test | 按改动面运行完整前端验证 |
