---
name: TailwindCSS 样式规范
description: "仅当用户明确要求 `TailwindCSS 样式规范` 技能时使用；super-agent-v3 默认不引入 Tailwind。"
disable_model_invocation: true
aliases: ["@TailwindCSS样式规范", "@tailwind"]
---

# TailwindCSS 样式规范

super-agent-v3 的当前新 UI 是 `frontend-app` React/Vite，默认使用现有 CSS 与组件模式。

## 使用边界

- 只有用户明确要求 TailwindCSS，或 `frontend-app/package.json` / 目标 package 已经包含 Tailwind 依赖时，才应用 Tailwind 规范。
- 不要为了普通 UI 美化引入 Tailwind、shadcn 或新的构建配置。
- 如果确需引入，先说明影响范围，并同步更新 lint/test/build 验证面。
- `cmd/agent-terminal/web-dist` 是构建产物目录，不在其中手写样式或组件。

## 默认替代

普通 UI 任务优先使用 `前端` 技能和 `frontend-app/src/styles.css`；设计判断可读取 `ui-ux-design`，但实现不改变技术栈。
