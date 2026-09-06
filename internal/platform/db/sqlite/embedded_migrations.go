package sqlite

import (
	"context"
	"database/sql"
	"embed"
)

// embeddedMigrations 是随 sqlite 包分发的规范迁移资源，供脱离仓库源码的二进制使用。
//
//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// RunEmbeddedMigrations 使用随二进制嵌入的规范迁移执行 SQLite schema 升级。
func RunEmbeddedMigrations(ctx context.Context, db *sql.DB) error {
	return runMigrations(ctx, db, migrationSource{
		filesystem:  embeddedMigrations,
		root:        "migrations",
		displayPath: "embedded SQLite migrations",
	})
}
