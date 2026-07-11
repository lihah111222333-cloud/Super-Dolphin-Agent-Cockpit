package fixture

func parseImportFiles(root string, targets ...string) []string {
	return append([]string{root}, targets...)
}

func scanStore(root string) {
	_ = parseImportFiles(root, "internal/store")
}

func storePolicyFacts() {
	_ = "internal/store/sqlc"
}

func returnPolicyClosure() func() {
	invoke := storePolicyFacts
	return func() { invoke() }
}

func exercise() {
	_ = "internal/platform/config"
	scanStore("repo")
	returnPolicyClosure()()
}
