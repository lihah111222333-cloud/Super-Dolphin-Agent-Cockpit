# 安全性分析

## 1. 本阶段目标

基于源码和配置审查认证授权、输入校验、注入、XSS/CSRF/SSRF、敏感信息、日志脱敏、CORS/限流和管理面风险。

## 2. 已读取文件

- `.env.example`
- `docker-compose.elk.yml`
- `frontend-app/src/shared/api/backendApi.js`
- `frontend-app/src/shared/api/wailsBridge.js`
- `frontend-app/src/App.test.jsx`
- `internal/platform/rpc/handler.go`
- `internal/platform/rpc/module.go`
- `internal/platform/observability/sanitizer.go`
- `internal/ui/wails/binding.go`
- `internal/store/dbquery/*`
- `cmd/mcp-orch/http_runner_test.go`
- `cmd/super-dolphin-updater/install.go`

## 3. 关键发现

- 前端 RPC facade 对关键参数做 fail-fast，包括 cwd、threadId、scope、content、limit 等。
- RPC handler 层提供 strict decoding、thread scope 和 provider capability gate。
- Wails trace 只记录参数大小/键名，不记录 raw params。
- 前端 bridge log 与 frontend trace 均过滤 prompt/content/token/password/secret/api_key/authorization 等字段。
- observability sanitizer 会按 key 和内容模式脱敏 token/password/secret/api key 等。
- `dbquery` 是受限只读 SQL 查询引擎，并自动补 limit。
- 本地 ELK compose 关闭 security，仅适合开发环境。

## 4. 证据说明

| 结论 | 文件 |
|---|---|
| 前端关键 RPC 参数 fail-fast | `frontend-app/src/shared/api/backendApi.js` |
| bridge log/trace 禁止敏感 key | `frontend-app/src/shared/api/wailsBridge.js` |
| Wails CallAPI trace 不记录 raw params | `internal/ui/wails/binding.go` |
| 后端 trace sanitizer 处理 token/password/secret/api key | `internal/platform/observability/sanitizer.go` |
| RPC thread scope/capability gate | `internal/platform/rpc/handler.go` |
| 本地 ELK security disabled | `docker-compose.elk.yml` |
| `.env.example` 包含示例 DB 变量名，不能作为生产 secret 模板直接提交真实值 | `.env.example` |

## 5. 风险与问题

- P1：仓库证据显示系统主要按本地桌面信任边界设计；若暴露到多用户网络环境，需要重新设计认证、授权、CORS、CSRF、rate limit。
- P1：mcp-orch 工具具备启动 agent、DAG 和文件能力，必须依赖 trusted scope、cwd 校验和工具权限持续生效。
- P1：本地 ELK compose 禁用安全，禁止按生产配置使用。
- P2：`.env.example` 包含示例凭据形态，应明确“仅示例”，避免复制为真实生产配置。

## 6. 无法判断的信息

- 无法判断生产认证、TLS、网络隔离、secret 管理、密钥轮换策略。
- 无法判断所有文件打开/共享文件路径在完整平台上是否有沙箱约束；本次只确认部分测试和 guard。

## 7. 下一阶段建议

继续性能与扩展性分析，重点看前端包/大文件、DB 查询、缓存、并发、队列和 provider 外部调用。
