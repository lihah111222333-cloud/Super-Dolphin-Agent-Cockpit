# CodeTrust 项目扫描报告

日期：2026-06-02

仓库：`/Users/ai/Desktop/Super-Dolphin`

扫描工具：CodeTrust `0.3.2`

扫描提交：`c4eb66b`

扫描模式：`files`

原始 JSON 临时路径：`/tmp/codetrust-super-dolphin-2026-06-02.json`

## 结论摘要

本次按“可行动源码”口径运行 CodeTrust。工具接收 2107 个候选文件，实际扫描 15 个文件，排除 2092 个文件，最终发现 1259 条问题。总体得分为 66，等级为 `LOW_TRUST`。

最主要的结论有三点：

1. CodeTrust v0.3.2 当前规则集主要覆盖 JS/TS/前端静态规则；Go 后端文件虽然进入候选列表，但被工具排除，不能据此判断 Go 后端安全或质量无问题。
2. 已扫描结果集中在 `frontend-app`，其中 `frontend-app/src/App.jsx` 是最高风险热点：408 条发现，覆盖复杂度、长函数、重复字符串、魔法数字、不可达分支和直接 HTML 写入。
3. 安全类发现共 3 条：测试文件中的 `new Function()` 1 条，以及 Mermaid SVG 渲染路径上的 `dangerouslySetInnerHTML` 2 条。后者需要确认 SVG 来源和消毒链路，不能直接视为已可利用漏洞，但应作为优先安全审查点。

本报告只记录扫描、分析和修复建议，未修改任何代码。

## 工作区状态

扫描前工作区已有未提交改动，视为用户已有变更。本次只新增本报告，不回滚、不格式化、不暂存这些文件。

```text
 M cmd/mcp-orch/orchestration/dag_ops_update_dag_test.go
 M cmd/mcp-orch/orchestration/dag_query.go
 M frontend-app/src/App.jsx
 M frontend-app/src/App.test.jsx
 M frontend-app/src/styles.css
 M frontend-app/src/styles.test.js
 M internal/contract/orchestration.go
 M internal/provider/codexapp/driver_model_selection_test.go
 M internal/provider/codexapp/supportutil/model_config.go
?? .agent/skills/react-doctor/
?? skills/
```

## 扫描范围

采用可行动源码范围，目的是减少 legacy、生成物、provider 镜像和第三方/minified 文件对结果的噪声。

包含范围：

```text
cmd/**/*.go
internal/**/*.go
pkg/**/*.go
frontend-app/**/*.js
frontend-app/**/*.jsx
frontend-app/**/*.ts
frontend-app/**/*.tsx
frontend-app/**/*.css
frontend-app/**/*.json
frontend-app/**/*.cjs
frontend-app/**/*.mjs
scripts/**/*.js
scripts/**/*.cjs
scripts/**/*.mjs
scripts/**/*.sh
.github/**/*.yml
.github/**/*.yaml
*.toml
*.yml
*.yaml
*.json
```

排除范围：

```text
cmd/agent-terminal/frontend/**
frontend-app/dist/**
docs/**
.agent/**
.agents/**
.claude/**
.codex/**
node_modules/**
vendor/**
```

排除原因：

- `cmd/agent-terminal/frontend/**` 是 legacy/package-embed Vue 前端，不属于当前 React/Vite 主产品面。
- `frontend-app/dist/**`、`node_modules/**`、`vendor/**` 是构建产物或依赖，不应作为本项目可维护源码直接修复。
- `docs/**` 包含历史报告、计划和代码地图，扫描价值低且容易放大重复字符串等低价值噪声。
- `.agent/**`、`.agents/**`、`.claude/**`、`.codex/**` 包含技能、镜像或 provider 配置产物，不应与当前产品源码混在同一修复队列。

## 扫描命令

先记录状态和工具信息：

```bash
git status --short
codetrust --version
codetrust rules list
```

核心扫描逻辑使用 `git -c core.quotePath=false ls-files -z` 生成候选文件，避免中文路径被 Git 转义，然后通过 Node 参数数组调用 CodeTrust：

