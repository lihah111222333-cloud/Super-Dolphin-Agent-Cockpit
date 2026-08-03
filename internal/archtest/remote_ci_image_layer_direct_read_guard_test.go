package archtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIReadsDependenciesAndCachesDirectlyFromImageLayer 锁死 ImageCache 镜像层直读，拒绝恢复外挂缓存。
func TestRemoteCIReadsDependenciesAndCachesDirectlyFromImageLayer(t *testing.T) {
	root := findRepoRoot(t)
	coordinator := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_request.go"))
	client := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/alicloud/eci/client.go"))
	for _, forbidden := range []string{"expanded-data", "ExpandedVolume", "remoteXKBCompSubPath", "remoteXKBDataSubPath"} {
		if strings.Contains(coordinator, forbidden) || strings.Contains(client, forbidden) {
			t.Fatalf("remote CI restored forbidden cache/dependency mount %q", forbidden)
		}
	}
	for _, required := range []string{
		`SourceVolume:     eci.EmptyDirVolume{Name: "source-data"}`,
		`WorkVolume:       eci.EmptyDirVolume{Name: "work-data"}`,
		`TempVolume:       eci.EmptyDirVolume{Name: "temp-data"}`,
		`/usr/local/go/bin/go build`,
		`--seed /opt/super-dolphin/cache/go-build --private $private_cache`,
	} {
		if !strings.Contains(coordinator, required) {
			t.Fatalf("remote CI image-layer direct-read contract is missing %q", required)
		}
	}
}
