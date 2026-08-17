package main

import (
	"path/filepath"
)

const sqliteMigrationsRelPath = "internal/platform/db/sqlite/migrations"

func sqliteMigrationsPath(root string, elems ...string) string {
	parts := append([]string{root, filepath.FromSlash(sqliteMigrationsRelPath)}, elems...)
	return filepath.Join(parts...)
}
