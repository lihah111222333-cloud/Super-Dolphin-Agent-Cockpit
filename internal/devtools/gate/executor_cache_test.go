package gate

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type failingGoCacheProxyWriter struct{}

func (failingGoCacheProxyWriter) Write([]byte) (int, error) {
	return 0, errors.New("fixture output failure")
}

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
	runGoBuildCacheFixture(t, canonicalSource, seedRoot, "", false)
	seedDigest := mustRuntimeSeedTreeDigest(t, seedRoot)
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
	trace := runGoBuildCacheFixture(t, canonicalSource, privateRoot, seedRoot, true)
	if strings.Contains(trace, "/compile ") || strings.Contains(trace, `\compile.exe `) {
		t.Fatalf("second worktree recompiled despite the canonical worker source path:\n%s", trace)
	}
	trace = runGoBuildCacheFixture(t, canonicalSource, privateRoot, seedRoot, true)
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

// super-dolphin-ci: helper
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

func TestExecuteGoBuildCacheProxyFinalizesStartedMarker(t *testing.T) {
	seedRoot := realTempDir(t)
	privateRoot := realTempDir(t)
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(privateRoot, "marker-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := ExecuteGoBuildCacheProxy([]string{
		"--seed", seedRoot,
		"--private", privateRoot,
		"--metrics", metricsPath,
	}, strings.NewReader(""), &output); err != nil {
		t.Fatalf("ExecuteGoBuildCacheProxy() error = %v", err)
	}
	if _, err := LoadGoBuildCacheProxyMetricsAt(privateRoot, metricsPath, seedRoot); err != nil {
		t.Fatalf("load finalized metrics: %v", err)
	}
	markers, err := goBuildCacheProxyStartedMarkers(metricsPath)
	if err != nil {
		t.Fatalf("list finalized proxy started markers: %v", err)
	}
	if len(markers) != 0 {
		t.Fatalf("finalized proxy retained started markers: %v", markers)
	}
}

func TestExecuteGoBuildCacheProxyAggregatesConcurrentHelpers(t *testing.T) {
	seedRoot := realTempDir(t)
	privateRoot := realTempDir(t)
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(privateRoot, "concurrent-helpers")
	if err != nil {
		t.Fatal(err)
	}
	actionID := bytes.Repeat([]byte{0x27}, goBuildCacheHashBytes)
	getRequest, err := json.Marshal(goBuildCacheProxyRequest{ID: 1, Command: "get", ActionID: actionID})
	if err != nil {
		t.Fatal(err)
	}
	input := string(getRequest) + "\n{\"ID\":2,\"Command\":\"close\"}\n"
	const helperCount = 8
	runConcurrentGoBuildCacheProxyHelpers(t, seedRoot, privateRoot, metricsPath, input, helperCount)
	requireGoBuildCacheProxyContributionCount(t, metricsPath, helperCount, "before finalization")
	metrics, err := LoadGoBuildCacheProxyMetricsAt(privateRoot, metricsPath, seedRoot)
	if err != nil {
		t.Fatalf("load aggregated helper metrics: %v", err)
	}
	if metrics.MissCount != helperCount || metrics.PrivateHitCount != 0 || metrics.BaselineHitCount != 0 || metrics.PutCount != 0 {
		t.Fatalf("aggregated helper metrics = %#v, want %d misses only", metrics, helperCount)
	}
	requireNoGoBuildCacheProxyStartedMarkers(t, metricsPath)
	requireGoBuildCacheProxyContributionCount(t, metricsPath, 0, "after finalization")
	if _, err := os.Stat(metricsPath + goBuildCacheProxyStartedFileSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy shared started marker exists after concurrent helpers: %v", err)
	}
}

func runConcurrentGoBuildCacheProxyHelpers(t *testing.T, seedRoot, privateRoot, metricsPath, input string, count int) {
	t.Helper()
	args := []string{"--seed", seedRoot, "--private", privateRoot, "--metrics", metricsPath}
	errorsByHelper := make(chan error, count)
	var group sync.WaitGroup
	for range count {
		group.Go(func() {
			var output bytes.Buffer
			errorsByHelper <- ExecuteGoBuildCacheProxy(args, strings.NewReader(input), &output)
		})
	}
	group.Wait()
	close(errorsByHelper)
	for err := range errorsByHelper {
		if err != nil {
			t.Fatalf("concurrent Go build cache proxy helper: %v", err)
		}
	}
}

