# P18 Phase 8：测试 + 守护

> 预计：1 天 | 依赖：Phase 0-7 + Phase 4.5

## 目标
全覆盖测试 + 架构守护 + 回归防护。

## 单元测试

| 模块 | 测试重点 |
|------|---------|
| memory/store | CRUD + 索引更新 + skipIndex |
| memory/paths | canonical git root + validateMemoryPath + sanitize |
| memory/truncate | 200行截断 + 25KB截断 + warning |
| memory/scan | 递归扫描 + MEMORY.md 排除 + frontmatter 解析 + 200 header 上限 |
| memory/prompt_builder | taxonomy 完整性 + 排除列表 + save/access/trust 规则 |
| memory/agent_memory | 三种 scope 目录 + sanitize + 空态处理 + 截断 |
| prompt/registry | name-cache + nil-cache + volatile 重算 + 失效 |
| prompt/sections | 12 个 section 内容关键字 |
| prompt/builder | 组装顺序 + filter nil |
| prompt/context | UserContext 聚合 + SystemContext gitStatus |

## 集成测试

- thread/start → PromptAssemblyService.AssembleStart() → instructions 正确
- turn/start → UserContext 前置 → 模型收到
- memory_write 新建 → MEMORY.md 更新 → memory_read 能读回
- memory_write **upsert**：已有同名时更新而非重复创建
- memory_search：keyword + type filter + limit + fail-soft
- memory_forget：删除后索引同步更新
- 缓存失效：clear 后 section 重算
- Phase 4.5 回归测试：
  - PromptAssembly 契约存在：`AssemblyService` / `StartInput` / `TurnInput` / `PromptAssemblySnapshot`
  - provider-specific 启动链：codex thread/start 收到 `baseInstructions + developerInstructions`；claude launch 收到完整 `--system-prompt`
  - provider-specific turn 链：codex turn/start 收到前置 synthetic input；claude turn 在 `prepareTurnLocked()` 前缀注入 UserContext
  - BaseInstructions 不污染 thread name/store/resume/Fork/Recover/toRef/SetName
  - Provider 切换（codex→claude + claude→codex 双向）时 section cache 清空
  - 子 Agent 调用 AssembleStart() 且产物传到子 agent，不走旧折叠路径
  - legacy prompt 只喂给 BaseInstructions，不重新污染 Prompt/launch name
  - `binding` / `rpc_types` / `service_handlers` / `resume` / `fork` / `recover` / `toRef` 回归覆盖
  - Claude Restart/Recovery 从 `PromptAssemblySnapshot` 恢复的回归用例
- rollout flags / kill switch：关闭后停止新 memory 写入与 prompt 注入，但不影响既有 `shared_files` 协作链路
- 可观测性：`memory_write/search/forget` 与 prompt cache invalidate 日志包含 `provider/threadID/reason/scope/result`
- 迁移脚本幂等性：重跑不重复造 memory

## 架构测试

```go
func TestMemoryModuleNoDependOnProvider(t *testing.T) {
    // memory 模块不依赖 provider 模块（单向依赖）
}
```

## 守护测试

```go
func TestPromptSectionsCount(t *testing.T) {
    // 确保 static=7, dynamic=5, total=12
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
go test -run TestCodeSizeGuard ./internal/archtest/...
```

## 验收
- 全部测试通过
- 无新增 lint 告警
- 架构依赖方向正确