```bash
node <<'NODE'
const { spawnSync } = require('child_process');
const fs = require('fs');

const rawJsonPath = '/tmp/codetrust-super-dolphin-2026-06-02.json';
const patterns = [
  'cmd/**/*.go', 'internal/**/*.go', 'pkg/**/*.go',
  'frontend-app/**/*.js', 'frontend-app/**/*.jsx', 'frontend-app/**/*.ts', 'frontend-app/**/*.tsx',
  'frontend-app/**/*.css', 'frontend-app/**/*.json', 'frontend-app/**/*.cjs', 'frontend-app/**/*.mjs',
  'scripts/**/*.js', 'scripts/**/*.cjs', 'scripts/**/*.mjs', 'scripts/**/*.sh',
  '.github/**/*.yml', '.github/**/*.yaml',
  '*.toml', '*.yml', '*.yaml', '*.json',
];
const exclude = /^(cmd\/agent-terminal\/frontend\/|frontend-app\/dist\/|docs\/|\.agent\/|\.agents\/|\.claude\/|\.codex\/)|(^|\/)node_modules\/|^vendor\//;
const listed = spawnSync('git', ['-c', 'core.quotePath=false', 'ls-files', '-z', ...patterns], { encoding: 'utf8' });
const files = listed.stdout.split('\0').filter(Boolean).filter((file) => !exclude.test(file));
const scan = spawnSync('codetrust', ['scan', '--format', 'json', ...files], {
  encoding: 'utf8',
  maxBuffer: 1024 * 1024 * 300,
});
const combined = `${scan.stdout || ''}${scan.stderr || ''}`;
const json = JSON.parse(combined.slice(combined.indexOf('{')));
fs.writeFileSync(rawJsonPath, JSON.stringify(json, null, 2));
NODE
```

CodeTrust 扫描命令退出码：`0`。

## 工具规则集

`codetrust rules list` 返回 29 条规则，覆盖以下类别：

| 类别 | 代表规则 |
| --- | --- |
| Security | `security/hardcoded-secret`、`security/eval-usage`、`security/sql-injection`、`security/dangerous-html`、`security/no-debugger` |
| Logic | `logic/dead-branch`、`logic/unused-variables`、`logic/missing-await`、`logic/promise-void`、`logic/duplicate-string`、`logic/magic-number` |
| Structure | `structure/long-function`、`structure/high-cyclomatic-complexity`、`structure/high-cognitive-complexity`、`structure/deep-nesting` |
| Coverage | `coverage/missing-test-file` |

注意：规则列表没有 Go 专项规则。扫描结果中的 Go 候选文件被排除，说明本轮 CodeTrust 证据主要适用于前端 JavaScript/React 代码面。

## 覆盖面与健康状态

| 指标 | 值 |
| --- | ---: |
| 候选文件数 | 2107 |
| 实际扫描文件数 | 15 |
| 工具排除文件数 | 2092 |
| 跳过文件数 | 0 |
| 执行规则数 | 435 |
| 规则失败数 | 0 |
| 扫描错误数 | 0 |
| 有发现的文件数 | 13 |
| 触发规则种类数 | 17 |

有发现的文件：

```text
frontend-app/public/wails/runtime.js
frontend-app/src/App.jsx
frontend-app/src/App.test.jsx
frontend-app/src/SettingsPage.test.jsx
frontend-app/src/entities/client/model/useClientStore.js
frontend-app/src/entities/client/model/useClientStore.test.js
frontend-app/src/features/prompts/PromptPageView.jsx
frontend-app/src/shared/api/backendApi.js
frontend-app/src/shared/api/backendApi.test.js
frontend-app/src/shared/api/wailsBridge.js
frontend-app/src/shared/api/wailsBridge.test.js
frontend-app/src/shared/ui/FocusTrapDialog.jsx
frontend-app/src/styles.test.js
```

## 总体结果

| 指标 | 值 |
| --- | ---: |
| 总体得分 | 66 |
| 等级 | `LOW_TRUST` |
| 发现总数 | 1259 |
| High | 88 |
| Medium | 311 |
| Low | 859 |
| Info | 1 |

维度得分：

| 维度        |   得分 | 发现数 | 严重级别分布                      |
| --------- | ---: | --: | --------------------------- |
| Security  | 71.4 |   3 | High 1, Medium 2            |
| Logic     | 63.3 | 973 | Medium 116, Low 856, Info 1 |
| Structure | 23.3 | 280 | High 87, Medium 193         |
| Style     |  100 |   0 | 无                           |
| Coverage  | 93.4 |   3 | Low 3                       |

## Top Rules

