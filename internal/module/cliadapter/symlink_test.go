package cliadapter

import "testing"

func TestSetupWorkspaceSkills_EmptyArgsError(t *testing.T) {
	if err := SetupWorkspaceSkills("", "/tmp/cache"); err == nil {
		t.Error("empty workspaceDir should error")
	}
	if err := SetupWorkspaceSkills("/tmp/ws", ""); err == nil {
		t.Error("empty cacheDir should error")
	}
}
