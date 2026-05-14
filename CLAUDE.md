# 任务完成验证：代码守卫

本规范强制具化了「验证闭环」：跑测试 + 构建/lint 之外，必须再跑代码守卫。
与 `@完成前验证` 技能配合：守卫绿 → 完成前验证 → 交付。

### Git Hook 已落地（pre-commit 强制守卫）

仓库通过 `.githooks/pre-commit` 在每次 `git commit` 时强制运行：

1. **后端代码守卫**：`test_with_guard.sh`
2. **前端代码守卫**：`size-guard.cjs` 以及前端测试

任一失败 → 中止 commit。落实「守卫不绿不得交付」红线，挡住人手疏漏。

**首次克隆或换机后启用：**

```bash
make install-hooks
```
或执行：
```bash
bash scripts/install-hooks.sh
```

脚本把 `core.hooksPath` 指向 `.githooks/`（受版本控制，团队同步一致）。

**边界与说明：**

- pre-commit **只跑两个守卫和短测**，不跑长测试 / gosec / race —— 这些重活归 `make test` 与 CI。
- pre-commit **不会**自动执行 `--freeze`，避免悄悄放宽 baseline；freeze 必须开发者显式决策。
- **紧急绕过**：`git commit --no-verify` 仅限事故/热修复场景（违反仓库规约 docs/1/会话习惯.md §10.12«禁止 bypass pre-commit hook»），事后**必须**补跑全面复检。

### Baseline 棘轮（Per-File Ratchet）

后端代码守卫增加了 per-file baseline 棘轮机制，基于 `internal/archtest/baseline.json`：

**三种模式：**

| 模式 | 命令 | 说明 |
|------|------|------|
| 检查（默认） | `go run scripts/code_size_guard.go` | CheckAll + baseline 棘轮 + 自动收缩 |
| 冻结 | `go run scripts/code_size_guard.go --freeze` | 全仓扫描建立/重建 baseline |
| 严格 | `go run scripts/code_size_guard.go --strict` | 无 baseline 全量检查 |

**核心规则：**

- baseline 只缩不放宽（ratchet）：代码改善时自动收紧指标阈值
- 指标恶化 → 守卫拦截，必须修复才能提交
- 文件删除 → 自动从 baseline 清理
- 文件指标全绿 → 自动毕业，从 baseline 移出

**红线：** 禁止随意 `--freeze` 全仓覆盖来逃避棘轮，freeze 必须有正当理由（如守卫规则变更）。
