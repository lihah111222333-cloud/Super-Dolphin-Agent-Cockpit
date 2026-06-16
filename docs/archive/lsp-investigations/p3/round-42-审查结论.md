# 第 42 轮审查结论

## 审查范围

- `internal/platform/shared/log_error.go`（LogIgnoredError 委托）
- `internal/platform/shared/validation.go`（FirstNonEmpty、FirstTrimmed、ClampLimit、FirstPayloadString、payloadString）
- `internal/platform/shared/idgen.go`（NewID 委托）
- `internal/platform/shared/idgen_agent.go`（NewAgentID、NewChildAgentID 委托）
- `internal/platform/shared/pathscope.go`（NormalizeRelativePath、ContainsPath、AppManagedDataRoots、isAllowedConfiguredSuperDolphinHome、hasPathSuffix、appendAppManagedDataRoot、cleanAppManagedDataRoot）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `log_error.go:1-12` | 弱契约 | "LogIgnoredError" 命名暗示「显式忽略错误」是 anti-pattern 入口 | 整个包提供「忽略错误」helper 是反 fail-fast 设计；调用方滥用让错误分析失效 | 重命名为 `LogTransientErrorBoundary`（明确「错误已在边界处理」）+ 文档限制使用场景 |
| `validation.go:18-27` FirstPayloadString | 静默 | 多 key 顺序查找，全部 trim 后空字符串则继续；最终静默返 "" | caller 无法区分「key 不存在」「key 存在但值为空」「值类型错误」 | 返回 `(string, bool)` 让 caller 决定零值语义 |
| `validation.go:29-38` payloadString | 静默 | type-switch `default: return ""` —— 数字、bool、数组等都静默返空 | LLM 返回 `{"text": 123}` 时静默丢弃；caller 不知 |
| `validation.go:33-34` payloadString 递归 | 弱契约 | map[string]any 时递归找 5 个 fallback 字段（text/summary/message/output/result）| 这 5 个字段优先级靠代码顺序；调用方需读源码才知 | 改为 caller 显式传 keys；或定义 schema 常量 |
| `pathscope.go:14-16` NormalizeRelativePath | 弱契约 | 仅 `filepath.Clean(strings.TrimSpace(path))` —— 无相对/绝对校验 | 名为 NormalizeRelativePath，但传绝对路径也会被「成功 clean」返回 | 加 `filepath.IsAbs(path)` 校验绝对路径输入应 error |
| `pathscope.go:104-106` appendAppManagedDataRoot | 静默 | 当 root==userHome 或 root 包含 userHome 时报错；但 line 107-109 「seen 已存在」时静默跳过 | 重复 root 是配置 bug 还是合理（如多个数据源 alias 同路径）？无文档 | 加注释说明去重语义；运维角度可加 Debug 日志 |
| `pathscope.go:97-99` appendAppManagedDataRoot | 静默 | `cleanAppManagedDataRoot` 返回空字符串时静默不 append | line 116-118 cleanAppManagedDataRoot 返 "" 仅当输入 trim 后为空；这是合理路径，但加 `seen` 不 dedup 让两次空根都跳过——OK | 加注释说明语义 |
| `pathscope.go:70-83` isAllowedConfiguredSuperDolphinHome | 弱契约 | 3 重判断：①不在 userHome 内（line 75-77）→ true；②basename==".super-dolphin"→ true；③包含 macOS/Windows app data 后缀→ true | 3 个条件靠 OR 逻辑，每个意图不直观 | 改为 enum + 显式 case；并在每个分支加注释解释意图 |
| `pathscope.go:34-46` AppManagedDataRoots | 静默 | env 配置的 SUPER_DOLPHIN_HOME 校验失败时返 error；但只校验 SUPER_DOLPHIN_HOME，其他 4 个硬编码路径（log/memory/skills/sharedfile）不校验 | 用户 home 下硬编码 `.multi-agent/{log,memory,skills}` 不是 SUPER_DOLPHIN_HOME 控制 → 用户改环境也无法重定向 | 4 个硬编码路径也应受 env 配置 |
| `pathscope.go:104` ContainsPath | 静默 | 一行 ContainsPath(root, target) 检查；本文件实现委托 pathutil | 第31轮已发现 ContainsPath 在 NFS / 网络挂载文件系统上可能阻塞秒级 | 同前轮：加 timeout 或 ContainsPath 慢调用监控 |
| `idgen.go:1-7, idgen_agent.go:1-10` | 弱契约 | 两文件总共 17 行 + 4 个 1-line 委托 —— 是否值得单独成文件？| 委托层无业务逻辑，但增加导航成本 | 合并到 shared/ids.go 或直接 import idgen |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `pathscope.go:22-67` AppManagedDataRoots | 调用 `os.UserHomeDir()` 同步 IO；多次 NormalizeAbsolutePath（pathutil 实现可能 stat 文件系统） | 加 duration 监控（启动期一次性，影响小但可见） |
| `pathscope.go:114-132` cleanAppManagedDataRoot | os.ExpandEnv（环境变量展开）+ NormalizeAbsolutePath 双重 IO | 同上 |
| `validation.go:18-27` FirstPayloadString | 递归查找在嵌套深度 > 5 时栈深；malicious payload 可能造成 stack overflow | 加深度限制（如 max 10 层） |
| `log_error.go:1-12` LogIgnoredError | 单行 logger.Error 调用；logger 内部可能阻塞（IO） | 已是 logger 责任，shared 层无需监控 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `log_error.go:8-11` 整个 helper | 「显式忽略错误」是 anti-pattern 入口 |
| `validation.go:18-27` FirstPayloadString | 全部为空时静默返 "" |
| `validation.go:35-37` payloadString | 非 string 非 map 类型静默返 "" |
| `pathscope.go:97-99` appendAppManagedDataRoot | cleanAppManagedDataRoot 返 "" 时静默不 append |
| `pathscope.go:107-109` | 重复 root 静默跳过 |
| `pathscope.go:14-16` NormalizeRelativePath | 绝对路径也被「成功 clean」 |
| `idgen.go, idgen_agent.go` | 委托层无错误返回（依赖底层 idgen 包内部不返错） |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `log_error.go` 整文件 | LogIgnoredError API 鼓励「错误已被处理」反模式 |
| `validation.go:14-16` ClampLimit | 4 个参数（val/min/max/defaultVal）含义靠位置约定 |
| `validation.go:33-34` payloadString | 5 个 fallback key 硬编码无文档 |
| `pathscope.go:14-16` NormalizeRelativePath | 名字与行为不符（接受绝对路径） |
| `pathscope.go:70-83` isAllowedConfiguredSuperDolphinHome | 3 重判断 OR 语义 |
| `pathscope.go:55-60` 硬编码 4 个 .multi-agent 子目录 | log/memory/skills/sharedfile 与 SUPER_DOLPHIN_HOME 不联动 |

