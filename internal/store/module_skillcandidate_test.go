package store

import (
	"os"
	"strings"
	"testing"
)

func TestStoreModuleDoesNotWireSkillCandidateStore(t *testing.T) {
	raw, err := os.ReadFile("module.go")
	if err != nil {
		t.Fatalf("read module.go: %v", err)
	}
	src := string(raw)
	for _, forbidden := range []string{
		`internal/store/skillcandidate`,
		`skillcandidate.Module`,
	} {
		if strings.Contains(src, forbidden) {
			t.Fatalf("store.Module still wires dormant skillcandidate backend via %q", forbidden)
		}
	}
}
