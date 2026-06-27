---
name: vue3
description: 当任务明确指向 super-agent-v3 的 legacy Vue/package-embed 前端 cmd/agent-terminal/frontend 时使用；不要用于当前默认 React/Vite 前端。
license: Complete terms in LICENSE.txt
---

# Vue 3 Legacy 前端技能

## 路由规则

当前默认前端是 `frontend-app` React/Vite。只有用户明确提到 legacy/package-embed 或以下任一目标时才使用本技能：

- `cmd/agent-terminal/frontend`
- legacy Vue
- package-embed / embedded bundle
- 旧 Vue 页面、旧 Wails 嵌入前端

如果用户只是说“前端”“UI”“页面”“React UI”“新 UI”，不要使用 Vue 路径；先查看 `frontend-app`。

## legacy Vue 架构约束

`cmd/agent-terminal/frontend` 使用无构建 Vue 3 ESM 方案：

1. 禁用 `.vue` SFC 文件；组件是 `.js` 文件。
2. 直接引入带 `.js` 后缀的模块或 `/lib/` 预构建 ESM。
3. 组件暴露 `setup()` 与 `template: \`...\`` 字符串模板。
4. 样式使用原生 CSS；不要为了 legacy 路径引入 Tailwind、Element Plus 或新构建链。

```javascript
import { ref } from '../lib/vue.esm-browser.prod.js';

export const MyComponent = {
  name: 'MyComponent',
  setup() {
    const count = ref(0);
    return { count };
  },
  template: `
    <button class="my-component" @click="count++">{{ count }}</button>
  `
};
```

## 验证

legacy Vue 变更使用仓库命令：

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

当前 React/Vite 前端变更使用：

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

## 常见错误

| 错误 | 修正 |
|---|---|
| 默认把前端任务路由到 Vue | 先确认是否明确 legacy；默认去 `frontend-app` |
| 在 legacy Vue 中新增 `.vue` / Vite 配置 | 保持无构建 ESM |
| 把 React 新 UI 的状态模型套到旧 Vue | 分开处理两个前端包 |
| 只 build 不跑 size/vitest | legacy 路径三条命令都要考虑 |
