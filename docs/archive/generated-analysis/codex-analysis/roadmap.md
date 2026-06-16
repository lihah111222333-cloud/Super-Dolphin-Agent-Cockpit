# 优化路线图

## P0：立即处理

- 当前扫描未发现需要立即停止使用的 P0 级源码问题。
- 若计划把本地服务暴露到网络或多用户环境，必须先补认证、授权、TLS、rate limit 和审计策略。

## P1：短期优化

- 拆分新 UI 大文件，先从 `ChatPage.jsx`、`useClientStore.js`、`styles.css` 的低耦合片段开始。
- 补生产部署 runbook：环境变量、secret 管理、备份恢复、告警、回滚。
- 为 mcp-orch 写能力建立权限和 cwd 回归清单。
- 为 DB migration gate 建立升级失败处理文档。
- 建立性能基线：frontend build/bundle、首屏渲染、DB 慢查询、provider 超时。

## P2：长期演进

- 明确 legacy Vue 退役或保留策略。
- 为 provider 接入扩展补标准模板和验收用例。
- 把代码地图和架构报告纳入定期刷新流程。
- 完善观测指标到告警规则的闭环。

## 30 天计划

1. 输出前端大文件拆分 RFC 和测试矩阵。
2. 输出生产部署/回滚 runbook 初版。
3. 补 mcp-orch 工具权限/cwd 测试清单。
4. 补 migration 运维说明。
5. 建立一次性能采样报告模板。

## 60 天计划

1. 完成第一批 UI 文件拆分。
2. 跑一次 DB 慢查询和索引检查。
3. 将关键 observability 指标接入告警规则。
4. 完成 provider 错误和重试策略复核。

## 90 天计划

1. 评估 legacy Vue 退役路径。
2. 建立稳定的架构债务月度报告。
3. 将性能、安全、运维风险接入发布前检查。