| 排名 | 规则 | 数量 | 说明 |
| ---: | --- | ---: | --- |
| 1 | `logic/duplicate-string` | 531 | 重复字符串，主要集中在测试和大型 UI 文件 |
| 2 | `logic/magic-number` | 241 | 魔法数字，主要集中在 UI 布局、测试断言和 runtime |
| 3 | `structure/long-function` | 163 | 长函数，核心热点为 `App.jsx` 和 store/test 文件 |
| 4 | `structure/high-cyclomatic-complexity` | 86 | 圈复杂度高，主要是页面组件、diff/markdown 解析、store normalize |
| 5 | `logic/unused-variables` | 70 | 未使用变量，需结合当前 dirty 工作区确认是否为迁移中残留 |
| 6 | `logic/dead-branch` | 61 | return/throw 后不可达分支 |
| 7 | `structure/high-cognitive-complexity` | 29 | 认知复杂度高，集中在大型组件和 normalize 函数 |
| 8 | `logic/promise-void` | 29 | Promise 未 await/return，需要确认是否有意 fire-and-forget |
| 9 | `logic/no-nested-ternary` | 24 | 嵌套三元表达式 |
| 10 | `logic/no-async-without-await` | 14 | async 函数内部没有 await |
| 11 | `coverage/missing-test-file` | 3 | 文件缺少对应测试 |
| 12 | `structure/deep-nesting` | 2 | 嵌套过深 |
| 13 | `security/dangerous-html` | 2 | 直接 HTML 写入 |
| 14 | `security/eval-usage` | 1 | `new Function()` |
| 15 | `logic/unnecessary-try-catch` | 1 | 无必要 try/catch |

## Top Files

| 排名 | 文件 | 发现数 | 主要规则 |
| ---: | --- | ---: | --- |
| 1 | `frontend-app/src/App.jsx` | 408 | duplicate-string 117, magic-number 84, unused-variables 64, high-cyclomatic-complexity 43, long-function 42 |
| 2 | `frontend-app/src/App.test.jsx` | 342 | duplicate-string 209, magic-number 82, long-function 48 |
| 3 | `frontend-app/src/entities/client/model/useClientStore.js` | 146 | high-cyclomatic-complexity 30, dead-branch 30, duplicate-string 23, long-function 22 |
| 4 | `frontend-app/src/entities/client/model/useClientStore.test.js` | 136 | duplicate-string 77, magic-number 22, promise-void 18, long-function 17 |
| 5 | `frontend-app/src/styles.test.js` | 48 | duplicate-string 41, long-function 6 |
| 6 | `frontend-app/src/shared/api/wailsBridge.js` | 45 | magic-number 22, dead-branch 6, duplicate-string 6, long-function 4 |
| 7 | `frontend-app/src/features/prompts/PromptPageView.jsx` | 41 | duplicate-string 13, unused-variables 6, high-cyclomatic-complexity 5 |
| 8 | `frontend-app/src/shared/api/wailsBridge.test.js` | 30 | magic-number 11, duplicate-string 11, long-function 7, eval-usage 1 |
| 9 | `frontend-app/src/shared/api/backendApi.test.js` | 27 | 测试重复字符串和魔法数字为主 |
| 10 | `frontend-app/public/wails/runtime.js` | 12 | promise-void、dead-branch、coverage |

## 安全类发现

| 优先级 | 规则 | 文件:行 | CodeTrust 结论 | 审计解读 |
| --- | --- | --- | --- | --- |
| P0 | `security/eval-usage` | `frontend-app/src/shared/api/wailsBridge.test.js:429` | 检测到危险的 `new Function()`，可执行任意代码 | 出现在测试中，用于加载 `public/wails/runtime.js`。风险低于生产路径，但仍应替换为受控 ESM/vitest import 或隔离执行方式，避免测试夹具把任意源码字符串当代码执行。 |
| P0 | `security/dangerous-html` | `frontend-app/src/App.jsx:4313` | 直接 HTML 赋值，可能存在 XSS | Mermaid 图表预览使用 `dangerouslySetInnerHTML` 注入 `state.svg`。需要确认 `state.svg` 只来自可信 Mermaid 渲染输出，并补充 SVG sanitizer 或信任边界说明。 |
| P0 | `security/dangerous-html` | `frontend-app/src/App.jsx:4318` | 直接 HTML 赋值，可能存在 XSS | Lightbox 中重复注入同一 SVG。若 Mermaid 输入来自用户/模型输出，SVG 中事件属性、外链、foreignObject 等必须被消毒或禁用。 |

对应源码片段：

```jsx
<div dangerouslySetInnerHTML={{ __html: state.svg }} />
```

```js
const runtime = new Function(`${runtimeSource}\nreturn { Call, Events };`)();
```

## 高优先级结构与逻辑发现

