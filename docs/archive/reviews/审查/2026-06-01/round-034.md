# Round 034 - 风险评估与签名变更影响分析

## 签名变更清单

以下精修涉及函数签名变更，会级联影响 caller：

| 函数 | 当前签名 | 目标签名 | 影响 caller 数 |
|------|----------|----------|---------------|
| eventsurface.Bind | `[]CancelFunc` | `([]CancelFunc, error)` | ~3 |
| bus.NewLogSink | `*LogSink` | `(*LogSink, error)` | ~2 |
| statemachine.AllowedTriggers | `[]string` | `([]string, error)` | ~5 |
| codexapp.mustJSON | `json.RawMessage` | `(json.RawMessage, error)` | ~8 |
| skill.hashResolutionEnvelope | `string` | `(string, error)` | ~2 |
| skill.defaultPersonalMirrorTargets | `[]Target` | `([]Target, error)` | ~2 |
| dashboard.dashboardOptionalTime | `*time.Time` | `(*time.Time, error)` | ~3 |
| prompt.cache key builder | `string` | `(string, error)` | ~4 |

**总计**：~8 个函数签名变更，~29 个 caller 需适配。

## 风险矩阵

| 风险 | 概率 | 影响 | 缓解 |
|------|------|------|------|
| 签名变更遗漏 caller | 低 | 编译失败（安全） | go build 会拦截 |
| 新 error 路径未被测试覆盖 | 中 | 运行期未处理 error | 互审 + 新增 test case |
| fx 装配改 required 后启动失败 | 中 | 服务无法启动 | 集成测试覆盖 |
| archtest 新规则误报 | 低 | CI 阻塞 | baseline 机制兜底 |
| 删除 nil-receiver guard 后 panic | 低 | 运行期 crash | fx 层保证非空 |

## 结论

签名变更影响可控（~29 caller），且 Go 编译器会强制所有 caller 适配。主要风险在"fx 装配改 required 后启动失败"——需要确保所有 fx module 的 Provide 函数在测试环境中有对应的 mock/stub。
