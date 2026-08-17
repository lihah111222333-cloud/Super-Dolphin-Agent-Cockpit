package remoteci

import "testing"

func TestWorkerExecutionImportedLocalTypeAliasMethodResolves(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{
		moduleMappings: []remoteGoModuleMapping{{importPath: "example.invalid/fixture", directory: "."}},
		goSources: map[string][]byte{
			"fixture.go": []byte(`package fixture

import "example.invalid/fixture/owner"

type Cache = owner.Cache
func root(cache Cache) { cache.Validate() }
`),
			"owner/cache.go": []byte(`package owner

type Cache struct{}
func (Cache) Validate() { validateDependency() }
func validateDependency() {}
`),
		},
	}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("imported local type alias method was not resolved precisely: %v", err)
	}
}
