# Message assistant 回复完成后出现大段空白的源码追溯

日期：2026-06-05
基线：`origin/main` = `8d141abfc99a2a5bbef017c2608f51a8dbd887db`
范围：只追溯当前 React 新 UI 与 Codex message assistant 的 live timeline 链路；未修改业务代码。

## 结论

这不是 provider 持久化出来的 assistant 文本尾部带了大量空行，也不是 Markdown/CSS 把普通段落渲染成多行空白。

高可信根因在前端 live 完成态：assistant 回复完成时，`turn/completed` 会先把 `_threadPatch` 合进 store，再把 assistant completion 合进 timeline；同时 Chat 页面有两套底部滚动触发。这个中间态会让固定剩余高度的 `.timeline` 保持一个过大的空白可视区域。重新选择同一对话后，页面重新走 `thread/messages` 历史归一化和 active thread 初始化路径，绕开这段 live completion 时序，所以显示恢复正常。

## 现场证据

启动的是最新 `origin/main`：

- `git fetch origin main` 后，`HEAD`、`origin/main`、`FETCH_HEAD` 都是 `8d141abfc99a2a5bbef017c2608f51a8dbd887db`。
- `./run-new-ui-desktop.sh` 已启动：Vite `http://127.0.0.1:5175`，Wails bridge `127.0.0.1:4512`，backend PID `99975`。
- 启动日志里的当前线程、token 数和截图一致：`ThreadID: agent_1780651688333514000`，`TotalTokens: 35647`。
- 同一线程的 rollout 文件 `/Users/ai/.codex/sessions/2026/06/05/rollout-2026-06-05T17-28-09-019e971c-8642-7ee3-a93f-a5beebd7008c.jsonl` 中，最终 assistant 文本长度正常，尾部不是大段空白。

## 源码链路

### 1. live 回复完成是双通道写入

Codex provider 会累计 message delta，并在 terminal event 注入到 `turn/completed.result`：

- `internal/provider/codexapp/session_approval.go:348-354`：message delta 进入 accumulator，terminal event 走 `injectAccumulatedResult`。
- `internal/provider/codexapp/session_approval.go:372-386`：把累计文本写到 `payload["result"]`。

前端接收 bridge event 时，`turn/completed` 的处理顺序是：

- `frontend-app/src/entities/client/model/useClientStore.js:3512-3514`：如果 payload 带 `_threadPatch`，先 `applyBridgePatch('ui/thread/patch', ...)`，再 `applyAssistantCompletion(...)`。
- `frontend-app/src/entities/client/model/useClientStore.js:3441-3460`：`applyBridgePatch` 单独执行一次 `set(...)`。
- `frontend-app/src/entities/client/model/useClientStore.js:3413-3422`：`applyAssistantCompletion` 再执行一次 `set(...)`，把 assistant completion 合入 timeline。

因此完成瞬间至少有两次 React/store 状态变更：先结构 patch/status/activeTurn，再 assistant message completion。

### 2. live timeline 与 history timeline 不是同一条路径

live bridge patch 写入：

- `frontend-app/src/entities/client/model/useClientStore.js:2512-2523`：从 bridge payload 取 `timelineItems`。
- `frontend-app/src/entities/client/model/useClientStore.js:2534-2543`：`bridgePatchTimeline` 将 patch items 归一化后 merge。
- `frontend-app/src/entities/client/model/useClientStore.js:2640-2655`：patch 同时更新 `timelinesByThread`、`activeTurnByThread`、`statuses`、token 等。

重新选对话后主要走 history：

- `internal/module/thread/history.go:53-91`：后端 `ReadMessages` 读取历史页。
- `internal/provider/codexapp/history_rollout.go:57-72`：本地 rollout message 解析时对文本做 `strings.TrimSpace(...)`。
- `frontend-app/src/entities/client/model/useClientStore.js:3166-3174`：前端 `normalizeThreadMessageItems` 把 `thread/messages` 结果转成 timeline item。
- `frontend-app/src/entities/client/model/useClientStore.js:3191-3203`：history page merge 后标记 `threadTimelineReadyByThread[id] = true`。

这解释了“重新选对话显示正常”：它切换到了稳定的 history hydration 路径，而不是继续使用刚完成时的 live patch/completion 中间态。

### 3. 大空白来自 timeline 布局/滚动状态，不来自 Markdown 空行

当前布局让 timeline 占满 status/composer 上方所有剩余高度：

- `frontend-app/src/styles.css:1335-1339`：`.conversation` 是三行 grid，第一行 `minmax(0, 1fr)`，后两行是 status 与 composer。
- `frontend-app/src/styles.css:1392-1401`：`.timeline` 高度 `100%`，`display:flex; flex-direction:column; align-items:flex-start; overflow:auto`。
- `frontend-app/src/styles.css:1517-1523`：每条 `.message` 是普通 flex item，固定列宽和 margin，不负责填充到底部。
- `frontend-app/src/styles.css:2451-2468`：`.work-status` 在 timeline 下面独立占一行。

