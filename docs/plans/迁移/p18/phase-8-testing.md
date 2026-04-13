# P18 Phase 8：测试 + 守护

> 预计：1 天 | 依赖：Phase 0-7 + Phase 4.5

## 目标
全覆盖测试 + 架构守护 + 回归防护。

## 单元测试

| 模块 | 测试重点 |
|------|---------|
| memory/store | CRUD + 索引更新 + skipIndex + `memory_index_update_failed` 修复路径 |
| memory/paths | canonical git root + `ValidateMemoryRoot/WritePath` + sanitize + override / trusted-source 规则 |
| memory/truncate | 200 行截断 + 25 KB 截断 + warning + last-newline byte truncate |
| memory/scan | 递归扫描 + MEMORY.md 排除 + frontmatter 解析 + 200 header 上限 + partial timeout |
| memory/prompt_builder | taxonomy 完整性 + 排除列表 + save/access/trust 规则 + ignore-memory 强提示 |
| memory/agent_memory | 单 scope 目录 + sanitize + 空态分级 + telemetry + 截断 |
| memory/retrieval | structured selector parse + whitelist + readFileState 时序 + cooldown |
| prompt/registry | name-cache + nil-cache + volatile 重算 + generation invalidate + singleflight |
| prompt/sections | 12 个 section 内容关键字 + tool/session/mode 变更失效 |
| prompt/builder | 输出 `ResolvedPromptSection/Snapshot`，不直接丢失结构 |
| prompt/context | UserContext 聚合 + SystemContext gitStatus |

## 集成测试

- fx app start/stop smoke + `fx.ValidateApp` 通过
- thread/start → PromptAssemblyService.AssembleStart() → instructions 正确
- 新会话自动加载 `MEMORY.md`，且 200 行索引上限在启动链路生效
- 四种 memory type（user/feedback/project/reference）端到端写入/读回/检索矩阵
- turn/start → UserContext 前置 → 模型收到
- memory_write 新建 → MEMORY.md 更新 → memory_read 能读回
- memory_write **upsert**：已有同名时更新而非重复创建
- memory_search：keyword + type filter + limit + fail-soft
- memory_list：scope/type 过滤、排序、forget 后同步更新
- memory_forget：删除后索引同步更新
- Agent memory：不同 agentType 隔离、单 scope 加载、preview/runtime 一致
- Relevant memories：非阻塞预取、结构化 selector 输出、三段式去重、60KB 阈值、cancel/singleflight/CAS consume
- `claudeMd` source resolver 专项矩阵：来源顺序、二段过滤、additional dirs、external include warning vs normal load、disable/bare gates、nested worktree 去重
- 缓存失效：clear 后 section 重算
- Phase 4.5 回归测试：
  - PromptAssembly 契约存在：`AssemblyService` / `StartInput` / `TurnInput` / `PromptAssemblySnapshot`
  - provider-specific 启动链：codex thread/start 收到 `baseInstructions + developerInstructions`；claude launch 收到完整 `--system-prompt`
  - start payload golden：codex / claude 各有一份固定 fixture，明确 `DisplayName / BaseInstructions / DeveloperInstructions / Snapshot` 的最终落点
  - provider-specific turn 链：codex `turn/start + turn/steer` 收到前置 synthetic input；claude turn 在 `prepareTurnLocked()` 前缀注入 UserContext
  - turn/steer payload golden：明确 `skill prompt / UserContext / attachment hint / user text / output schema` 的最终顺序
  - BaseInstructions 不污染 thread name/store/resume/Fork/Recover/toRef/SetName
  - Provider 切换（codex→claude + claude→codex 双向）时 section cache 清空
  - auto-compact / partial compact cleanup hook 会触发 section cache invalidate
  - 子 Agent 调用 AssembleStart() 且产物传到子 agent，不走旧折叠路径
  - legacy prompt 只做 displayName fallback，不重新污染 BaseInstructions/launch name
  - `binding` / `rpc_types` / `service_handlers` / `resume` / `fork` / `recover` / `toRef` 回归覆盖
  - presence-aware bool alias 冲突（显式 `false` 不被 legacy `true` 覆盖）与 config alias 归一测试
  - Claude Restart/Recovery 从 `PromptAssemblySnapshot` 恢复的回归用例
  - `PromptAssemblySnapshot` 的 `Version + Hash + Provider` 校验与 compat fallback 用例
- 并发安全：并发 `memory_write/forget` 不产生重复索引或损坏 `MEMORY.md`；section cache 并发 build/invalidate 不串 provider；`go test -race` 覆盖 memory/prompt/retrieval/provider switch
- rollout flags / kill switch：关闭后停止新 memory 写入与 prompt 注入，但不影响既有 `shared_files` 协作链路
- 错误处理：`feature_disabled`、敏感信息校验失败、frontmatter/type 校验失败、manifest timeout、selector invalid JSON、PromptRegistry 顶层 build 失败都返回显式错误且无半写文件
- 安全负例：path traversal / symlink race / ACL deny / secret redaction / 恶意 memory 注入 / selector 不继承主链高权限
- 可观测性：`memory_write/search/forget` 与 prompt cache invalidate 日志包含 `provider/threadID/reason/scope/result`
- 迁移脚本幂等性：重跑不重复造 memory，且对被跳过内容输出 skip reason
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

```bash
go build ./internal/module/memory/... ./internal/module/prompt/... ./internal/module/thread/... ./internal/provider/codexapp/... ./internal/provider/claudecli/...
go vet ./internal/module/memory/... ./internal/module/prompt/... ./internal/module/thread/...
go test ./internal/module/memory/... ./internal/module/prompt/... ./internal/module/thread/... ./internal/provider/codexapp/... ./internal/provider/claudecli/...
go test -race ./internal/module/memory/... ./internal/module/prompt/... ./internal/provider/unified/...
go test -run TestCodeSizeGuard ./internal/archtest/...
golangci-lint run ./internal/module/memory/... ./internal/module/prompt/... ./internal/module/thread/...
```

## 验收
- 全部测试通过
- 无新增 lint 告警
- 架构依赖方向正确
