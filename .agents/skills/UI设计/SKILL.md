---
name: UI设计
description: "仅当用户明确点名 `UI设计` 技能时使用。"
disable_model_invocation: true
aliases: ["@UI设计", "@ui-design", "@ui-ux-design"]
---

# UI 设计

这是 `ui-ux-design` 的 super-agent-v3 兼容入口。

## 路由规则

1. 普通 UI 开发、修复和审查先加载 `前端` 技能。
2. 默认目标是 `frontend-app` React/Vite；`cmd/agent-terminal/web-dist` 只是构建同步后的嵌入产物，不作为 UI 源码编辑。
3. 不默认引入 Tailwind、shadcn 或新 UI 框架；只有用户明确要求或仓库已有依赖时才读取 `ui-ux-design/references/ui-styling/GUIDE.md`。
4. 可使用 `ui-ux-design` 的配色、排版、布局和可用性判断，但实现必须贴合当前仓库组件和 CSS。

## 验证

- `frontend-app` 变更：`cd frontend-app && npm run lint && npm test && npm run build`
- docs/skills-only：`python3 scripts/validate_super_agent_skills.py` + `git diff --check`