普通 assistant Markdown 不会保留尾部空白：

- `frontend-app/src/pages/chat/ChatPage.jsx:3788-3790`：`normalizeMessageText` 只把 CRLF 转 LF，不主动制造空白。
- `frontend-app/src/pages/chat/ChatPage.jsx:3885-3886`：Markdown 输入按换行拆分。
- `frontend-app/src/pages/chat/ChatPage.jsx:4016-4018`：空 Markdown 行只被消费，不生成 DOM 节点。
- `frontend-app/src/styles.css:1718-1726`：`.message-markdown` 使用 `white-space: normal`。

所以截图中的大段空白更像 `.timeline` 剩余可视高度或滚动位置异常，不是 message 文本中的“空行”。

### 4. 滚动触发有两个来源，且覆盖不足

第一套滚动在 `Conversation`：

- `frontend-app/src/pages/chat/ChatPage.jsx:105-110`：`scrollTimelineElementToBottom` 对 timeline 设置 `scrollTo({ top: scrollHeight, behavior: 'smooth' })`，随后设置 `scrollTop = scrollHeight`。
- `frontend-app/src/pages/chat/ChatPage.jsx:1509-1517`：`timelineAutoScrollKey` 只看 active thread、最后一条可滚动消息、pending reasoning。
- `frontend-app/src/pages/chat/ChatPage.jsx:5067-5080`：当 auto-scroll key 变化且认为仍贴底时，下一帧滚到底。

第二套滚动在 `ConversationTimeline`：

- `frontend-app/src/pages/chat/ChatPage.jsx:5296-5304`：当 visible message 数量变化时，对末尾 `bottomRef` 调 `scrollIntoView({ behavior: 'smooth' })`。
- `frontend-app/src/pages/chat/ChatPage.jsx:5316-5319`：`bottomRef` 位于 visible messages 与 pending reasoning 之后。

这两套逻辑都只验证“滚动到底部”的数值行为，没有验证最后一条 assistant 与 `.work-status` / composer 之间的实际可见距离。

现有测试缺口：

- `frontend-app/src/pages/chat/ChatPage.test.jsx:854-889` 只断言 assistant 内容增长后 `scrollTop` 被设到 `scrollHeight`。
- `frontend-app/src/pages/chat/ChatPage.test.jsx:829-851` 只断言底部按钮点击后 `scrollTop` 变化。
- `frontend-app/src/entities/client/model/useClientStore.test.js:3043-3084` 覆盖 `turn/completed.result` 替换较短 runtime reply，但不模拟 `_threadPatch` 先到、completion 后到的同一 terminal event 顺序。
- `frontend-app/src/entities/client/model/useClientStore.test.js:3188-3228` 覆盖 partial patch 不丢 runtime reply，但没有接 ChatPage DOM 间距断言。

## 排除项

1. **不是 Wails bridge 未连接。** 这次应用从 `origin/main` 正常启动，Vite 和 backend 都 ready，截图里的 token usage 和 backend log 对上了。
2. **不是历史持久化文本带空白。** rollout history 里 assistant 文本尾部正常；`parseRolloutLine` 还会 `TrimSpace`。
3. **不是普通 Markdown 渲染空行。** blank Markdown line 不生成节点，`.message-markdown` 不保留普通空白。
4. **不是 thread/messages 历史路径的稳定缺陷。** 用户描述“重新选对话正常”，源码也显示重新选对话走 history hydration，而异常发生在 live completion 时序。

## 修复入口建议

最小修复应从 `frontend-app` 入手，不需要改 provider 或 Go history：

1. 在 `useClientStore` 增加回归测试，覆盖 `turn/completed` payload 同时带 `_threadPatch` 和 `result` 的顺序，确保最终 timeline 只保留可见 assistant，并且 terminal-only patch 不制造额外可见项。
2. 在 `ChatPage.test.jsx` 增加 DOM/layout 回归：模拟 active assistant 回复完成、pending reasoning 消失、assistant completion 替换后，断言最后一条 `.message.assistant` 到 `.work-status` 的 gap 不超过预期阈值，或断言 timeline 仍处于合理贴底状态。
3. 修复方向优先考虑合并/协调两套滚动触发：`scrollTimelineElementToBottom(...)` 与 `bottomRef.scrollIntoView(...)` 不应在 completion 中间态竞争；completion 后应以最终 rendered layout 为准再做一次确定性的底部对齐。

## 验证建议

文档阶段未改代码。后续修复完成后建议运行：

```bash
cd frontend-app
npm test -- src/pages/chat/ChatPage.test.jsx src/entities/client/model/useClientStore.test.js
```

若修复触及样式，再追加：

```bash
cd frontend-app
npm test -- src/styles.test.js
```
