# 2026-05-24 Skill CWD Source Boundary Test Report

## 目标

- 记录技能页本地技能 RPC 的 cwd 单一来源策略。
- 记录 provider mirror 非 canonical 的边界，避免后续把 `.claude/skills`、`.agents/skills` 当作冲突真值来源。
- 保存本次 `cwd is required` 与误报冲突修复的验证证据。

## 结论

- 技能页本地技能 RPC 的 cwd 单一来源为 `SkillsPage.cwd` / `threadScopeCwd` 优先，`projectStore.state.active` 只作为回退。
- 空 cwd 或 `.` 不进入后端 `skills/local/*` RPC；前端直接显示“项目路径未就绪，无法读取或保存技能。”。
- Super-Dolphin canonical 技能源以项目 `.agent/skills` 和 active personal roots 为准。
- `.claude/skills` 与 `.agents/skills` 是 provider-native mirror，是生成物，不是 canonical truth，不应作为 same-name conflict 的扫描源。

## 覆盖范围

- `skills/local/read`
- `skills/local/listFiles`
- `skills/local/write`
- `skills/local/delete`
- `skills/local/importDir`
- `skills/summary/suggest`
- `skills/resolution_list`

## 验证命令

```powershell
cd D:\project\Super-Dolphin\cmd\agent-terminal\frontend
npx vitest run vue-app/use-skill-editor-personal.test.js vue-app/skills-page-cwd.test.js vue-app/skills-page.test.js vue-app/skills-resolution-ux.test.js vue-app/app-root.behavior.test.js vue-app/use-skill-resolutions.test.js
npx vitest run
npm run build
```

```powershell
cd D:\project\Super-Dolphin
$env:REAL_GO_BIN='/d/Configuration/go/bin/go.exe'
& 'D:\Configuration\msys64\usr\bin\bash.exe' ./scripts/test_with_guard.sh ./internal/module/skill -run TestSkillRPCRejectsEmptyCWD -count=1
```

## 验证结果

- 前端目标回归：6 files / 134 tests passed。
- 前端全量 Vitest：132 files / 1458 tests passed。
- 前端构建：passed；仅保留既有 Vite dynamic import 与 chunk size warning。
- 后端 fail-fast 回归：passed；`ErrMissingCWD` 行为保持不变。
- Playwright CLI 黑盒：打开 `http://127.0.0.1:4511/`，进入技能页，点击 `Agent工程学` 的“编辑详情”，详情弹窗正常加载。
- 后端日志 `agent-terminal-2026-05-24-7.log` 中，针对 `cwd is required`、`params_preview:"{}"`、`skills/local/read/write/listFiles` 失败记录的扫描结果为 0 条。
- 直接调用 `skills/resolution_list` 并携带 `cwd:"D:\\project\\Super-Dolphin"` 返回 `{"items":[]}`，未复现 49 个 provider mirror 冲突。

## 风险点

- 黑盒验证没有点击真实“保存技能”，避免修改项目 `.agent/skills` 内容；保存路径由前端单元测试覆盖。
- Provider mirror 目录仍会作为生成物存在；后续修复不能把 mirror 重新纳入 canonical 冲突扫描。
- 后端缺 cwd fail-fast 是设计约束，不能用进程 cwd 默认值掩盖前端传参错误。

## 后续约束

- 新增技能页本地 RPC 时必须复用同一 cwd helper。
- 新增 provider mirror 逻辑时必须保持 mirror 非 canonical，canonical 冲突源只来自项目技能和 active personal roots。
- `personal/hub` 继续保持 catalog-only，不扫描、不 mirror、不参与 effective skill set。
