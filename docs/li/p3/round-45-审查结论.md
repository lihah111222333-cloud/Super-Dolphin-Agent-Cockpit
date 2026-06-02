# 第 45 轮审查结论

## 审查范围

- `internal/platform/config/config.go`（New、PrimeProcessEnvironment、validateTrustedDevRuntimeMode、trustedDevEntrypoint、loadDotEnv、applyDotEnv、parseDotEnvLineStrict、hasPackagedRuntimeManifest、exportRPCAddrIfMissing、exportDatabaseURLIfMissing、resolveProjectRoot、resolvePackagedProjectRoot、envOr、envOrCompat、envBoolOr、envPositiveIntOr）

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `config.go:218-234` resolveProjectRoot | 兜底链 | PROJECT_ROOT env → packaged root（macOS bundle）→ os.Getwd → 空字符串 | 最终 fallback 空字符串（line 233 Getwd 失败时）。第35轮已发现 memory/config.go 的同类问题——空 ProjectRoot 让 migration 路径、memory 路径、sharedfile 路径全部基于 cwd 或空 | Getwd 失败时 return error（不是空字符串） |
| `config.go:270-280` envBoolOr | 静默 | `strconv.ParseBool` 失败时静默返 fallback | 环境变量拼写错误（如 `SKILL_PROGRESSIVE_DISCLOSURE=ture`）被静默忽略 | 解析失败 Warn 日志 + 返 fallback |
| `config.go:282-292` envPositiveIntOr | 静默 | `strconv.Atoi` 失败或 ≤0 时静默返 fallback | 环境变量配错（如 `NOTIFY_QUEUE_CAPACITY=abc`）被静默忽略 | 同上 Warn 日志 |
| `config.go:101-118` loadDotEnv | 静默 | 非 packaged 模式下 .env 读取失败静默 return nil（line 116-117） | 开发者期望 .env 生效但文件权限错误 → 静默不加载，配置缺失 | 非 NotExist 错误应 Warn |
| `config.go:121-138` applyDotEnv | 静默 | 非 strict 模式下 parse 错误 `continue`（line 128-129） | .env 中格式错误行被静默跳过；开发者不知道某行未生效 | 非 strict 模式也应 Warn |
| `config.go:130` applyDotEnv | 静默 | 已有同名 env 时静默跳过（`os.Getenv(key) != ""`） | 合理（env 优先于 .env）但无日志——开发者不知 .env 值被覆盖 | 加 Debug 日志「.env key=%s overridden by env」 |
| `config.go:218-234` resolveProjectRoot | 弱契约 | line 224 检查 `migrations/` 目录存在才认为是 packaged root | 如果 migrations/ 被误删，fallback 到 cwd——可能是完全不相关的目录 | 加 Warn 日志「packaged root rejected: migrations/ missing」 |
| `config.go:92-99` trustedDevEntrypoint | 弱契约 | 4 个硬编码 entrypoint 字符串白名单 | 新增 dev 入口需改代码；且 env 值可被伪造（非安全边界） | 加注释说明「not a security boundary, defense-in-depth only」 |
| `config.go:38` envOrCompat | 弱契约 | legacy env 存在时 Warn 但仍使用 | 合理的 deprecation 路径；但 Warn 日志可能被忽略 | 加 deadline（如 "deprecated since 2025-01, will be removed 2026-06"） |
| `config.go:24` setenvForConfig | 全局副作用 | `var setenvForConfig = os.Setenv` 全局变量 | 测试可替换（line 24 是 test hook），但生产代码修改进程环境是全局副作用 | 文档化「New() 会修改进程环境变量」 |

## 协程延迟定位方案

| 位置 | 延迟风险点 | 定位方案 |
| --- | --- | --- |
| `config.go:26-65` New | 同步路径：PrimeProcessEnvironment（磁盘 stat + .env 读取）→ embeddedpg.ResolveFromEnvironment → 构造 cfg | 加 duration 日志（启动期一次性，但 embedded postgres resolve 可能涉及 binary 查找） |
| `config.go:67-76` PrimeProcessEnvironment | resolveProjectRoot（os.Executable + os.Stat + os.Getwd）→ validateTrustedDevRuntimeMode（os.Stat）→ loadDotEnv（os.ReadFile） | 同上 |
| `config.go:174-184` hasPackagedRuntimeManifest | os.Stat 同步 IO | 在 NFS 上可能慢；但启动期一次性 |
| `config.go:218-234` resolveProjectRoot | os.Executable + os.Stat + os.Getwd 三次 syscall | 启动期一次性，可接受 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `config.go:218-234` resolveProjectRoot | Getwd 失败返空字符串 |
| `config.go:270-280` envBoolOr | ParseBool 失败静默 fallback |
| `config.go:282-292` envPositiveIntOr | Atoi 失败或 ≤0 静默 fallback |
| `config.go:116-117` loadDotEnv | 非 packaged 模式 .env 读取失败静默 nil |
| `config.go:128-129` applyDotEnv | 非 strict 模式 parse 错误静默 continue |
| `config.go:130` applyDotEnv | 已有 env 时 .env 值静默跳过 |
| `config.go:222-228` resolveProjectRoot | packaged root 无 migrations/ 时静默 fallback cwd |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `config.go:218-234` resolveProjectRoot | 3 层 fallback 链无文档 |
| `config.go:92-99` trustedDevEntrypoint | 4 个硬编码白名单 |
| `config.go:24` setenvForConfig | 全局副作用 |
| `config.go:38` envOrCompat | legacy env 无 removal deadline |
| `config.go:270-292` envBoolOr/envPositiveIntOr | 非法值静默 fallback |
| `config.go:36-57` New | 16 个配置字段从 env 读取，每个有独立 fallback 逻辑 |

