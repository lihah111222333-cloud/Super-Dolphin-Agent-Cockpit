# Production hook E2E

`scripts/tests/test_gate_hook_production_e2e.sh` 只消费已由发布流程安装的
`super-dolphin-gate`。它不生成 production config、密钥、accepted image 或 Docker
fixture，也不使用测试 connector。

Git 链路在临时 linked worktree 和本地 bare remote 中执行真实 `git commit` 与
`git push`。驱动先提交一个尾随空白违规，要求终态失败和可执行的
`job/status/wait` 反馈；随后修复 staged tree，等待队列终态，要求 signed receipt、
真实 gate results、source/image tree 一致，再验证直接 CLI 与 pre-push ActionGrant。

```sh
GATE_HOOK_E2E_EVIDENCE_DIR=/private/path/evidence \
  bash scripts/tests/test_gate_hook_production_e2e.sh git
```

Codex 链路必须由真实 Stop 或 SubagentStop 生命周期把原始 JSON 直接送入 stdin。
驱动拒绝不属于当前活动 worktree 的 cwd，拒绝 Stop 携带 agent_id，并要求
SubagentStop 的公开 agent_id。输出必须是唯一 JSON，且通过态关联 job、receipt、
source tree 和 status 命令。

```sh
bash scripts/tests/test_gate_hook_production_e2e.sh codex
```

若发布 launcher、production config、coordinator、accepted truth image、runtime seed
或 Docker authority 尚未部署，驱动按真实错误 fail closed；不得用仓库内 fixture
替代该证据。
