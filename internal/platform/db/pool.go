package db

// Pool 类型别名已移除。db 层通过 *sql.DB 提供 SQLite 连接。
// 此文件保留以便 grep 追溯历史；不要重新引入 pgxpool.Pool 依赖。
