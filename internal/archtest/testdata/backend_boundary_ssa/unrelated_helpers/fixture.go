package fixture

func parseImportFiles(root string, targets ...string) []string {
	return append([]string{root}, targets...)
}

func scanStore(root string) {
	_ = parseImportFiles(root, "internal/store")
}

func configFact() {
	_ = "internal/platform/config"
}

func sqlcFact() {
	_ = "internal/store/sqlc"
}
