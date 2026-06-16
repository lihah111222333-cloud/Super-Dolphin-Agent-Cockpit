# Super-Dolphin Thread 历史加载慢二次评审结论

生成时间：2026-06-03

评审方式：两个独立子代理分别做前后端全链路评审，第三个总结代理按生产可行性、性能优先、风险、准确性、维护性去重筛选，仅保留高可信真阳性问题。

复投复核：2026-06-03 追加两个投票审查 agent。主 Agent 逐项追源码复核高票项与边界票，确认风险是否真实可达、是否已有上层防护。

## 核心结论

高可信根因是链式放大：前端点击 `thread-card` 后等待全量历史拉完，后端 `thread/messages` 每页请求仍全量读取历史再内存分页，最后前端一次性渲染大量 DOM、Markdown 和 Mermaid。最终修复应优先保证“最近页先显示”，同时让后端真正只读取最近页。

## 证据锚点

- 前端点击入口：`frontend-app/src/pages/chat/ChatPage.jsx` 的 `ThreadDisplayCardContent` 调用 `store.setActiveThread(thread.id)`。
- 前端全量补齐：`frontend-app/src/entities/client/model/useClientStore.js` 的 `fetchThreadMessagePages` 循环拉取 `thread/messages` 直到全量完整。
- 前端等待阻塞：`syncThreadState` 并行发 `ui/state/get` 和 `thread/messages`，但会等待 `messagesPromise` 完成后再清理 loading。
- Conversation 渲染：`ConversationTimeline` 对 `messages` 直接 `map(<TimelineMessage />)`，历史越长 DOM 和 Markdown/Mermaid 成本越高。
- 后端伪分页：`internal/module/thread/history.go:ReadMessages` 先读完整 history，再 `selectMessagesPage` 内存分页。
- Live session 读取：`internal/module/thread/lifecycle_helpers.go:readMessagesSource` 对 live session 调 `ReadHistory(..., 0)`。
- Provider 历史读取：Codex、Claude、persisted `historyjsonl` 路径当前都倾向全文件扫描后解析。

## 复投与主 Agent 可达性复核

| Finding | 两票结果 | 主 Agent 可达性裁决 | 已有上层防护 | 最终处理 |
|---|---|---|---|---|
| 后端 `thread/messages` 真实分页 | 2/2 确认 P0 | 真实可达：`ReadMessages` 必经 `readMessagesSource` 取 `all`，再内存分页 | `resumeInFlight` 只防后台 resume stampede，不防 `thread/messages` 全量读 | 保持 P0 |
| `hasMore/nextBefore` 替代精确 `total` 驱动 | 2/2 确认 P0 | 真实可达：前端用 `total` 判断继续翻页，后端用 `len(all)` 计算 total | 当前 DTO 只有 `messages/total`，没有服务端游标防护 | 保持 P0 |
| Codex、Claude、persisted jsonl tail/page 读取 | 2/2 确认 P0 | 真实可达：三条路径都顺序扫描文件；Codex/Claude 即使传 limit 也是读全后裁剪 | scanner buffer 只防超长行崩溃，不降低 IO/解析量 | 保持 P0 |
| 前端首屏最近页先显示 | 2/2 确认 P0 | 真实可达：`fetchThreadMessagePages` 收集全量后 `applyThreadMessageItems`，`syncThreadState` 等 `messagesPromise` | 有缓存时可不闪 placeholder；无可信缓存时仍阻塞首屏 | 保持 P0 |
| 避免一次性挂载全历史 | 2/2 确认 P0，1 票建议改措辞 | 真实可达：当前前台全量 apply 后 `messages.map` 全量挂 DOM，Markdown 同步解析，Mermaid 挂载即 render | `mergeTimelineItems` 只去重/排序，不提供窗口化或懒 materialize | 保持 P0，措辞改为“任何补齐/加载不得一次性挂载全历史” |
| request token / generation guard | 2/2 部分确认，建议降级 | 风险可达但边界更窄：`activeChanged` 已防 snapshot 抢 active；message apply 与 finally 清 loading 无 per-request token | 已有 `activeChanged` 和可信缓存防护，未覆盖同 thread A/B/A 旧请求收尾 | 从 P0 降为 P1，聚焦 message/backfill apply/loading |
| 初始页大小 50/100 | 2/2 确认 P1 | 可达：首包常量 300；但只有停止全量循环后收益才稳定 | 无直接防护；当前 page size 被测试固化 | 保持 P1 |
| Markdown/Mermaid 懒渲染 | 2/2 确认 P1 | 可达：Markdown render 同步解析；Mermaid 组件挂载后立即 render | React slow render trace 能观测慢渲染，但不防卡顿 | 保持 P1 |
| 后台补齐限流/取消/去重 | 1 确认，1 部分确认 | 当前没有真正后台补齐；若按方案引入后台补齐，风险会可达 | 现有 `startThreadMessagesLoad` 无 in-flight 去重；后端 `resumeInFlight` 不覆盖消息页 RPC | 改为 P1 准入要求 |
| 性能埋点 | 2/2 部分确认 | 专项缺口可达：已有 RPC duration、patch slow、React slow render，但缺 click-to-first-message 和历史文件/页读取指标 | 通用 observability 已覆盖部分慢点观测 | 改为“补充专项性能埋点” |
| 完整虚拟列表不作为首版唯一修复 | 2/2 确认 P2 | 判断成立：当前无 windowing；但一上来完整虚拟列表风险较高 | 无上层防护 | 保持 P2 |
| 精确 total / 全局计数索引不做首版 | 2/2 确认 P2 | 判断成立：精确 total 当前来自全量读取，是放大点 | 无索引防护 | 保持 P2 |
| 只做 placeholder/动画 | 2/2 确认 P2 | 判断成立：placeholder 不改变 RPC/IO/DOM 成本 | 可信缓存已经缓解部分刷新闪烁 | 保持 P2 |
| 前端继续推断 numeric `before` | 2/2 确认 P2 | 风险可达：前端优先取 numeric id，后端可能合成 ID；tail page 后语义更易错 | 当前全量 decorate 后 cursor 尚可工作，真分页后防护不足 | 保持 P2 |

