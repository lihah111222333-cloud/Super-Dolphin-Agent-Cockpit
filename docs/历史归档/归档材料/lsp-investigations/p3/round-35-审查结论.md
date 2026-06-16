# 第 35 轮审查结论

## 审查范围

- `cmd/mcp-orch/memory/index.go`（loadIndex、parseIndex、buildIndexView、parseIndexLine、readTypeHint）
- `cmd/mcp-orch/memory/config.go`（NewConfig、defaultRootDir、projectRoot、machineID、parseBoolEnv）
- `cmd/mcp-orch/memory/store.go`（类型定义）
- `internal/sidecar/orch/store/taskdag/factory.go`（queryOne、queryMany、queryValue、mapRows、parseLeaseDuration、bindWakeupTurnTx、fencedWakeupMutation、wakeupFenceFromMark/Retry/Fail）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `config.go:39-47` defaultRootDir | 兜底链 | UserHomeDir 失败 → fallback platformCfg.ProjectRoot → fallback 空字符串 | 返回空字符串时 memory 系统静默禁用（RootDir=""），但 `Enabled` 可能仍为 true → 后续操作在空路径上执行 | 空字符串时 Enabled 强制 false + Warn 日志 |
| `config.go:49-57` projectRoot | 兜底链 | platformCfg.ProjectRoot → os.Getwd → 空字符串 | Getwd 失败（极端情况：cwd 被删除）时返回空字符串，后续 filepath.Join 产生相对路径 | 同上：空字符串时 fail-fast |
| `config.go:59-64` machineID | 兜底 | Hostname 失败时返回 `"local-machine"` 硬编码 | 多机部署时如果 Hostname 都失败，所有机器 machineID 相同 → memory 冲突 | 至少 Warn 日志 + 加 random suffix |
| `config.go:66-75` parseBoolEnv | 静默 | 非法值（如 "maybe"）走 `default: return fallback` | 环境变量拼写错误（如 "ture"）静默使用 fallback，配置错误无感知 | 非法值 Warn 日志 + 返回 fallback |
| `index.go:12-24` loadIndex | 兜底 | MEMORY.md 读取失败 → 静默 fallback 到 scanEntries 重建 | 文件权限错误 / 磁盘故障被静默降级为 rebuild；rebuild 可能不完整 | 区分 ErrNotExist（合理 rebuild）和其他错误（应 fail-fast） |
| `index.go:87-93` readTypeHint | 弱契约 | 从路径第一段推断 memory type；路径不含 `/` 时 parts[0] 是文件名 | 文件直接放在 root 下（无子目录）时 type 推断错误 | 加 fallback 到 Unknown + Warn |
| `factory.go:85-87` bindWakeupTurnTx | 弱契约 | `requireBound && count == 0` 时返回 generic error | 调用方无法区分「wakeup 不存在」vs「已被其他 turn 绑定」 | 改为 typed error（ErrWakeupNotFound / ErrWakeupAlreadyBound） |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `index.go:12-24` loadIndex | scanEntries 扫描磁盘目录（可能有数百个 memory 文件）| 加 duration 日志；>500ms 打 Warn |
| `index.go:43-61` buildIndexView | 对每个 entry 调 filepath.Rel + filepath.ToSlash | entries 少时 OK；>100 时加批量处理 |
| `config.go:40` os.UserHomeDir | 在某些容器环境下可能阻塞（NIS/LDAP lookup）| 加 timeout（但 Go 标准库不支持 ctx）；至少记录耗时 |
| `factory.go:72-89` bindWakeupTurnTx | DB 事务内操作；如果事务持有时间长，锁竞争 | 加事务 duration 监控 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `config.go:39-47` defaultRootDir | UserHomeDir 失败静默 fallback |
| `config.go:49-57` projectRoot | Getwd 失败静默返空 |
| `config.go:59-64` machineID | Hostname 失败静默返 "local-machine" |
| `config.go:66-75` parseBoolEnv | 非法值静默 fallback |
| `index.go:12-24` loadIndex | MEMORY.md 读取失败（非 NotExist）静默 rebuild |
| `index.go:87-93` readTypeHint | 路径无子目录时 type 推断可能错误 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `config.go:25-37` NewConfig | Enabled=true 但 RootDir="" 是合法状态（但后续操作会失败） |
| `config.go:66-75` parseBoolEnv | 接受 8 种合法值 + 任意非法值（无文档） |
| `index.go:12-24` loadIndex | 返回 `(entries, rebuilt bool, source string, error)` 四元组，rebuilt 语义靠调用方理解 |
| `factory.go:85-87` bindWakeupTurnTx | count==0 的原因不可区分 |
| `factory.go:91-99` fencedWakeupMutation | fence 参数的有效性靠调用方保证 |

