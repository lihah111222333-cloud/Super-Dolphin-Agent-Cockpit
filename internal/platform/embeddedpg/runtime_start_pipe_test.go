package embeddedpg

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestStartDoesNotWaitForPostgresChildStdoutAfterPGCtlStart(t *testing.T) {
	if pgDeps.goos() == "windows" {
		t.Skip("fake pg_ctl script uses /bin/sh")
	}
	cfg, holderPIDPath := newStartWithStdoutHolderConfig(t)
	t.Cleanup(func() {
		raw, err := os.ReadFile(holderPIDPath)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			return
		}
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
		}
	})

	started := time.Now()
	if err := Start(context.Background(), cfg); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Start() waited %s for stdout held by postgres child; pg_ctl start must not use output pipes", elapsed)
	}
}

func newStartWithStdoutHolderConfig(t *testing.T) (contract.EmbeddedPostgresConfig, string) {
	t.Helper()
	temp := t.TempDir()
	holderPIDPath := filepath.Join(temp, "stdout-holder.pid")
	cfg := newInitializedRuntimeConfig(t, temp, `#!/bin/sh
last=""
for arg in "$@"; do last="$arg"; done
if [ "$last" = "status" ]; then
  exit 3
fi
if [ "$last" = "start" ]; then
  (sleep 3) &
  printf '%s\n' "$!" > "$SUPER_DOLPHIN_TEST_HOLDER_PID"
  exit 0
fi
exit 99
`)
	t.Setenv("SUPER_DOLPHIN_TEST_HOLDER_PID", holderPIDPath)
	return cfg, holderPIDPath
}
