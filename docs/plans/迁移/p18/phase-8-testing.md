# P18 Phase 8：测试 + 守护

> 预计：1 天 | 依赖：Phase 0-7

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

- thread/start → PromptRegistry.Build() → instructions 正确
- turn/start → UserContext 前置 → 模型收到
- memory_write 新建 → MEMORY.md 更新 → memory_read 能读回
- memory_write **upsert**：已有同名时更新而非重复创建
- memory_search：keyword + type filter + limit + fail-soft
- memory_forget：删除后索引同步更新
- 缓存失效：clear 后 section 重算
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
go build ./internal/module/memory/... ./internal/module/prompt/...
go vet ./internal/module/memory/... ./internal/module/prompt/...
go test ./internal/module/memory/... ./internal/module/prompt/...
go test -run TestCodeSizeGuard ./internal/archtest/...
```

## 验收
- 全部测试通过
- 无新增 lint 告警
- 架构依赖方向正确
