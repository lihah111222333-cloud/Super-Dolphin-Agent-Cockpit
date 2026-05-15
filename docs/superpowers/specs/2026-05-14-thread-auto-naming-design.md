# 线程自动命名与去标签化设计

> 日期: 2026-05-14
> 状态: 待审阅

## 背景与问题陈述
1. **命名体验差**：当前用户新建对话时，如果未提供名字，系统会 fallback 到底层的 `agent_id`（形如 `agent_177...`），这种纯技术的命名方式对用户毫无意义。
2. **状态泄露**：前端 UI 会渲染“待启动”徽章（`pendingLaunch`），暴露出后端 Lazy Load（首轮消息到达后才拉起进程）的技术细节，这增加了认知负担，打破了产品沉浸感。
3. **技术债务与前车之鉴**：以前尝试通过修改命名来解决时，可能误触了 `agent_id` 的生成，导致底层路由错乱、UUID 丢失等严重 Bug。必须明确区分“业务展示名称（Name）”和“系统调度键（AgentID）”。

## 目标
1. **统一初始化占位符**：新建对话且未发送消息前，默认显示“新对话”，而非 `agent_177...`。
2. **首轮自动提取**：用户发送第一条消息后，系统根据内容自动抽取短标题，覆盖占位符。
3. **前端去标签化**：完全移除“待启动”状态在 UI 上的呈现，给用户提供纯粹、无痕的聊天体验。
4. **稳定性保障**：严格保护底层 `agent_id` 和 UUID 生成机制不受破坏。

---

## 架构设计与改动点

### 1. 后端：解耦展示名与进程 ID（默认显示“新对话”）
**核心原则**：绝对不触碰 `agent_id`（`agent_<timestamp>`）的生成与流转。仅修改展示名称的 Fallback 逻辑。
* **位置**：`internal/module/thread/spawn.go` 中的 `resolveDisplayName` 函数。
* **逻辑**：当 `Prompt` 和 `Name` 均为空时，不再 fallback 返回 `agentID`，而是返回常量 `"新对话"`。这使得数据库中 `agent_threads` 表的 `Name` 字段写入“新对话”，但主键/调度键依然保持为 `agent_xxx`。

### 2. 后端：首条消息的标题提取
* **位置**：`internal/module/thread/` (首条消息路由或 spawn 阶段)。
* **逻辑**：在收到首条用户消息（`SpawnIfNeeded` 被触发时）：
  1. 如果线程当前的 `Name` 不是“新对话”，且未被手动重命名过，则跳过（防止覆盖用户自定义名称）。
  2. 使用纯字符串算法提取：去除 `@mention`、语气词和祈使句（如“请帮我”），保留专业术语或动名词短语，截断为最高 8 个显示单元。
  3. 如果提取失败或结果过短（如用户只发了“你好”），则回退保持“新对话”或原名。
  4. 提取出来的结果被赋值给 `thread.Name` 并持久化。

### 3. 前端：UI 去标签化
* **位置 1**：`cmd/agent-terminal/frontend/vue-app/components/unified-chat/CmdCardGrid.js`
  * **改动**：删除渲染 `<span v-if="card.pendingLaunch" class="thread-pending-badge">待启动</span>` 的 DOM 节点和样式。
* **位置 2**：`cmd/agent-terminal/frontend/vue-app/components/unified-chat/ThreadRailSidePanel.js`
  * **改动**：同上，移除侧边栏对话列表中关于 `thread.pendingLaunch` 的角标展示。

---

## 测试与验证要求
（在进入实现环节时，必须完成以下验证以确保不重复之前的 Bug）

1. **核心调度隔离测试**：验证修改 `resolveDisplayName` 后，`AgentID` 是否依旧是合法的 `agent_<timestamp>` 格式，验证进程能否被正确拉起，确保 UUID 没有任何断层。
2. **状态机测试**：验证在隐藏前端“待启动”徽章后，系统底层是否仍然能正确处理 `pendingLaunch` 的延迟加载逻辑。
3. **命名覆盖测试**：
   * 确保用户手动重命名的线程在第一句话到达后**不会**被覆盖。
   * 发送空消息、短标点符号时，是否安全地保持“新对话”。
