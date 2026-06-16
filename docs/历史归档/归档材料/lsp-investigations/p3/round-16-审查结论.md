# 第 16 轮审查结论

## 审查范围

- `internal/contract/errors.go`（sentinel errors：ErrSessionNotFound、ErrSkillMissingCWD、ErrThreadRuntimeRequired、SkillApprovalRequiredError、store sentinels）
- `internal/contract/memory.go`（MemoryScope/MemoryType 解析、AgentMemoryReader/Writer 接口、AgentMemoryError、MemoryReadRequest/WriteRequest）
- `internal/contract/session.go`（SessionStarter/Provider/Resolver 接口、SessionThreadRef/Binding、TurnThreadCleaner）
- `internal/contract/hooks.go`（HookManager/HookLifecycle/HookReviewStore 接口）
- `internal/contract/config.go`（Config/SkillConfig/AgentConfig/NotifyConfig/LSPConfig 类型定义）
- `internal/contract/manifest.go`（BuildManifest、normalizeManifestEnv、addMCPProjectRootEnv、inferManifestProjectRootFromBinaryDir）

> 与第 01-15 轮覆盖的所有 `cmd/`、`internal/platform/`、`internal/module/turn/` 不重复。

## 高危发现（违反 Fail-Fast）

| 文件:行 | 类别 | 当前实现 | 风险 | 修复建议 |
| --- | --- | --- | --- | --- |
| `memory.go:26-39` `ParseMemoryScope` | 兜底 | 空字符串默认返回 `MemoryScopeUser`；未知值返回 `MemoryScope("")` | 空字符串 = user 是隐式默认；调用方传空时不知道自己拿到了 user scope | 空字符串应返回 `MemoryScope("")` 让调用方显式判断；或返回 error |
| `memory.go:60-75` `ParseMemoryType` | 兜底 | 空字符串和 "unknown" 都返回 `MemoryTypeUnknown`；未知值也返回 `MemoryTypeUnknown` | 用户拼错 type（如 "projct"）拿到 Unknown 而非 error；与 round-06 `parseMemoryWriteType` 配合后被 `invalid_input` 拒绝，但错误信息不精确 | 未知值返回 error 或专用 sentinel |
| `memory.go:168-170` `NewAgentMemoryError` | 弱契约 | `code` 仅 trim；`err` 可以为 nil | code 为空时 `AgentMemoryError{Code: "", Err: nil}` 的 `Error()` 返回 ""；调用方拿到空字符串 error | code 为空应 panic |
| `memory.go:172-181` `AgentMemoryErrorCode` | 兜底 | err==nil 返回 "" | 合理 | OK |
| `manifest.go:18-66` `BuildManifest` | 兜底 | `ctx.BinaryDir` 为空时 `filepath.Join("", "mcp-lsp")` 得到相对路径 | 与 round-13 resolveBinaryDir 同根；exec.Command 会在 PATH 查找 | 至少 Warn；或在入口校验 BinaryDir 非空 |
| `manifest.go:70-86` `addMCPProjectRootEnv` | 兜底 | 四层 fallback：env map → os.Getenv → ctx.ProjectRoot → inferFromBinaryDir | 四层 fallback 掩盖配置缺失；最终都找不到时 env 中不设 PROJECT_ROOT，子进程启动后自行 resolve | 至少 debug log "PROJECT_ROOT not resolved" |
| `manifest.go:88-103` `inferManifestProjectRootFromBinaryDir` | 兜底 | 从 binaryDir 向上遍历找 `migrations/` 目录；找不到返回 "" | 无限向上遍历直到 root；如果 binaryDir 是 `/usr/local/bin`，会扫描整个文件系统 | 加最大深度限制（如 10 层） |
| `manifest.go:105-116` `normalizeManifestProjectRoot` | 兜底 | 非绝对路径时尝试 `filepath.Abs`；失败时保留原路径 | `filepath.Abs` 失败（如 Getwd 失败）时保留相对路径，后续 filepath.Join 可能产生非法路径 | Abs 失败应 return error |
| `manifest.go:118-120` `hasManifestMigrationsDir` | 静默 | `os.Stat` 错误（非 NotExist）被当成"不存在" | 权限错误被当成"没有 migrations 目录" | 区分 NotExist 与其它 |
| `config.go:1-78` 整个文件 | 弱契约 | 纯类型定义，无 Validate 方法 | Config 字段全部可为零值；构造后无校验 | 加 `func (c *Config) Validate() error` |
| `errors.go:28-38` `SkillApprovalRequiredError` | 弱契约 | `Request ApprovalRequest` 字段无校验；Error() 返回固定字符串不含 Request 信息 | 调用方拿到 error 后必须 `errors.As` 才能获取 Request；Error() 文本无法定位具体 approval | Error() 应包含 `req.ToolName` 或 `req.CallID` |
| `session.go:12-14` `SessionStarter` | 弱契约 | `StartSession` / `ResumeSession` 返回 `(Session, error)`；Session 可能为 nil + nil error | 接口文档未标注"Session must not be nil when error is nil" | 在接口注释中标注；或在 contract 层加 `requireNonNilSession` helper |
| `hooks.go:19-26` `HookManager` | 弱契约 | 6 个方法全部返回 `(T, error)`；无 nil 校验约定 | 实现方可能返回零值 + nil error | 接口文档标注 |

## 静默错误清单

