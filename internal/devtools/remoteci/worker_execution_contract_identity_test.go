package remoteci

import (
	"strings"
	"testing"
)

func TestWorkerExecutionDigestIgnoresUnrelatedDeclarationPositionShift(t *testing.T) {
	base := workerExecutionPositionFixture("")
	shifted := workerExecutionPositionFixture("func inserted() {}\n")
	changed := workerExecutionPositionFixture("", "changed")
	baseDigest := workerExecutionPositionFixtureDigest(t, base)
	if shiftedDigest := workerExecutionPositionFixtureDigest(t, shifted); shiftedDigest != baseDigest {
		t.Fatalf("unrelated declaration position shift changed worker digest: %q != %q", shiftedDigest, baseDigest)
	}
	if changedDigest := workerExecutionPositionFixtureDigest(t, changed); changedDigest == baseDigest {
		t.Fatal("reachable declaration change was omitted from worker digest")
	}
}

func TestWorkerExecutionDigestScopesGroupedExplicitConstants(t *testing.T) {
	base := workerExecutionGroupedConstFixture(`used = "stable"`, `unrelated = "before"`)
	unrelated := workerExecutionGroupedConstFixture(`used = "stable"`, `unrelated = "after"`)
	semantic := workerExecutionGroupedConstFixture(`used = "changed"`, `unrelated = "before"`)
	baseDigest := workerExecutionPositionFixtureDigest(t, base)
	if unrelatedDigest := workerExecutionPositionFixtureDigest(t, unrelated); unrelatedDigest != baseDigest {
		t.Fatalf("unrelated grouped constant changed worker digest: %q != %q", unrelatedDigest, baseDigest)
	}
	if semanticDigest := workerExecutionPositionFixtureDigest(t, semantic); semanticDigest == baseDigest {
		t.Fatal("reachable grouped constant change was omitted from worker digest")
	}
	if workerExecutionPositionFixturePreviousGroupedDigest(t, base) == workerExecutionPositionFixturePreviousGroupedDigest(t, unrelated) {
		t.Fatal("historical grouped-declaration digest did not preserve the previous broad identity")
	}
}

func TestWorkerExecutionDigestTracksGroupedConstDependenciesAndIota(t *testing.T) {
	dependencyBase := workerExecutionGroupedConstFixture(`base = "one"`, `used = base`)
	dependencyChanged := workerExecutionGroupedConstFixture(`base = "two"`, `used = base`)
	if workerExecutionPositionFixtureDigest(t, dependencyBase) == workerExecutionPositionFixtureDigest(t, dependencyChanged) {
		t.Fatal("referenced grouped constant change was omitted from worker digest")
	}
	iotaBase := workerExecutionGroupedConstFixture("padding = iota", "used")
	iotaShifted := workerExecutionGroupedConstFixture("padding = iota", "inserted", "used")
	if workerExecutionPositionFixtureDigest(t, iotaBase) == workerExecutionPositionFixtureDigest(t, iotaShifted) {
		t.Fatal("grouped iota ordinal change was omitted from worker digest")
	}
}

func TestWorkerExecutionStableUnitKeysKeepDuplicateInitializersDistinct(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte("package fixture\nfunc init() {}\nfunc init() {}\n"),
	}}
	units := snapshot.buildWorkerExecutionGoIndex().initializers["."]
	if len(units) != 2 || units[0].key == units[1].key {
		t.Fatalf("duplicate initializer keys are not distinct: %#v", units)
	}
}

func workerExecutionPositionFixture(prefix string, body ...string) []byte {
	usedBody := "stable"
	if len(body) > 0 {
		usedBody = body[0]
	}
	return []byte("package fixture\nimport \"fmt\"\n" + prefix +
		"func root() { used() }\n" +
		"func used() { fmt.Println(\"" + usedBody + "\") }\n")
}

func workerExecutionGroupedConstFixture(specs ...string) []byte {
	return []byte("package fixture\nimport \"fmt\"\nconst (\n" +
		strings.Join(specs, "\n") +
		"\n)\nfunc root() { fmt.Println(used) }\n")
}

func workerExecutionPositionFixtureDigest(t *testing.T, source []byte) string {
	return workerExecutionPositionFixtureDigestWithMode(t, source, false)
}

func workerExecutionPositionFixturePreviousGroupedDigest(t *testing.T, source []byte) string {
	return workerExecutionPositionFixtureDigestWithMode(t, source, true)
}

func workerExecutionPositionFixtureDigestWithMode(t *testing.T, source []byte, previousGroupedDeclaration bool) string {
	t.Helper()
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{"fixture.go": source}}
	index := snapshot.buildWorkerExecutionGoIndex()
	if previousGroupedDeclaration {
		index = snapshot.buildWorkerExecutionGoIndexPreviousGroupedDeclaration()
	}
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatal(err)
	}
	assets := &workerExecutionAssets{entries: map[string]remoteGitTreeEntry{}, fragments: map[string]workerExecutionFragment{}}
	digest, err := digestWorkerExecutionClosure(closure, assets)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
