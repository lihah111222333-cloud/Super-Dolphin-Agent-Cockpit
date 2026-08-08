package remoteci

import "testing"

func TestWorkerExecutionRootsAreCanonical(t *testing.T) {
	if err := validateWorkerExecutionRoots(workerExecutionRoots); err != nil {
		t.Fatal(err)
	}
}

// TestWorkerExecutionCommandClassifierSeparatesPackagePath 拒绝把 Go 包路径误判为 Gate 自执行命令。
func TestWorkerExecutionCommandClassifierSeparatesPackagePath(t *testing.T) {
	if workerExecutionLooksLikeCommand([]string{"cmd/super-dolphin-gate"}) {
		t.Fatal("Go package path was classified as a super-dolphin-gate self-command")
	}
	if !workerExecutionLooksLikeCommand([]string{"super-dolphin-gate", "worker"}) {
		t.Fatal("actual super-dolphin-gate self-command was not classified")
	}
}

func TestWorkerExecutionMakefileSelectsOnlyTargetClosure(t *testing.T) {
	makefile := parseWorkerExecutionMakefile([]byte(`TOOL := ./scripts/worker.sh

worker: prerequisite
	$(TOOL)

prerequisite:
	go run ./scripts/helper.go

unrelated:
	echo unrelated
`))
	target, ok := makefile.targets["worker"]
	if !ok {
		t.Fatal("worker target was not parsed")
	}
	if len(target.dependencies) != 1 || target.dependencies[0] != "prerequisite" {
		t.Fatalf("worker dependencies = %v", target.dependencies)
	}
	if _, ok := makefile.targets["unrelated"]; !ok {
		t.Fatal("unrelated target was not parsed independently")
	}
	variable, ok := makefile.variables["TOOL"]
	if !ok || variable.value != "./scripts/worker.sh" {
		t.Fatalf("TOOL variable = %#v, found=%t", variable, ok)
	}
}

func TestWorkerExecutionClosureSkipsAbsentOptionalFunctionNodes(t *testing.T) {
	tests := []struct {
		name             string
		source           string
		symbol           string
		wantSignatureNil bool
		wantBodyNil      bool
	}{
		{
			name:             "missing results",
			source:           "package fixture\nfunc noResults() {}\n",
			symbol:           "noResults",
			wantSignatureNil: true,
		},
		{
			name:        "missing body",
			source:      "package fixture\nfunc declarationOnly() error\n",
			symbol:      "declarationOnly",
			wantBodyNil: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := &remoteGitTreeSnapshot{
				goSources: map[string][]byte{"fixture.go": []byte(test.source)},
			}
			index := snapshot.buildWorkerExecutionGoIndex()
			unit, err := index.resolveRoot(workerExecutionRoot{
				directory: ".",
				symbol:    test.symbol,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := unit.signature == nil; got != test.wantSignatureNil {
				t.Fatalf("signature nil = %t, want %t", got, test.wantSignatureNil)
			}
			if got := unit.dependencies == nil; got != test.wantBodyNil {
				t.Fatalf("dependencies nil = %t, want %t", got, test.wantBodyNil)
			}

			closure := newWorkerExecutionGoClosure(index)
			closure.enqueue(unit)
			if err := closure.resolve(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkerExecutionDigestIncludesLocalReplacementModuleMetadata(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{
		moduleMappings: []remoteGoModuleMapping{{importPath: "example.invalid/replaced", directory: "internal/replaced"}},
		byPath: map[string]remoteGitTreeEntry{
			"internal/replaced/go.mod": {mode: "100644", kind: "blob", objectID: "go-mod-v1", path: "internal/replaced/go.mod"},
			"internal/replaced/go.sum": {mode: "100644", kind: "blob", objectID: "go-sum-v1", path: "internal/replaced/go.sum"},
		},
	}
	assets := &workerExecutionAssets{snapshot: snapshot, entries: make(map[string]remoteGitTreeEntry)}
	if err := assets.addLocalGoModuleMetadata(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"internal/replaced/go.mod", "internal/replaced/go.sum"} {
		if _, ok := assets.entries[path]; !ok {
			t.Fatalf("worker execution assets omitted local module metadata %q: %#v", path, assets.entries)
		}
	}
	closure := &workerExecutionGoClosure{selected: map[string]*workerExecutionGoUnit{}, usedImports: map[string]map[string]struct{}{}}
	first, err := digestWorkerExecutionClosure(closure, assets)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.byPath["internal/replaced/go.mod"] = remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: "go-mod-v2", path: "internal/replaced/go.mod"}
	assets.entries = make(map[string]remoteGitTreeEntry)
	if err := assets.addLocalGoModuleMetadata(); err != nil {
		t.Fatal(err)
	}
	second, err := digestWorkerExecutionClosure(closure, assets)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("local replacement go.mod metadata change did not change worker execution digest: %q", first)
	}
}