以下是代表性 High 级别发现，不是完整列表。完整数据见 `/tmp/codetrust-super-dolphin-2026-06-02.json`。

| 规则 | 文件:行 | 发现 |
| --- | --- | --- |
| `structure/high-cyclomatic-complexity` | `frontend-app/src/App.jsx:462` | `summarizeUnifiedDiff` 圈复杂度 43，阈值 10 |
| `structure/high-cognitive-complexity` | `frontend-app/src/App.jsx:462` | `summarizeUnifiedDiff` 认知复杂度 65，阈值 20 |
| `structure/long-function` | `frontend-app/src/App.jsx:462` | `summarizeUnifiedDiff` 117 行，阈值 40 |
| `structure/long-function` | `frontend-app/src/App.jsx:580` | `parseUnifiedDiffLineEntries` 85 行，阈值 40 |
| `structure/high-cyclomatic-complexity` | `frontend-app/src/App.jsx:1401` | `normalizeSkill` 圈复杂度 25，阈值 10 |
| `structure/high-cyclomatic-complexity` | `frontend-app/src/App.jsx:2099` | `scheduleStateFromCron` 圈复杂度 23，阈值 10 |
| `structure/long-function` | `frontend-app/src/App.jsx:3045` | `ChatPage` 279 行，阈值 40 |
| `structure/long-function` | `frontend-app/src/App.jsx:3517` | `ThreadRail` 245 行，阈值 40 |
| `structure/high-cyclomatic-complexity` | `frontend-app/src/App.jsx:3763` | `ModelSelector` 圈复杂度 40，阈值 10 |
| `structure/high-cognitive-complexity` | `frontend-app/src/App.jsx:4154` | `renderInlineMarkdown` 认知复杂度 55，阈值 20 |
| `structure/deep-nesting` | `frontend-app/src/App.jsx:4154` | `renderInlineMarkdown` 嵌套深度 8，阈值 4 |
| `structure/high-cyclomatic-complexity` | `frontend-app/src/App.jsx:4468` | `MarkdownMessage` 圈复杂度 41，阈值 10 |
| `structure/high-cognitive-complexity` | `frontend-app/src/App.jsx:4468` | `MarkdownMessage` 认知复杂度 85，阈值 20 |
| `structure/long-function` | `frontend-app/src/App.jsx:5487` | `WorkflowPage` 574 行，阈值 40 |
| `structure/long-function` | `frontend-app/src/App.jsx:6217` | `SkillsPage` 621 行，阈值 40 |
| `structure/high-cognitive-complexity` | `frontend-app/src/entities/client/model/useClientStore.js:503` | `normalizeThread` 认知复杂度 53，阈值 20 |
| `structure/high-cognitive-complexity` | `frontend-app/src/entities/client/model/useClientStore.js:825` | `normalizeTimelineItem` 认知复杂度 71，阈值 20 |

## 其他典型发现

### Floating Promise

`logic/promise-void` 共 29 条。代表样例：

| 文件:行 | 发现 |
| --- | --- |
| `frontend-app/public/wails/runtime.js:327` | `send()` 疑似未 await 或 return |
| `frontend-app/src/App.jsx:4246` | `initialize()` 疑似未 await 或 return |
| `frontend-app/src/App.jsx:8966` | `loadLspPromptHint()` 疑似未 await 或 return |
| `frontend-app/src/App.jsx:8967` | `loadCurrentScopeCwd()` 疑似未 await 或 return |
| `frontend-app/src/App.jsx:8968` | `loadInjectedPromptVisibility()` 疑似未 await 或 return |
| `frontend-app/src/entities/client/model/useClientStore.js:2383` | `initializeEvents()` 疑似未 await 或 return |
| `frontend-app/src/entities/client/model/useClientStore.js:2733` | `saveActiveComposerDraft()` 疑似未 await 或 return |

建议逐条区分两类情况：

- 必须等待结果：补 `await`、错误处理和测试断言。
- 有意 fire-and-forget：显式使用 `void somePromise()`，并确保内部捕获错误或上报失败，避免静默吞错。

### 不可达分支

`logic/dead-branch` 共 61 条。代表样例：

| 文件:行 | 发现 |
| --- | --- |
| `frontend-app/public/wails/runtime.js:383` | return/throw 后存在不可达代码 |
| `frontend-app/src/App.jsx:424` | return/throw 后存在不可达代码 |
| `frontend-app/src/App.jsx:1010` | return/throw 后存在不可达代码 |
| `frontend-app/src/App.jsx:1444` | return/throw 后存在不可达代码 |
| `frontend-app/src/entities/client/model/useClientStore.js` | 多处 normalize/control-flow 路径命中 |

