# 聊天斜杠命令与能力面板设计

日期：2026-07-11

## 目标

1. 在聊天输入框首个非空字符为 `/` 时打开统一命令面板。
2. 让用户通过同一入口检索 Skills、提示词、自动化、MCP 工具和少量内置聊天命令。
3. Skill 与 MCP 工具使用结构化请求字段绑定到本次发送，不把能力名称伪装成普通提示词。
4. 保持 Suiyuan 输入栏现有布局、项目选择、模型选择、附件和发送行为稳定。

## 非目标

- 不支持在正文中间或 URL、文件路径中触发命令面板。
- 不因选择自动化条目而直接执行任务。
- 不允许通过斜杠命令修改 MCP 工具生命周期或服务器配置。
- 不新增通用脚本语言、命令参数语法或用户自定义命令编辑器。
- 不用静态 Skills、提示词、自动化或 MCP 假数据替代运行时目录。

## 触发与关闭规则

命令面板只在输入框内容满足 `^\s*/[^\s]*$` 时打开。斜杠必须是输入框首个非空字符，斜杠后的连续非空文本是查询词。

- 输入 `/`：打开面板并显示全部可用类别。
- 继续输入 `/review`：按名称、描述和关键词实时筛选。
- 输入空格、换行或把斜杠移出首个非空位置：关闭面板，保留原始文本。
- `Escape`：关闭本次面板，不修改草稿。
- 选择条目：移除触发用的 `/查询文本`，再执行该类别的选择语义。
- 中文输入法组合态期间不处理 Enter、方向键或字符触发，避免候选词被误提交。

## 统一目录模型

新增 `features/slash-commands` 模块。聊天组件只消费归一化条目，不直接解析各页面的原始响应。

每个目录条目至少包含：

```text
id
kind: builtin | skill | prompt | automation | mcp_tool
name
label
description
keywords
payload
disabled
disabledReason
```

条目 `id` 必须包含类别与稳定业务标识，不能使用数组索引。目录按 `builtin -> skill -> prompt -> automation -> mcp_tool` 分组；查询结果先按前缀命中、名称包含、关键词命中排序，再保持各数据源原始稳定顺序。

## 数据源适配

### Skills

复用技能看板的项目级目录来源，读取 canonical `name`、显示名、描述、scope、`skill_file` 和触发词。菜单不得从文件系统自行扫描第二份技能目录。

### 提示词

复用提示词看板列表获取可检索元数据。用户选择后再通过现有 prompt get 接口读取完整内容，避免首次打开面板时批量加载所有正文。

### 自动化

复用自动化看板项目数据。只有具备可编辑任务内容、prompt 或 command template 的条目可插入聊天；缺少可用内容的条目显示禁用原因，不猜测执行文本。

### MCP 工具

新增只读的 MCP 工具目录接口，输入当前 `workspaceRoot`，返回当前运行时可绑定的 canonical 工具元数据：

```text
serverName
toolName
displayName
description
enabled
disabledReason
```

目录必须来自实际 toolbridge/MCP surface，`toolName` 必须是 turn start 接受的 canonical 名称。不得用 SQLite/Playwright 服务器开关、生命周期记录或 UI 显示名推测工具名。工具面不可准备、名称冲突未解决或目录响应畸形时必须返回显式错误。

### 内置命令

首版只提供：

- `/new`：创建新对话并保持当前项目。
- `/clear`：清空当前输入文本、附件和能力标签。

内置命令使用前端注册表，名称和行为有组件测试锁定。

## 组件边界

1. `slashCommandCatalogService` 调用各业务 API 并输出统一条目及逐类别状态。
2. `useSlashCommandPalette` 负责触发解析、查询、当前高亮、键盘事件和选择分发。
3. `SlashCommandPalette` 只负责 listbox 渲染、分组、加载、错误与空状态。
4. `ComposerCapabilityChips` 展示已选择的 Skill 和 MCP 工具，并提供逐项移除。
5. `ComposerDock` 负责把面板、标签和 textarea 组合起来，不直接实现目录请求或发送协议。
6. client store 负责能力标签的草稿状态、发送快照、成功清理和失败回滚。

## 选择语义

### Skill

选择后生成可移除的 Skill 标签，允许多选并按 canonical name 去重。发送时设置：

```text
selectedSkills: [canonical skill names]
selectedSkillRefs: [available stable scope/path refs]
manualSkillSelection: true
```

没有选中 Skill 时不发送空数组，`manualSkillSelection` 保持 `false`。

### MCP 工具

选择后生成可移除的工具标签，允许多选并按 canonical `toolName` 去重。发送时通过 `enabledTools` 传递 canonical 工具名。禁用、暂停、移除或无法解析 canonical 名称的工具不可选择。

### 提示词

选择后异步读取完整提示词，将内容替换触发用的 `/查询文本`，关闭面板并把焦点还给输入框。读取失败时保留原查询文本并显示错误。