| 位置 | 现象 |
| --- | --- |
| `memory.go:26-39` | ParseMemoryScope 空字符串默认 user |
| `memory.go:60-75` | ParseMemoryType 未知值默认 Unknown |
| `manifest.go:70-86` | addMCPProjectRootEnv 四层 fallback 无日志 |
| `manifest.go:88-103` | inferManifestProjectRootFromBinaryDir 无限向上遍历 |
| `manifest.go:105-116` | normalizeManifestProjectRoot Abs 失败保留相对路径 |
| `manifest.go:118-120` | hasManifestMigrationsDir stat 错误当不存在 |

## 弱契约清单

| 位置 | 弱契约点 |
| --- | --- |
| `memory.go:26-39` | ParseMemoryScope 空字符串 = user |
| `memory.go:60-75` | ParseMemoryType 未知值 = Unknown |
| `memory.go:168-170` | NewAgentMemoryError code 可为空 |
| `config.go:67-78` | Config 无 Validate |
| `errors.go:28-38` | SkillApprovalRequiredError.Error() 不含 Request 信息 |
| `session.go:12-14` | SessionStarter 返回 nil Session 无约束 |
| `hooks.go:19-26` | HookManager 返回零值无约束 |
| `manifest.go:18-66` | BuildManifest ctx.BinaryDir 可为空 |
| `manifest.go:88-103` | inferManifestProjectRootFromBinaryDir 无深度限制 |
| `manifest.go:105-116` | normalizeManifestProjectRoot Abs 失败不报错 |

## 修复优先级

### P0（必须本周修）
1. `manifest.go:88-103` inferManifestProjectRootFromBinaryDir 加最大深度限制（如 10 层），避免扫描整个文件系统
2. `memory.go:26-39` ParseMemoryScope 空字符串不应默认 user——改为返回 `MemoryScope("")` 让调用方显式处理
3. `memory.go:168-170` NewAgentMemoryError code 为空应 panic

### P1（本月）
4. `manifest.go:70-86` addMCPProjectRootEnv 四层 fallback 至少 debug log
5. `manifest.go:105-116` normalizeManifestProjectRoot Abs 失败返回 error
6. `manifest.go:118-120` hasManifestMigrationsDir 区分 NotExist 与其它
7. `memory.go:60-75` ParseMemoryType 未知值返回 error
8. `config.go` 加 `Config.Validate()` 方法
9. `manifest.go:18-66` BuildManifest 入口校验 BinaryDir 非空

### P2（下个 sprint）
10. `errors.go:28-38` SkillApprovalRequiredError.Error() 包含 Request 信息
11. `session.go:12-14` 接口文档标注 nil 约束
12. `hooks.go:19-26` 接口文档标注返回值约束

## 边界条件

1. **`ParseMemoryScope` 空字符串 = user 是历史兼容**：memory_read_tool.go 的 `parseMemoryReadScope` 调用 `ParseMemoryScope("")` 期望得到 user scope。改为返回空 MemoryScope 后，`parseMemoryReadScope` 需要显式处理空值（fallback 到 user 或报错）。建议在 `parseMemoryReadScope` 层做 fallback，不在 contract 层。
2. **`inferManifestProjectRootFromBinaryDir` 的无限遍历**：当前 binaryDir 通常是 `/path/to/app/Contents/Resources/bin`，向上 3 层就能找到 `migrations/`。加 10 层限制不会影响正常路径。但如果 binaryDir 是 `/usr/local/bin`（dev 模式），向上遍历不会找到 migrations，最终返回 ""——这是合理的 fallback。
3. **`normalizeManifestProjectRoot` 的 Abs 失败**：`filepath.Abs` 内部调用 `os.Getwd()`，只有在进程 cwd 被删除时才会失败。极端场景但存在。改 error 后 `addMCPProjectRootEnv` 需要处理这个 error（当前是 void 函数）。
4. **`NewAgentMemoryError` code 为空 panic 的影响**：grep 调用面确认所有调用点都传了非空 code。当前 `memory_read_tool.go` 和 `memory_write_tool.go` 都传了字面量 code，不会为空。
5. **`Config.Validate()` 的范围**：Config 字段很多，Validate 应该只校验"必须非零"的字段（如 DatabaseURL、RPCAddr）。可选字段（如 Skill.TokenBudget）用零值表示"用默认"是合理的。
6. **`ParseMemoryType` 未知值改 error 的影响**：`parseMemoryReadType` 在 `memory_read_tool.go:99-106` 中对空字符串返回 Unknown 是合法的（"不过滤 type"）。改 error 后需要区分"空字符串 = 不过滤"与"非法值 = 报错"。建议：空字符串仍返回 Unknown（合法），非空未知值返回 error。
7. **contract 包是纯类型/接口定义层**：本轮发现的问题大多是"缺少校验"而非"有错误逻辑"。contract 层的设计哲学是"定义契约，不做实现"。校验应该在实现层（如 platform/config、module/turn）做。但 Parse* 函数是 contract 层的实现，应该严格。

---

下一轮范围建议：
- `internal/contract/manifest.go` 剩余部分（normalizeManifestEnv、addLSPWorkspaceRootEnv、cloneManifestEnv）
- `internal/contract/orchestration.go`（OrchestrationService 接口）
- `internal/contract/provider.go`（Session 接口、TurnHandle）
- `internal/contract/bus.go`（ResilientSubscribe）
- `internal/contract/frc.go`（FRCConfig）
- 或切换到 `internal/sidecar/lsp/tools/tool_xref.go` + `tool_grep.go`
