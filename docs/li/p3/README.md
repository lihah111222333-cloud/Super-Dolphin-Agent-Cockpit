# P3: 兜底/静默/Fail-Fast 强契约审查总览

> 100 轮滚动审查（每 5 分钟一轮）。本系列只做审查与定位，不直接修改源码。

## 审查目标

代码库严格遵循 **100% Fail-Fast** 模式，禁止以下 4 类反模式：

### 1. 兜底 (Fallback)
- `if err != nil { return DefaultValue, nil }` —— 错误被吞，返回默认值掩盖问题
- `if err != nil { return &Empty{}, nil }` —— 返回空对象骗过下游
- `value, _ := mayFail()` —— 错误显式忽略
- 不区分 "未配置" 与 "解析失败" 的 fallback 链

### 2. 静默 (Silent Failures)
- `log.Warn(err); return nil` —— 仅记录不传播
- `defer func() { recover() }()` —— recover 后不 re-panic、不转 error
- 空 `catch` / 空 error 分支
- `_ = err` / `_, _ = ...`
- goroutine 内部 panic / err 未上报到主 goroutine

### 3. 弱契约 (Weak Contracts)
- 公开函数对必填参数零值放行（empty string、nil pointer、零值 struct）
- map / slice 入参未做 nil 检查就解引用
- interface 入参未做类型断言保护
- 通过环境变量/全局配置兜底替代显式入参
- 多个含糊的可选参数（应使用 functional options 或拆函数）

### 4. 违反 Fail-Fast (Soft Degradation)
- 配置加载失败用 default config
- 关键依赖（DB、LSP server、auth provider）连接失败仍启动
- 必需文件缺失时使用空内容
- 关键校验失败仅 log warning

## 输出文件

每轮一份：`round-NN-审查结论.md`（NN 两位数，从 01 开始）

每份结构（参考 `docs/li/p1` 风格）：

```
# 第 NN 轮审查结论
## 审查范围
## 高危发现（违反 Fail-Fast）  | 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
## 静默错误清单
## 弱契约清单
## 修复优先级（P0/P1/P2）
## 边界条件
```

## 审查方法

1. **范围去重**：每轮先读已有 round-XX 的"审查范围"，本轮选未覆盖的目录/包。
2. **优先级路径**：先扫 `cmd/`、`internal/platform/`、`internal/module/`，最后扫 `pkg/`、`scripts/`。
3. **取证要求**：每条发现必须给出 `文件:行号` + 当前代码片段 + 风险说明。仅靠模糊感觉不算发现。
4. **不修源码**：审查阶段产出问题清单，修复由后续 P3.x 子计划承接。

## 审查工具

- `grep -rn "return nil$"` / `return.*, nil$` 找返回零值
- `grep -rn "_ = "` 找显式忽略
- `grep -rn "recover()"` 找 panic recover
- `grep -rn "log\..*err.*\n.*return nil"` 找 log + return
- 人工 review 公开函数签名是否做参数校验

## 进度跟踪

- 总计：100 轮
- 已完成：见 `round-NN-*.md` 文件数
- 调度方式：会话内 cron `*/5 * * * *`（session-only，关掉 CLI 即停）
