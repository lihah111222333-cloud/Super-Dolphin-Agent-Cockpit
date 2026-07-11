package fixture

type policy interface {
	apply()
}

type localPolicy struct{}

func parseImportFiles(root string, targets ...string) []string {
	return append([]string{root}, targets...)
}

func scanStore(root string) {
	_ = parseImportFiles(root, "internal/store")
}

func (localPolicy) apply() {
	_ = "internal/store/sqlc"
}

func exercise() {
	_ = "internal/platform/config"
	scanStore("repo")
	var selected policy = localPolicy{}
	selected.apply()
}
