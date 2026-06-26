# 交接

## 如何继续

1. 查看 `PARTITIONS/Pxx.files` 确认每个 worker 的授权文件。
2. 查看 worker 最终报告中的 changed files 与验证命令。
3. 用 `git diff --name-only` 检查是否出现跨分区写入。
4. 对所有变更 Go 文件运行单文件守卫。
5. 最后运行 `make guard`。

## 注意

- 不要手改 `internal/store/sqlc` 或 `cmd/mcp-orch/store/sqlc` 的生成文件。
- 不要把 `_test.go` 的普通 `Test...` 函数作为必须机械补注释的对象。
- 不要把 worker 的“完成”当最终完成；父任务必须独立验证。