## 修复优先级

### P0（必须本周修）
1. **`config.go:218-234` resolveProjectRoot Getwd 失败返空字符串**——这是全局配置的根路径。空 ProjectRoot 让 `autoMigrate`（第27轮 module.go:101-110）在 cwd 下找 migrations/（可能不存在）→ 启动失败但错误信息不直观。改为 Getwd 失败时 return error。
2. **`config.go:270-292` envBoolOr/envPositiveIntOr 非法值静默 fallback**——运维配错环境变量（如 `NOTIFY_QUEUE_CAPACITY=abc`）时系统静默用默认值运行。运维不知道配置未生效，可能在高负载时才发现 queue 太小。改为 Warn 日志。

### P1（本月）
3. `config.go:101-118` loadDotEnv 非 NotExist 错误加 Warn
4. `config.go:121-138` applyDotEnv 非 strict 模式 parse 错误加 Warn
5. `config.go:222-228` resolveProjectRoot packaged root 无 migrations/ 加 Warn
6. `config.go:38` envOrCompat legacy env 加 removal deadline
7. `config.go:24` setenvForConfig 文档化全局副作用

### P2（下个 sprint）
8. `config.go:92-99` trustedDevEntrypoint 加注释说明非安全边界
9. `config.go:130` applyDotEnv 已有 env 时加 Debug 日志
10. `config.go:218-234` resolveProjectRoot 3 层 fallback 文档化

## 边界条件

1. **`config.go:26-65` New 是项目启动的第一个函数**：所有模块（db、runner、notify、lsp、orch）都依赖 `*Config`。New 的错误处理质量直接决定启动失败时的诊断体验。当前 New 内部的 fail-fast 做得不错（line 58-63 exportRPCAddr/DatabaseURL 失败都返 error），但 resolveProjectRoot 的空字符串 fallback 是唯一漏洞。
2. **`config.go:101-118` loadDotEnv 的 packaged vs dev 双模式**：packaged 模式（有 runtime-manifest.json）下 .env 必须存在且格式正确（strict=true）；dev 模式下 .env 可选且格式宽松（strict=false）。这是合理的双模式设计——生产环境严格，开发环境宽松。但 dev 模式的宽松让格式错误静默——建议 dev 模式也 Warn（不 error）。
3. **`config.go:192-216` exportRPCAddrIfMissing / exportDatabaseURLIfMissing 的全局副作用**：这两个函数修改进程环境变量（os.Setenv）。注释（line 186-191）解释了原因：子进程（mcp-orch、mcp-lsp）需要继承这些 env。这是 Go 进程模型的合理做法（fork+exec 继承 env），但**对测试有副作用**——并行测试可能互相污染。`setenvForConfig` 变量（line 24）是 test hook 解法。
4. **`config.go:236-250` resolvePackagedProjectRoot 的 macOS bundle 检测**：检查 exe 路径是否在 `Contents/MacOS/` 下，如果是则 Resources/ 是 project root。这是 macOS .app bundle 的标准结构。Windows/Linux 不走此路径（line 242 `filepath.Base(exeDir) != "MacOS"` 直接返空）。跨平台兼容性良好。
5. **`config.go:148-165` parseDotEnvLineStrict 的安全性**：line 163 `strings.Trim(value, "'\"")` 去除引号——但不处理转义（如 `KEY="value with \"quotes\""`）。这是简化 .env parser，不支持完整 shell 语法。对于 DATABASE_URL 等值（可能含特殊字符），引号内的 `=` 不会被误切（因为 `strings.Cut` 只切第一个 `=`）。但 value 含引号时会被 trim 掉——如 `KEY="hello"` → value=`hello`（正确）；`KEY='it"s'` → value=`it"s`（正确）。
6. **`config.go:79-89` validateTrustedDevRuntimeMode 的防御性设计**：阻止在 packaged 环境下用 dev 模式运行（除非通过 trusted entrypoint）。这是防止生产环境误开 dev 模式的安全防御。**正面案例**——但 entrypoint 白名单可被伪造（env 可被任何进程设置），所以注释应明确「defense-in-depth, not security boundary」。

---

**本轮总结**：发现 2 个 P0 问题：①resolveProjectRoot Getwd 失败返空字符串是全局配置根路径的漏洞；②envBoolOr/envPositiveIntOr 非法值静默 fallback 让运维配错无感知。`config.go` 整体 fail-fast 做得不错（packaged 模式严格、export 失败返 error），但 env 解析层的静默 fallback 是系统性弱点。`validateTrustedDevRuntimeMode` 是防御性设计正面案例。

**累计进度**：45 轮完成。cron `fd4b4728` 继续推进。
