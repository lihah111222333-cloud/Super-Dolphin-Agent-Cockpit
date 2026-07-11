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

func makePolicyClosure() func() {
	calls := 0
	return func() {
		calls++
		storePolicyFacts()
	}
}

func exercise() {
	_ = "internal/platform/config"
	scanStore("repo")
	maker := makePolicyClosure
	invoke := maker()
	invoke()
}
