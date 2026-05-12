# 长 result e2e 测试 — claude CLI 截断探针

> 关联：ADR-015 v4.1 §2.3 + §2.4 步骤 0
> 文件：`turn_result_long_e2e_test.go`（build tag `e2e_claude`）

## 目的

`internal/provider/claudecli/event_map.go:130` 用 `dataString(raw.Data, "result")`
直读 claude CLI `--output-format stream-json` 输出的 `type=result` 事件的
`result` 字段。**provider 层不截断**，但 claude CLI 二进制对长 result 的真实
行为代码层无法核 —— 需要真启 CLI 拿实证。

本测试即为该实证脚手架：让 agent 输出已知长度的稳定字符串（"ABC" 重复 N 次），
检查 `result` 字段长度是否符合预期。

## 运行命令

```bash
go test -tags=e2e_claude ./internal/provider/claudecli/ \
    -run TestTurnResultLong_NotTruncated -v -count=1
```

也可只跑单档：

```bash
go test -tags=e2e_claude ./internal/provider/claudecli/ \
    -run TestTurnResultLong_NotTruncated_16KB -v -count=1
```

## 环境前提

- `claude` CLI 在 `PATH`（测试启动时若找不到会 `t.Skip`，不会报错）
- 已登录的 claude 会话，**或** `ANTHROPIC_API_KEY` 环境变量已导出
  （取决于本机 claude CLI 版本采用哪条鉴权路径；本测试不主动检查任一变量，
  完全依赖 `claude -p` 自身能否成功调用真实后端）
- 三个测试都会真实消耗 token（3KB / 8KB / 16KB 三档），请确认配额

## 三种可能结果及含义

| 结果 | 含义 | 后续动作 |
|------|------|----------|
| **全 PASS**（3KB/8KB/16KB 都通过） | **情况 A**：claude CLI 不截断长 result，provider 层链路完整 | C2 后续**不需要** provider 代码改动；ADR-015 §2.4 步骤 1 跳过 |
| **部分 FAIL**（例如 16KB 截断、3KB/8KB 通过） | **情况 B**：claude CLI 在某阈值之上截断 `result` 字段 | 需补 provider 累加器，与 W-C1 codex 累加器**接口语义对称**但代码独立实现；进入 ADR-015 §2.4 步骤 1 |
| **全 FAIL** | claude CLI 行为异常 — 可能是 prompt 让 agent 生成长度不够（模型自由发挥），也可能是 CLI 真截断到很小阈值 | 看失败日志里 `head[:200]` / `tail[-200:]` / `gotLen` / `sha256`：<br>• 若 head/tail 都是干净 "ABC" 但 gotLen 离期望差很多 → CLI 截断<br>• 若 head/tail 含模型寒暄 → 调整 prompt 后重跑 |

## 失败诊断字段

测试失败时输出以下信息（参考 `runClaudeLongResultProbe` 内 `t.Logf` / `t.Fatalf`）：

- `copies` — 期望的 "ABC" 拼接次数
- `wantLowerBound` — 期望长度下限（= copies × 3 × 0.9）
- `gotLen` — 实际 `result` 长度
- `sha256` — 实际 `result` 内容哈希（便于人工或后续脚本比对）
- `head=` — `result` 前 200 字节（看是否含模型框架文本）
- `tail=` — `result` 后 200 字节（**看是否中间截断**：若 tail 不是 "...ABC" 结尾或硬切在字符中间，CLI 截断概率很高）

## 后续动作

拿到实测数据后，由 C-A 主 agent 决定情况 A/B/C 分流：

- **A** → 收尾 C2 worker，记录到 ADR-015 §2.4 步骤 0 验证结论
- **B** → 启第二轮 C2.2 worker 补 provider 累加器（接口与 W-C1 codex 累加器对称）
- **C** → 调整 prompt 或调查 CLI 版本，重跑此脚手架

## 范围约束

本测试**只测 CLI 行为**，不动 provider 代码、不动 `event_map.go` / `session_events.go`、
不新建累加器。情况 B 的 provider 改动属于 ADR-015 §2.4 步骤 1，由独立 worker 落地。
