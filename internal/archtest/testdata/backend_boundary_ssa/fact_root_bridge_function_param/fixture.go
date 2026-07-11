package fixture

func parseImportFiles(root string, targets ...string) []string {
	return append([]string{root}, targets...)
}

func scanStore(root string) {
	_ = parseImportFiles(root, "internal/store")
}

func sqlcPolicyFact() {
	_ = "internal/store/sqlc"
}

func invokeBridge(next func()) {
	next()
}

func factBearingRoot() {
	scanStore("repo")
	_ = "internal/platform/config"
	invokeBridge(sqlcPolicyFact)
}

func exercise() {
	factBearingRoot()
}
