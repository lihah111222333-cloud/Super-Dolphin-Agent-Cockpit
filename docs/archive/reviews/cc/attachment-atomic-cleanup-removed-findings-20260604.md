# 附件集成分支原子化清理记录（2026-06-04）

## 背景

原集成分支 `integration/attachment-audit-20260604` 中部分提交虽然修复了真实问题，但单个提交混合了多个风险域，
不满足“小步、单一行为、单一回滚面”的原子提交要求。当前 clean 分支仅保留较清晰的原子提交；以下提交未纳入。

## 未纳入提交与原因

| 原提交 | 标题 | 未纳入原因 | 删除后的影响 |
|---|---|---|---|
| `5874b8e87` | 修复附件消息时间线显示 | 同时改 timeline/history 归一化、ChatPage 渲染和多组测试；可用但提交边界偏宽 | 已发送附件在历史/时间线中可能仍不可见或丢失展示 |
| `0722f2ee5` | 阻止附件保存未完成时发送 | 同时引入 pending 计数、sendDraft 阻断、Composer 禁用态和多层测试 | 粘贴/拖拽图片保存未完成时，发送按钮/发送路径可能提前执行 |
| `9aab49652` | 补充附件粘贴拖拽失败反馈 | 混合保存失败反馈、无项目阻断提示、native/drop/paste 多入口提示体系 | 失败和项目不可用场景的用户可见反馈较弱，部分操作可能只静默返回 |
| `8df4ae2d7` | 拒绝不支持的附件类型 | 同时引入扩展名 allow/deny 策略、选择/拖拽/粘贴/发送前过滤和 Go 侧测试 | unsupported 文件类型过滤不完整；PDF/MP4 等策略不会被前端统一提示；可执行/未知类型风险需重新收敛 |
| `592fcd317` | 限制图片附件资源占用 | 同时做前端图片大小/批量 gate、去重、warning 和 Wails 解码硬限制 | 大图片/批量图片可能造成 FileReader/base64/saveClipboardImage 资源压力；Wails 解码缺少同提交硬防护 |
| `ae3d9f1d1` | 修复图片附件 MIME 识别 | 同时修 MIME allow-list、unsupported image 拦截、generic MIME fallback、paste/drop source 语义 | HEIC/TIFF 等 unsupported image MIME 可能进入读取/保存链路；真实粘贴 File 的 warning source 仍可能按 drop 记录 |
| `1c9a7b13d` | 记录附件链路修复计划 | 历史计划文档，不是 clean 分支的可执行修复 | 计划内容由本文档替代记录；不影响运行时代码 |

## 后续建议：重新拆分的原子任务

1. 时间线附件展示：只处理 sent/history/timeline attachment normalization 与 ChatPage 展示。
2. 图片保存 pending 阻断：只处理 `attachmentSavePendingCount`、发送禁用和 `sendDraft` 阻断。
3. 附件失败反馈：只处理 paste/drop/native drop 失败或项目不可用时的可见提示。
4. unsupported 附件策略：只处理扩展名 allow/deny、选择/拖拽/发送前一致过滤。
5. 图片资源限制：拆成前端 gate 与 Wails 解码硬限制两个提交。
6. 图片 MIME 稳定性：只处理 supported/unsupported MIME 与 generic MIME fallback。
7. paste/drop source 语义：只处理真实 paste File 传递 `source='paste'`，不和 MIME 改动混合。

## 当前 clean 分支保留的提交

- 修复原生拖拽附件目标过滤
- 修复拖拽附件路径兜底
- 收敛附件发送输入契约
- 修复粘贴文件附件链路
- 放宽原生附件拖拽目标
- 补齐拖拽路径兜底
- 修复 `turn/start` 附件合约
