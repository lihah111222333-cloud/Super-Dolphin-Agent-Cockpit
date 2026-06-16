# P18 Phase 8：测试 + 守护

> 预计：1-2 天（建议拆 P0 / P1） | 依赖：Phase 0-7 + Phase 4.5

## 目标
全覆盖测试 + 架构守护 + 回归防护。

## 执行分层
- **P0（blocking）**：PromptAssembly / provider 注入 / memory store / retrieval 去重并发 / 安全负例 / rollback drill / roundtrip
- **P1（扩展）**：benchmark 基线、额外 golden、更多 compat matrix、长期 arch guard

## 单元测试

| 模块 | 测试重点 |
|------|---------|
| memory/store | CRUD + `canonical_name`（NFC/case fold/whitespace collapse）+ 索引更新 + skipIndex + Delete/Rebuild + `memory_index_update_failed` 修复路径 |
| memory/paths | canonical git root + `ValidateMemoryRoot/WritePath` + sanitize + override / trusted-source 规则 |
| memory/truncate | 200 行截断 + 25 KB 字符串长度语义 + warning + last-newline 边界截断 |
| memory/scan | 递归扫描 + MEMORY.md 排除 + frontmatter 解析 + 200 header 上限 + partial timeout |
| memory/prompt_builder | taxonomy 完整性 + 排除列表 + save/access/trust 规则 + ignore-memory 强提示 + `skipIndex/extraGuidelines` API + `Searching past context` gated/deferred |
| memory/agent_memory | 单 scope 目录 + sanitize + 空态分级 + telemetry + 截断 |
| memory/retrieval | structured selector parse + whitelist + readFileState 时序 + query cache key + attachment/replay identity + cooldown |
| memory/migration | `shared_files → 磁盘记忆` owner/scope 归属 + 幂等 + dry-run/apply/rollback manifest + skip reason 输出 |
| prompt/registry | name-cache + nil-cache + volatile 重算 + generation invalidate + singleflight |
| prompt/sections | 12 个 section 内容关键字 + tool/session/mode 变更失效 |
| prompt/builder | 输出 `ResolvedPromptSection/Snapshot`，不直接丢失结构 |
| prompt/context | UserContext 聚合 + SystemContext gitStatus |

## 集成测试

