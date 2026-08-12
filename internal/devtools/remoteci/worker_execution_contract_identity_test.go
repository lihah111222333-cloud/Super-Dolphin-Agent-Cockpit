package remoteci

import "testing"

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

func workerExecutionPositionFixtureDigest(t *testing.T, source []byte) string {
	t.Helper()
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{"fixture.go": source}}
	index := snapshot.buildWorkerExecutionGoIndex()
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
