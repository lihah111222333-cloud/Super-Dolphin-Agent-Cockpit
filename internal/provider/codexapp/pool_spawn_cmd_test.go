package codexapp

import (
	"context"
	"strings"
	"testing"
)

func TestBuildPoolSpawnCmdRequiresHome(t *testing.T) {
	t.Parallel()
	_, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{})
	if err == nil {
		t.Fatal("empty home should error")
	}
	_, err = BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{Home: "   "})
	if err == nil {
		t.Fatal("whitespace-only home should error")
	}
}

func TestBuildPoolSpawnCmdInjectsCODEXHOME(t *testing.T) {
	t.Parallel()
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home: "/canonical/home",
		ParentEnv: []string{
			"PATH=/usr/bin",
			"HOME=/home/user",
			"CODEX_HOME=/stale/leak", // must be shadowed by the override
			"OPENAI_API_KEY=secret",  // must be dropped
		},
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "CODEX_HOME=/canonical/home") {
		t.Fatalf("canonical CODEX_HOME missing:\n%s", env)
	}
	if strings.Contains(env, "CODEX_HOME=/stale/leak") {
		t.Fatalf("stale CODEX_HOME should be shadowed:\n%s", env)
	}
	if strings.Contains(env, "OPENAI_API_KEY=") {
		t.Fatalf("non-allowlisted env leaked:\n%s", env)
	}
	for _, must := range []string{"PATH=/usr/bin", "HOME=/home/user"} {
		if !strings.Contains(env, must) {
			t.Errorf("allowlisted env %q dropped:\n%s", must, env)
		}
	}
}

func TestBuildPoolSpawnCmdDefaultsParentEnvToOSEnviron(t *testing.T) {
	// Not Parallel: t.Setenv mutates process env and the stdlib
	// testing framework forbids combining it with t.Parallel.
	t.Setenv("OPENAI_API_KEY", "should-be-dropped")
	t.Setenv("TZ", "UTC") // on the allowlist
	cmd, err := BuildPoolSpawnCmd(context.Background(), PoolSpawnArgs{
		Home: "/realpath/home",
		// ParentEnv intentionally nil -> defaults to os.Environ()
	})
	if err != nil {
		t.Fatalf("BuildPoolSpawnCmd error = %v", err)
	}
	env := strings.Join(cmd.Env, "\n")
	if strings.Contains(env, "OPENAI_API_KEY=") {
		t.Fatalf("OS-Environ default should still filter rogue keys:\n%s", env)
	}
	if !strings.Contains(env, "TZ=UTC") {
		t.Fatalf("allowlisted OS-Environ value missing:\n%s", env)
	}
}
