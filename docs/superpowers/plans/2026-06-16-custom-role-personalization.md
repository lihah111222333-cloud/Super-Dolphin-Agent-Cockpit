# 定制角色个性化打通实施计划

> 执行要求：按 `superpowers:executing-plans` 分步落地；实现前先用 TDD 写最小失败用例；只改本功能相关文件。

## 目标

打通“定制角色”页面里的个性化能力：

- 个人资料可在前端编辑、保存、重新加载。
- 记忆导入入口可打开现有 prompt intent 向导，并默认进入 `recall` 场景。
- 后端新增独立 `personalization` 模块，负责 profile 的持久化、RPC 接口和 prompt 注入。
- prompt 动态段新增 `personalization_profile`，在生成 prompt 时注入非空 profile 信息。

## 方案选择

采用 A 方案：复用现有 `uipreference.Store` 保存项目级 profile，并新增独立业务模块。

选择原因：

- 不引入数据库 schema 变更，风险低。
- `uipreference.Store` 已经适合保存 UI 级、项目级偏好数据。
- 新模块边界清晰，避免把定制角色逻辑继续堆进 prompt 模块。

## 后端变更

新增 `internal/module/personalization`：

- `dto.go`：定义 profile、RPC params/result 和字段长度限制。
- `service.go`：实现读取、保存、裁剪和校验 profile。
- `provider.go`：实现 `contract.DynamicSectionProvider`，将 profile 渲染成 prompt 动态段。
- `rpc.go`：注册 `personalization/profile/get` 和 `personalization/profile/save`。
- `module.go`：通过 fx 装配 service、handler、provider，并注册动态段 provider。

修改现有后端路径：

- `internal/contract/prompt.go`：增加 `DynamicSectionPersonalizationProfile` 常量。
- `internal/module/prompt/dynamic.go`：把 `personalization_profile` 加入动态段白名单。
- `internal/app/modules.go`：装配 `personalization.Module`。

后端约束：

- `cwd` 为空必须返回错误。
- profile JSON 损坏必须 fail-fast，不静默吞错。
- profile 字段保存前统一 `strings.TrimSpace`。
- 短字段最大 80 rune，长字段最大 1200 rune。

## 前端变更

修改 `frontend-app/src/shared/api/backendApi.js`：

- 增加 `getPersonalizationProfile`。
- 增加 `savePersonalizationProfile`。
- 增加对应 RPC method 常量和 payload 校验。

修改 `frontend-app/src/shared/api/backendApi.contractMatrix.js`：

- 将两个新 RPC 加入前端契约矩阵。

修改 `frontend-app/src/features/prompts/PromptPageView.jsx`：

- 进入页面时按当前 `cwd` 加载 profile。
- 编辑表单使用本地 draft，避免把查询结果直接复制成长期状态。
- 保存后更新 TanStack Query cache。
- “导入外部记忆”按钮打开现有 prompt wizard，并默认使用 `recall`。

## 测试计划

后端：

- service 测试覆盖空 profile、保存裁剪、缺少 cwd、超长字段、损坏 JSON。
- provider 测试覆盖空 profile 不注入、非空 profile 渲染。
- RPC 测试覆盖 get/save 的请求和响应 shape。

前端：

- backend API 测试覆盖 get/save facade 和缺少参数校验。
- Prompt 页面测试覆盖加载/保存 profile。
- Prompt 页面测试覆盖记忆导入按钮打开 `recall` 向导。

## 验证命令

计划完成后运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/personalization -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/module/prompt -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/contract -count=1
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 ./internal/app -count=1
go test ./internal/module/personalization ./internal/module/prompt -run "TestPromptProvider|TestProfileRPC|TestRegisterDynamicProviderMakesSlotRenderable" -count=1
```

```powershell
cd frontend-app
npm test -- src/shared/api/backendApi.test.js src/shared/api/backendApi.contractMatrix.test.js src/features/prompts/PromptPageView.test.jsx
npm run lint
npm test
npm run build
```

## 提交边界

只提交以下范围：

- `docs/superpowers/plans/2026-06-16-custom-role-personalization.md`
- `internal/module/personalization/**`
- `internal/contract/prompt.go`
- `internal/module/prompt/dynamic.go`
- `internal/app/modules.go`
- `frontend-app/src/shared/api/backendApi*`
- `frontend-app/src/features/prompts/PromptPageView*`

不处理当前工作区里已有的聊天页、截图、codemap 等无关改动。
