# 视觉辅助指南

基于浏览器的视觉头脑风暴辅助工具，用于展示 mockup、图表和选项。

## 何时使用

按问题判断，而不是按会话判断。测试标准是：**用户看到它是否比阅读它更容易理解？**

**当内容本身是视觉内容时，使用浏览器：**

- **UI mockup**：线框图、布局、导航结构、组件设计
- **架构图**：系统组件、数据流、关系图
- **并排视觉比较**：比较两种布局、两种配色、两种设计方向
- **设计打磨**：问题涉及外观、间距、视觉层级时
- **空间关系**：渲染成图表的状态机、流程图、实体关系

**当内容是文本或表格时，使用终端：**

- **需求和范围问题**：“X 是什么意思？”、“哪些功能在范围内？”
- **概念性 A/B/C 选择**：在用文字描述的方案之间选择
- **取舍列表**：优缺点、对比表
- **技术决策**：API 设计、数据建模、架构方案选择
- **澄清问题**：任何答案是文字而不是视觉偏好的问题

关于 UI 主题的问题不自动等于视觉问题。“你想要哪种向导？”是概念问题，使用终端。“这些向导布局哪个感觉对？”是视觉问题，使用浏览器。

## 工作方式

服务器会监听一个目录中的 HTML 文件，并把最新文件提供给浏览器。你把 HTML 内容写入 `screen_dir`，用户会在浏览器中看到并可以点击选择选项。选择会记录到 `state_dir/events`，你在下一轮读取。

**内容片段 vs 完整文档：** 如果 HTML 文件以 `<!DOCTYPE` 或 `<html` 开头，服务器会原样提供（只注入 helper 脚本）。否则服务器会自动用框架模板包裹你的内容：添加 header、CSS 主题、选择指示器和所有交互基础设施。**默认写内容片段。** 只有当你需要完全控制页面时，才写完整文档。

## 启动会话

```bash
# Start server with persistence (mockups saved to project)
scripts/start-server.sh --project-dir /path/to/project

# Returns: {"type":"server-started","port":52341,"url":"http://localhost:52341",
#           "screen_dir":"/path/to/project/.superpowers/brainstorm/12345-1706000000/content",
#           "state_dir":"/path/to/project/.superpowers/brainstorm/12345-1706000000/state"}
```

保存响应中的 `screen_dir` 和 `state_dir`。告诉用户打开 URL。

**查找连接信息：** 服务器会把启动 JSON 写入 `$STATE_DIR/server-info`。如果你在后台启动服务器且没有捕获 stdout，读取该文件即可获得 URL 和端口。使用 `--project-dir` 时，在 `<project>/.superpowers/brainstorm/` 中查找会话目录。

**注意：** 将项目根目录作为 `--project-dir` 传入，这样 mockup 会持久保存在 `.superpowers/brainstorm/` 中，并且在服务器重启后仍保留。不传时，文件会进入 `/tmp` 并被清理。如果 `.superpowers/` 尚未加入 `.gitignore`，提醒用户添加。

**按平台启动服务器：**

**Claude Code（macOS / Linux）：**
```bash
# Default mode works — the script backgrounds the server itself
scripts/start-server.sh --project-dir /path/to/project
```

**Claude Code（Windows）：**
```bash
# Windows auto-detects and uses foreground mode, which blocks the tool call.
# Use run_in_background: true on the Bash tool call so the server survives
# across conversation turns.
scripts/start-server.sh --project-dir /path/to/project
```
通过 Bash 工具调用时，设置 `run_in_background: true`。然后在下一轮读取 `$STATE_DIR/server-info` 获取 URL 和端口。

**Codex：**
```bash
# Codex reaps background processes. The script auto-detects CODEX_CI and
# switches to foreground mode. Run it normally — no extra flags needed.
scripts/start-server.sh --project-dir /path/to/project
```