## P0 必须做

| 问题 | 生产可行性 | 性能优先 | 风险 | 准确性 | 维护性 | 结论 |
|---|---:|---:|---:|---:|---:|---|
| 后端 `thread/messages` 改为真实分页，不能先读全量再分页 | 中高 | 最高 | 中高 | 高 | 高 | 必须新增分页语义，避免 `ReadHistory(..., 0)` 服务首屏 |
| 响应改由 `hasMore/nextBefore` 驱动，不再依赖精确 `total` | 高 | 最高 | 中 | 最高 | 高 | `total` 可保留兼容，但不能阻塞首屏或驱动循环 |
| Codex、Claude、persisted jsonl 支持 tail/page 读取 | 中 | 最高 | 中高 | 高 | 中高 | 至少首版支持最近页读取，避免全文件扫描 |
| 前端首屏只等待最近页，不等待全量补齐 | 高 | 高 | 中 | 高 | 高 | 第一页返回即渲染，旧页后台或滚动加载 |
| 避免任何补齐/加载把全历史一次性挂载 | 高 | 高 | 中 | 高 | 中高 | 旧页应按需 materialize，不能把几千条直接塞进 timeline |

## P1 建议做

| 建议 | 生产可行性 | 性能优先 | 风险 | 准确性 | 维护性 | 结论 |
|---|---:|---:|---:|---:|---:|---|
| 初始页大小从 300 降到 50 或 100 | 高 | 高 | 低中 | 高 | 高 | 首屏更轻，后续页可继续较大批量 |
| Markdown / Mermaid 懒渲染 | 高 | 中高 | 低中 | 高 | 中高 | 离屏复杂内容不应立即解析和渲染 |
| 消息 apply/loading 增加 request token / generation guard | 高 | 中 | 中 | 高 | 高 | 已有 `activeChanged` 只保护 snapshot active；消息页和 loading 收尾仍需防旧请求 |
| 若引入后台补齐，必须限流、可取消、按线程去重 | 高 | 中高 | 低中 | 高 | 高 | 防止多页补齐制造重复 RPC/IO |
| 补充专项性能埋点 | 高 | 中 | 低 | 高 | 高 | 现有通用 RPC/render trace 不足以定位 click-to-first-message、历史文件大小、页读取耗时 |

## P2 后续观察 / 暂不建议

| 项目 | 结论 |
|---|---|
| 一步到位重做完整虚拟列表 | 暂不作为首版唯一修复；可变高度聊天、滚动锚点、Markdown 内容复杂，风险偏高 |
| 首版建立全局消息计数索引或强求精确 total | 暂不建议；容易抵消 tail 读取收益 |
| 只做 placeholder、动画或 loading 文案优化 | 删除为核心方案；会掩盖真实瓶颈 |
| 前端继续用 numeric `before` 推断游标 | 暂不建议；tail 读取后合成 ID 语义容易错 |

## 最小可上线方案

1. 后端扩展 `thread/messages`：返回 `{ messages, hasMore, nextBefore }`，最近页只读取 `limit` 或 `limit + 1` 条。
2. 保留旧 `ReadHistory(limit=0)` 给 compact、fork、token 估算等全量场景；聊天首屏走新的 page 读取接口。
3. Codex、Claude、persisted jsonl 路径实现一致的分页语义，`nextBefore` 由后端生成，前端不自行猜 cursor。
4. 前端拆分加载状态：`initialLoading` 只等 snapshot + 最近页；`backfillLoading` 后台或滚动触发旧页加载。
5. 保留现有 snapshot active 防护；若首版引入后台补齐，则 message/backfill apply 和 loading 清理必须带 generation guard。
6. Timeline 只 materialize 当前已展示页，后台旧页不要一次性全部挂载。

## 必要测试覆盖

- 前端：无缓存 thread 第一页返回即显示，不等待第二页。
- 前端：有可信缓存时刷新期间不闪 placeholder。
- 前端：A/B/A 快速切换，旧请求不能覆盖当前 thread、清错 loading 或误置 ready。
- 前端：后台补齐保持时间顺序、去重，并由 `hasMore/nextBefore` 驱动。
- 后端：`thread/messages limit=N` 不调用 `ReadHistory limit=0` 服务首屏。
- 后端：Codex、Claude、persisted fallback 分页顺序、`hasMore`、`nextBefore` 一致。
- 后端：长行、空消息、系统噪声过滤、重复时间戳场景分页正确。
- 回归：旧 `ReadHistory(limit=0)` 全量契约仍可用于 compact、fork、token 等非首屏路径。
