package archtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIReadsDependenciesAndCachesDirectlyFromImageLayer 锁死 ImageCache 镜像层直读，拒绝恢复外挂缓存。
type imageLayerGuardSources struct {
	coordinator            string
	client                 string
	executorSeed           string
	gateImage              string
	closureGenerator       string
	runtimeDependencyImage string
}

func TestRemoteCIReadsDependenciesAndCachesDirectlyFromImageLayer(t *testing.T) {
	root := findRepoRoot(t)
	sources := readImageLayerGuardSources(t, root)
	assertForbiddenImageLayerMounts(t, sources)
	assertForbiddenFrontendCacheTerms(t, sources)
	assertForbiddenFrontendCachePaths(t, sources)
	assertFrontendOverlayReadsImageLayer(t, sources.executorSeed)
	assertRequiredImageLayerCoordinatorMarkers(t, sources.coordinator)
	assertRequiredImageLayerFrontendMarkers(t, sources.executorSeed)
}

func readImageLayerGuardSources(t *testing.T, root string) imageLayerGuardSources {
	t.Helper()
	return imageLayerGuardSources{
		coordinator:            readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_request.go")),
		client:                 readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/alicloud/eci/client.go")),
		executorSeed:           readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/executor_seed.go")),
		gateImage:              readRemoteCIContractGuardFile(t, filepath.Join(root, "build/gate/Dockerfile")),
		closureGenerator:       readRemoteCIContractGuardFile(t, filepath.Join(root, "build/gate/closure/closure.go")),
		runtimeDependencyImage: readRemoteCIContractGuardFile(t, filepath.Join(root, "build/gate/runtime-deps.Dockerfile")),
	}
}

func assertForbiddenImageLayerMounts(t *testing.T, sources imageLayerGuardSources) {
	t.Helper()
	for _, forbidden := range []string{"expanded-data", "ExpandedVolume", "BootstrapVolume", "FlexVolume", "SubPath", "current-gate", "remoteXKBCompSubPath", "remoteXKBDataSubPath"} {
		if strings.Contains(sources.coordinator, forbidden) || strings.Contains(sources.client, forbidden) {
			t.Fatalf("remote CI restored forbidden cache/dependency mount %q", forbidden)
		}
	}
}

func assertForbiddenFrontendCacheTerms(t *testing.T, sources imageLayerGuardSources) {
	t.Helper()
	for _, forbidden := range []string{"copyViteCacheSeed", "frontend-build-cache", "/runtime/frontend/build-cache"} {
		for _, source := range []struct {
			name string
			text string
		}{
			{name: "executor seed", text: sources.executorSeed},
			{name: "generated gate image", text: sources.gateImage},
			{name: "gate image generator", text: sources.closureGenerator},
			{name: "runtime dependency image", text: sources.runtimeDependencyImage},
		} {
			if !strings.Contains(source.text, forbidden) {
				continue
			}
			t.Fatalf("remote CI %s restored forbidden per-shard or fake frontend cache %q", source.name, forbidden)
		}
	}
}

func assertForbiddenFrontendCachePaths(t *testing.T, sources imageLayerGuardSources) {
	t.Helper()
	for _, forbidden := range []string{"/out/frontend-build-cache", "/opt/super-dolphin-gate/runtime/frontend/build-cache"} {
		if strings.Contains(sources.gateImage, forbidden) || strings.Contains(sources.closureGenerator, forbidden) || strings.Contains(sources.runtimeDependencyImage, forbidden) {
			t.Fatalf("remote CI restored forbidden per-shard or fake frontend cache %q", forbidden)
		}
	}
}

func assertFrontendOverlayReadsImageLayer(t *testing.T, executorSeed string) {
	t.Helper()
	overlayStart := strings.Index(executorSeed, "func installFrontendRuntimeOverlay")
	overlayEnd := strings.Index(executorSeed, "// runtimeSeedManifestForProgram")
	if overlayStart < 0 || overlayEnd <= overlayStart {
		t.Fatal("remote CI frontend overlay implementation is missing")
	}
	overlay := executorSeed[overlayStart:overlayEnd]
	for _, forbidden := range []string{"WalkDir", "os.ReadFile", "os.WriteFile"} {
		if strings.Contains(overlay, forbidden) {
			t.Fatalf("remote CI frontend overlay copies image-layer cache via %q", forbidden)
		}
	}
}

func assertRequiredImageLayerCoordinatorMarkers(t *testing.T, coordinator string) {
	t.Helper()
	for _, required := range []string{
		`SourceVolume:     eci.EmptyDirVolume{Name: "source-data"}`,
		`WorkVolume:       eci.EmptyDirVolume{Name: "work-data"}`,
		`TempVolume:       eci.EmptyDirVolume{Name: "temp-data"}`,
		`/usr/local/go/bin/go build`,
		`--seed /opt/super-dolphin/cache/go-build --private $private_cache`,
		`worker go-module-overlay /opt/super-dolphin-gate/runtime/go-mod-cache "$private_mod_cache"`,
		`GOMODCACHE="$private_mod_cache" GOPROXY=off`,
	} {
		if !strings.Contains(coordinator, required) {
			t.Fatalf("remote CI image-layer direct-read contract is missing %q", required)
		}
	}
}

func assertRequiredImageLayerFrontendMarkers(t *testing.T, executorSeed string) {
	t.Helper()
	for _, required := range []string{
		`os.Symlink(viteSeedRoot, filepath.Join(targetRoot, ".vite"))`,
		`os.Mkdir(filepath.Join(targetRoot, ".vite-temp"), 0o700)`,
	} {
		if !strings.Contains(executorSeed, required) {
			t.Fatalf("remote CI frontend image-layer direct-read contract is missing %q", required)
		}
	}
}
