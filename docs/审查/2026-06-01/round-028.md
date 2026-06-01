# Round 028 - 模式归纳：unchecked type assertion 反模式

## 系统性问题

`v, _ := x.(T)` 或 `x.(T)` 无 comma-ok 的模式散布在 RPC dispatch、event handling、config reading 路径中。

## 受影响位置

| 文件 | 行 | 上下文 | 严重度 |
|------|-----|--------|--------|
| skill/scope_model.go | 224 | sort closure | blocker |
| codexapp/session_rollout_events.go | 239 | RPC content | moderate |
| codexapp/driver_pool_routing.go | 553 | sandbox config | moderate |
| codexapp/event_map.go | 385 | nested payload | moderate |
| claudecli/session_events.go | 405 | dataBool | moderate |
| uistate/config_rpc.go | 190, 205 | preference/config | major |
| uistate/patch.go | 335 | patch field | moderate |
| mcpcontrol/handlers.go | 221 | interface adapt | moderate |
| mcpserver/tool_result.go | 35 | atomic.Value | moderate |
| skill/mirror_publisher.go | 146 | sync.Map value | moderate |
| skill/skills_match.go | 92 | config map | moderate |
| prompt/match_when_support.go | 18 | want value | moderate |

## 统一精修方案

1. **全部改为 comma-ok**：`v, ok := x.(T); if !ok { return/log }`。
2. **golangci-lint 规则**：启用 `forcetypeassert` linter。
3. **archtest 守卫**：regex 扫描 `\.\([A-Z]` 后面没有 `, ok` 的模式。

## 预期影响

- ~12 个文件需要修改。
- 每处改动 2-3 行（加 ok check + error/log 分支）。
- 无签名变更。
