# Round 026 - 模式归纳：`_, _ := json.Marshal(v)` 反模式

## 系统性问题

全代码库存在 ~8 处 `data, _ := json.Marshal(v)` 或 `_ = json.Unmarshal(...)` 模式。这些不是"不可能失败"的场景——它们处理的是 `any` 类型、用户输入、或含 interface 字段的 struct。

## 受影响位置

| 文件 | 行 | 用途 | 严重度 |
|------|-----|------|--------|
| skill/service.go | 92 | hash 计算 | blocker |
| codexapp/support.go | 35 | RPC params | major |
| insight/flusher.go | 182 | DB 持久化 | major |
| memory/index.go | 344 | index 数据 | major |
| eventsurface/legacy.go | 122 | event payload | moderate |
| prompt/cache.go | 18 | cache key | major |
| dashboard/ui_page.go | 194 | tags 解析 | moderate |
| bootstrap/env.go | 256 | 启动配置 | moderate |

## 统一精修方案

1. **全局 lint 规则**：在 `golangci-lint` 配置中启用 `errcheck` 对 `json.Marshal`/`json.Unmarshal` 的检查。
2. **逐个修复**：每处改为 `([]byte, error)` 签名或 `if err != nil { panic/return }`。
3. **archtest 守卫**：加 `grep -rn 'json\.Marshal\|json\.Unmarshal' | grep '_ ='` 的 CI 检查。

## 预期影响

- 8 个文件需要修改。
- 部分函数签名变更（如 `hashResolutionEnvelope`、`mustJSON`）会级联影响 caller。
