package prompt

import (
	"os"
	"testing"
)

func TestNewConfig_SkillProgressiveDisclosureDefaultsTrue(t *testing.T) {
	restoreEnv(t, envEnableSkillProgressiveDisclosure)
	if err := os.Unsetenv(envEnableSkillProgressiveDisclosure); err != nil {
		t.Fatalf("unset %s: %v", envEnableSkillProgressiveDisclosure, err)
	}

	cfg := NewConfig(nil)
	if !cfg.EnableSkillProgressiveDisclosure {
		t.Fatal("EnableSkillProgressiveDisclosure default = false, want true (P25 Phase 4 close)")
	}
}

func TestNewConfig_SkillProgressiveDisclosureRespectsExplicitTrue(t *testing.T) {
	t.Setenv(envEnableSkillProgressiveDisclosure, "true")

	cfg := NewConfig(nil)
	if !cfg.EnableSkillProgressiveDisclosure {
		t.Fatal("EnableSkillProgressiveDisclosure with env=true = false, want true")
	}
}

func TestNewConfig_SkillProgressiveDisclosureRespectsExplicitFalse(t *testing.T) {
	t.Setenv(envEnableSkillProgressiveDisclosure, "false")

	cfg := NewConfig(nil)
	if cfg.EnableSkillProgressiveDisclosure {
		t.Fatal("EnableSkillProgressiveDisclosure with env=false = true, want false")
	}
}

func restoreEnv(t *testing.T, key string) {
	t.Helper()

	old, ok := os.LookupEnv(key)
	t.Cleanup(func() {
		if ok {
			if err := os.Setenv(key, old); err != nil {
				t.Fatalf("restore %s: %v", key, err)
			}
			return
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("restore unset %s: %v", key, err)
		}
	})
}
