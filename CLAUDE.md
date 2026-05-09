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