建议优先清理生产路径中的不可达代码，再处理测试文件中的重复结构。

### 未使用变量

`logic/unused-variables` 共 70 条。`frontend-app/src/App.jsx` 中代表样例包括：

```text
SkillMarkdownPreview
Tag
AppShell
Titlebar
ThemeIcon
NavRail
ChatPage
ProjectSelector
```

这些命中需要结合当前未提交改动判断：如果是迁移中临时残留，应在对应功能收口时删除；如果是 CodeTrust 对 React 组件使用方式识别不足，应在后续扫描中确认是否误报。

### Coverage

`coverage/missing-test-file` 共 3 条：

| 文件 | 发现 |
| --- | --- |
| `frontend-app/public/wails/runtime.js` | 文件有 29 个函数，但未找到对应测试文件 |
| `frontend-app/src/features/prompts/PromptPageView.jsx` | 文件有 57 个函数，但未找到对应测试文件 |
| `frontend-app/src/shared/ui/FocusTrapDialog.jsx` | 文件有 2 个函数，但未找到对应测试文件 |

`FocusTrapDialog.jsx` 属于可访问性基础组件，建议补最小行为测试。`runtime.js` 如果是 Wails runtime shim，应确认是否可由集成测试覆盖，避免为外部运行时复制件编写低价值单测。

## 修复建议

### P0：安全信任边界

1. 审查 Mermaid SVG 渲染链路：确认 `state.svg` 来源、Mermaid 配置、安全级别和是否允许用户/模型输入控制图表内容。
2. 如果 SVG 输入可受用户或模型影响，引入明确 sanitizer，至少过滤事件属性、脚本、`foreignObject`、外链引用和危险 URL scheme。
3. 将 `wailsBridge.test.js` 中的 `new Function()` 替换为更受控的测试加载方式，例如把 runtime shim 改为可 import 的测试夹具，或使用 Vitest transform/import 而不是运行任意拼接字符串。

### P1：降低核心前端复杂度

1. 从 `App.jsx` 中拆出 diff 解析、inline markdown 渲染、Mermaid 渲染、Workflow/Skills/Memory 页面块，每次拆分都配套局部回归测试。
2. 将 `useClientStore.js` 的 normalize 函数拆成小的字段解析器，并为关键输入形态补 table-driven 测试。
3. 对 `promise-void` 逐条分类：必须等待的补 `await`，有意异步启动的使用 `void` 加错误上报，禁止隐式吞错。

### P2：清理低价值噪声并建立扫描基线

1. 对测试文件中的重复字符串和魔法数字先按断言语义分组，不要机械抽常量；只抽能提升可读性的 fixture 名称、选择器和稳定 payload。
2. 清理生产路径中的未使用变量和不可达分支。对误报保留注释或规则例外前，必须先确认真实控制流。
3. 为 `PromptPageView.jsx` 和 `FocusTrapDialog.jsx` 补充最小测试，降低 coverage 维度的可行动缺口。
4. 后续若继续使用 CodeTrust，建议保留本次 JSON 作为非提交基线，复扫时只比较新增问题，避免一次性修复 1259 条造成大范围风险。

## 残留风险与限制

1. Go 后端未被 CodeTrust v0.3.2 实际扫描。本项目的 Go 质量和安全判断仍需依赖 `make guard`、`./scripts/test_with_guard.sh <packages> -count=1`、架构测试、sqlc 校验和人工安全审计。
2. CodeTrust 对 React 组件、测试夹具、Wails runtime shim 的上下文理解有限，`unused-variables`、`missing-test-file`、`promise-void` 中可能存在误报。
3. `dangerouslySetInnerHTML` 是安全审计入口，不等价于已存在可利用 XSS。需要结合 Mermaid 渲染配置、输入来源和 SVG sanitizer 证据做最终判定。
4. 本轮未运行 Go/frontend 测试，因为任务是 docs-only 扫描报告，且没有修改业务代码。
5. 当前工作区已有未提交源码改动，扫描结果反映的是本地 dirty tree 状态，不一定等同于 `main` 干净状态。

## 后续验证建议

针对代码修复阶段，建议按影响面运行：

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

如果后续修复涉及 Go 后端：

```bash
./scripts/test_with_guard.sh <affected packages> -count=1
make guard
```

如果涉及 SQL/store：

```bash
make sqlc-verify
```

