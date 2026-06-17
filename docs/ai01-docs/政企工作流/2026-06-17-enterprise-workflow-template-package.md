# 政企工作流模板库 SOP 落地方案

**日期:** 2026-06-17
**工作区:** `D:\project\Super-Dolphin-worktrees\feature-integration-20260617`
**分支:** `codex/feature-integration-20260617`

## 1. 目标

把“政企自动化”从少量前端卡片升级为仓库内置模板库：

- 首版提供 6 类模板：宣传视频、日报/周报、项目汇报、会议纪要、数据分析简报、审批材料。
- 模板按业务流程组织，输出类型只是模板字段，不按 PPT/Word/视频单独拆分类目。
- Workflow 页支持模板目录、筛选、详情表单、DAG 草案预览和创建入口。
- DAG Designer 与 Workflow UI 使用同一份模板资产，避免两套模板口径分叉。
- 继续复用现有 `thread/start`、`turn/start`、`main/dag_designer_zh`、`task_create_dag`、`outputs.to_sharedfile`、`outputs.to_artifact`。
- 不新增工作流引擎、DB schema、RBAC、DAG 级审批阻断、外部 OA/IM/网盘/审批系统集成。

## 2. 技术映射

| SOP 要求 | 本项目落点 |
|---|---|
| 内置模板资产 | `internal/platform/shared/workflowtemplates/assets/manifest.json` 与 `government-enterprise/*.yaml` |
| 模板只读加载、校验、渲染 | `internal/platform/shared/workflowtemplates` |
| UI 模板接口 | `workflowTemplates/list`、`workflowTemplates/get`、`workflowTemplates/renderDag` |
| DAG Designer 模板工具 | `workflow_template_list`、`workflow_template_get`、`workflow_template_render_dag` |
| 前端模板目录、表单、预览 | `frontend-app/src/pages/workflows/WorkflowPage.jsx` |
| 最终材料展示 | 复用 `WorkflowFinalOutputPanel`，文本/JSON 内嵌预览，视频预览，Office/PDF 系统打开 |

## 3. 模板资产契约

模板路径：

```text
internal/platform/shared/workflowtemplates/assets/
  manifest.json
  government-enterprise/
    promo-video.yaml
    daily-weekly-report.yaml
    project-briefing.yaml
    meeting-minutes.yaml
    data-analysis-brief.yaml
    approval-material.yaml
```

首版模板 id：

- `government-enterprise/promo-video`
- `government-enterprise/daily-weekly-report`
- `government-enterprise/project-briefing`
- `government-enterprise/meeting-minutes`
- `government-enterprise/data-analysis-brief`
- `government-enterprise/approval-material`

关键规则：

- `version` 使用正整数，破坏性变更递增。
- `title`、`description` 至少提供 `zh` 和 `en`。
- `output_types` 只允许 `video`、`pptx`、`docx`、`xlsx`、`markdown`、`pdf`、`json`。
- `ui_schema` 显式声明 `key`、`type`、`required`、`label.zh`、`placeholder.zh`、`help.zh`。
- 每个节点必须有顶层 `assigned_to` 和 `config.ui`。
- 每个模板必须有复核节点，且 final 节点依赖复核节点。
- `final_node_key` 必须唯一并匹配真实节点。
- 默认输出路径只允许 `reports/workflows/` 或 `dag/` 前缀，并必须包含 `{{run_id}}` 或 `{{run_key}}`。
- 视频模板的 final 节点必须使用 `outputs.to_artifact`，并声明 `source_tool: video_with_audio`、`source_path_field: output_path`。

## 4. Workflow 页交互

1. 用户进入自动化页，点击或查看“政企工作流模板库”。
2. 模板目录展示名称、业务流、输出类型、预计节点数、是否复核、是否支持定时。
3. 用户可按业务流、输出类型、定时能力筛选模板。
4. 点击模板卡片只选中模板，不立即启动模型。
5. 页面显示模板详情、参数表单和 DAG 草案预览。
6. 用户填写主题、材料、输出格式、复核人、保存目录等必填项。
7. 点击“创建工作流”后，前端创建 deferred DAG Designer 线程并发送模板 brief。
8. 页面保留在 Workflow 页，展示设计进度和“查看设计对话”入口。
9. DAG Designer 必须先发现模型、prompt、command card、shared_file，再调用 `task_create_dag`。

## 5. Prompt 约束

`main/dag_designer_zh` 需要遵守：

- 政企模板场景先调用 `workflow_template_list/get/render_dag`。
- 创建 DAG 前说明阶段数、依赖关系、顺序/并行关系、每阶段 skill/prompt/command_card 选择。
- 不硬编码 provider、model、prompt_key、agent_key、command_ref、sharedfile path。
- automation 只能使用已发现的 `command_card`。
- 大结果写 `outputs.to_sharedfile`，视频成片在发现 `video_with_audio` 后使用 `outputs.to_artifact`。
- 支持定时的场景默认使用 `CRON_TZ=Asia/Shanghai`，用户未明确时间时必须确认。
- 目标格式如 `pptx/docx/pdf/mp4` 缺少生成能力时，必须明确提示能力缺口，不能伪造文件。

## 6. 边界

- 模板库只读加载和渲染，不写 DB、不创建 DAG、不写 shared_file。
- UI 的“创建工作流”仍通过 DAG Designer 创建，避免新增未验收的后端落库 RPC。
- “复核”首版表达为复核材料/复核结论节点，不宣称已有 DAG 级审批阻断。
- 外部发布、OA/IM/网盘/审批系统流转不在首版范围。

## 7. 后续扩展

- 在应用层增加 `workflow_template_create_dag`，内部复用 `RenderDAGDraft` 和现有 `task_create_dag` 语义，减少聊天二次解释成本。
- 为 Office/PDF/视频生成补齐可发现 command card 后，增强模板能力缺口检查。
- 设计真正 DAG HITL 状态机时单独立项，不混入模板库首版。
