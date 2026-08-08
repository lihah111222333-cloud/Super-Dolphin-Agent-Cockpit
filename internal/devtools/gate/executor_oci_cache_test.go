package gate

import "testing"

func TestExecutorRemoteGoBuildCacheSeedRootUsesOCISeedWithoutEnvironmentSelection(t *testing.T) {
	seedRoot, err := ExecutorRemoteGoBuildCacheSeedRoot()
	if err != nil {
		t.Fatalf("ExecutorRemoteGoBuildCacheSeedRoot() error = %v", err)
	}
	if seedRoot != ExecutorOCIProjectGoBuildCacheSeedRoot {
		t.Fatalf("OCI seed root = %v", seedRoot)
	}
}
