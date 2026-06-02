# Round 033 - 精修验证策略

## 目的

定义 12-agent 精修完成后的验证流程，确保修复不引入回归。

## 验证层次

### Layer 1：编译通过

```bash
go build ./...
```

### Layer 2：单元测试全量通过

```bash
./scripts/test_with_guard.sh ./internal/... -count=1
```

### Layer 3：archtest 守卫通过

```bash
make guard
```

确认 baseline.json 中无新增违规。

### Layer 4：新增守卫规则通过

round-032 提出的 4 条新规则必须在精修后 baseline 为 0。

### Layer 5：互审

12 个 agent 两两互审：
- A1 审 A2，A2 审 A3，...，A12 审 A1。
- 互审标准：
  1. 修复是否完整（不留残余兜底）
  2. 签名变更是否级联到所有 caller
  3. 测试是否覆盖新增 error 路径
  4. 是否引入新的 lint 违规

### Layer 6：集成分支终审

合并到集成分支后：
- 全量 `make test`
- 全量 `make guard`
- `go vet ./...`
- `golangci-lint run`

通过 → 合并主分支。
不通过 → 打回对应 agent 重做。

## 回退策略

每个 agent 在独立 worktree 工作。如果终审不通过：
1. 定位失败的 agent 修改。
2. 在该 worktree 中修复。
3. 重新提交互审。
4. 重新合并集成分支。
