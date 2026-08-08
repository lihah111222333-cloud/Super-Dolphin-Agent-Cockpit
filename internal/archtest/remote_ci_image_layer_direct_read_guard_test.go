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
	executorViteCache      string
	executor               string
	gateImage              string
	closureGenerator       string
	runtimeDependencyImage string
	viteConfig             string
}

func TestRemoteCIReadsDependenciesAndCachesDirectlyFromImageLayer(t *testing.T) {
	root := findRepoRoot(t)
	sources := readImageLayerGuardSources(t, root)
	assertForbiddenImageLayerMounts(t, sources)
	assertForbiddenFrontendCacheTerms(t, sources)
	assertForbiddenFrontendCachePaths(t, sources)
	assertFrontendOverlayReadsImageLayer(t, sources.executorViteCache)
	assertNormalShardDoesNotRescanImageTrees(t, root)
	assertRequiredImageLayerCoordinatorMarkers(t, sources.coordinator)
	assertRequiredImageLayerFrontendMarkers(t, sources.executorSeed, sources.executorViteCache, sources.viteConfig, sources.executor)
}

func assertNormalShardDoesNotRescanImageTrees(t *testing.T, root string) {
	t.Helper()
	file := parseRemoteCIContractGuardFile(t, filepath.Join(root, "internal", "devtools", "gate", "executor_seed.go"))
	for _, functionName := range []string{"prepareExecutorRuntimeSeeds", "installFrontendRuntimeSeed"} {
		if remoteCIFunctionByName(file, functionName) == nil {
			t.Fatalf("remote CI image seed hot-path function %q is missing", functionName)
		}
		if remoteCIFunctionContainsIdentifier(file, functionName, "RuntimeSeedTreeDigest") ||
			remoteCIFunctionHasSelector(file, functionName, "filepath", "WalkDir") {
			t.Fatalf("normal shard %s restored a full image-tree scan", functionName)
		}
	}
}

func readImageLayerGuardSources(t *testing.T, root string) imageLayerGuardSources {
	t.Helper()
	return imageLayerGuardSources{
		coordinator:            readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_request.go")),
		client:                 readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/alicloud/eci/client.go")),
		executorSeed:           readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/executor_seed.go")),
		executorViteCache:      readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/executor_frontend_vite_cache.go")),
		executor:               readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/executor.go")),
		gateImage:              readRemoteCIContractGuardFile(t, filepath.Join(root, "build/gate/Dockerfile")),
		closureGenerator:       readRemoteCIContractGuardFile(t, filepath.Join(root, "build/gate/closure/closure.go")),
		runtimeDependencyImage: readRemoteCIContractGuardFile(t, filepath.Join(root, "build/gate/runtime-deps.Dockerfile")),
		viteConfig:             readRemoteCIContractGuardFile(t, filepath.Join(root, "frontend-app/vite.config.js")),
	}
}

func assertForbiddenImageLayerMounts(t *testing.T, sources imageLayerGuardSources) {
	t.Helper()
	for _, forbidden := range []string{"expanded-data", "ExpandedVolume", "FlexVolume", "SubPath", "current-gate", "remoteXKBCompSubPath", "remoteXKBDataSubPath"} {
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

func assertFrontendOverlayReadsImageLayer(t *testing.T, executorViteCache string) {
	t.Helper()
	overlayStart := strings.Index(executorViteCache, "func installFrontendRuntimeOverlay")
	overlayEnd := strings.Index(executorViteCache, "// installFrontendViteCacheOverlay")
	if overlayStart < 0 || overlayEnd <= overlayStart {
		t.Fatal("remote CI frontend overlay implementation is missing")
	}
	overlay := executorViteCache[overlayStart:overlayEnd]
	for _, forbidden := range []string{"WalkDir", "os.ReadFile", "os.WriteFile"} {
		if strings.Contains(overlay, forbidden) {
			t.Fatalf("remote CI frontend overlay copies image-layer cache via %q", forbidden)
		}
	}
}

func assertRequiredImageLayerCoordinatorMarkers(t *testing.T, coordinator string) {
	t.Helper()
	for _, required := range []string{
		`SourceVolume:`,
		`eci.EmptyDirVolume{Name: "source-data"}`,
		`WorkVolume:`,
		`eci.EmptyDirVolume{Name: "work-data"}`,
		`TempVolume:`,
		`eci.EmptyDirVolume{Name: "temp-data"}`,
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

func assertRequiredImageLayerFrontendMarkers(t *testing.T, executorSeed string, executorViteCache string, viteConfig string, executor string) {
	t.Helper()
	for _, required := range []string{
		`os.Symlink(viteSeedRoot, filepath.Join(targetRoot, ".vite"))`,
	} {
		if !strings.Contains(executorViteCache, required) {
			t.Fatalf("remote CI frontend image-layer direct-read contract is missing %q", required)
		}
	}
	if !strings.Contains(executorSeed, `installFrontendRuntimeOverlays(seedPath, viteSeedRoot, targetRoot, filepath.Join(layout.tmp, ".vite-temp"))`) {
		t.Fatal("remote CI frontend executor does not wire the private Vite cache path")
	}
	for _, required := range []string{
		`installFrontendViteCacheOverlay(viteSeedRoot, privateCacheRoot)`,
		`os.Symlink(depsSeedRoot, filepath.Join(privateCacheRoot, "deps"))`,
	} {
		if !strings.Contains(executorViteCache, required) {
			t.Fatalf("remote CI frontend cache helper is missing %q", required)
		}
	}
	if !strings.Contains(executor, "SUPER_DOLPHIN_VITE_CACHE_DIR") {
		t.Fatal("executor does not publish private Vite cache path")
	}
	if strings.Contains(executorViteCache, `os.Mkdir(filepath.Join(targetRoot, ".vite-temp"), 0o700)`) {
		t.Fatal("remote CI frontend overlay restored node_modules/.vite-temp intermediate path")
	}
	for _, required := range []string{
		"cacheDir: resolveFrontendViteCacheDir(env),",
		"SUPER_DOLPHIN_VITE_CACHE_DIR",
	} {
		if !strings.Contains(viteConfig, required) {
			t.Fatalf("frontend Vite config does not consume private cache path %q", required)
		}
	}
}
