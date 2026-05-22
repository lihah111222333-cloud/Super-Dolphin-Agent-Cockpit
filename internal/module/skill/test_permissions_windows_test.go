//go:build windows

package skill

import "testing"

func makeOwnerOnlyFileBroadForTest(t *testing.T, path string) {
	t.Helper()
	if err := runTestICACLS(path, "/grant", "*S-1-5-11:(R,W)"); err != nil {
		t.Fatalf("grant broad owner policy ACL: %v", err)
	}
}
