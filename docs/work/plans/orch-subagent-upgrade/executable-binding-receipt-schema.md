# S0 executable binding / receipt（planning-only）

Gate S0 目前已经具备三件事：十四份可检查的子规格摘要、`release-gate-dag.json` 的机器依赖图，以及 B01–B18 阻断台账。它们仍然是规划输入，不是执行器或授权 owner。为避免把“有路径和行号”误读成“已经执行”，本文件定义一个最小的、不可授权的 executable binding receipt 形状，供后续证据运行器实现前进行字段评审。

## 为什么不在 S0 启用授权

S0 没有受信的 runner 身份、provider process receipt、跨进程 fencing、恢复域外 custody、签名 trust root 或发布决策 owner。`observed` 只能表示某个外部 runner 报告了观察结果；它不能变成 GREEN attestation、capability lease、runtime grant 或 `ENABLE`。因此 receipt 必须同时固定 `authority=planning_only`、`release_decision=NO-GO`，并以 `granted=false`、`runtime_effect=none`、`capability_lease=null` 形成不可升级的负授权断言。

## 最小 schema（v1）

```json
{
  "schema_version": 1,
  "authority": "planning_only",
  "release_decision": "NO-GO",
  "binding": {
    "token": "<S0 acceptance token>",
    "spec_id": "<DAG spec node>",
    "depends_on": ["<ordinary incoming DAG node ids>"],
    "dependency_digest": "<sha256(canonical sorted dependency array)>",
    "source": {
      "path": "<workspace-relative proposal/test path>",
      "symbol": "<token>",
      "line": 1,
      "sha256": "<sha256(current source bytes)>"
    }
  },
  "execution": {
    "status": "planned",
    "runner_path": "<workspace-relative runner/fixture path>",
    "command_digest": "<sha256(argv and relevant runner contract)>"
  },
  "authorization": {
    "granted": false,
    "runtime_effect": "none",
    "capability_lease": null
  }
}
```

The command itself and credentials are never serialized; only a non-zero digest is retained. `status` is restricted to `planned`, `observed`, or `unverified`. The bounded guard in `scripts/orch_s0_executable_receipt.py` resolves the token through `acceptance-inventory.json`, resolves `spec_id` and incoming dependencies through `release-gate-dag.json`, checks source path/symbol/line and source digest, and rejects any authorization promotion. It performs no process launch, network call, database write, grant issuance, or release decision.

This shape is intentionally not added to either S0 machine input. The existing inventory and DAG remain the only writable planning inputs, and the receipt guard is an additional read-only consistency check. A future executable evidence runner may consume this shape only after VERIFY/ROLLOUT owners define signed process receipts, custody and recovery semantics; until then every result remains `LOCAL_NON_AUTHORITATIVE NOT_VERIFIED` and all gates remain `NO-GO`.
