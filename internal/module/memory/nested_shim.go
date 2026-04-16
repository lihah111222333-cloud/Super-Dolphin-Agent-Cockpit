// Package memory compatibility shim for the nested subpackage migration.
// Owned by the nested subpackage split; keep here until remaining root tests
// stop depending on nested wrappers, then delete.
package memory

import nestedpkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/nested"

type NestedRuntime = nestedpkg.NestedRuntime

func MatchTargetPath(target string, globs []string, baseDir string) bool {
	return nestedpkg.MatchTargetPath(target, globs, baseDir)
}
