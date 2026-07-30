package gate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSQLCVerifyRealGenerationGreenAndRed(t *testing.T) {
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}

	t.Run("green", func(t *testing.T) {
		source := newExecutorGitSnapshot(t, map[string]string{
			"internal/store/sqlc/generated.go":     "package sqlc\n",
			"cmd/mcp-orch/store/sqlc/generated.go": "package sqlc\n",
			"scripts/sqlc_postprocess.sh":          "#!/bin/sh\nexit 0\n",
		})
		sqlcBinary := filepath.Join(realTempDir(t), "sqlc")
		writeTestFile(t, sqlcBinary, "#!/bin/sh\nexit 0\n", 0o700)
		if err := runSQLCVerify(context.Background(), gitBinary, sqlcBinary, "/bin/bash", source, os.Environ(), ioDiscard{}, ioDiscard{}); err != nil {
			t.Fatalf("runSQLCVerify clean generation: %v", err)
		}
	})

	t.Run("red", func(t *testing.T) {
		source := newExecutorGitSnapshot(t, map[string]string{
			"internal/store/sqlc/generated.go":     "package sqlc\n",
			"cmd/mcp-orch/store/sqlc/generated.go": "package sqlc\n",
			"scripts/sqlc_postprocess.sh":          "#!/bin/sh\nexit 0\n",
		})
		sqlcBinary := filepath.Join(realTempDir(t), "sqlc")
		writeTestFile(t, sqlcBinary, "#!/bin/sh\nprintf '// drift\\n' >> internal/store/sqlc/generated.go\n", 0o700)
		err := runSQLCVerify(context.Background(), gitBinary, sqlcBinary, "/bin/bash", source, os.Environ(), ioDiscard{}, ioDiscard{})
		if err == nil || !strings.Contains(err.Error(), "generated output differs") {
			t.Fatalf("runSQLCVerify drift error = %v", err)
		}
	})

	t.Run("postprocess", func(t *testing.T) {
		source := newExecutorGitSnapshot(t, map[string]string{
			"internal/store/sqlc/generated.go":     "package sqlc\n\nvar Value any\n",
			"cmd/mcp-orch/store/sqlc/generated.go": "package sqlc\n",
			"scripts/sqlc_postprocess.sh":          "#!/bin/sh\nprintf 'package sqlc\\n\\nvar Value any\\n' > internal/store/sqlc/generated.go\n",
		})
		sqlcBinary := filepath.Join(realTempDir(t), "sqlc")
		writeTestFile(t, sqlcBinary, "#!/bin/sh\nprintf 'package sqlc\\n\\nvar Value interface{}\\n' > internal/store/sqlc/generated.go\n", 0o700)
		if err := runSQLCVerify(context.Background(), gitBinary, sqlcBinary, "/bin/bash", source, os.Environ(), ioDiscard{}, ioDiscard{}); err != nil {
			t.Fatalf("runSQLCVerify canonical postprocess: %v", err)
		}
	})
}
