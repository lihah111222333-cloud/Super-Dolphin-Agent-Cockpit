# 政企工作流模板库技术任务拆分

**状态:** 按 SOP 修正版执行
**目标:** 以 6 类政企模板库为主线，替换旧的“三场景卡片直接启动 brief”方案。

## T1. 内置模板资产

- 新增 `internal/platform/shared/workflowtemplates/assets/manifest.json`。
- 新增 6 个 `government-enterprise/*.yaml` 模板：
  - `government-enterprise/promo-video`
  - `government-enterprise/daily-weekly-report`
  - `government-enterprise/project-briefing`
  - `government-enterprise/meeting-minutes`
  - `government-enterprise/data-analysis-brief`
  - `government-enterprise/approval-material`
- 每个模板包含 `ui_schema`、`dag_template`、复核节点、唯一 `final_node_key` 和 `final_output`。

验收：

- 模板数量为 6。
- `version` 为正整数。
- `title`、`description` 覆盖 `zh` 和 `en`。
- 每个模板 `requires_review=true`。
- final 节点依赖 review 节点。
- 每个节点包含 `config.ui`。

## T2. 模板加载与渲染包

- 新增 `internal/platform/shared/workflowtemplates`。
- 提供 `ListTemplates`、`GetTemplate`、`RenderDAGDraft`。
- 只读加载模板资产，不写 DB、不创建 DAG、不写 shared_file。

验收：

- 缺必填参数 fail-fast。
- `output_path` 必须位于 `reports/workflows/` 或 `dag/`，并包含 `{{run_id}}` 或 `{{run_key}}`。
- 禁止绝对路径、`..`、模板资产目录写入。
- `output_types` 只允许 `video/pptx/docx/xlsx/markdown/pdf/json`。
- 视频模板缺少 `outputs.to_artifact` 或 `video_with_audio` 契约时加载失败。
- 渲染结果包含模板来源 metadata。

## T3. 模板 RPC 与 host tool

- 新增 `internal/module/workflowtemplate`。
- 注册 UI RPC：
  - `workflowTemplates/list`
  - `workflowTemplates/get`
  - `workflowTemplates/renderDag`
- 注册 DAG Designer 只读工具：
  - `workflow_template_list`
  - `workflow_template_get`
  - `workflow_template_render_dag`
- 支持 `category`、`business_flow`、`output_type`、`supports_schedule` 筛选。
- 支持 `version`、`values/user_inputs`、`runtime_context` 入参。

验收：

- RPC 返回 JSON 可供前端使用。
- host tool 使用严格 schema，未知字段 fail-fast。
- 版本不匹配时报明确错误。
- 不新增数据库表和 dashboard Service 写路径。

## T4. Workflow 模板目录入口

- `WorkflowPage.jsx` 从模板 RPC 读取政企模板摘要。
- 自动化页展示“政企工作流模板库”。
- 卡片展示业务流、节点数、复核、定时能力、输出类型。
- 支持业务流、输出类型、定时能力筛选。
- 点击卡片只选中模板，不立即启动设计器。

验收：

- 6 类模板常驻展示。
- 筛选条件能改变卡片列表。
- 选模板前不调用 `startThread`。

## T5. 模板详情、参数表单和 DAG 预览

- 选中模板后调用 `workflowTemplates/get`。
- 根据 `ui_schema` 渲染表单。
- 本地渲染 DAG 草案预览，展示节点、依赖、复核节点和 final 节点。
- 必填字段缺失时前端阻断。

验收：

- 表单显示主题、输入材料、输出格式、复核人、保存目录等字段。
- 默认保存目录为 `reports/workflows/<template-slug>/{{run_id}}/`。
- 预览可看到 review/final。

## T6. DAG Designer 创建通道

- 继续复用 `thread/start` + `turn/start`。
- `thread/start` 使用 `agentKey: dag_designer`、`promptKey: main/dag_designer_zh`、`deferSpawn: true`、`providerNativeSkills: false`。
- brief 包含 `template_id`、`template_version`、`ui_schema`、`dag_template`、用户参数、`review_node`、`final_node_key`、`config.ui`、`outputs.to_sharedfile`。
- 页面保留设计进度和“查看设计对话”入口。

验收：

- `startThread` 先于 `startTurn`。
- `startThread` 失败不调用 `startTurn`。
- `startTurn` 失败时显示明确错误。
- 模板启动后不自动切到聊天页。

## T7. Prompt 契约

- 更新 `main/dag_designer_zh`。
- 增加 6 类政企模板、阶段评估、顺序/并行、skill/command 发现、`config.ui`、`sharedfile`、`final_node_key`、`CRON_TZ=Asia/Shanghai` 约束。
- 明确 `workflow_template_render_dag` 只渲染草案，不落库、不启动。

验收：

- Prompt 测试覆盖模板工具和关键约束。
- 不教授旧字段或不可用工具。
- 不保留 `enterprise-workflows/` 旧路径。

## T8. Final Output 展示

- 复用 `WorkflowFinalOutputPanel`。
- `md/json/text` 内嵌读取。
- `mp4` 视频预览。
- `pptx/docx/pdf/xlsx` 文件卡片和系统打开。

验收：

- 文本、JSON、视频、Office/PDF 路径均有测试覆盖。
- 打开失败时显示明确错误。

## T9. 验证

Go：

```powershell
go test ./internal/platform/shared/workflowtemplates ./internal/module/workflowtemplate ./internal/platform/toolbridge ./internal/platform/shared/builtinprompts -count=1
```

前端：

```powershell
cd frontend-app
npm test -- --run src/pages/workflows/WorkflowPage.test.jsx
npm run lint
npm test
npm run build
```

收尾：

```powershell
git diff --stat
git diff --check
```