func requireGoBuildCacheProxyContributionCount(t *testing.T, metricsPath string, want int, phase string) {
	t.Helper()
	contributions, err := goBuildCacheProxyContributionPaths(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(contributions) != want {
		t.Fatalf("helper contribution count %s = %d, want %d", phase, len(contributions), want)
	}
}

func requireNoGoBuildCacheProxyStartedMarkers(t *testing.T, metricsPath string) {
	t.Helper()
	markers, err := goBuildCacheProxyStartedMarkers(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 0 {
		t.Fatalf("Go build cache proxy retained started markers: %v", markers)
	}
}

func TestLoadGoBuildCacheProxyMetricsSumsAllHelperCounters(t *testing.T) {
	seedRoot := realTempDir(t)
	privateRoot := realTempDir(t)
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(privateRoot, "sum-all-counters")
	if err != nil {
		t.Fatal(err)
	}
	parts := []GoBuildCacheProxyMetrics{
		{SchemaVersion: goBuildCacheProxyMetricsSchemaVersion, PrivateHitCount: 2, MissCount: 3, SeedRoots: []string{seedRoot}},
		{SchemaVersion: goBuildCacheProxyMetricsSchemaVersion, BaselineHitCount: 5, PutCount: 7, SeedRoots: []string{seedRoot}},
	}
	for index, part := range parts {
		path := filepath.Join(privateRoot, fmt.Sprintf("%s.helper-%d.json", filepath.Base(metricsPath), index))
		if err := writeGoBuildCacheProxyMetrics(path, part); err != nil {
			t.Fatal(err)
		}
	}
	metrics, err := LoadGoBuildCacheProxyMetricsAt(privateRoot, metricsPath, seedRoot)
	if err != nil {
		t.Fatalf("load summed helper metrics: %v", err)
	}
	if metrics.PrivateHitCount != 2 || metrics.BaselineHitCount != 5 || metrics.MissCount != 3 || metrics.PutCount != 7 {
		t.Fatalf("summed helper metrics = %#v", metrics)
	}
}

func TestExecuteGoBuildCacheProxyRetainsStartedMarkerAfterServeFailure(t *testing.T) {
	seedRoot := realTempDir(t)
	privateRoot := realTempDir(t)
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(privateRoot, "failed-marker-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	err = ExecuteGoBuildCacheProxy([]string{
		"--seed", seedRoot,
		"--private", privateRoot,
		"--metrics", metricsPath,
	}, strings.NewReader(""), failingGoCacheProxyWriter{})
	if err == nil || !strings.Contains(err.Error(), "fixture output failure") {
		t.Fatalf("ExecuteGoBuildCacheProxy() error = %v", err)
	}
	markers, markerErr := goBuildCacheProxyStartedMarkers(metricsPath)
	if markerErr != nil {
		t.Fatalf("list failed proxy started markers: %v", markerErr)
	}
	if len(markers) != 1 {
		t.Fatalf("failed proxy retained %d started markers, want one: %v", len(markers), markers)
	}
	if _, err := os.Stat(metricsPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed proxy published final metrics: %v", err)
	}
}

func TestExecuteGoBuildCacheProxyRetainsStartedMarkerAfterRequestFailure(t *testing.T) {
	seedRoot := realTempDir(t)
	privateRoot := realTempDir(t)
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(privateRoot, "failed-request-lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("{\"ID\":1,\"Command\":\"get\"}\n{\"ID\":2,\"Command\":\"close\"}\n")
	var output bytes.Buffer
	err = ExecuteGoBuildCacheProxy([]string{
		"--seed", seedRoot,
		"--private", privateRoot,
		"--metrics", metricsPath,
	}, input, &output)
	if err == nil || !strings.Contains(err.Error(), "Go build cache get request is malformed") {
		t.Fatalf("ExecuteGoBuildCacheProxy() error = %v", err)
	}
	if !strings.Contains(output.String(), "Go build cache get request is malformed") {
		t.Fatalf("proxy response did not report request failure: %s", output.String())
	}
	markers, markerErr := goBuildCacheProxyStartedMarkers(metricsPath)
	if markerErr != nil {
		t.Fatalf("list failed request started markers: %v", markerErr)
	}
	if len(markers) != 1 {
		t.Fatalf("failed request retained %d started markers, want one: %v", len(markers), markers)
	}
	if _, err := os.Stat(metricsPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed request published final metrics: %v", err)
	}
}

func testGoBuildCacheProxyLauncher() string {
	return strconv.Quote(os.Args[0]) + " -test.run=^TestGoBuildCacheProxyHelper$ --"
}

func TestGoBuildCacheProxyConfigAcceptsSingleSeed(t *testing.T) {
	seedRoot := realTempDir(t)
	privateRoot := realTempDir(t)
	config, err := parseGoBuildCacheProxyConfig([]string{
		"--seed", seedRoot,
		"--private", privateRoot,
	})
	if err != nil {
		t.Fatalf("parse single Go build cache seed: %v", err)
	}
	if config.seedRoot != seedRoot || config.privateRoot != privateRoot {
		t.Fatalf("Go build cache proxy config = %#v", config)
	}
	for name, args := range map[string][]string{
		"duplicate seed": {
			"--seed", seedRoot, "--seed", seedRoot, "--private", privateRoot,
		},
		"duplicate private": {
			"--seed", seedRoot, "--private", privateRoot, "--private", seedRoot,
		},
		"missing seed": {
			"--private", privateRoot, "--private", seedRoot,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseGoBuildCacheProxyConfig(args); err == nil {
				t.Fatalf("parseGoBuildCacheProxyConfig(%v) error = nil", args)
			}
		})
	}
}

func TestExecutorRemoteGoBuildCacheSeedRootUsesOnlyFixedOCIImageRoot(t *testing.T) {
	seedRoot, err := ExecutorRemoteGoBuildCacheSeedRoot()
	if err != nil {
		t.Fatal(err)
	}
	if seedRoot != ExecutorOCIProjectGoBuildCacheSeedRoot {
		t.Fatalf("direct seed root = %v", seedRoot)
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

func TestGoBuildCacheProxyReadsPrivateThenImageLayerSeed(t *testing.T) {
	actionID := bytes.Repeat([]byte{0x7a}, goBuildCacheHashBytes)
	seedRoot := realTempDir(t)
	privateRoot := realTempDir(t)
	writeGoBuildCacheEntryFixture(t, seedRoot, actionID, "seed")
	config, err := parseGoBuildCacheProxyConfig([]string{
		"--seed", seedRoot,
		"--private", privateRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	entry, _, err := findGoBuildCacheEntryWithLayer(config, actionID)
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(entry.path); err != nil || string(content) != "seed" {
		t.Fatalf("image seed content = %q, %v", content, err)
	}
	writeGoBuildCacheEntryFixture(t, privateRoot, actionID, "private")
	entry, _, err = findGoBuildCacheEntryWithLayer(config, actionID)
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(entry.path); err != nil || string(content) != "private" {
		t.Fatalf("private cache content = %q, %v", content, err)
	}
}

func TestGoBuildCacheProxyMetricsAttributePrivateAndImageLayerHits(t *testing.T) {
	actionID := bytes.Repeat([]byte{0x4a}, goBuildCacheHashBytes)
	seedRoot := realTempDir(t)
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
	if config.metrics.BaselineHitCount != 1 || config.metrics.PrivateHitCount != 1 || config.metrics.MissCount != 1 {
		t.Fatalf("cache metrics = %#v", config.metrics)
	}
}

func TestGoBuildCacheProxyKeepsSeedHitsOutOfPrivateDelta(t *testing.T) {
	actionID := bytes.Repeat([]byte{0x61}, goBuildCacheHashBytes)
	untouchedID := bytes.Repeat([]byte{0x62}, goBuildCacheHashBytes)
	seedRoot := filepath.Join(realTempDir(t), "00000000000000000042")
	if err := os.Mkdir(seedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	privateRoot := realTempDir(t)
	writeGoBuildCacheEntryFixture(t, seedRoot, actionID, "accessed")
	writeGoBuildCacheEntryFixture(t, seedRoot, untouchedID, "untouched")
	seedDigest := mustRuntimeSeedTreeDigest(t, seedRoot)
	config, err := parseGoBuildCacheProxyConfig([]string{"--seed", seedRoot, "--private", privateRoot})
	if err != nil {
		t.Fatal(err)
	}
	response, err := getGoBuildCacheProxyEntry(config, goBuildCacheProxyRequest{ID: 1, ActionID: actionID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(response.DiskPath, seedRoot+string(os.PathSeparator)) {
		t.Fatalf("seed response path = %q", response.DiskPath)
	}
	if _, err := readGoBuildCacheEntry(privateRoot, actionID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("seed hit polluted private delta: %v", err)
	}
	if _, err := readGoBuildCacheEntry(privateRoot, untouchedID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unaccessed entry was promoted: %v", err)
	}
	if after := mustRuntimeSeedTreeDigest(t, seedRoot); after != seedDigest {
		t.Fatalf("seed mutated: %s != %s", after, seedDigest)
	}
	if config.metrics.BaselineHitCount != 1 || config.metrics.PrivateHitCount != 0 {
		t.Fatalf("promotion changed hit attribution: %#v", config.metrics)
	}
}

func TestLoadGoBuildCacheProxyMetricsRejectsForgedSeedIdentity(t *testing.T) {
	privateRoot := realTempDir(t)
	seedRoot := filepath.Join(realTempDir(t), "00000000000000000042")
	if err := os.Mkdir(seedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	metrics := newGoBuildCacheProxyMetrics(seedRoot)
	metrics.BaselineHitCount = 1
	if err := writeGoBuildCacheProxyMetrics(GoBuildCacheProxyMetricsPath(privateRoot), metrics); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGoBuildCacheProxyMetricsAt(privateRoot, GoBuildCacheProxyMetricsPath(privateRoot), filepath.Join(realTempDir(t), "00000000000000000042")); err == nil {
		t.Fatal("LoadGoBuildCacheProxyMetricsAt accepted forged seed identity")
	}
}

func TestGoBuildCacheProxyMetricsRejectsMultipleSeedRoots(t *testing.T) {
	metrics := GoBuildCacheProxyMetrics{
		SchemaVersion: goBuildCacheProxyMetricsSchemaVersion,
		SeedRoots:     []string{"/seed/one", "/seed/two"},
	}
	if err := validateGoBuildCacheProxyMetrics(metrics); err == nil {
		t.Fatal("Go build cache proxy metrics accepted multiple seed roots")
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
	seedRoot string,
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
		"GOCACHE="+cacheRoot,
	)
	if seedRoot != "" {
		proxyCommand := testGoBuildCacheProxyLauncher()
		proxyCommand += " --seed " + strconv.Quote(seedRoot)
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
		ExecutorGoRoot,
		ExecutorRuntimeSeedRoot+"/frontend/node_modules",
		"gate worker go-cache-proxy --seed /seed --private /private",
	)
	joined := "\n" + strings.Join(environment, "\n") + "\n"
	for _, want := range []string{
		"\nCGO_ENABLED=1\n",
		"\nHOME=/workspace/work/home\n",
		"\nGIT_AUTHOR_NAME=Super Dolphin Gate Executor\n",
		"\nGIT_AUTHOR_EMAIL=gate-executor@super-dolphin.invalid\n",
		"\nGIT_AUTHOR_DATE=946684800 +0000\n",
		"\nGIT_COMMITTER_NAME=Super Dolphin Gate Executor\n",
		"\nGIT_COMMITTER_EMAIL=gate-executor@super-dolphin.invalid\n",
		"\nGIT_COMMITTER_DATE=946684800 +0000\n",
		"\nGOCACHE=/workspace/work/go-cache\n",
		"\nGOARCH=amd64\n",
		"\nGOCACHEPROG=gate worker go-cache-proxy --seed /seed --private /private\n",
		"\nGOMODCACHE=/workspace/work/go-mod-cache\n",
		"\nGOPROXY=off\n",
		"\nGOROOT=/usr/local/go\n",
		"\nGOTMPDIR=/workspace/work/tmp\n",
		"\nGOOS=linux\n",
		"\nLD_LIBRARY_PATH=/usr/lib/x86_64-linux-gnu:/lib/x86_64-linux-gnu:/usr/lib/aarch64-linux-gnu:/lib/aarch64-linux-gnu:/usr/lib:/lib\n",
		"\nFONTCONFIG_FILE=fonts.conf\n",
		"\nFONTCONFIG_PATH=/etc/fonts\n",
		"\nXDG_DATA_DIRS=/usr/local/share:/usr/share\n",
		"\nGSETTINGS_SCHEMA_DIR=/usr/share/glib-2.0/schemas\n",
		"\nPATH=" + ExecutorSearchPath + "\n",
		"\nSUPER_DOLPHIN_GATE_GIT=/usr/bin/git\n",
		"\nSUPER_DOLPHIN_GATE_NODE=/usr/local/bin/node\n",
		"\nSUPER_DOLPHIN_GATE_XVFB_RUN=/usr/bin/xvfb-run\n",
		"\nnpm_config_cache=/opt/super-dolphin-gate/runtime/frontend/npm-cache\n",
		"\nnpm_config_logs_dir=/workspace/work/npm-cache/_logs\n",
		"\nPLAYWRIGHT_BROWSERS_PATH=/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright\n",
		"\nSUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED=/opt/super-dolphin-gate/runtime/frontend/node_modules\n",
		"\nSUPER_DOLPHIN_VITE_CACHE_DIR=/workspace/work/tmp/.vite-temp\n",
		"\nSUPER_DOLPHIN_TEST_BACKEND=remote-worker\n",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("executor environment missing %q", strings.TrimSpace(want))
		}
	}
	for _, forbidden := range []string{"\nGIT_CONFIG_GLOBAL=", "\nGOFLAGS=", "\nGOWORK=", "\nFONTCONFIG_SYSROOT="} {
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

func TestExecutorEnvironmentCarriesValidatedWorkloadTimeout(t *testing.T) {
	base := []string{"CI=true"}
	withoutTimeout, err := appendExecutorWorkloadTimeout(context.Background(), base)
	if err != nil {
		t.Fatalf("appendExecutorWorkloadTimeout() without timeout: %v", err)
	}
	if !slices.Equal(withoutTimeout, base) {
		t.Fatalf("environment without timeout = %v, want %v", withoutTimeout, base)
	}
	workloadContext, err := WithExecutorWorkloadTimeout(context.Background(), 10*time.Minute)
	if err != nil {
		t.Fatalf("WithExecutorWorkloadTimeout() error = %v", err)
	}
	withTimeout, err := appendExecutorWorkloadTimeout(workloadContext, base)
	if err != nil {
		t.Fatalf("appendExecutorWorkloadTimeout() error = %v", err)
	}
	want := []string{"CI=true", ExecutorWorkloadTimeoutEnvironment + "=10m0s"}
	if !slices.Equal(withTimeout, want) {
		t.Fatalf("environment with timeout = %v, want %v", withTimeout, want)
	}
}

func TestExecutorEnvironmentUsesDistinctPrivateViteCachePerShard(t *testing.T) {
	readCacheDir := func(environment []string) string {
		for _, variable := range environment {
			if value, ok := strings.CutPrefix(variable, "SUPER_DOLPHIN_VITE_CACHE_DIR="); ok {
				return value
			}
		}
		return ""
	}
	first := executorEnvironment(newExecutorLayout("/workspace/work/s184"), executorSearchPath, "/workspace/work/s184/go-mod-cache", ExecutorGoRoot, ExecutorRuntimeSeedRoot+"/frontend/node_modules", "")
	second := executorEnvironment(newExecutorLayout("/workspace/work/s185"), executorSearchPath, "/workspace/work/s185/go-mod-cache", ExecutorGoRoot, ExecutorRuntimeSeedRoot+"/frontend/node_modules", "")
	firstDir := readCacheDir(first)
	secondDir := readCacheDir(second)
	if firstDir == "" || secondDir == "" || firstDir == secondDir {
		t.Fatalf("per-shard Vite cache dirs = %q, %q; want distinct non-empty paths", firstDir, secondDir)
	}
	for name, dir := range map[string]string{"first": firstDir, "second": secondDir} {
		if filepath.Base(dir) != ".vite-temp" || strings.Contains(dir, string(filepath.Separator)+"node_modules"+string(filepath.Separator)+".vite") {
			t.Fatalf("%s Vite cache dir = %q; want private .vite-temp outside image .vite", name, dir)
		}
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
					ExecutorGoRoot,
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
