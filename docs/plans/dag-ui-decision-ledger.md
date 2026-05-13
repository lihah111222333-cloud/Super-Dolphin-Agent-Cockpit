# DAG UI 决策台账

> 日期：2026-05-14
> 状态：UI 方案拍板前的总台账。本文只记录产品/UI 决策，不替代具体 implementation plan。
> 适用范围：当前真实前端以 `cmd/agent-terminal/frontend/vue-app` 为准；`internal/ui/wails/frontend` 仍是嵌入壳/占位，不作为 DAG UI 设计源头。

## 1. 为什么单列本文

DAG 后端、C-A lifecycle、final_output、sharedfile、prompt_template-first 路线已陆续落地，但 UI 决策分散在蓝图、实施计划、旧 P10、ADR 和聊天记录中。后续所有 DAG 前端任务（T5/T6/T7/T8/F8/F9/F10/F11/H15 等）开工前，先回本文确认是否需要用户拍板。

## 2. 已锁定的 UI 产品边界

1. **DAG 是一等入口**：左侧 DAG 入口保留，目标是让用户在 UI 上看到 DAG 列表、详情、Start、节点进度、run 历史和最终产物。
2. **实时刷新先 polling**：节点状态和列表最新 run 状态先用 3-5s polling；WebSocket/订阅升级放加固阶段，除非 polling 体验已证明不够。
3. **final_output 单产物优先**：H14 只支持单 `final_output` pointer；文件型展示打开/读取/下载，小 text/json 展示摘要；bundle/multi-artifact 等真实 dogfood 证明强需求后再升级。
4. **Shared Files 不是废弃入口**：sharedfile 是文件存储/协作空间，也可承载最终文件；UI 通过 `final_output` 高亮/筛选最终产物，折叠 working/debug 中间产物。
5. **prompt_template-first 优先**：当前不恢复命令卡模板库/管理 UI，不做 shell/http kind 管理界面；需要外部系统集成时另立 ADR/任务。
6. **旧 P10 模板库降级**：旧 P10 的 DAG 模板库、fork preview、lineage、复杂 cost preview 不进入当前 M3/M4 主线；当前只保留“AI 设计 DAG + 用户微调 + Start”的产品闭环。
7. **HITL 留位不实装**：`waiting_human` enum 已在状态机中留位，完整人审 UI、通知、timeout 策略放 H10 或真实场景触发。
8. **外部 trigger 推迟**：当前优先 manual/scheduled；External RPC trigger surface 后置。

## 3. 需要用户拍板的 UI 决策

