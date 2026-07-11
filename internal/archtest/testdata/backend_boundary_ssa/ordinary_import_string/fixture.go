package fixture

func parseImportFiles(root string, targets ...string) []string {
	return append([]string{root}, targets...)
}

func exercise() {
	_ = parseImportFiles("repo", "internal/store")
	_ = "internal/platform/config"
}
