# 多厂商模型管理 UI/后台设计

日期：2026-06-23

## 目标

新增一个多厂商模型管理模块，让用户在当前 React UI 中可视化管理 OpenRouter、DeepSeek、Qwen 等模型厂商配置，并把选中的厂商应用到现有 Codex 启动偏好中。

v1 的目标是配置中心，不是商业网关。它提供厂商列表、API key 环境变量状态、预算配置、token pool 策略和可视化编辑页，但不采集真实用量、不做扣费账本，也不保存 API key 明文。

## 已批准范围

- 使用当前 `frontend-app` 的设置页体系新增“Model Providers”管理界面。
- 后端新增 `modelProviders/*` RPC 门面，复用现有 UI preference 存储。
- 存储厂商 registry JSON 到 `settings.modelProviders.registry`。
- API key 只保存环境变量名，不保存明文；后端只检测 `os.Getenv(envKey)` 是否存在并返回脱敏状态。
- OpenRouter、DeepSeek、Qwen 作为默认厂商模板出现，用户可以启用、编辑、应用。
- 应用厂商时写入现有 Codex 偏好里的 `codexModelProvider`，可选写入厂商绑定的 `codexHome` 和 `codexInstanceKey`。

## 不做的事

- 不新增数据库表或迁移。
- 不存储 API key 明文，不提供 plaintext key 输入框。
- 不做真实 billing、余额同步、用量上报或扣费账本。
- 不做多用户 RBAC 或团队级权限。
- 不把 OpenRouter、DeepSeek、Qwen 提升为新的顶层 provider；它们仍然通过 Codex model provider 配置进入启动参数。
- 不引入新的前端或后端依赖。

## 架构

### 前端

在当前设置体验中增加“Model Providers”页面或面板：

- 左侧是厂商列表，展示名称、启用状态、是否 active、API key 环境变量是否已配置。
- 右侧是厂商详情表单，编辑 `baseURL`、`envKey`、`codexModelProvider`、`defaultModel`、预算和 token pool 字段。
- 页面没有 API key 明文输入框，只允许填写环境变量名并展示检测结果。
- “保存”只更新 registry。
- “应用”会调用后端 apply RPC，把厂商映射到现有 Codex 偏好。

### 后端

新增轻量 RPC 门面，保持在 UI state / preference 边界内：

- `modelProviders/list`：读取 registry，补齐默认模板，返回带脱敏环境变量状态的厂商列表。
- `modelProviders/save`：校验并保存 registry。
- `modelProviders/apply`：校验目标厂商可用后，写入现有 Codex provider 偏好。

后端不暴露环境变量值，不记录 API key，不新增 secret store。

### 存储

复用现有 UI preference 存储，key 为：

```text
settings.modelProviders.registry
```

示例结构：

```json
{
  "vendors": [
    {
      "id": "openrouter",
      "label": "OpenRouter",
      "enabled": true,
      "baseURL": "https://openrouter.ai/api/v1",
      "envKey": "OPENROUTER_API_KEY",
      "codexModelProvider": "openrouter",
      "defaultModel": "openai/gpt-4.1",
      "codexHome": "",
      "codexInstanceKey": "",
      "budget": {
        "dailyUsd": 5,
        "monthlyUsd": 100
      },
      "tokenPool": {
        "priority": 10,
        "fallbackVendorId": "deepseek"
      }
    }
  ],
  "activeVendorId": "openrouter"
}
```

## 校验和失败策略

所有写入都 fail-fast：

- `id`、`label`、`baseURL`、`envKey`、`codexModelProvider`、`defaultModel` 不能为空。
- `baseURL` 必须是 HTTP(S) 绝对 URL。
- `envKey` 必须匹配安全环境变量名格式：大写字母、数字、下划线，且不能以数字开头。
- `budget.dailyUsd`、`budget.monthlyUsd`、`tokenPool.priority` 如填写则必须是非负数。
- `fallbackVendorId` 如填写，必须指向 registry 中存在的厂商。
- 应用厂商时，目标厂商必须存在、启用、配置有效，且对应环境变量必须存在。

任何校验失败都返回明确错误，不静默降级为默认厂商。

## UI 行为

- 默认显示 OpenRouter、DeepSeek、Qwen 三个厂商模板。
- 未保存过 registry 时，`list` 返回默认模板，页面可直接编辑。
- 厂商行展示四类状态：active、enabled、env configured、env missing。
- 表单保存后保持当前选中厂商。
- 应用成功后将该厂商标记为 active，并同步现有 Codex 启动偏好。
- 预算和 token pool 字段在 v1 中只表示策略配置，不承诺真实扣费或自动路由。

## 测试策略

后端测试覆盖：

- 默认 registry 返回 OpenRouter、DeepSeek、Qwen。
- 保存时拒绝非法 URL、非法 env key、缺失必填字段和不存在的 fallback。
- list 返回 configured/missing 状态但不返回 API key 明文。
- apply 拒绝禁用、缺 env、非法厂商。
- apply 成功写入现有 Codex 偏好。

前端测试覆盖：

- 页面渲染厂商列表和状态。
- 表单保存调用正确 RPC。
- 缺失 env 时应用按钮或错误提示表现正确。
- 应用成功后 active 状态更新。

验证命令按改动面执行：

- Go 后端：`pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/uistate -count=1`
- 前端：在 `frontend-app` 下运行 `npm run lint`、`npm test`、`npm run build`

## 后续实现边界

实现计划应保持小步：

1. 后端先做 registry 类型、默认模板、校验和 RPC。
2. 前端再做 API wrapper、设置页入口和 Model Providers 表单。
3. 最后补测试和运行对应验证。

若后续需要真实 billing、跨厂商自动路由或持久 token pool 运行时调度，应作为单独规格处理。
