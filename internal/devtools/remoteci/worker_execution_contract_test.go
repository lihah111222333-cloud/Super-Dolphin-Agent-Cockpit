package remoteci

import "testing"

func TestWorkerExecutionRootsAreCanonical(t *testing.T) {
	if err := validateWorkerExecutionRoots(workerExecutionRoots); err != nil {
		t.Fatal(err)
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