## 修复优先级

### P0（必须本周修）
1. **`pathscope.go:14-16` NormalizeRelativePath 不校验输入**——名字承诺「relative path」但接受绝对路径。调用方期望「相对路径处理」逻辑（如 join base），但传绝对路径不报错。下游可能基于「假设 relative」逻辑出错（如 `filepath.Join(base, abs)` 在 POSIX 上会返绝对路径忽略 base）。改为 abs 输入返 error。
2. **`log_error.go` 整个文件存在的合理性**——`LogIgnoredError` 命名鼓励「记 log 后忽略错误」反模式。在「100% Fail-Fast」目标下应避免提供这种 helper。建议：①重命名表达边界语义；②加严格使用场景文档；③或彻底删除让 caller 显式 fmt.Errorf。

### P1（本月）
3. `validation.go:18-27` FirstPayloadString 改 (string, bool) 返回
4. `validation.go:29-38` payloadString 加非预期类型 Warn 日志
5. `pathscope.go:55-60` 4 个 .multi-agent 子目录与 env 联动
6. `pathscope.go:70-83` isAllowedConfiguredSuperDolphinHome 拆分清晰判断
7. `validation.go:33-34` 5 个 fallback key 改为 schema 常量

### P2（下个 sprint）
8. `idgen.go, idgen_agent.go` 合并或删除（直接 import idgen）
9. `pathscope.go:104` ContainsPath 加 NFS 阻塞监控
10. `validation.go` payloadString 加深度限制

## 边界条件

1. **`log_error.go` 是项目内 fail-soft 哲学的具象化**：单个 helper 仅 4 行委托。但**它的存在本身即是问题**——一个项目要追求 100% fail-fast，就不应该提供「LogIgnoredError」式的便捷工具。这是治理层问题：API 设计鼓励的行为决定开发者实际行为。建议在团队内公开讨论是否保留此 API。
2. **`pathscope.go` 的安全设计是正面案例**：line 104-106 拒绝「app-managed data root 包含整个 user home」是良好的安全防御——防止 SUPER_DOLPHIN_HOME 配错为 `/home/user`，导致 super-dolphin 误删用户文档。`isAllowedConfiguredSuperDolphinHome` 白名单 macOS/Windows 标准 app data 路径也是 OS-aware 的细致处理。但 line 70-83 的 3 重 OR 逻辑可读性差。
3. **`validation.go:18-27` FirstPayloadString 与第 36 轮 ext.go MarshalJSON 的对称性**：两处都是「LLM 输入 → 自定义提取规则」。FirstPayloadString 处理 LLM 返回的多种字段命名（text/summary/message/output/result），是对 LLM 输出格式不稳定的容错。这是合理设计——但应文档化优先级，让 prompt 工程师知道「让 LLM 优先输出 text 字段」。
4. **`pathscope.go:55-60` 硬编码 4 个 `.multi-agent` 子目录**：当用户设 SUPER_DOLPHIN_HOME=/custom/path 时，期望所有数据都在 /custom/path 下。但 log/memory/skills/sharedfile 仍在 `~/.multi-agent/` 下。这是配置语义不一致——用户配 env 期望全局重定向但部分目录无法重定向。**P1 因为影响用户配置的合理预期**。
5. **`idgen.go` 和 `idgen_agent.go` 各自 7-10 行**：两个文件加起来 4 个 1-line 委托。Go 风格建议——同包多文件应按业务逻辑分隔，而非按函数数量分隔。当前 split 让 reviewer 难以快速找到 ID 生成逻辑。建议合并为 `shared/ids.go`。
6. **`validation.go:14-16` ClampLimit 的位置参数 anti-pattern**：`ClampLimit(val, min, max, defaultVal int)` —— 4 个 int 参数容易传错顺序（如 min/max 互换调用方不会立即报错）。Go 风格通常用 struct option pattern 或具名 helper（如 `Clamp(val).Min(0).Max(100).Default(50)`）。但这是 minor refactor，P2 优先级。

---

**本轮总结**：发现 2 个 P0 问题：①NormalizeRelativePath 不校验输入但承诺 relative；②LogIgnoredError 整个 helper 鼓励 fail-soft 反模式。`pathscope.go` 的安全防御（拒绝 root 包含 user home）是正面案例，但 SUPER_DOLPHIN_HOME 与硬编码子目录不联动是配置语义不一致。`validation.go:33-34` 的 5 个 LLM 字段 fallback 应文档化。`idgen.go`/`idgen_agent.go` 应合并。

**累计进度**：42 轮完成。cron `fd4b4728` 继续推进。
