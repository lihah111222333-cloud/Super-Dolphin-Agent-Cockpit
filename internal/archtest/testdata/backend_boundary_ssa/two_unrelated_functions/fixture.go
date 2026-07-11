package fixture

func parseImportFiles(root string, targets ...string) []string {
	return append([]string{root}, targets...)
}

func scanStoreWithConfig(root string) {
	_ = parseImportFiles(root, "internal/store")
	_ = "internal/platform/config"
}

func unrelatedSQLCFact() {
	_ = "internal/store/sqlc"
}
