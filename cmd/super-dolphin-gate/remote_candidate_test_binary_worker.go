package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/workerio"
)

const remoteBuilderRequestKeyEnv = "SUPER_DOLPHIN_REMOTE_BUILDER_REQUEST_KEY"

func runRemoteBuildTestBinaries(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return protocolError("_remote-build-test-binaries does not accept arguments")
	}
	config, err := loadRemoteMaterializeConfig(os.LookupEnv)
	if err != nil {
		return err
	}
	requestKey, ok := os.LookupEnv(remoteBuilderRequestKeyEnv)
	if !ok || !validRemoteRequestObjectKey(requestKey) {
		return errors.New("remote builder request key is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteMaterializeTimeout)
	defer cancel()
	transfer := func(_ context.Context, key string, max int64, destination io.Writer) (int64, error) {
		c, err := workerio.NewClient(workerio.Config{RoleName: config.RoleName, Endpoint: config.Endpoint, Bucket: config.Bucket, Key: key, MaxBytes: max}, workerio.Dependencies{})
		if err != nil {
			return 0, err
		}
		return c.Download(ctx, destination)
	}
	var data bytes.Buffer
	if _, err := transfer(ctx, requestKey, remoteRequestMaxBytes, &data); err != nil {
		return fmt.Errorf("download builder request: %w", err)
	}
	request, err := remoteci.DecodeCandidateTestBinaryBuilderRequest(data.Bytes())
	if err != nil {
		return err
	}
	shard := builderRequestShard(request)
	if err := materializeRemoteBaseline(ctx, remoteExpandedBasePath, gatecontract.ExecutorSourcePath, gatecontract.ExecutorWorkRoot, shard, transfer); err != nil {
		return err
	}
	temp, manifestPath, patchPath, err := stageRemoteSourceObjects(ctx, gatecontract.ExecutorWorkRoot, shard, transfer)
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if err := verifyRemoteMaterializedSource(ctx, gatecontract.ExecutorSourcePath, manifestPath, patchPath, shard); err != nil {
		return err
	}
	if _, err := materializeRemoteCandidateCLIArtifact(ctx, remoteExpandedBasePath, request.CandidateCLI.ManifestKey, request.CandidateCLI.ManifestSHA256, request.CandidateTree, request.CandidateCLI.SourceSHA256, request.CandidateCLI.ToolchainSHA256, transfer); err != nil {
		return err
	}
	result, files, err := buildRemoteWorkerCandidateTestBinaries(ctx, request)
	if err != nil {
		return err
	}
	for key, file := range files {
		if err := remoteBuilderUpload(ctx, config, key, file); err != nil {
			return err
		}
	}
	encoded, _, err := remoteci.EncodeCandidateTestBinaryBuilderResult(result)
	if err != nil {
		return err
	}
	resultPath := filepath.Join(gatecontract.ExecutorWorkRoot, "builder.result.json")
	if err := os.WriteFile(resultPath, encoded, 0o600); err != nil {
		return err
	}
	if err := remoteBuilderUpload(ctx, config, request.OutputPrefix+"builder.result.json", resultPath); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "candidate test binaries built job=%s tree=%s\n", request.JobID, request.CandidateTree)
	return err
}

func builderRequestShard(request remoteci.CandidateTestBinaryBuilderRequest) remoteci.ShardRequest {
	return remoteci.ShardRequest{SchemaVersion: remoteci.ShardRequestSchemaVersion, JobID: request.JobID, ShardIdentity: "sha256:" + strings.Repeat("0", 64), Profile: "local-fast", PlanDigest: "sha256:" + strings.Repeat("0", 64), BaselineManifest: request.BaselineManifest, OCIProjectCache: request.OCIProjectCache, RunnerBaseCommit: request.RunnerBaseCommit, RunnerBaseTree: request.RunnerBaseTree, SourceTreeSHA: request.CandidateTree, PatchFormat: request.PatchFormat, PatchKey: request.PatchKey, PatchSHA256: request.PatchSHA256, PatchSize: request.PatchSize, ManifestKey: request.ManifestKey, ManifestSHA256: request.ManifestSHA256, CandidateCLI: request.CandidateCLI, GateIDs: []gatecontract.GateID{"builder"}}
}

