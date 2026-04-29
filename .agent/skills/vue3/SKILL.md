---
name: vue3
description: Vue 3 核心技能（适配 V3 无构建 ESM 架构）。用于 Vue3 基础语法、响应式状态管理、组件设计以及基于字符串模板的原生组件封装。本技能强制遵循 V3 仓库的无打包工具 (Buildless) 约定。
license: Complete terms in LICENSE.txt
---

## 何时使用此技能

当用户需要在 V3 仓库的前端进行开发时加载：
- 编写或修改前端 Vue 3 JS 组件 (`cmd/agent-terminal/frontend/vue-app/**/*.js`)
- 需要查询 Vue 3 组合式 API (Composition API) 最佳实践
- 进行前端状态管理、组件拆分与复用
- 处理前端生命周期、事件处理和原生 HTML/CSS UI 渲染

## V3 架构强制契约 (MUST READ)

> [!WARNING]
> 本项目的 Vue 3 前端采用了**无构建 (Buildless)** 的原生 ES Modules 方案。严禁使用任何需要打包编译的技术。

1. **禁用 `.vue` SFC 文件**：所有组件均应是 `.js` 文件。
2. **纯 ESM 引入**：必须直接引入带 `.js` 后缀的模块或由 `/lib/` 提供的预构建 ESM。
   ```javascript
   // ✅ 正确导入方式
   import { ref, reactive, computed } from '../lib/vue.esm-browser.prod.js';
   import { useThreadStore } from '../stores/threads.js';
   ```
3. **基于字符串模板的组件结构**：组件必须暴露包含 `setup()` 和 `template: \`...\`` 的对象。
   ```javascript
   export const MyComponent = {
     name: 'MyComponent',
     props: { ... },
     setup(props, { emit }) {
       const count = ref(0);
       return { count };
     },
     template: `
       <div class="my-component">
         <span>{{ count }}</span>
       </div>
     `
   };
   ```
4. **禁用重量级 UI 框架**：**严禁使用 Element Plus、Tailwind CSS** 等框架。所有样式应当手写原生 CSS，并存放于 `styles/` 目录或直接添加到 `styles.css`，使用原生的 Semantic HTML 和 Vanilla CSS。

## 路径约定

- 技能根目录为 `.agent/skills/vue3/`。
- 本文档内部链接统一使用相对路径（如 `./examples/getting-started/`）。

## 核心指导与 API 映射

虽然组件编写形式变为原生 JS，但核心的 Vue 3 逻辑与响应式范式依然完全适用。

### 1. 组合式 API (Composition API)
优先阅读以下映射了解 `ref`、`reactive` 和 `computed`：
- `./api/composition-api-setup.md`
- `./api/reactivity-core.md`
- `./api/composition-api-lifecycle.md`
- `./examples/essentials/reactivity-fundamentals.md`

### 2. 模板语法与内置指令
在 `template: \`...\`` 字符串中，以下语法完全支持：
- `./examples/essentials/template-syntax.md`
- `./examples/essentials/class-and-style.md`
- `./examples/essentials/list.md` (v-for)
- `./examples/essentials/conditional.md` (v-if)
- `./examples/essentials/event-handling.md` (v-on / @)
- `./examples/essentials/forms.md` (v-model)

### 3. 组件系统
- `./examples/components/props.md`
- `./examples/components/events.md`
- `./examples/components/slots.md`
- `./examples/reusability/composables.md` (V3 极其推荐使用 Composables 来提取公用逻辑，存放于 `composables/` 目录)

## 最佳实践

1. **响应式隔离**：利用 Composables 函数 (`useXxx.js`) 封装与 DOM 无关的业务逻辑。
2. **防注入与转义**：在使用字符串模板拼接时注意防范 XSS，应当使用 Vue 的 `{{ }}` 或 `v-text`。
3. **单向数据流**：严格遵循 Props 下发，Events (`emit`) 上报的原则，不可在子组件内部直接修改父组件传入的 Props 对象属性。
4. **CSS 命名空间**：因无 SFC 样式隔离 (Scoped CSS)，必须对组件的最外层包裹专属 CSS 类名（如 `.my-feature-panel`），并在 `styles.css` 中嵌套定义样式避免全局污染。

## 资源

- Vue3 API: https://cn.vuejs.org/api/
- 仓库前端入口: `cmd/agent-terminal/frontend/vue-app/app.js`

## 关键词

Vue 3, ES Modules, Buildless, Composition API, string template, native CSS, composables, no SFC, no build
