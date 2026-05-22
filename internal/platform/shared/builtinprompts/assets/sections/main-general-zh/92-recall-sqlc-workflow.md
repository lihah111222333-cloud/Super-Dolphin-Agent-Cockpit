SQLC 工作流：先改 schema/migration，再改 sql/queries/*.sql，最后运行 make sqlc-generate 和 make sqlc-verify。

注意事项：
- `sqlc.yaml` 的 schema 是显式清单；新增影响 sqlc 解析的 migration 后必须把文件加入 schema 列表。
- 不要手改 internal/store/sqlc 生成文件来“修”类型错误；回到源 SQL 或 migration 修正后重新生成。
- store 层新增查询时，同步 contract、querier interface、mapper、fake/stub 和同包测试。
- `make sqlc-verify` 会 regenerate 并比较 generated diff；在未提交工作区里如 generated 文件本身是本任务 diff，可用临时 index 验证源 SQL 与生成物一致。
