package localci

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func canonicalDockerFixture(t *testing.T) (string, string, string) {
	t.Helper()
	seccomp, root, source := dockerFixture(t)
	for _, path := range []string{seccomp, root, source} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		switch path {
		case seccomp:
			seccomp = resolved
		case root:
			root = resolved
		case source:
			source = resolved
		}
	}
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	return seccomp, root, source
}

func calledDockerCommand(calls [][]string, prefix ...string) bool {
	for _, call := range calls {
		if len(call) >= len(prefix) && slices.Equal(call[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}
