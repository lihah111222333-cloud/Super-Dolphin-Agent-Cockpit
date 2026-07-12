---
name: 字段守卫
description: 当新增、删除、重命名或跨层传递结构化字段，以及修改 DTO、mapper、schema、API/RPC、事件、配置、store、序列化或前后端契约时自动使用；追踪生产源到全部消费端并建立 fail-fast 字段守卫。
trigger_words: [字段变更, 新增字段, 删除字段, 重命名字段, 字段映射, DTO 变更, mapper, schema 变更, contract drift, RPC 字段, API 字段, event payload, store DTO, config schema]
force_words: [字段守卫, 自守卫, field guard, field mapping, 字段映射, 字段透传, 契约漂移]
---

# 字段守卫

以本次变更符号为根，对“生产字段 -> 转换/传输 -> 消费字段”逐层发现到明确终止边界。生产字段改变后，只要已发现的任一消费端未同步登记，至少一个自动化检查必须失败；未完成边界扩展时不得声称全仓覆盖。

## 当前仓库事实

- canonical skill 位于 `<cwd>/.agents/skills`；provider mirror 是派生物。
- `trigger_words` / `force_words` 由 parser 读取，但当前 `MatchPreview` 只把它们作为 UI 预览辅助信号。
- Go 现有范式见 `internal/archtest/metric_registry_test.go`、`wire_dto_field_registry_test.go` 和 taskdag store 测试：反射派生生产集合，显式 registry 登记消费，并检查 missing、stale 与豁免原因。
- 前端入口是 `frontend-app/scripts/frontend-contract-store-guard.mjs`、`tsconfig.contracts.json`、RPC audit 与 Vitest；不得引用不存在的旧 `scripts/frontend_code_guard.mjs`。
- 仓库当前没有一个能自动证明全仓全部字段链路完整的 scanner。

## 必做闭环

1. **确认生产真值源**：语言类型、协议、schema、配置或 manifest。生产集合必须由反射、AST、类型系统、schema parser 或生成器动态枚举；禁止把字段名复制成手工数组。
2. **发现消费链**：用 LSP 定义、引用、调用层级和序列化边界追踪 mapper、DTO、SQL、store、RPC/API/event、生成类型与 UI。不得仅搜索同名字段。
3. **计算差集**：

```text
missing = dynamically_enumerated_producer_fields
          - dynamically_derived_consumer_coverage
          - reasoned_exemptions
```

消费侧允许显式 registry，但 registry 不是实现真值；必须用 mapper AST、类型约束、roundtrip/one-hot 测试、schema、snapshot 或调用链验证 mapped 条目，并反查 stale。若只能证明登记，结论必须写“登记覆盖已验证、实现覆盖未验证”。

4. **建立守卫**：未知字段、解析失败、无法枚举、missing、stale 或无效豁免必须报错阻断，禁止返回空集合或跳过。双向 mapper 分方向验证；map/slice/pointer 按需验证深拷贝。
5. **管理豁免**：每项包含 `Field | Direction | Reason | Evidence/Owner`；空原因、“暂时不用”“以后再加”均无效。
6. **fail-first**：临时移除真实登记或 mapper 分支，记录失败命令和准确缺口；恢复后记录通过命令。没有 fail-first 证据不得声称守卫有效。
7. **接入门禁**：守卫进入对应测试、CI 或 pre-push。前端 contract 变更至少运行 `cd frontend-app && npm run lint && npm test && npm run build`。

## 输出

输出链路、动态差集、守卫/豁免设计、fail-first 与恢复后的证据。导航中断、生成 owner 不明或 diagnostics 不可得时记录 blocker，不得写成 PASS。禁止用默认值、空结构、吞错、旧字段兼容、降低 baseline、重录 snapshot、删测试或缩小扫描范围掩盖漂移。
