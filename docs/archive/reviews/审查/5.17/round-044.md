# Round 044 - 量化引擎潜在风险审查

## 时间

- 开始：2026-05-17 07:39:21 KST
- 结束：2026-05-17 07:47:35 KST
- 说明：按用户最新指示，本轮不等待满 30 分钟，完成审查后直接写入并启动下一轮。

## 本轮范围

本轮审查 sharedfile 的磁盘 source、DB 索引、路径策略、agent 工具入口和 DAG subscriber 输出物化，重点看量化任务输出文件是否会丢失、复用旧内容或产生不可见状态。

- `cmd/mcp-orch/store/sharedfile/store.go`
- `internal/store/sharedfile/store.go`
- `internal/store/sharedfile/disk_integration_test.go`
- `internal/platform/sharedfilefs/disk.go`
- `internal/platform/sharedfilepath/policy.go`
- `cmd/mcp-orch/orchestration/sharedfile_adapter.go`
- `cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go`
- `cmd/mcp-orch/tools/shared_file_tools.go`
- `cmd/mcp-orch/tools/registry_tools.go`

## Findings

1. **[major] sharedfile Upsert 先写磁盘再写 DB，DB 失败会留下不可列出的孤儿输出**
   - 证据：`Upsert()` 先 `writeDiskAndDecideInline()`，随后才 `UpsertSharedFile()`（`cmd/mcp-orch/store/sharedfile/store.go:31-47`；桌面 store 同逻辑在 `internal/store/sharedfile/store.go:75-90`）。`List()` 只查 DB，不扫磁盘（`cmd/mcp-orch/store/sharedfile/store.go:96-108`），测试明确锁定 disk-only 文件不会出现在列表（`internal/store/sharedfile/disk_integration_test.go:184-206`）。
   - 风险：量化输出已经写到 `.agnet/shared`，但 DB upsert 失败后 dashboard/工具列表看不到；后续同路径 preflight 可能又从磁盘读到旧内容，造成结果可见性和状态机分裂。
   - 建议：引入 pending row/事务后补偿，或在 DB upsert 失败时删除刚写入的磁盘文件；启动时做 disk/DB reconciliation。

2. **[major] Delete 先删 DB 再删磁盘，磁盘删除失败会留下不可追踪残留**
   - 证据：`Delete()` 先 `DeleteSharedFile()`，再 `ResolveAbs()` 和 `RemoveDisk()`（`cmd/mcp-orch/store/sharedfile/store.go:111-129`；桌面 store 在 `internal/store/sharedfile/store.go:99-117`）。`RemoveDisk()` 只有非不存在错误才返回失败（`internal/platform/sharedfilefs/disk.go:162-168`）。
   - 风险：如果磁盘权限/IO 失败，DB 行已消失但文件仍在 sandbox；后续路径复用或手工读取时可能误用旧量化结果。
   - 建议：删除改成标记 tombstone 后异步清理，或先安全移动磁盘文件到隔离区再删 DB。

3. **[major] agent 输出物化遇到已存在 sharedfile 会直接复用旧内容**
   - 证据：`materializeSharedfileAfterClaim()` 先 `configuredSharedfileAlreadyExists()`；若存在，直接把 node result 写成该 path，并记录 “preserve existing content”，不写入当前 raw result（`cmd/mcp-orch/orchestration/dag_turn_completed_subscriber.go:300-323`）。
   - 风险：量化 DAG 重跑、重试或路径碰撞时，新一轮 agent 的真实输出可能被旧文件替代，但节点仍进入 done，下游消费过期结果。
   - 建议：sharedfile 路径包含 run_id/attempt，或对已存在文件做内容 hash/owner/run fence；需要复用时显式配置 `reuse_existing`。

4. **[moderate] shared_file_read 只做 traversal/absolute 校验，不做写入前缀白名单或系统保留段限制**
   - 证据：`ValidateReadPath()` 注释说明只做 lexical safety，不做 prefix whitelist（`internal/platform/sharedfilepath/policy.go:92-98`）；`readSharedFile()` 使用该函数后直接 `store.Get()`（`cmd/mcp-orch/tools/shared_file_tools.go:59-76`）。
   - 风险：为兼容旧数据，agent 可读取任意非 traversal 相对路径的 DB 行，包括 `_internal/` 或历史 legacy prefix；若这些行包含系统进度、handoff 或敏感调试信息，读权限比写权限更宽。
   - 建议：读路径也区分 agent/system caller；agent 默认限制在公开 prefix，legacy/internal 需要显式授权。

5. **[moderate] shared_file_list 的 limit 不夹紧，直接传入 SQL LIMIT**
   - 证据：tool handler 把用户传入 `in.Limit` 直接放入 `ListFilter`（`cmd/mcp-orch/tools/registry_tools.go:126-132`），store 继续直接传给 `ListSharedFilesParams.Limit`（`cmd/mcp-orch/store/sharedfile/store.go:96-100`），SQL 使用 `LIMIT $2`（`cmd/mcp-orch/sql/queries/shared_file.sql:15-20`）。
   - 风险：limit=0 会返回空列表，负数/极大值行为依赖数据库；量化 DAG 设计器可能误判“没有文件”或触发大列表查询。
   - 建议：工具入口按统一 `ClampLimit` 设置默认值、上限和最小值。

6. **[moderate] DAG/router 写 sharedfile 的审计身份固定为 `node-router`**
   - 证据：`sharedFileWriterUpdatedBy = "node-router"`，adapter 写入时不带 dag_key/node_key/run_id/thread_id（`cmd/mcp-orch/orchestration/sharedfile_adapter.go:70-95`）。
   - 风险：多个量化节点写同一路径时，sharedfile 行只能看到统一身份，难以追踪是哪次运行覆盖了输出。
   - 建议：SharedFileWriter 端口传入 writer metadata，至少包含 dag_key、node_key、run_id、wakeup_id。

## 误报与已覆盖项

- 写路径会做 whitelist、absolute path 和 traversal 防护（`internal/platform/sharedfilepath/policy.go:61-75`）。
- agent 工具写入额外禁止 `handoff/tasks/` 系统保留段（`internal/platform/sharedfilepath/policy.go:77-90`；`cmd/mcp-orch/tools/shared_file_tools.go:79-98`）。
- 磁盘写使用 tmp + rename，能避免半写文件替换正式文件（`internal/platform/sharedfilefs/disk.go:99-143`）。

## 验证

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/store/sharedfile ./internal/store/sharedfile ./internal/platform/sharedfilefs ./internal/platform/sharedfilepath ./cmd/mcp-orch/orchestration -count=1
```

结果：通过。

## 下一轮建议

- Round 045 审查 shared_file MCP 工具、registry 工具、UI memory sharedfile delete/promote guard 与权限边界。
