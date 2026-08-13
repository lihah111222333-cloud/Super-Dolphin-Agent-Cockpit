package remoteci

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

// LoadTrustedGateLauncherCompileClosure 加载 host launcher 编译闭包，并绑定实际 go:embed 资产。
func LoadTrustedGateLauncherCompileClosure(
	ctx context.Context,
	repoRoot string,
	treeOID string,
) (sourceDigest string, toolchainDigest string, entries []sourceexport.TreeEntry, err error) {
	_, toolchainDigest, entries, err = LoadGateCLICompileClosure(ctx, repoRoot, treeOID)
	if err != nil {
		return "", "", nil, err
	}
	snapshot, err := loadRemoteGitTreeSnapshot(ctx, repoRoot, treeOID)
	if err != nil {
		return "", "", nil, fmt.Errorf("load trusted launcher compile tree: %w", err)
	}
	assetPaths, err := snapshot.trustedGateLauncherEmbeddedAssets(entries)
	if err != nil {
		return "", "", nil, err
	}
	if len(assetPaths) != 0 {
		assets, err := loadReadOnlyTreePaths(ctx, repoRoot, treeOID, assetPaths)
		if err != nil {
			return "", "", nil, err
		}
		entries = append(entries, assets...)
		sort.Slice(entries, func(left, right int) bool {
			return entries[left].Path < entries[right].Path
		})
	}
	canonical, err := canonicalContextDigests(entries)
	if err != nil {
		return "", "", nil, fmt.Errorf("build canonical trusted launcher compile context: %w", err)
	}
	return canonical.ContextDigest, toolchainDigest, cloneTreeEntries(entries), nil
}

func (snapshot *remoteGitTreeSnapshot) trustedGateLauncherEmbeddedAssets(entries []sourceexport.TreeEntry) ([]string, error) {
	loaded := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		loaded[entry.Path] = struct{}{}
	}
	selected := make(map[string]struct{})
	for _, entry := range entries {
		if path.Ext(entry.Path) != ".go" || strings.HasSuffix(entry.Path, "_test.go") {
			continue
		}
		assets, err := snapshot.resolveGoEmbedAssets(path.Dir(entry.Path), entry.Data)
		if err != nil {
			return nil, fmt.Errorf("resolve trusted launcher go:embed assets in %q: %w", entry.Path, err)
		}
		for assetPath := range assets {
			if _, exists := loaded[assetPath]; exists {
				continue
			}
			selected[assetPath] = struct{}{}
		}
	}
	paths := make([]string, 0, len(selected))
	for assetPath := range selected {
		paths = append(paths, assetPath)
	}
	sort.Strings(paths)
	return paths, nil
}