**Gemini CLI：**
```bash
# Use --foreground and set is_background: true on your shell tool call
# so the process survives across turns
scripts/start-server.sh --project-dir /path/to/project --foreground
```

**其他环境：** 服务器必须在对话轮次之间持续后台运行。如果你的环境会回收 detached 进程，使用 `--foreground`，并用平台的后台执行机制启动命令。

如果浏览器无法访问 URL（远程/容器环境中常见），绑定非 loopback host：

```bash
scripts/start-server.sh \
  --project-dir /path/to/project \
  --host 0.0.0.0 \
  --url-host localhost
```

使用 `--url-host` 控制返回 URL JSON 中打印的主机名。

## 循环

1. **检查服务器存活**，然后把 **HTML 写入** `screen_dir` 中的新文件：
   - 每次写入前，检查 `$STATE_DIR/server-info` 是否存在。如果不存在（或 `$STATE_DIR/server-stopped` 存在），说明服务器已关闭：继续前用 `start-server.sh` 重启。服务器会在 30 分钟不活动后自动退出。
   - 使用语义化文件名：`platform.html`、`visual-style.html`、`layout.html`
   - **绝不要复用文件名**：每个屏幕都使用新文件
   - 使用 Write 工具：**绝不要用 cat/heredoc**（会把噪音倒进终端）
   - 服务器会自动提供最新文件

2. **告诉用户会看到什么，然后结束你的轮次：**
   - 提醒他们 URL（每一步都提醒，不只是第一次）
   - 简短总结屏幕内容（例如：“正在展示首页的 3 个布局选项”）
   - 请他们在终端回复：“看一下并告诉我你的想法。如果愿意，也可以点击选择一个选项。”

3. **在下一轮**，用户在终端回复后：
   - 如果 `$STATE_DIR/events` 存在，读取它：这里包含用户浏览器交互（点击、选择），格式为 JSON lines
   - 和用户的终端文字合并，得到完整反馈
   - 终端消息是主要反馈；`state_dir/events` 提供结构化交互数据

4. **迭代或前进**：如果反馈改变当前屏幕，写一个新文件（例如 `layout-v2.html`）。只有当前步骤验证后，才进入下一个问题。

5. **回到终端时卸载**：当下一步不需要浏览器（例如澄清问题、取舍讨论）时，推送等待屏幕来清除旧内容：

   ```html
   <!-- filename: waiting.html (or waiting-2.html, etc.) -->
   <div style="display:flex;align-items:center;justify-content:center;min-height:60vh">
     <p class="subtitle">Continuing in terminal...</p>
   </div>
   ```

   这能避免用户继续盯着已经解决的选择，而对话已经前进。下一个视觉问题出现时，照常推送新内容文件。

6. 重复直到完成。

## 编写内容片段

只写页面内部的内容。服务器会自动用框架模板包裹它（header、主题 CSS、选择指示器和所有交互基础设施）。

**最小示例：**

```html
<h2>Which layout works better?</h2>
<p class="subtitle">Consider readability and visual hierarchy</p>

<div class="options">
  <div class="option" data-choice="a" onclick="toggleSelect(this)">
    <div class="letter">A</div>
    <div class="content">
      <h3>Single Column</h3>
      <p>Clean, focused reading experience</p>
    </div>
  </div>
  <div class="option" data-choice="b" onclick="toggleSelect(this)">
    <div class="letter">B</div>
    <div class="content">
      <h3>Two Column</h3>
      <p>Sidebar navigation with main content</p>
    </div>
  </div>
</div>
```

就是这些。不需要 `<html>`、CSS 或 `<script>` 标签。服务器会提供所有这些内容。

## 可用 CSS 类

框架模板为你的内容提供这些 CSS 类：

### 选项（A/B/C 选择）

```html
<div class="options">
  <div class="option" data-choice="a" onclick="toggleSelect(this)">
    <div class="letter">A</div>
    <div class="content">
      <h3>Title</h3>
      <p>Description</p>
    </div>
  </div>
</div>
```

