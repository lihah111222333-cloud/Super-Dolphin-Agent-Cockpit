# 风险登记表

| 编号 | 优先级 | 风险类型 | 问题描述 | 影响范围 | 证据文件 | 建议处理方式 |
|---|---|---|---|---|---|---|
| R-001 | P1 | 前端 | `styles.css`、`ChatPage.jsx`、`useClientStore.js` 等超大文件维护困难 | 新 UI、测试、回归 | `frontend-app/src/*` | 建立拆分计划，先拆纯 UI/样式和无状态 helper |
| R-002 | P1 | 运维 | 生产部署、备份、告警、回滚证据不足 | 发布和事故恢复 | `README.md`、`.github/workflows/ci.yml` | 补生产 runbook 和回滚演练文档 |
| R-003 | P1 | 安全 | 本地桌面信任边界若暴露到网络，多用户认证授权不足 | RPC、MCP、Wails、toolbridge | `internal/platform/rpc/*`、`internal/ui/wails/*` | 明确部署边界；网络化前设计 authz/authn/rate limit |
| R-004 | P1 | 数据 | migration 数量多且有最低版本 gate，环境落后会启动失败 | DB 启动、升级 | `internal/platform/db/module.go`、`migrations/*` | 建立 migration 发布和失败恢复手册 |
| R-005 | P1 | 安全 | 本地 ELK compose 关闭 Elasticsearch 安全 | 日志/观测部署 | `docker-compose.elk.yml` | 标注仅本地开发；生产启用 TLS/auth |
| R-006 | P1 | 后端 | mcp-orch 工具具备启动 agent、DAG、文件等写能力 | MCP 工具面 | `cmd/mcp-orch/tools/registry.go` | 保持 trusted scope、cwd 校验和工具权限测试 |
| R-007 | P2 | 架构 | current React UI 与 legacy Vue 并存，容易误判修改路径 | 前端维护 | `docs/doc/codemap/README.md` | 继续在文档和 PR 模板中强调 current UI 路径 |
| R-008 | P2 | 性能 | 未建立前端/DB/provider 性能基线 | 性能优化 | `frontend-app`、`sql/queries`、`internal/provider` | 增加定期性能采样和慢查询记录 |
| R-009 | P2 | 文档 | 生产外部依赖和 SLO 缺失 | 运维、产品决策 | `docs/codex-analysis/*` | 补充部署平台、SLO、用户规模信息 |
