# ADR-019: 保留 legacy frontend package 作为 embed fallback

Date: 2026-06-16

Status: Accepted

## Context

当前桌面开发主路径是 `frontend-app/`：

- `run-new-ui-desktop.sh` 和 `run-new-ui-desktop.ps1` 启动 `frontend-app`
  Vite，并通过 `VITE_DEV_URL` 让 `cmd/agent-terminal` 代理当前 React UI。
- 打包脚本会先构建 `frontend-app/dist`，再同步到
  `cmd/agent-terminal/frontend/dist`，供 Go embed 使用。
- `cmd/agent-terminal/frontend/**` 仍包含 legacy Vue/Vite source、tests、
  size guard 和 package-embed 相关脚本。

因此，`cmd/agent-terminal/frontend/**` 不再是当前 UI 的默认开发入口，但
它仍承担无 dev proxy 时的 embedded asset fallback 和 package-embed 验证职责。

## Decision

保留完整 `cmd/agent-terminal/frontend/**` legacy package。

本轮文件治理不删除 Vue source、legacy tests、size guard 或 package 配置；只有在
单独退役任务中证明 package-embed fallback 已有替代路径后，才允许删除或缩减。

## Consequences

- 当前 UI 事实源仍是 `frontend-app/`。
- `cmd/agent-terminal/frontend/**` 只在明确涉及 legacy/package-embed fallback
  时修改。
- 项目导航和 agent 默认阅读路径应把它标为 legacy embed fallback，而不是当前
  React UI。
- 文档瘦身任务不能把 legacy package 删除动作和历史文档归档混在同一批。

## Retirement Gates

后续若要退役 legacy package，必须先满足：

1. 新的 embed fallback 路径明确，且 Go embed 不再依赖
   `cmd/agent-terminal/frontend/dist`。
2. Linux/macOS/Windows package scripts 和对应 guard tests 全部更新并通过。
3. Desktop smoke 证明无 dev proxy 时仍能加载当前 UI。
4. `cmd/agent-terminal/frontend/**` 的 source、tests、size guard、package
   配置删除范围可用 `git diff --name-status --find-renames` 清楚解释。

## Verification

本 ADR 只记录决策，不改变运行行为。验证范围是引用扫描、项目地图检查和文档
入口检查；真正退役时再运行 package guard、frontend build 和 desktop smoke。