## 修复优先级

### P0（必须本周修）
1. **`config.go:39-47` defaultRootDir 空字符串 + Enabled=true 矛盾**——这会导致后续 memory 操作在空路径上执行（如 `filepath.Join("", "MEMORY.md")` → 当前目录下的 MEMORY.md）。改为：NewConfig 末尾加 invariant `if cfg.Enabled && cfg.RootDir == "" { cfg.Enabled = false; warn }` 。
2. **`index.go:12-24` loadIndex 不区分 NotExist 和其他错误**——磁盘故障（ErrPermission / ErrIO）被静默降级为 rebuild。rebuild 可能读到不完整数据。改为：`if errors.Is(err, os.ErrNotExist) { rebuild } else { return nil, false, "", err }`。

### P1（本月）
3. `config.go:66-75` parseBoolEnv 非法值加 Warn
4. `config.go:59-64` machineID 失败加 Warn + random suffix
5. `factory.go:85-87` bindWakeupTurnTx 改 typed error
6. `index.go:87-93` readTypeHint 无子目录时 fallback + Warn

### P2（下个 sprint）
7. `config.go:49-57` projectRoot 空字符串时 fail-fast
8. `factory.go` 整体加事务 duration 监控
9. `index.go:12-24` loadIndex 加 duration 日志

## 边界条件

1. **`config.go` 的 Enabled + RootDir="" 矛盾是真实 bug**：`parseBoolEnv(envEnableMemory, false)` 可能返回 true（用户设了 `ENABLE_MEMORY_SYSTEM=true`），但 `defaultRootDir` 在容器环境（无 HOME、无 platformCfg）返回空字符串。此时 `cfg.Enabled=true, cfg.RootDir=""`。后续 `loadIndex("")` 会在 cwd 下找 MEMORY.md——这不是预期行为。**P0 因为它影响 memory 系统正确性**。
2. **`index.go:12-24` loadIndex 的 rebuild 路径设计意图**：MEMORY.md 不存在时 rebuild 是合理的（首次使用 memory 系统）。但 `os.ReadFile` 失败不仅限于 NotExist——权限错误、磁盘满、路径过长都会触发。当前代码把所有失败都走 rebuild，这是 fail-soft 过度。
3. **`factory.go` 的泛型 helper 设计是正面案例**：`queryOne`/`queryMany`/`queryValue` 三个泛型函数统一了 store 层的错误包装模式。每个 store 方法只需传 `call` + `operation` + `entity`，错误包装自动完成。这是 DRY 的良好实践，减少了遗漏错误包装的风险。
4. **`factory.go:72-89` bindWakeupTurnTx 的 fence 设计**：`requireBound` 参数让同一函数服务两种语义（「必须绑定成功」vs「尽力绑定」）。这是 bool 参数 anti-pattern——建议拆为两个函数 `BindWakeupTurn`（必须成功）和 `TryBindWakeupTurn`（尽力）。
5. **`config.go:59-64` machineID 的 "local-machine" fallback**：在单机开发环境合理。但如果 memory 系统被用于多机协作（如共享 NFS memory 目录），所有 Hostname 失败的机器都用 "local-machine" 会导致 memory 冲突。当前项目看起来是单机使用，P2 优先级合理。
6. **`parseBoolEnv` 的 8 种合法值**：`1/true/yes/on` + `0/false/no/off`。这是 Go 社区常见模式（参考 `strconv.ParseBool` 只接受 `1/t/TRUE/0/f/FALSE`）。但 `parseBoolEnv` 比 `strconv.ParseBool` 更宽松（接受 yes/no/on/off），且不报错。建议至少在非法值时打 Warn 让运维知道配置被忽略。

---

**本轮总结**：发现 2 个 P0 问题：①memory config 的 Enabled=true + RootDir="" 矛盾导致 memory 操作在 cwd 下执行；②loadIndex 不区分 NotExist 和磁盘故障，后者被静默降级为 rebuild。`factory.go` 的泛型 helper 是 DRY 正面案例。memory 子系统整体 fail-soft 过度——多处 fallback 链让配置错误难以暴露。

**累计进度**：35 轮完成。cron `fd4b4728` 继续推进。
