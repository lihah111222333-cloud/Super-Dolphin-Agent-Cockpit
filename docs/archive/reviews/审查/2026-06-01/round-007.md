# Round 007 - 第二梯队：memory 模块兜底

## 来源

Round-002 扫雷 agent 报告：module/memory 5 条 findings。

## Findings 确认

### 1. [major] memory/service.go:369 — mergeAndWriteMemory 错误静默吞掉

**证据**：agent 报告 line 369 merge 写入失败后不返回 error。
**影响**：memory 合并写入失败时，调用方以为成功，用户的 memory 数据丢失但无感知。
**精修**：error 上抛给 caller。

### 2. [moderate] memory/service.go:232 — loadConsolidationStamp 错误吞掉返回空字符串

**证据**：读取 consolidation stamp 文件失败时返回 `""`。
**影响**：consolidation 逻辑以为"从未 consolidate 过"，可能触发不必要的重复 consolidation。
**精修**：区分"文件不存在"（合法空）和"读取失败"（error）。

### 3. [moderate] memory/auto_dream_task.go:298 — consolidation error 只 log 不上报

**证据**：consolidation 失败后 log.Warn 但 caller 不知道。
**影响**：自动 dream task 以为 consolidation 成功，继续后续步骤。
**精修**：返回 error，让 dream task 标记为 failed。

### 4. [moderate] memory/entrypoint_provider.go:86 — SafeReadEntrypoint 错误丢弃

**证据**：`SafeReadEntrypoint` 名字暗示"安全读取"，但实际是吞错误。
**影响**：entrypoint 文件损坏时返回零值，prompt 组装拿到空 entrypoint。
**精修**：改名为 `ReadEntrypoint`，返回 error；或保留 Safe 语义但 log.Error。

### 5. [moderate] memory/service.go:348 — dedup Check error 后继续写入

**证据**：去重检查失败后 log 但继续执行写入。
**影响**：去重失败 → 可能写入重复 memory entry。
**精修**：dedup 失败应 abort 写入，返回 error。
