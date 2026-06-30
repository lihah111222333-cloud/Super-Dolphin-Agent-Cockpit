package archtest

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSessionPathsLiteralPlacementGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	targets := map[string][]string{
		"internal/provider/codexapp/history_rollout.go": {
			`"rollout-*-`,
			`"sessions", "*", "*", "*"`,
		},
		"internal/util/historyjsonl/history.go": {
			`"rollout-*-`,
			`"sessions", "*", "*", "*"`,
		},
		"internal/module/thread/scratchpad.go": {
			`"super-agent-v3"`,
			`"scratchpad"`,
			"sanitizeScratchpadPath",
			"managedScratchpadLeaf",
			"managedScratchpadNamespace",
			"isManagedScratchpadDir",
		},
	}

	var violations []string
	for rel, forbidden := range targets {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		source := string(data)
		for _, token := range forbidden {
			if strings.Contains(source, token) {
				violations = append(violations, rel+" contains session path placement literal "+strconv.Quote(token))
			}
		}
	}
	if len(violations) > 0 {
		t.Fatalf("session path placement literals must stay in internal/platform/sessionpaths:\n%s", strings.Join(violations, "\n"))
	}
}
