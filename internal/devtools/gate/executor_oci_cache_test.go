package gate

import "testing"

func TestExecutorRemoteGoBuildCacheSeedRootsUsesOCISeedWithoutEnvironmentSelection(t *testing.T) {
	roots, err := ExecutorRemoteGoBuildCacheSeedRoots()
	if err != nil {
		t.Fatalf("ExecutorRemoteGoBuildCacheSeedRoots() error = %v", err)
	}
	if len(roots) != 1 || roots[0] != ExecutorOCIProjectGoBuildCacheSeedRoot {
		t.Fatalf("OCI seed roots = %v", roots)
	}
}