func remoteBuilderUpload(ctx context.Context, config remoteMaterializeConfig, key, file string) error {
	input, err := os.Open(file)
	if err != nil {
		return err
	}
	defer input.Close()
	client, err := workerio.NewClient(workerio.Config{RoleName: config.RoleName, Endpoint: config.Endpoint, Bucket: config.Bucket, Key: key, MaxBytes: 512 << 20}, workerio.Dependencies{})
	if err != nil {
		return err
	}
	_, err = client.Upload(ctx, input)
	return err
}

func buildRemoteWorkerCandidateTestBinaries(ctx context.Context, request remoteci.CandidateTestBinaryBuilderRequest) (remoteci.CandidateTestBinaryBuilderResult, map[string]string, error) {
	goBinary := filepath.Join(gatecontract.ExecutorPortableGoRoot, "bin", "go")
	if err := verifyRemoteBuilderToolchain(ctx, goBinary, gatecontract.ExecutorPortableGoRoot, gatecontract.ExecutorSourcePath, request.CandidateCLI.ToolchainSHA256, runRemoteBuilderGoOutput); err != nil {
		return remoteci.CandidateTestBinaryBuilderResult{}, nil, err
	}
	private := filepath.Join(gatecontract.ExecutorWorkRoot, "builder-gocache")
	if err := os.MkdirAll(private, 0700); err != nil {
		return remoteci.CandidateTestBinaryBuilderResult{}, nil, err
	}
	if request.OCIProjectCache == nil {
		return remoteci.CandidateTestBinaryBuilderResult{}, nil, errors.New("candidate test builder OCI project cache is required")
	}
	ociCacheRoot := request.OCIProjectCache.CachePath
	executable, err := os.Executable()
	if err != nil || !filepath.IsAbs(executable) {
		return remoteci.CandidateTestBinaryBuilderResult{}, nil, errors.New("candidate builder cache proxy executable is invalid")
	}
	launcher := strconv.Quote(executable) + " worker go-cache-proxy"
	// Match the Linux ECI executor. Missing native dependencies are an image
	// failure, never a reason to alter candidate-test semantics.
	env := []string{"GOENV=off", "GOTOOLCHAIN=local", "GOROOT=" + gatecontract.ExecutorPortableGoRoot, "GOMODCACHE=" + filepath.Join(gatecontract.ExecutorRuntimeSeedRoot, "go-mod-cache"), "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=1", "GOCACHE=" + private, "PATH=" + gatecontract.ExecutorPortableSearchPath, "HOME=" + gatecontract.ExecutorWorkRoot, "TMPDIR=" + gatecontract.ExecutorWorkRoot}
	if !request.CGOEnabled {
		return remoteci.CandidateTestBinaryBuilderResult{}, nil, errors.New("candidate test builder requires CGO_ENABLED=1")
	}
	result := remoteci.CandidateTestBinaryBuilderResult{SchemaVersion: remoteci.CandidateTestBinaryBuilderResultSchemaVersion, JobID: request.JobID, CandidateTree: request.CandidateTree, Platform: "linux/amd64", GoToolchain: gatecontract.RequiredGoToolchain, CGOEnabled: true, ToolchainSHA256: request.CandidateCLI.ToolchainSHA256}
	files := map[string]string{}
	for index, target := range request.Targets {
		if !target.CGOEnabled {
			return result, nil, errors.New("candidate test builder target requires CGO_ENABLED=1")
		}
		listMetricsPath, err := gatecontract.GoBuildCacheProxyMetricsPathForInvocation(private, "builder-list-"+strconv.Itoa(index))
		if err != nil {
			return result, nil, err
		}
		buildMetricsPath, err := gatecontract.GoBuildCacheProxyMetricsPathForInvocation(private, "builder-build-"+strconv.Itoa(index))
		if err != nil {
			return result, nil, err
		}
		var cacheCommand strings.Builder
		cacheCommand.WriteString(launcher)
		cacheCommand.WriteString(" --seed ")
		cacheCommand.WriteString(strconv.Quote(ociCacheRoot))
		cacheCommand.WriteString(" --private ")
		cacheCommand.WriteString(strconv.Quote(private))
		cacheCommandText := cacheCommand.String()
		listEnv := append(append([]string(nil), env...), "GOCACHEPROG="+cacheCommandText+" --metrics "+strconv.Quote(listMetricsPath))
		listStart := time.Now()
		closureData, err := runRemoteBuilderGoOutput(ctx, goBinary, []string{"list", "-deps", "-json", "-test", target.Package}, listEnv)
		if err != nil {
			return result, nil, err
		}
		closure, err := semanticGoTestCompileClosure(gatecontract.ExecutorSourcePath, closureData)
		if err != nil {
			return result, nil, fmt.Errorf("digest candidate test compile closure: %w", err)
		}
		listMS := uint64(time.Since(listStart).Milliseconds())
		graph := filepath.Join(private, "action-"+strconv.Itoa(index)+".json")
		binary := filepath.Join(gatecontract.ExecutorWorkRoot, fmt.Sprintf("candidate-%02d.test-bin", index))
		buildStart := time.Now()
		buildEnv := append(append([]string(nil), env...), "GOCACHEPROG="+cacheCommandText+" --metrics "+strconv.Quote(buildMetricsPath))
		if err := runRemoteBuilderGo(ctx, goBinary, []string{"test", "-c", "-mod=readonly", "-buildvcs=false", "-trimpath", "-debug-actiongraph=" + graph, "-o", binary, target.Package}, buildEnv); err != nil {
			return result, nil, err
		}
		buildWallMS := uint64(time.Since(buildStart).Milliseconds())
		graphMetrics, err := readCandidateTestBinaryActionGraph(graph)
		if err != nil {
			return result, nil, err
		}
		listCache, err := gatecontract.LoadGoBuildCacheProxyMetricsAt(private, listMetricsPath, []string{ociCacheRoot})
		if err != nil {
			return result, nil, err
		}
		buildCache, err := gatecontract.LoadGoBuildCacheProxyMetricsAt(private, buildMetricsPath, []string{ociCacheRoot})
		if err != nil {
			return result, nil, err
		}
		cache := mergeRemoteBuilderCacheMetrics(listCache, buildCache)
		privateIdentity, err := gatecontract.RuntimeSeedTreeDigest(private)
		if err != nil {
			return result, nil, err
		}
		manifest, manifestPath, err := remoteBuilderManifest(request, target, binary, closure)
		if err != nil {
			return result, nil, err
		}
		result.Builds = append(result.Builds, remoteci.CandidateTestBinaryBuilderBuild{Artifact: manifest, Metrics: remoteci.CandidateTestBinaryBuildMetrics{GoListWallMS: listMS, BuildWallMS: buildWallMS, CompileActionMS: graphMetrics.compileActionMS, LinkActionMS: graphMetrics.linkActionMS, CompileCriticalWallMS: graphMetrics.compileCriticalWallMS, GOCachePrivateHits: cache.PrivateHitCount, GOCacheOCIProjectCacheHits: cache.BaselineHitCount, GOCacheMisses: cache.MissCount, GOCachePuts: cache.PutCount, GOCachePrivateRootIdentity: privateIdentity}})
		files[manifest.BinaryKey] = binary
		files[manifest.ManifestKey] = manifestPath
	}
	if err := result.ValidateAgainst(request); err != nil {
		return result, nil, err
	}
	return result, files, nil
}