- fx app start/stop smoke + `fx.ValidateApp` 通过
- thread/start → `PromptAssemblyService.AssembleStart()` → `StartAssembly{BaseInstructions, DeveloperInstructions, Snapshot}` 落点正确
- 新会话自动加载 `MEMORY.md`，且 200 行索引上限在启动链路生效
- 四种 memory type（user/feedback/project/reference）在 hook / slash command / `memory_read` 链路上的端到端覆盖矩阵
- turn/start → `PromptAssemblyService.AssembleTurn()` → `TurnAssembly.UserContextText` 前置 → 模型收到
- `turn/end` hook 命中保存意图 → topic file / `MEMORY.md` 更新 → `memory_read` 能读回
- `/memory`：当前作用域可见记忆视图正确，线程/agent 可见集符合 ACL
- `/forget`：删除后 topic file / `MEMORY.md` / 索引同步更新
- `cmd/mcp-orch` registry 仅暴露 `memory_read`，不再注册 `memory_write/search/list/forget`
- `shared_file_read/write` V2 兼容矩阵：path canonicalization、`file <path> not found`、固定 `agent` actor、10 MiB 上限、flags 关闭时旧 `shared_files` 链路不受影响
- 种子迁移矩阵：`shared_files → 磁盘记忆` owner/scope 归属、幂等、dry-run/apply/rollback manifest、skip reason 可验证
- repair/debug 面：显式 `RebuildMemoryIndex()` 可修复索引；`memory_read` degraded 视图可用；默认 registry 不额外暴露写入/删除类 public memory tool
- hook / slash command / migrate 共享 authorizer 与写锁，不做隐式后台修复
- Agent memory：不同 agentType 隔离、单 scope 加载、preview/runtime 一致，且 `@agent` 仅在当前线程授权链可见
- Relevant memories：非阻塞预取、`manifest.jsonl` sidecar / query cache key、结构化 selector 输出、三段式去重、history/replay roundtrip、60KB 阈值、cancel/singleflight/CAS consume，且与 `TurnAssembly.UserContextText` 分离、走 attachment/hint 链
- `claudeMd` source resolver 专项矩阵：来源顺序、二段过滤、additional dirs、external include warning vs normal load、disable/bare gates、nested worktree 去重
- 缓存失效矩阵：`/clear`、`/compact`、worktree、`/resume restore`、provider switch、auto-compact、partial compact
- Phase 4.5 回归测试：
  - PromptAssembly 契约存在：`PromptAssemblyService` / `StartInput` / `TurnInput` / `StartAssembly` / `TurnAssembly` / `PromptAssemblySnapshot`
  - provider-specific 启动链：codex thread/start 收到 `StartAssembly.BaseInstructions + StartAssembly.DeveloperInstructions`；claude launch 收到由同一 `StartAssembly` 映射出的完整 `--system-prompt`
  - start payload golden：codex / claude 各有一份固定 fixture，明确 `DisplayName / BaseInstructions / DeveloperInstructions / Snapshot` 的最终落点
  - provider-specific turn 链：codex `turn/start + turn/steer` 消费 `TurnAssembly.UserContextText` 生成前置 synthetic input；claude turn 在 `prepareTurnLocked()` 消费同一 `TurnAssembly.UserContextText` 前缀注入 UserContext
  - turn/steer payload golden：明确 `skill prompt / UserContext / attachment hint / user text / output schema` 的最终顺序
  - `internal/module/prompt/service.go` 服务级契约：`AssembleStart()` 必须产出 `{DisplayName, BaseInstructions, DeveloperInstructions, Provider, Version, Hash}` 一致快照；`AssembleTurn()` 是 `TurnAssembly.UserContextText` 的唯一生产者，provider adapter 只能消费不能重算
  - BaseInstructions 不污染 thread name/store/resume/Fork/Recover/toRef/SetName
  - displayName / snapshot 的 store roundtrip：start/upsert/reload/lookupThreadMeta/resume/fork/recover 后保持一致
  - Provider 切换（codex→claude + claude→codex 双向）时 section cache 清空
  - auto-compact / partial compact cleanup hook 会触发 section cache invalidate
  - `TurnAssembly` 交接契约：thread/turn → provider adapter 不再走 `FirstNonEmpty(req.BaseInstructions, req.Prompt)` 兜底
  - Resume/Recover 不重复发布 `thread.Started`；Recover 不发送会冲空 UI 字段的缩水 Started 事件
  - unified optional capability / `SessionIdentity` 合同与 roundtrip 序列化测试
  - 子 Agent 调用 `PromptAssemblyService.AssembleStart()` 且产物传到子 agent，不走旧折叠路径
  - orchestration bridge 协议测试：thread launch request → orchestration contract → remote launcher payload 不丢 `displayName / baseInstructions / developerInstructions`
  - legacy prompt 只做 displayName fallback，不重新污染 BaseInstructions/launch name
  - `binding` / `rpc_types` / `service_handlers` / `resume` / `fork` / `recover` / `toRef` 回归覆盖
  - Wails `LaunchAgent(name,prompt,cwd)` 旧入口兼容用例：未显式传 `Name` 时仍能得到正确 `displayName`，且 legacy `prompt` 不再回灌成 provider instructions
  - `SetName()` / `thread/name/set` 回归：只更新 displayName 槽位与 provider rename sync，不得把 `BaseInstructions` / snapshot 回写到旧 `Prompt` 污染链
  - presence-aware bool alias 冲突（显式 `false` 不被 legacy `true` 覆盖）与 config alias 归一测试
  - Claude Restart/Recovery 从 `PromptAssemblySnapshot` 恢复的回归用例
  - `PromptAssemblySnapshot` 的 `Version + Hash + Provider` 校验与 compat fallback 用例
- 并发安全：并发 `turn/end` hook 写盘与 `/forget` 不产生重复索引或损坏 `MEMORY.md`；section cache 并发 build/invalidate 不串 provider；`./scripts/go_with_guard.sh test -race ...` 覆盖 memory/prompt/retrieval/provider switch
- rollout flags / kill switch：关闭后停止新 memory 写入与 prompt 注入，但不影响既有 `shared_files` 协作链路
- 错误处理：`feature_disabled`、敏感信息校验失败、frontmatter/type 校验失败、manifest timeout、selector invalid JSON、PromptRegistry 顶层 build 失败都返回显式错误且无半写文件；敏感信息规则集版本 / 稳定错误码 / fixture 样例可验证
- 安全负例：path traversal / symlink race / ACL deny / `@agent` 越权读取 / cross-machine local scope replay / secret redaction / 恶意 memory 注入 / selector 不继承主链高权限
- 可观测性：`turn/end` hook 写盘、`memory_read`、`/forget` 与 prompt cache invalidate 日志包含 `provider/threadID/reason/scope/result`
- 迁移脚本幂等性：重跑不重复造 memory，且对被跳过内容输出 skip reason
- 兼容/迁移：`turn/end` hook 写盘与迁移脚本共用 `ExclusionClassifier`；`memory_read` 在索引损坏时返回 `degraded=true` / `source=rebuilt_view`；dry-run/apply 报告可验证
- `MEMORY.md` repair drill：故意打坏 index 后，可从 topic files 重建并恢复 hook 顺序

