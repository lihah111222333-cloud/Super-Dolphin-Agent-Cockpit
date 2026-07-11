package fixture

type policy interface {
	apply()
}

type reachablePolicy struct{}
type unreachablePolicy struct{}

func parseImportFiles(root string, targets ...string) []string {
	return append([]string{root}, targets...)
}

func scanStore(root string) {
	_ = parseImportFiles(root, "internal/store", "internal", "cmd")
}

func (reachablePolicy) apply() {
	_ = "internal/store/sqlc"
}

func (unreachablePolicy) apply() {
	_ = "go.uber.org/fx"
	_ = "backend-boundary-unreachable-fx-marker"
}

func exercise() {
	_ = "internal/platform/config"
	scanStore("repo")
	var selected policy = reachablePolicy{}
	selected.apply()
}