func mergeRemoteBuilderCacheMetrics(left, right gatecontract.GoBuildCacheProxyMetrics) gatecontract.GoBuildCacheProxyMetrics {
	merged := left
	merged.PrivateHitCount += right.PrivateHitCount
	merged.BaselineHitCount += right.BaselineHitCount
	merged.MissCount += right.MissCount
	merged.PutCount += right.PutCount
	return merged
}
func runRemoteBuilderGo(ctx context.Context, binary string, args, env []string) error {
	_, err := runRemoteBuilderGoOutput(ctx, binary, args, env)
	return err
}
func runRemoteBuilderGoOutput(ctx context.Context, binary string, args, env []string) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = gatecontract.ExecutorSourcePath
	command.Env = env
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("candidate test builder go %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

// verifyRemoteBuilderToolchain binds the running portable Go installation to the
// candidate's exact toolchain-lock digest before it compiles a test binary.
// CandidateGateToolchainSHA256 is the SHA-256 of build/gate/toolchain.lock, not
// a hash that can be reconstructed from GOROOT alone.
func verifyRemoteBuilderToolchain(
	ctx context.Context,
	goBinary string,
	goRoot string,
	sourceRoot string,
	expectedDigest string,
	run func(context.Context, string, []string, []string) ([]byte, error),
) error {
	if ctx == nil || run == nil || !filepath.IsAbs(goBinary) || !filepath.IsAbs(goRoot) || !filepath.IsAbs(sourceRoot) {
		return errors.New("candidate test builder toolchain verification input is invalid")
	}
	lock, err := os.ReadFile(filepath.Join(sourceRoot, "build", "gate", "toolchain.lock"))
	if err != nil {
		return fmt.Errorf("read candidate test builder toolchain lock: %w", err)
	}
	actualDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(lock))
	if actualDigest != expectedDigest {
		return errors.New("candidate test builder toolchain lock identity drift")
	}
	env := []string{"GOENV=off", "GOTOOLCHAIN=local", "GOROOT=" + goRoot, "PATH=" + gatecontract.ExecutorPortableSearchPath}
	version, err := run(ctx, goBinary, []string{"version"}, env)
	if err != nil || strings.TrimSpace(string(version)) != "go version "+gatecontract.RequiredGoToolchain+" linux/amd64" {
		return errors.Join(errors.New("candidate test builder Go version drift"), err)
	}
	identity, err := run(ctx, goBinary, []string{"env", "GOROOT", "GOTOOLDIR"}, env)
	if err != nil {
		return fmt.Errorf("read candidate test builder GOROOT identity: %w", err)
	}
	fields := strings.Fields(string(identity))
	wantToolDir := filepath.Join(goRoot, "pkg", "tool", "linux_amd64")
	if len(fields) != 2 || fields[0] != goRoot || fields[1] != wantToolDir {
		return errors.New("candidate test builder GOROOT identity drift")
	}
	return nil
}

