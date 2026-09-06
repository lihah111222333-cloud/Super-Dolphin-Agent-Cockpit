# 独立 provider 认证契约

独立 `mcp-orch` 默认不读取宿主个人 CLI 配置。Claude provider 的 `auth_mode` 明确控制认证来源：

| `auth_mode` | 行为 | 安全边界 |
| --- | --- | --- |
| 缺省或 `provider_home` | 设置 `CLAUDE_CONFIG_DIR` 为 provider `home` | 配置、MCP、插件和认证上下文与宿主隔离；该目录必须由运维显式登录/配置 |
| `host_default` | 不设置 `CLAUDE_CONFIG_DIR`，保留宿主默认 Claude keychain/config 解析 | 允许读取宿主默认认证上下文；只能由显式服务配置选择，不得静默降级 |

`host_default` 不是认证兜底：配置解析仍然 fail-fast，provider 启动仍必须通过原生协议探针。生产配置选择该模式时，运维必须确认服务进程的宿主账号、keychain 可见性和默认 Claude 配置属于预期身份；服务不会复制、导出或记录凭据。

Codex 不接受 `auth_mode`，始终使用显式 `CODEX_HOME`。未知值、把认证模式用于 Codex、或缺失 provider 路径的配置均立即拒绝。
