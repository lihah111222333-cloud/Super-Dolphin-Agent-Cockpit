# Round 031 - 精修优先级排序与分组

## 目的

将 round-002 ~ round-030 的所有 findings 按精修优先级分组，为 12-agent 精修阶段做准备。

## P0：必须立即修复（blocker，可能导致 panic 或数据损坏）

| # | 文件 | 问题 | 精修复杂度 |
|---|------|------|-----------|
| 1 | platform/db/module.go:306 | Scan error 跳过 | 低 |
| 2 | skill/skills_meta.go:169 | 越界索引 | 低 |
| 3 | prompt/user_context_builder.go:258 | 无 bounds check | 低 |
| 4 | eventsurface/bind.go:83 | 静默返回 nil | 中（签名变更） |
| 5 | uistate/module.go:117 | RPC error 丢弃 | 低 |
| 6 | skill/scope_model.go:224 | unchecked type assert | 低 |
| 7 | turn/tool_result_lifecycle.go:56 | nil cfg panic | 低 |

## P1：必须修复（major，静默降级或数据丢失）

| # | 文件 | 问题 | 精修复杂度 |
|---|------|------|-----------|
| 8 | skill/mirror_manifest.go:412 | 吞 resolveOwnerIdentity | 中 |
| 9 | skill/skills_fs.go:300,:422 | requireCWD 丢弃 | 低 |
| 10 | turn/skills.go:323 | 丢弃 conflict error | 低（删 dead code） |
| 11 | app/thread_orchestration_adapter.go:23 | noop facade | 中 |
| 12 | bus/sink.go:23 | 空 sink | 中（签名变更） |
| 13 | threadprompt/runtime_catalog.go:183 | 逻辑反转 | 低 |
| 14 | codexapp/support.go:35 | mustJSON 吞 error | 中（级联） |
| 15 | skill/service.go:92 | hash 空 marshal | 低 |
| 16 | memory/service.go:369 | merge write 吞 error | 低 |
| 17 | prompt/cache.go:18 | cache key marshal | 中 |
| 18 | insight/flusher.go:182 | DB 持久化 nil | 低 |
| 19 | statemachine/factory.go:73 | AllowedTriggers 吞 error | 中 |
| 20 | dashboard/dag_snapshot.go:448 | time parse 吞 error | 低 |

## P2：应修复（moderate，静默但不致命）

约 30 条，见 round-025~028 模式归纳。建议用 archtest 守卫批量拦截。

## 12-Agent 精修分配建议

| Agent | 负责范围 | 预计文件数 |
|-------|----------|-----------|
| A1 | P0 #1-3（低复杂度 blocker） | 3 |
| A2 | P0 #4-5（签名变更 blocker） | 2+caller |
| A3 | P0 #6-7 + P1 #10（低复杂度） | 3 |
| A4 | P1 #8-9（skill mirror/fs） | 3 |
| A5 | P1 #11-12（app adapter + bus） | 4 |
| A6 | P1 #13-14（threadprompt + codexapp） | 4 |
| A7 | P1 #15-16（skill hash + memory） | 2 |
| A8 | P1 #17-18（prompt cache + insight） | 2 |
| A9 | P1 #19-20（statemachine + dashboard） | 2 |
| A10 | P2 模式：json.Marshal 全局 | 8 |
| A11 | P2 模式：unchecked type assert 全局 | 12 |
| A12 | P2 模式：nil-receiver guard 全局 | 15 |
