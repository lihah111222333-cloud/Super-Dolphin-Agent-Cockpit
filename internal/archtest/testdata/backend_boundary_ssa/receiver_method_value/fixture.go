package fixture

type policyFacts struct{}

func parseImportFiles(root string, targets ...string) []string {
	return append([]string{root}, targets...)
}

func scanStore(root string) {
	_ = parseImportFiles(root, "internal/store")
}

func (policyFacts) collect() {
	_ = "internal/store/sqlc"
}

func exercise() {
	_ = "internal/platform/config"
	scanStore("repo")
	invoke := policyFacts{}.collect
	invoke()
}
