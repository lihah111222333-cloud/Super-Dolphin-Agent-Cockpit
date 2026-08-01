package gate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExecutorReusesPreparedCachesWithinShard(t *testing.T) {
	config, program, sharedCacheRoot := newPreparedShardCacheFixture(t)
	for run := range 2 {
		if err := executeProgram(context.Background(), config, GateIDBackendTestWithGuard, program); err != nil {
			t.Fatalf("execute shared shard cache fixture %d: %v", run, err)
		}
		assertDirectoryEmpty(t, config.workRoot)
	}
	for _, name := range []string{"first-gate", "second-gate"} {
		if _, err := os.Stat(filepath.Join(sharedCacheRoot, name)); err != nil {
			t.Fatalf("shared shard cache marker %q: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(config.goBuildCacheSeedRoot, "first-gate")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runner cache seed was mutated: %v", err)
	}
	standaloneConfig := config
	standaloneConfig.preparedRuntimeSeeds = nil
	err := executeProgram(context.Background(), standaloneConfig, GateIDBackendTestWithGuard, program)
	if err == nil || !strings.Contains(err.Error(), "validate Go module proxy seed") {
		t.Fatalf("standalone executor accepted a changed runtime tree: %v", err)
	}
	assertDirectoryEmpty(t, config.workRoot)
}

func TestExecutorGoBuildCacheSeedIsPortableAcrossWorktrees(t *testing.T) {
	files := map[string]string{
		"go.mod":          "module example.com/build-cache-fixture\n\ngo 1.22\n",
		"fixture.go":      "package fixture\n\nfunc Value() int { return 42 }\n",
		"fixture_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 42 {\n\t\tt.Fatal(\"unexpected value\")\n\t}\n}\n",
	}
	firstSource := writeGoBuildCacheFixture(t, files)
	secondSource := writeGoBuildCacheFixture(t, files)
	canonicalSource := filepath.Join(realTempDir(t), "workspace", "work", "lanes", "lane-0", "run", "source")
	seedRoot := realTempDir(t)
	if err := os.Chmod(seedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	materializeGoBuildCacheFixture(t, firstSource, canonicalSource)
	runGoBuildCacheFixture(t, canonicalSource, seedRoot, nil, false)
	seedDigest := mustRuntimeSeedTreeDigest(t, seedRoot)
	newestSeedRoot := realTempDir(t)

	privateRoot := realTempDir(t)
	if err := os.Chmod(privateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := seedExecutorGoBuildCache(seedRoot, privateRoot); err != nil {
		t.Fatalf("validate shared Go build cache and worktree-private layer: %v", err)
	}
	if err := os.RemoveAll(canonicalSource); err != nil {
		t.Fatalf("remove first materialized worktree: %v", err)
	}
	materializeGoBuildCacheFixture(t, secondSource, canonicalSource)
	trace := runGoBuildCacheFixture(t, canonicalSource, privateRoot, []string{newestSeedRoot, seedRoot}, true)
	if strings.Contains(trace, "/compile ") || strings.Contains(trace, `\compile.exe `) {
		t.Fatalf("second worktree recompiled despite the canonical worker source path:\n%s", trace)
	}
	trace = runGoBuildCacheFixture(t, canonicalSource, privateRoot, []string{newestSeedRoot, seedRoot}, true)
	if strings.Contains(trace, "/compile ") || strings.Contains(trace, `\compile.exe `) {
		t.Fatalf("reused private write layer recompiled the unchanged worktree:\n%s", trace)
	}
	if afterDigest := mustRuntimeSeedTreeDigest(t, seedRoot); afterDigest != seedDigest {
		t.Fatalf("shared Go build cache seed was mutated: %s != %s", afterDigest, seedDigest)
	}
}

func TestExecutorWorkloadTimeoutStartsAfterPreparation(t *testing.T) {
	const workloadTimeout = 10 * time.Minute
	preparationCtx, err := WithExecutorWorkloadTimeout(context.Background(), workloadTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := preparationCtx.Deadline(); ok {
		t.Fatal("workload deadline started before executor preparation completed")
	}
	started := time.Now()
	executionCtx, cancel := executorWorkloadContext(preparationCtx)
	defer cancel()
	deadline, ok := executionCtx.Deadline()
	if !ok || deadline.Before(started.Add(workloadTimeout-time.Second)) ||
		deadline.After(started.Add(workloadTimeout+time.Second)) {
		t.Fatalf("execution deadline = %v, ok=%t", deadline, ok)
	}
}

func TestGoBuildCacheProxyHelper(_ *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		return
	}
	if err := ExecuteGoBuildCacheProxy(os.Args[separator+1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "Go build cache proxy helper: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func testGoBuildCacheProxyLauncher() string {
	return strconv.Quote(os.Args[0]) + " -test.run=^TestGoBuildCacheProxyHelper$ --"
}

func TestGoBuildCacheProxyConfigAcceptsOrderedSeedChain(t *testing.T) {
	newest := realTempDir(t)
	oldest := realTempDir(t)
	privateRoot := realTempDir(t)
	config, err := parseGoBuildCacheProxyConfig([]string{
		"--seed", newest,
		"--seed", oldest,
		"--private", privateRoot,
	})
	if err != nil {
		t.Fatalf("parse ordered Go build cache seed chain: %v", err)
	}
	if !slices.Equal(config.seedRoots, []string{newest, oldest}) || config.privateRoot != privateRoot {
		t.Fatalf("Go build cache proxy config = %#v", config)
	}
	for name, args := range map[string][]string{
		"duplicate seed": {
			"--seed", newest, "--seed", newest, "--private", privateRoot,
		},
		"duplicate private": {
			"--seed", newest, "--private", privateRoot, "--private", oldest,
		},
		"missing seed": {
			"--private", privateRoot, "--private", oldest,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseGoBuildCacheProxyConfig(args); err == nil {
				t.Fatalf("parseGoBuildCacheProxyConfig(%v) error = nil", args)
			}
		})
	}
}

func TestGoBuildCacheIndexUsesInjectedTimestamp(t *testing.T) {
	actionID := bytes.Repeat([]byte{0x5a}, goBuildCacheHashBytes)
	content := []byte("cached output")
	outputID := sha256.Sum256(content)
	privateRoot := realTempDir(t)
	outputPath, err := goBuildCachePath(privateRoot, outputID[:], "d")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, outputPath, string(content), 0o600)
	storedAt := time.Unix(1_700_000_000, 123).UTC()
	request := goBuildCacheProxyRequest{
		ActionID: actionID,
		OutputID: outputID[:],
		BodySize: int64(len(content)),
	}
	if err := writeGoBuildCacheIndex(privateRoot, request, storedAt); err != nil {
		t.Fatalf("write Go build cache index: %v", err)
	}
	entry, err := readGoBuildCacheEntry(privateRoot, actionID)
	if err != nil {
		t.Fatalf("read Go build cache entry: %v", err)
	}
	if !entry.storedAt.Equal(storedAt) {
		t.Fatalf("stored timestamp = %v, want %v", entry.storedAt, storedAt)
	}
}

func TestGoBuildCacheProxyReadsPrivateThenNewestSeed(t *testing.T) {
	actionID := bytes.Repeat([]byte{0x7a}, goBuildCacheHashBytes)
	newest := realTempDir(t)
	oldest := realTempDir(t)
	privateRoot := realTempDir(t)
	writeGoBuildCacheEntryFixture(t, oldest, actionID, "oldest")
	writeGoBuildCacheEntryFixture(t, newest, actionID, "newest")
	config, err := parseGoBuildCacheProxyConfig([]string{
		"--seed", newest,
		"--seed", oldest,
		"--private", privateRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := findGoBuildCacheEntry(config, actionID)
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(entry.path); err != nil || string(content) != "newest" {
		t.Fatalf("newest seed content = %q, %v", content, err)
	}
	writeGoBuildCacheEntryFixture(t, privateRoot, actionID, "private")
	entry, err = findGoBuildCacheEntry(config, actionID)
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(entry.path); err != nil || string(content) != "private" {
		t.Fatalf("private cache content = %q, %v", content, err)
	}
}

func TestGoBuildCacheProxyMetricsAttributePrivateAndGenerationHits(t *testing.T) {
	actionID := bytes.Repeat([]byte{0x4a}, goBuildCacheHashBytes)
	seedRoot := filepath.Join(realTempDir(t), "00000000000000000042")
	if err := os.Mkdir(seedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	privateRoot := realTempDir(t)
	writeGoBuildCacheEntryFixture(t, seedRoot, actionID, "seed")
	config, err := parseGoBuildCacheProxyConfig([]string{"--seed", seedRoot, "--private", privateRoot})
	if err != nil {
		t.Fatal(err)
	}
	request := goBuildCacheProxyRequest{ID: 1, Command: "get", ActionID: actionID}
	if _, _, err := handleGoBuildCacheProxyRequest(config, bufio.NewReader(strings.NewReader("")), request); err != nil {
		t.Fatal(err)
	}
	writeGoBuildCacheEntryFixture(t, privateRoot, actionID, "private")
	request.ID = 2
	if _, _, err := handleGoBuildCacheProxyRequest(config, bufio.NewReader(strings.NewReader("")), request); err != nil {
		t.Fatal(err)
	}
	request.ID = 3
	request.ActionID = bytes.Repeat([]byte{0x5b}, goBuildCacheHashBytes)
	if _, _, err := handleGoBuildCacheProxyRequest(config, bufio.NewReader(strings.NewReader("")), request); err != nil {
		t.Fatal(err)
	}
	if config.metrics.BaselineHitCount != 1 || config.metrics.BaselineHitByGeneration["00000000000000000042"] != 1 ||
		config.metrics.PrivateHitCount != 1 || config.metrics.MissCount != 1 {
		t.Fatalf("cache metrics = %#v", config.metrics)
	}
}

func TestLoadGoBuildCacheProxyMetricsRejectsForgedSeedIdentity(t *testing.T) {
	privateRoot := realTempDir(t)
	seedRoot := filepath.Join(realTempDir(t), "00000000000000000042")
	if err := os.Mkdir(seedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	metrics := newGoBuildCacheProxyMetrics([]string{seedRoot})
	metrics.BaselineHitCount = 1
	metrics.BaselineHitByGeneration["00000000000000000042"] = 1
	if err := writeGoBuildCacheProxyMetrics(GoBuildCacheProxyMetricsPath(privateRoot), metrics); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGoBuildCacheProxyMetrics(privateRoot, []string{filepath.Join(realTempDir(t), "00000000000000000042")}); err == nil {
		t.Fatal("LoadGoBuildCacheProxyMetrics accepted forged seed identity")
	}
}

func writeGoBuildCacheEntryFixture(t *testing.T, root string, actionID []byte, content string) {
	t.Helper()
	outputID := sha256.Sum256([]byte(content))
	outputPath, err := goBuildCachePath(root, outputID[:], "d")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, outputPath, content, 0o600)
	indexPath, err := goBuildCachePath(root, actionID, "a")
	if err != nil {
		t.Fatal(err)
	}
	index := fmt.Sprintf(
		"v1 %x %x %d %d\n",
		actionID,
		outputID,
		len(content),
		time.Unix(1_700_000_000, 0).UnixNano(),
	)
	writeTestFile(t, indexPath, index, 0o600)
}

func writeGoBuildCacheFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := realTempDir(t)
	for path, content := range files {
		writeTestFile(t, filepath.Join(root, path), content, 0o600)
	}
	return root
}

func materializeGoBuildCacheFixture(t *testing.T, source string, canonicalSource string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(canonicalSource), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(canonicalSource, os.DirFS(source)); err != nil {
		t.Fatalf("materialize worktree %s at canonical worker path: %v", source, err)
	}
}

func runGoBuildCacheFixture(
	t *testing.T,
	source string,
	cacheRoot string,
	seedRoots []string,
	trace bool,
) string {
	t.Helper()
	args := []string{"test", "-mod=readonly", "-run=^$", "-count=1"}
	if trace {
		args = append(args, "-x")
	}
	args = append(args, "./...")
	command := exec.Command(filepath.Join(build.Default.GOROOT, "bin", "go"), args...)
	command.Dir = source
	environment := make([]string, 0, len(os.Environ())+8)
	for _, variable := range os.Environ() {
		if strings.HasPrefix(variable, "GOCACHE=") || strings.HasPrefix(variable, "GOCACHEPROG=") {
			continue
		}
		environment = append(environment, variable)
	}
	command.Env = append(environment,
		"CGO_ENABLED=0",
		"GOWORK=off",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOFLAGS=-p=1",
		"GOCACHE="+cacheRoot,
	)
	if len(seedRoots) != 0 {
		proxyCommand := testGoBuildCacheProxyLauncher()
		for _, seedRoot := range seedRoots {
			proxyCommand += " --seed " + strconv.Quote(seedRoot)
		}
		command.Env = append(command.Env, "GOCACHEPROG="+proxyCommand+" --private "+strconv.Quote(cacheRoot))
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("compile Go cache fixture in %s: %v\n%s", source, err, output)
	}
	return string(output)
}

func newPreparedShardCacheFixture(t *testing.T) (executorConfig, ExecutorProgram, string) {
	t.Helper()
	script := "#!/bin/sh\nset -eu\nif test -e \"$GOCACHE/first-gate\"; then\n  printf reused > \"$GOCACHE/second-gate\"\nelse\n  printf first > \"$GOCACHE/first-gate\"\nfi\n"
	source := newExecutorGitSnapshot(t, map[string]string{
		"cache.sh":                       script,
		"go.sum":                         "module sum\n",
		"frontend-app/package-lock.json": "{\"lockfileVersion\":3}\n",
	})
	if err := os.Chmod(filepath.Join(source, "cache.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimeRoot, manifestPath := writeRuntimeSeedFixture(t, source)
	commitExecutorSnapshot(t, source, "shared shard cache fixture")
	config := newTestExecutorConfig(t, source)
	config.runtimeSeedRoot = runtimeRoot
	config.runtimeSeedManifest = manifestPath
	preparedRuntimeSeeds, err := prepareExecutorRuntimeSeeds(runtimeRoot, manifestPath, true, false)
	if err != nil {
		t.Fatalf("prepare immutable runtime seeds: %v", err)
	}
	config.preparedRuntimeSeeds = preparedRuntimeSeeds
	writeTestFile(
		t,
		filepath.Join(runtimeRoot, "go-proxy", "github.com", "kelindar", "event", "@v", "v1.5.2.info"),
		"{\"accepted_tree_is_now_immutable\":false}\n",
		0o600,
	)
	writeTestFile(t, filepath.Join(config.goBuildCacheSeedRoot, "prewarmed"), "runner-cache\n", 0o600)
	sharedCacheRoot := realTempDir(t)
	if err := os.Chmod(sharedCacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := seedExecutorGoBuildCache(config.goBuildCacheSeedRoot, sharedCacheRoot); err != nil {
		t.Fatalf("prepare shard Go build cache write layer: %v", err)
	}
	config.goBuildCacheRoot = sharedCacheRoot
	program := ExecutorProgram{
		Strategy:      ExecutorStrategyCommands,
		Steps:         []ExecutorStep{{Argv: []string{"./cache.sh"}}},
		RequiredPaths: []string{"cache.sh"},
		NeedsGoSeed:   true,
	}
	return config, program, sharedCacheRoot
}

func TestExecutorEnvironmentIsClosedAndUsesPrivateModuleMetadata(t *testing.T) {
	layout := newExecutorLayout("/workspace/work")
	environment := executorEnvironment(
		layout,
		executorSearchPath,
		layout.goModCache,
		ExecutorPortableGoRoot,
		ExecutorRuntimeSeedRoot+"/frontend/node_modules",
		"gate worker go-cache-proxy --seed /seed --private /private",
	)
	joined := "\n" + strings.Join(environment, "\n") + "\n"
	for _, want := range []string{
		"\nHOME=/workspace/work/home\n",
		"\nGIT_AUTHOR_NAME=Super Dolphin Gate Executor\n",
		"\nGIT_AUTHOR_EMAIL=gate-executor@super-dolphin.invalid\n",
		"\nGIT_AUTHOR_DATE=946684800 +0000\n",
		"\nGIT_COMMITTER_NAME=Super Dolphin Gate Executor\n",
		"\nGIT_COMMITTER_EMAIL=gate-executor@super-dolphin.invalid\n",
		"\nGIT_COMMITTER_DATE=946684800 +0000\n",
		"\nGOCACHE=/workspace/work/go-cache\n",
		"\nGOCACHEPROG=gate worker go-cache-proxy --seed /seed --private /private\n",
		"\nGOMODCACHE=/workspace/work/go-mod-cache\n",
		"\nGOPROXY=off\n",
		"\nGOROOT=/opt/super-dolphin-gate/runtime/go\n",
		"\nGOTMPDIR=/workspace/work/tmp\n",
		"\nLD_LIBRARY_PATH=/opt/super-dolphin-gate/runtime/rootfs/usr/lib/x86_64-linux-gnu:/opt/super-dolphin-gate/runtime/rootfs/lib/x86_64-linux-gnu:/opt/super-dolphin-gate/runtime/rootfs/usr/lib/aarch64-linux-gnu:/opt/super-dolphin-gate/runtime/rootfs/lib/aarch64-linux-gnu:/opt/super-dolphin-gate/runtime/rootfs/usr/lib:/opt/super-dolphin-gate/runtime/rootfs/lib\n",
		"\nFONTCONFIG_SYSROOT=/opt/super-dolphin-gate/runtime/rootfs\n",
		"\nFONTCONFIG_FILE=fonts.conf\n",
		"\nFONTCONFIG_PATH=/opt/super-dolphin-gate/runtime/rootfs/etc/fonts\n",
		"\nXDG_DATA_DIRS=/opt/super-dolphin-gate/runtime/rootfs/usr/local/share:/opt/super-dolphin-gate/runtime/rootfs/usr/share\n",
		"\nGSETTINGS_SCHEMA_DIR=/opt/super-dolphin-gate/runtime/rootfs/usr/share/glib-2.0/schemas\n",
		"\nPATH=" + ExecutorPortableSearchPath + "\n",
		"\nSUPER_DOLPHIN_GATE_GIT=/opt/super-dolphin-gate/runtime/bin/git\n",
		"\nSUPER_DOLPHIN_GATE_NODE=/opt/super-dolphin-gate/runtime/node/bin/node\n",
		"\nSUPER_DOLPHIN_GATE_XVFB_RUN=/opt/super-dolphin-gate/runtime/bin/xvfb-run\n",
		"\nnpm_config_cache=/opt/super-dolphin-gate/runtime/frontend/npm-cache\n",
		"\nnpm_config_logs_dir=/workspace/work/npm-cache/_logs\n",
		"\nPLAYWRIGHT_BROWSERS_PATH=/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright\n",
		"\nSUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED=/opt/super-dolphin-gate/runtime/frontend/node_modules\n",
		"\nSUPER_DOLPHIN_TEST_BACKEND=remote-worker\n",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("executor environment missing %q", strings.TrimSpace(want))
		}
	}
	for _, forbidden := range []string{"\nGIT_CONFIG_GLOBAL=", "\nGOFLAGS=", "\nGOWORK="} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("executor environment leaks child-process policy %q", strings.TrimSpace(forbidden))
		}
	}
	if strings.Contains(joined, "SECRET") {
		t.Fatal("executor environment inherited an undeclared secret")
	}
	keys := environmentKeys(environment)
	if compacted := slices.Compact(slices.Clone(keys)); len(compacted) != len(keys) {
		t.Fatal("executor environment contains duplicate keys")
	}
}

func TestExecutorAuditOutputContainsNoHostEnvironmentValues(t *testing.T) {
	layout := newExecutorLayout("/workspace/work")
	var output bytes.Buffer
	fmt.Fprintf(
		&output,
		"%s",
		strings.Join(
			environmentKeys(
				executorEnvironment(
					layout,
					executorSearchPath,
					"/opt/runtime/go-mod-cache",
					ExecutorPortableGoRoot,
					ExecutorRuntimeSeedRoot+"/frontend/node_modules",
					"",
				),
			),
			",",
		),
	)
	if strings.Contains(output.String(), os.Getenv("HOME")) {
		t.Fatal("audit output contains a host environment value")
	}
}