| ID | 决策点 | 推荐默认 | 涉及任务 |
|---|---|---|---|
| UI-D1 | DAG 主界面形态 | 把 `DagsPage` 从 DataPage 薄壳升级成 DAG Console：左侧列表/顶部筛选，右侧详情；modal 只作为过渡或窄屏详情。 | T5/T7/F10 |
| UI-D2 | Start CTA 门禁 | v1 在 `draft/ready` 且 manual/scheduled DAG 可见 Start；缺 caller/owner/运行中等情况显示禁用原因。复杂 cost preview 不进 v1。 | T5.3 |
| UI-D3 | 状态色与 legend | 按主状态 9 态显示色块；额外派生 `draft`、`ready_unassigned`、`finalizing` 只作为 UI display state，不写回 DB。 | T5.2/T7/F10 |
| UI-D4 | 节点行信息密度 | 节点行展示 title、node_type、status、provider/model/agent_key、assigned_to、spawning_thread_id 链接、最终产物标记；高级 config 折叠。 | T5.2/T6 |
| UI-D5 | 子 agent/thread 跳转 | 用 `spawning_thread_id` 作为一等链接，不解析 `node.result`；thread 已归档时显示“已归档/可打开历史”。 | T6.1 |
| UI-D6 | AI 帮你设计流程入口 | DAG 页放“AI 设计流程”按钮，点击后创建/切到新 thread，并注入 designer prompt 与可用资源列表；不在 DAG 页内做复杂 wizard。 | T8.1/T8.2 |
| UI-D7 | 节点编辑表单范围 | v1 只做 typed schema 表单，先覆盖 agent 节点的 prompt/model/provider/inputs/outputs/depends_on；automation/hybrid 先只读或高级折叠。 | F8.1/F8.2 |
| UI-D8 | 拓扑图技术路线 | v1 用 mermaid 只读拓扑 + 节点点击联动详情；拖拽编辑/d3/canvas 放后续。 | F9.1 |
| UI-D9 | run 历史入口 | DAG 详情内置 Run History Panel，默认选最近 run；点击历史 run 后切换节点状态快照、final_output、事件/错误摘要。 | F10.1 |
| UI-D10 | Shared Files 清理体验 | H15 做软删除/TTL 预览/批量清理；被 `final_output` 引用、pinned、running run 相关文件默认保护。删除前给 UI 可见确认和导出提示。 | H15 |
| UI-D11 | final_output 通知入口 | v1 先在 DAG 详情和 Shared Files 展示；定时任务产品化时，通知/任务摘要里加“查看最终产物”链接，但不推送中间产物。 | H15/通知后续 |
| UI-D12 | 错误信息人话翻译 | failure_class、ADR-006 size cap、caller identity、verdict_lost 等进入错误 catalog；节点详情展示“发生了什么 / 下一步怎么修”。 | H1/F12 |
| UI-D13 | waiting_human/HITL | 不进 M3；等真实 ask_human 场景出现后，一次性设计审批卡、timeout、reject/approve 后续状态。 | H10 |
| UI-D14 | sharedfile 锁可视化 | F11 剩余范围是 reads/writes/lock_mode 与节点联动；final_output 高亮已由 H14 完成，不再算 F11 阻塞。 | F11.1 |
| UI-D15 | 模板/实例编辑关系 | 当前不做 DAG template library；用户微调的是 DAG draft/ready 定义，run 使用 snapshot。旧 P10 的“Save as template”后置。 | F8/F10 后续 |
| UI-D16 | 运行中编辑策略 | 运行中 DAG 只允许已完成节点后追加满足条件的新节点；普通 edit/remove/update_dag 在 UI 禁用并解释原因。 | F4.5/F8 |
| UI-D17 | 多 run 并发可视化 | F6.5 前 UI 可以按当前单 running run 认知；F6.5 后 Run History 必须区分并发 run，不把模板状态和 run 状态混在一起。 | F6.5/F10 |
| UI-D18 | Wails 壳范围 | 不把 `internal/ui/wails/frontend/index.html` 当 DAG UI 主目标；如要迁移/替换壳，另开桌面壳任务。 | UI 基建后续 |
| UI-D19 | 实时事件与大规模拓扑阈值 | v1 继续用 3-5s polling；WS/订阅、cursor node page、cluster/virtualized topology 只在真实大 DAG 或 stale UI 痛点出现后恢复。禁止提前渲染 10000-node mermaid。 | T5/T7/F9/H6b |
| UI-D20 | 金融/合规模板预设 | 不在当前 DAG Console v1 做金融预设 badge、合规说明、模板 Use/Save preview；等 P9/P12/P13 和真实金融 dogfood 同时触发后再设计。 | P10/P12/P13 后续 |
| UI-D21 | 编辑历史、回滚与多人冲突 | v1 不做 realtime collaboration、不做 revision history UI；先依赖版本/CAS 错误提示。H4 触发后再做“正在编辑”、undo/rollback、revision diff。 | H4/F8 |
| UI-D22 | 通知噪音与本地化 | `dag_node_completed` 等后台清理 reason 不应原样进用户通知；后续通知层做本地化映射或白名单 skip。final_output/cron miss/run timeout 通知需要统一降噪策略。 | ADR-016/H6b/H15 |
| UI-D23 | 节点对话入口 | H9 `task_post_message` 未落地前，不在 DAG 页做节点 chat surface；v1 用子 thread 链接 + sharedfile/final_output 承接上下文和产物。 | H9/T6 |
| UI-D24 | 高级字段注册表 | verify/activity/cost/growth/swarm/output_schema 等 P8-P13 字段不进 v1 主表单；后续必须通过 typed registry + feature gate 暴露，不让用户直接编辑 raw JSON/YAML。 | P8-P13/F8 后续 |

## 4. 推荐实现顺序

1. **U0：冻结 UI 方案文档**
   先用本文收齐决策，再写一份 DAG Console v1 spec。只做设计，不改前端。

2. **U1：DAG Console v1**
   覆盖 T5/T6/T7/F10 的最小闭环：列表字段、详情节点列表、Start、子 thread 链接、最近 run/history、final_output 卡片。目标是“能看见、能启动、能追踪”。

3. **U2：AI 设计 + 用户微调闭环**
   覆盖 T8/F8/F9：AI 设计按钮、typed schema 表单、mermaid 拓扑、用户改一处 prompt 后 Start。目标是 Need 2 端到端。

4. **U3：Shared Files / retention / cleanup**
   覆盖 H15/F11：final_output 保护、中间产物折叠、TTL/软删/批量清理、reads/writes/lock_mode 联动。

5. **U4：H 阶段高级产品化**
   错误 catalog、HITL、通知入口、Hybrid/external action、模板库/Save as template、复杂 cost preview、编辑历史/回滚、task_post_message、WS/大规模拓扑、监控告警 UI 等按真实场景逐项恢复，不提前堆复杂度。

## 5. 当前文档缺口已收敛为本台账

- H14 已完成 final_output UI；F11 不再承担 final_output 高亮，只剩 sharedfile 锁可视化和中间产物体验深化。
- Need 1 还缺 T7 列表字段和 F10 run 历史 UI 才算用户可见闭环。
- Need 2 的后端与 prompt_template seed 已基本到位，剩余主要是 T8/F8/F9 UI 设计与实现。
- 旧 P10 的模板库/preview/lineage/cost preview、金融预设、大规模 UI、WS 实时事件、多人编辑冲突均已登记为后续项，不是当前 DAG UI v1 必做项。