## 性能守护

- benchmark：
  - `BenchmarkAssembleStart_3ClaudeFiles_200MemoryIndex`
  - `BenchmarkRelevantMemoryPrefetch_200Headers`
  - `BenchmarkCacheInvalidate_ProviderSwitch`
- 指标：`prompt_assembly_ms`、`base_instructions_tokens`、`user_context_tokens`、`manifest_scan_ms`、`selector_ms`、`selector_cache_hit_ratio`、`section_cache_bytes`、`snapshot_bytes`
- 回归要求：provider 切换、compact invalidate、manifest repair scan 不能把单轮 prompt 装配延迟拉出基线预算

## 架构测试

```go
func TestMemoryModuleNoDependOnProvider(t *testing.T) {
    // memory 模块不依赖 provider 模块（单向依赖）
}

func TestPromptModuleDoesNotDependOnProviderTransport(t *testing.T) {
    // prompt 只产 assembly，不直接依赖 provider transport 细节
}

func TestProviderConsumesAssemblyButDoesNotImportPromptBuilders(t *testing.T) {
    // provider 只消费 assembly 结果，不反向依赖 memory/prompt builder 实现
}

func TestPromptStoreImportsUsePromptstoreAlias(t *testing.T) {
    // 凡 import internal/store/prompt 必须使用 promptstore 别名，避免与 internal/module/prompt 混淆
}
```

## 守护测试

```go
func TestPromptSectionsCount(t *testing.T) {
    // 确保 static=7, dynamic=5, total=12 slots
}

func TestPromptFixtureRendersExpectedNonNilSections(t *testing.T) {
    // 固定 fixture 下命中预期非 nil sections，且允许实际渲染数 < 12
}

func TestPromptContainsKeyRules(t *testing.T) {
    // 防过度设计三原则
    // 四类高危动作
    // LSP 工具链禁止项
    // 四种记忆类型
    // 排除列表关键词
}
```

## 仓库契约验证

- 文件 ≤ 400 行
- 函数 ≤ 80 行
- CC ≤ 10
- 包非测试文件 ≤ 15

## 验证命令

> Go 构建/测试默认走 guarded wrapper，避免绕过仓库级 code-size / raw-go guard。

```bash
./scripts/go_with_guard.sh build ./internal/module/memory/... ./internal/module/prompt/... ./internal/module/thread/... ./internal/provider/codexapp/... ./internal/provider/claudecli/... ./internal/sidecar/orch/orchestration/... ./internal/sidecar/orch/tools/...
./scripts/go_with_guard.sh vet ./internal/module/memory/... ./internal/module/prompt/... ./internal/module/thread/... ./internal/sidecar/orch/orchestration/...
./scripts/go_with_guard.sh test ./internal/module/memory/... ./internal/module/prompt/... ./internal/module/thread/... ./internal/provider/codexapp/... ./internal/provider/claudecli/... ./internal/sidecar/orch/orchestration/... ./internal/sidecar/orch/tools/...
./scripts/go_with_guard.sh test -race ./internal/module/memory/... ./internal/module/prompt/... ./internal/provider/unified/... ./internal/sidecar/orch/orchestration/...
./scripts/go_with_guard.sh test -bench 'BenchmarkAssembleStart|BenchmarkRelevantMemoryPrefetch|BenchmarkCacheInvalidate' -run '^$' ./internal/module/prompt/... ./internal/module/memory/...
./scripts/go_with_guard.sh test -run 'TestCodeSizeGuard|TestMemoryModuleNoDependOnProvider|TestPromptModuleDoesNotDependOnProviderTransport|TestProviderConsumesAssemblyButDoesNotImportPromptBuilders|TestPromptStoreImportsUsePromptstoreAlias' ./internal/archtest/...
golangci-lint run ./internal/module/memory/... ./internal/module/prompt/... ./internal/module/thread/... ./internal/sidecar/orch/orchestration/... ./internal/sidecar/orch/tools/...
```

## 验收
- 全部测试通过
- 无新增 lint 告警
- 架构依赖方向正确