**多选：** 给容器添加 `data-multiselect`，让用户选择多个选项。每次点击会切换该项。指示条会显示计数。

```html
<div class="options" data-multiselect>
  <!-- same option markup — users can select/deselect multiple -->
</div>
```

### 卡片（视觉设计）

```html
<div class="cards">
  <div class="card" data-choice="design1" onclick="toggleSelect(this)">
    <div class="card-image"><!-- mockup content --></div>
    <div class="card-body">
      <h3>Name</h3>
      <p>Description</p>
    </div>
  </div>
</div>
```

### Mockup 容器

```html
<div class="mockup">
  <div class="mockup-header">Preview: Dashboard Layout</div>
  <div class="mockup-body"><!-- your mockup HTML --></div>
</div>
```

### 分屏视图（并排）

```html
<div class="split">
  <div class="mockup"><!-- left --></div>
  <div class="mockup"><!-- right --></div>
</div>
```

### 优缺点

```html
<div class="pros-cons">
  <div class="pros"><h4>Pros</h4><ul><li>Benefit</li></ul></div>
  <div class="cons"><h4>Cons</h4><ul><li>Drawback</li></ul></div>
</div>
```

### Mock 元素（线框构建块）

```html
<div class="mock-nav">Logo | Home | About | Contact</div>
<div style="display: flex;">
  <div class="mock-sidebar">Navigation</div>
  <div class="mock-content">Main content area</div>
</div>
<button class="mock-button">Action Button</button>
<input class="mock-input" placeholder="Input field">
<div class="placeholder">Placeholder area</div>
```

### 排版和章节

- `h2`：页面标题
- `h3`：章节标题
- `.subtitle`：标题下方的次级文本
- `.section`：带底部外边距的内容块
- `.label`：小号大写标签文本

## 浏览器事件格式

当用户在浏览器中点击选项时，其交互会记录到 `$STATE_DIR/events`（每行一个 JSON 对象）。推送新屏幕后，该文件会自动清空。

```jsonl
{"type":"click","choice":"a","text":"Option A - Simple Layout","timestamp":1706000101}
{"type":"click","choice":"c","text":"Option C - Complex Grid","timestamp":1706000108}
{"type":"click","choice":"b","text":"Option B - Hybrid","timestamp":1706000115}
```

完整事件流展示用户的探索路径：他们可能在最终决定前点击多个选项。最后一个 `choice` 事件通常是最终选择，但点击模式也可能揭示犹豫或偏好，值得追问。

如果 `$STATE_DIR/events` 不存在，说明用户没有和浏览器交互：只使用他们的终端文字。

## 设计提示

- **按问题调整保真度**：布局问题用线框，打磨问题用更高保真
- **每页都解释问题**：“哪种布局感觉更专业？”而不是只写“选一个”
- **先迭代再前进**：如果反馈改变当前屏幕，写新版本
- **每屏最多 2-4 个选项**
- **重要时使用真实内容**：摄影作品集要用真实图片（Unsplash）。占位内容会掩盖设计问题。
- **保持 mockup 简单**：聚焦布局和结构，不追求像素级完美

## 文件命名

- 使用语义化名称：`platform.html`、`visual-style.html`、`layout.html`
- 绝不要复用文件名：每个屏幕必须是新文件
- 迭代时：追加版本后缀，如 `layout-v2.html`、`layout-v3.html`
- 服务器按修改时间提供最新文件

## 清理

```bash
scripts/stop-server.sh $SESSION_DIR
```

如果会话使用了 `--project-dir`，mockup 文件会保留在 `.superpowers/brainstorm/` 供之后参考。只有 `/tmp` 会话会在停止时删除。

## 参考

- 框架模板（CSS 参考）：`scripts/frame-template.html`
- Helper 脚本（客户端）：`scripts/helper.js`
