# 定制角色个性化打通设计

> 日期: 2026-06-16
> 状态: 待审阅
> 方案: A - 后端新增 personalization 模块，前端打通个人资料与记忆导入入口

## 背景

当前新 UI 的 `定制角色` 页面已经具备三类角色能力:

- `专家能力`: 通过 prompt intent 创建并进入可用专家列表。
- `参考资料`: 通过 prompt intent 创建并进入 recall catalog。
- `默认规则`: 通过 prompt intent 创建并进入项目默认规则。

后端已有 `prompt-intents/*` RPC、动态 prompt section，以及 `threadprompt` provider 注入链路。也就是说，专家、参考资料、默认规则的核心链路已经存在。

当前缺口集中在两个入口:

- `个人资料` 卡片仍是占位态，无法保存用户背景、偏好和定制说明。
- `从其他 AI 导入记忆` 入口仍是禁用态，无法复用现有参考资料创建链路。

## 目标

- 在后端新增 `internal/module/personalization` 模块，负责项目级个人资料的读取、保存和 prompt 注入。
- 在前端 `定制角色` 页面提供可编辑的个人资料表单，并通过 RPC 持久化。
- 将 `从其他 AI 导入记忆` 入口接入现有 `参考资料` 创建向导，不新增导入格式解析器。
- 保持现有专家、参考资料、默认规则的创建、编辑、复制、强制使用能力不退化。

## 非目标

- 不新增数据库 migration。个人资料先使用现有 `uipreference.Store` 按项目 `cwd` 保存 JSON。
- 不新增外部依赖。
- 不实现跨 AI 平台自动解析、批量导入或文件上传。
- 不改动 legacy Vue 前端。
- 不重写 prompt asset 或 prompt intent 现有流程。

## 后端设计

新增模块位置:

- `internal/module/personalization`

模块职责:

- 注册 RPC:
  - `personalization/profile/get`
  - `personalization/profile/save`
- 使用 `uipreference.Store` 保存项目级 profile JSON。
- 注册一个独立的动态 prompt provider，将非空个人资料注入 prompt 组装链路。

建议偏好 key:

- `personalization.profile`

Profile 结构保持直接:

```go
type Profile struct {
    DisplayName        string `json:"displayName"`
    Role               string `json:"role"`
    Background         string `json:"background"`
    CustomInstructions string `json:"customInstructions"`
}
```

保存规则:

- 字段前后空白统一裁剪。
- 全空 profile 允许保存，用于清空个人资料。
- 单字段长度设置明确上限，超出直接返回 RPC 错误。
- `cwd` 为空、store 读写失败或 JSON 异常时 fail-fast，不做静默兜底。

Prompt 注入:

- 在 `internal/contract/prompt.go` 和 `internal/module/prompt/dynamic.go` 中新增专用动态 section，例如 `personalization_profile`。
- `personalization` 模块注册 provider，读取当前 `cwd` 的 profile。
- profile 全空时不输出该 section。
- profile 非空时输出简短、结构化的中文说明，供后续对话作为长期个性化背景。

模块装配:

- 在 `internal/app/modules.go` 中接入 personalization 模块。
- 只暴露 profile RPC 和 prompt provider，不新增额外全局状态。

## 前端设计

主要改动位置:

- `frontend-app/src/features/prompts/PromptPageView.jsx`
- `frontend-app/src/shared/api/backendApi.js`
- 相关测试文件

页面行为:

- `个人资料` 卡片从禁用态改为真实编辑区。
- 页面加载时调用 `personalization/profile/get`。
- 用户编辑后点击保存，调用 `personalization/profile/save`。
- 保存成功后刷新本地 profile 状态，并显示当前 profile 摘要。
- 保存失败时显示页面现有错误提示，不吞错。

表单字段:

- 昵称或显示名
- 职业或角色
- 背景资料
- 定制说明

记忆导入入口:

- `从其他 AI 导入记忆` 按钮改为可用。
- 点击后打开现有 prompt intent 向导，并默认选择 `参考资料` 类型。
- 用户在向导中粘贴来自其他 AI 的记忆内容，提交后仍走 `prompt-intents/commit`，进入现有 recall catalog。
- 不在本次实现中解析不同平台导出的专有格式。

## 数据流

个人资料:

1. 前端进入 `定制角色` 页面。
2. 调用 `personalization/profile/get` 获取当前项目 profile。
3. 用户编辑并保存。
4. 后端校验、裁剪并写入 `uipreference.Store`。
5. 后续线程构建 prompt 时，personalization provider 读取 profile 并注入动态 section。

记忆导入:

1. 用户点击 `从其他 AI 导入记忆`。
2. 前端打开现有 prompt intent 向导，默认 `kind=recall`。
3. 用户提交内容。
4. 后端现有 `prompt-intents/commit` 写入参考资料。
5. 后续 prompt 通过已有 recall catalog provider 注入。

## 测试计划

后端:

- profile get 在无记录时返回空 profile。
- profile save 会裁剪字段并持久化。
- 超长字段返回错误。
- prompt provider 在 profile 为空时不输出 section。
- prompt provider 在 profile 非空时输出包含关键字段的 section。

前端:

- `backendApi` 增加 profile get/save facade 测试。
- `PromptPageView` 加载并展示 profile。
- 编辑 profile 后会调用 save RPC。
- `从其他 AI 导入记忆` 点击后打开参考资料向导。
- 保留专家、参考资料、默认规则现有关键测试。

验证命令:

- 每个 Go 文件修改后运行 `pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 <file.go>`。
- 后端运行受影响包的 `go test` 或仓库包装命令。
- 前端运行受影响测试，必要时运行 `npm run lint`、`npm test`、`npm run build`。

## 风险与边界

- 个人资料使用 `uipreference.Store`，避免本次引入数据库 schema 风险；后续如果需要跨项目共享，再单独设计迁移。
- prompt 注入新增专用 section，避免混入现有专家、参考资料或默认规则 provider。
- 本次实现必须只暂存和提交本功能相关文件；工作区已有的无关前端改动不得被 stage。