func remoteBuilderManifest(request remoteci.CandidateTestBinaryBuilderRequest, target remoteci.CandidateTestBinaryBuildTarget, binary, closure string) (remoteci.CandidateTestBinaryArtifactRef, string, error) {
	data, err := os.ReadFile(binary)
	if err != nil {
		return remoteci.CandidateTestBinaryArtifactRef{}, "", err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	key := path.Join(request.OutputPrefix, digest+".test-bin")
	manifest := remoteci.CandidateTestBinaryArtifactManifest{SchemaVersion: remoteci.CandidateTestBinaryArtifactSchemaVersion, CandidateTree: request.CandidateTree, Package: target.Package, Mode: target.Mode, Platform: "linux/amd64", GoToolchain: gatecontract.RequiredGoToolchain, CGOEnabled: true, ToolchainSHA256: request.CandidateCLI.ToolchainSHA256, BuildFlags: []string{"-mod=readonly", "-buildvcs=false", "-trimpath"}, CompileClosureSHA256: closure, BinaryKey: key, BinarySHA256: "sha256:" + digest, BinarySize: int64(len(data))}
	encoded, mdigest, err := remoteci.EncodeCandidateTestBinaryArtifactManifest(manifest)
	if err != nil {
		return remoteci.CandidateTestBinaryArtifactRef{}, "", err
	}
	manifestPath := binary + ".manifest.json"
	if err := os.WriteFile(manifestPath, encoded, 0600); err != nil {
		return remoteci.CandidateTestBinaryArtifactRef{}, "", err
	}
	return remoteci.CandidateTestBinaryArtifactRef{CandidateTree: request.CandidateTree, Package: target.Package, Mode: target.Mode, Platform: "linux/amd64", GoToolchain: gatecontract.RequiredGoToolchain, CGOEnabled: true, ToolchainSHA256: request.CandidateCLI.ToolchainSHA256, BuildFlags: manifest.BuildFlags, CompileClosureSHA256: closure, ManifestKey: path.Join(request.OutputPrefix, strings.TrimPrefix(mdigest, "sha256:")+".manifest.json"), ManifestSHA256: strings.TrimPrefix(mdigest, "sha256:"), BinaryKey: key, BinarySHA256: digest, BinarySize: int64(len(data))}, manifestPath, nil
}
