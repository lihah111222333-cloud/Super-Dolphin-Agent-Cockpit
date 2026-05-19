### Task 4: 技术债务清理（TODO Count）

**Files:**
- Modify: 违反 `todo_count` 的 34 个文件。

#### 评选唯一修复方案
- **唯一方案**: 采取**清算或转移（Fix or Evict）**二元法。
  - *原因*: 代码库中不允许出现“等未来有空再做”的无效占位符。如果该缺陷不影响当前业务流，它就是伪需求，可以直接删除代码注释；如果真有需要，应当作为 Feature Request 在专门的 Issue 跟踪系统中记录，让主干代码保持纯净。

#### 边界条件 (Boundary Conditions)
1. **防范围蔓延**: 对于能够在一个 Commit (20 行内) 解决的 TODO（例如补充超时控制、补充错误类型检查），直接就地实现并删除 TODO。
2. **拒绝大规模强行实现**: 如果该 TODO 涉及到架构级变动（例如 `// TODO: 重写整个 RPC 鉴权层`），**禁止在本次任务中直接去开发该功能**，唯一动作是将其转移到独立的架构文档/工单系统中，并从源代码中剔除此注释。

- [ ] **Step 1: 定位代码中的 TODO 标记**
搜索文件内的 `// TODO` 或 `// FIXME`。

- [ ] **Step 2: 消除债务**
```go
// 修复后（就地补齐情况）
func Fetch(ctx context.Context) {
    client.Get(ctx, ...)
}
```

- [ ] **Step 3: 运行验证**
运行: `make guard`
Expected: `todo_count` 清零，基线自动收缩。

- [ ] **Step 4: Commit**
```bash
git add .
git commit -m "refactor: clean up legacy todo tags"
```
