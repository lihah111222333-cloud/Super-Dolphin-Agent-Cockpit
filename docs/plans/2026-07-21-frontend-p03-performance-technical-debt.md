# 前端 P03 停止反馈性能技术债务

> **状态：** OPEN / 本轮明确排除性能验收
>
> **记录日期：** 2026-07-21
>
> **代码范围：** `frontend-app`

## 1. 本轮裁决

本轮前端 95+ 可维护性任务不再把性能测试作为完成门禁。原因是同机存在其他持续高负载任务，无法在冻结负载窗口内稳定取得可比的 P03 停止反馈测量。

该裁决只缩小本轮验收范围，不改变冻结评分器、性能基线、阈值或控制项状态。P03 不得记录为 PASS，完整冻结评分器的 `FINAL_GATE` 也不得记录为 PASS。

## 2. 已取得的原始证据

- 评分对象：`03bd70beaf1763e876119fdc4b43dd41a9ad3b10`
- subject tree：`a2fadba1a922ba4cd44ca9e9308519a22ec40cbb`
- `SCORE_BASE`：`3eece3dcba402ab1ede11fcf030a5cb6d1596258`
- 原始显示分：`97.5`
- 控制结果：24 个 PASS，唯一失败为 `P03-feedback-budget`
- 原始最终门禁：`FAIL`，原因是性能维度 `75.0 < 80` 且 P03 未 PASS
- 原始报告：`docs/plans/evidence/frontend-maintainability-95/03bd70beaf1763e876119fdc4b43dd41a9ad3b10.json`
- 原始报告 SHA-256：`bfbe80767798ca1935298764fecfd6281da68f86d215f650df792a929c93067e`
- P03 冻结 median：`0.20801855555570606 ms`
- P03 最大回归比：`1.15`
- P03 通过阈值：`0.23922133888906194 ms`

第一次评分的负载入口取得了两次可比样本，但测量期间同机负载随后显著上升，因此不能把单次 P03 FAIL 直接归因为本轮应用壳或响应校验器拆分。

第二次评分针对隔离性能候选启动后，负载窗口持续不可比，连续可比样本始终为 0；按本轮裁决主动终止，进程退出码为 130，未进入性能测量。

## 3. 隔离候选，不进入本轮集成

隔离分支：`codex/frontend-95-p03-fast-pending-20260721`

候选 HEAD：`035f1686dff25842bb1945f8f64bb20cdf2575db`

候选只优化立即完成的 `thread.interrupt` 快速路径：RPC 经一个 microtask 仍未 settled 时才创建 0ms pending timer，避免立即 resolve/reject 分配随后立即清理的 timer 和模块级 `WeakMap` 状态。15 秒超时、严格成功响应校验、慢请求 pending、confirmed/unconfirmed 可见反馈均保留。

候选已取得以下非性能证据：

- 两个变更文件 LSP diagnostics 无项；
- 定向测试 4/4 文件、67/67 测试通过；
- 工作树 `git diff --check` 通过。

版本化验证回执：`docs/plans/evidence/frontend-maintainability-95/p03-isolated-candidate-receipt.md`。

由于没有取得可比 P03 PASS，候选不得合并到本轮集成分支。

## 4. 后续偿还条件

仅在主机没有并行重任务、Node/npm/Go/OS/CPU/内存与冻结基线一致时继续：

1. 从最新 `main` 建立清洁集成工作树，并确认冻结 `SCORE_BASE` 和治理路径未漂移。
2. 使用 Node `v20.20.0`、npm `10.8.2` 和 Go `1.26.5`。
3. 要求评分器负载窗口连续两次满足三个 load average 与冻结值的差值均不超过 `4.5`。
4. 先在未优化集成提交上重复 P03；若稳定 PASS，关闭“疑似代码回归”，保留环境波动记录。
5. 若未优化提交在可比环境下重复 FAIL，再复核并评估隔离候选；不得删除确认校验、15 秒超时或用户可见 pending 语义来换取分数。
6. 候选只有在 P03 PASS、完整 `npm run lint`、`npm test`、`npm run build` 和冻结 `FINAL_GATE=PASS` 全部绑定同一提交后才可合并。

## 5. 关闭标准

以下条件全部满足后才可将本债务标记为 CLOSED：

- 同一提交上的 P03 原始 runner 报告为 PASS；
- 完整冻结评分器 `FINAL_GATE=PASS`；
- 报告绑定准确的 subject SHA/tree 与 `SCORE_BASE` SHA/tree；
- 集成工作树清洁，最新 `main` 是该提交祖先；
- 不存在通过修改 scorer、baseline、阈值或治理文件取得的假绿。