### 自动化

选择后将自动化名称与任务内容插入输入框，内容保持可编辑，不立即运行自动化。没有任务内容的条目保持禁用。

### 内置命令

`/new` 与 `/clear` 在确认选择后立即执行，不生成能力标签。

## 草稿与发送数据流

能力选择属于当前项目、当前会话的 composer 草稿，而不是全局设置。

1. 草稿快照在现有 `draft` 与 `attachments` 之外保存 Skill refs 和 MCP tool names。
2. 切换项目或会话前保存当前快照，返回后恢复文本、附件和能力标签。
3. `createSendDraftRequest` 对能力字段做规范化、去重和有效性检查，并把快照放入 rollback request。
4. `turn/start` 发送 `selectedSkills`、`selectedSkillRefs`、`manualSkillSelection` 和 `enabledTools`。
5. optimistic state 清空文本、附件和能力标签。
6. 发送成功后清除对应 composer 快照。
7. 发送失败或新线程恢复失败时，恢复文本、附件和能力标签；错误继续通过现有 action toast 显示。

只选择能力标签、没有正文或附件时不能发送。用户必须提供具体任务，避免无意调用 Skill 或 MCP 工具。

## 加载与错误处理

- 面板首次打开时按当前 `cwd` 请求目录，并复用 TanStack Query 缓存。
- 项目切换后使用新的查询键，不跨项目复用项目级 Skills、提示词或自动化。
- 缺少项目时，项目相关类别显示明确禁用原因；本地内置命令仍可用。
- 各类别独立显示加载或错误状态。某一类别失败时可以查看其他已成功类别，但失败必须在面板内可见，不能静默隐藏或用旧静态列表替代。
- 目录响应缺少必填字段时该类别整体进入错误状态，不跳过坏条目制造不完整列表。
- Prompt 正文读取、Skill/MCP 发送前校验失败时保留草稿和当前选择。
- stale Skill 或 MCP tool 不允许发送；界面显示失效原因并要求用户移除或重新选择。

## 交互与可访问性

- textarea 始终保有输入焦点；面板使用 `listbox/option` 语义并通过 `aria-activedescendant` 表示当前项。
- `ArrowUp`、`ArrowDown` 循环移动高亮；`Enter` 选择；`Escape` 关闭；`Tab` 接受当前项并把焦点留在输入框。
- 鼠标 hover 更新当前高亮，点击执行选择。
- 类别标题不是可选项；禁用项可聚焦阅读原因但不能确认。
- 面板状态变化使用克制的 `aria-live` 文本，不朗读整个结果列表。
- 中英文文案进入现有 `APP_COPY`，能力名称保持数据源原文。

## 视觉与响应式约束

- 面板锚定在 composer card 上方，不改变输入栏宽度或推挤对话时间线。
- 桌面最大宽度约 520px，同时不超过 composer 可用宽度。
- 列表最大高度约 360px，结果区域内部滚动；类别标题可保持可见。
- 使用现有 Suiyuan surface、border、text 和 primary token，不新增独立色板或渐变。
- 条目图标和能力标签使用 Lucide 图标；标签保持紧凑，不在标签内堆叠描述。
- 移动端面板左右贴合 composer 内边界，长名称和描述单行省略；不得覆盖发送按钮。
- 明暗主题保持同一结构与尺寸，背景必须不透明。

## 验证

### 纯模型测试

- 仅首个非空 `/` 触发，URL、路径、正文中间和换行后不触发。
- 查询提取、排序、分类、去重和禁用项处理。
- Skill、Prompt、Automation、MCP Tool 与 builtin 的选择结果。

### 组件测试

- listbox 默认关闭，输入 `/` 后打开。
- Arrow、Enter、Tab、Escape、鼠标和 IME 行为。
- prompt 异步读取成功与失败。
- Skill/MCP 标签添加、重复选择和移除。
- 缺少项目、加载中、类别错误、全部无结果和长名称状态。

### Store 与协议测试

- composer 快照保存和恢复能力字段。
- 成功发送清空，失败发送完整回滚。
- `selectedSkills`、`selectedSkillRefs`、`manualSkillSelection` 与 `enabledTools` 负载。
- stale 或 disabled capability 在 RPC 前阻断。
- 新会话首次 turn 与既有会话后续 turn 使用同一能力语义。

### 样式与浏览器验证

- 面板不挤压 composer 或对话时间线。
- 结果区有稳定最大高度和滚动条。
- 桌面、移动宽度、明暗主题、中英文和长名称。
- 项目选择、模型选择、附件、发送、中断与侧栏交互无回归。

### 完整前端验证

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

若新增 MCP 工具目录 RPC，还需运行对应 Go 包测试、RPC contract matrix、LSP diagnostics 和仓库守卫。
