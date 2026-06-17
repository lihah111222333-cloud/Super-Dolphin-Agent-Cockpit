package builtinprompts

import (
	"io/fs"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmbeddedPromptAssetsDoNotTeachLegacyOrchestrationToolNames(t *testing.T) {
	t.Parallel()

	forbidden := []string{"orchestration_launch_agent", "orchestration_get_agent_report"}
	err := fs.WalkDir(embeddedAssets, "assets", func(assetPath string, entry fs.DirEntry, err error) error {
		require.NoError(t, err)
		if entry.IsDir() {
			return nil
		}
		switch path.Ext(assetPath) {
		case ".md", ".json":
		default:
			return nil
		}
		body, readErr := embeddedAssets.ReadFile(assetPath)
		require.NoError(t, readErr)
		for _, name := range forbidden {
			require.NotContains(t, string(body), name, assetPath)
		}
		return nil
	})
	require.NoError(t, err)
}
