package remoteci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const remoteBuilderRequestKeyEnvironment = "SUPER_DOLPHIN_REMOTE_BUILDER_REQUEST_KEY"

// remoteCandidateTestBinaryBuilderBootstrapSH verifies the candidate CLI before
// using it to materialize and compile in the Linux builder ECI group.
const remoteCandidateTestBinaryBuilderBootstrapSH = `set -eu; candidate="/candidate-bootstrap/${SUPER_DOLPHIN_REMOTE_CANDIDATE_CLI_KEY##*/}"; bootstrap_cli="$TMPDIR/candidate-super-dolphin-gate"; test "$(sha256sum "$candidate" | awk '{print $1}')" = "${SUPER_DOLPHIN_REMOTE_CANDIDATE_CLI_SHA256#sha256:}"; test "$(wc -c < "$candidate" | tr -d ' ')" = "$SUPER_DOLPHIN_REMOTE_CANDIDATE_CLI_SIZE"; cp "$candidate" "$bootstrap_cli"; chmod 0755 "$bootstrap_cli"; expected="$(printf 'gate_source_sha256=%s\nplatform=linux/amd64\ntoolchain_digest=%s' "$SUPER_DOLPHIN_CANDIDATE_GATE_SOURCE_SHA256" "$SUPER_DOLPHIN_CANDIDATE_GATE_TOOLCHAIN_SHA256")"; test "$("$bootstrap_cli" worker cli-identity)" = "$expected"; exec "$bootstrap_cli" _remote-build-test-binaries`

func remoteCandidateTestBinaryTargets(shards []gate.ContainerShard) ([]CandidateTestBinaryBuildTarget, error) {
	seen := make(map[string]CandidateTestBinaryBuildTarget)
	for _, shard := range shards {
		for _, id := range shard.GateIDs {
			parent, kind, target, targeted, err := gate.ParseWorkloadID(string(id))
			if err != nil {
				return nil, err
			}
			if !targeted || parent != gate.GateIDBackendTestWithGuard || kind != gate.WorkloadTargetGoTest {
				continue
			}
			goTarget, err := gate.ParseGoTestTarget(target)
			if err != nil {
				return nil, err
			}
			candidate := CandidateTestBinaryBuildTarget{Package: goTarget.Package, Mode: "test", CGOEnabled: true}
			seen[candidate.Package+"\x00"+candidate.Mode] = candidate
		}
	}
	targets := make([]CandidateTestBinaryBuildTarget, 0, len(seen))
	for _, target := range seen {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(left, right int) bool { return targets[left].Package < targets[right].Package })
	return targets, nil
}

// buildCandidateTestBinaryArtifacts uses the injected builder only as a test
// double. Production always compiles in its single dedicated Linux ECI group.
func (coordinator *Coordinator) buildCandidateTestBinaryArtifacts(ctx context.Context, input RunInput, shards []gate.ContainerShard, jobID, tempRoot string, assets *remoteAssets, objectKeys, groups *[]string) error {
	if coordinator.config.CandidateTestBinaryBuilder != nil {
		built, err := buildRemoteCandidateTestBinaryArtifacts(ctx, coordinator.config.CandidateTestBinaryBuilder, input, shards, jobID, tempRoot, coordinator.config.SourcePrefix)
		if err != nil {
			return err
		}
		assets.candidateTestBinaries = built
		assets.candidateTestBinaryRefs = make([]CandidateTestBinaryArtifactRef, len(built))
		for index, asset := range built {
			assets.candidateTestBinaryRefs[index] = asset.ref
		}
		return coordinator.uploadCandidateTestBinaryAssets(ctx, built, objectKeys)
	}
	targets, err := remoteCandidateTestBinaryTargets(shards)
	if err != nil || len(targets) == 0 {
		return err
	}
	request := candidateTestBinaryBuilderRequest(input, jobID, assets, targets, coordinator.config.SourcePrefix)
	data, digest, err := EncodeCandidateTestBinaryBuilderRequest(request)
	if err != nil {
		return err
	}
	requestPath := filepath.Join(tempRoot, "candidate-test-binary-builder.request.json")
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		return fmt.Errorf("write remote candidate test binary builder request: %w", err)
	}
	requestKey := coordinator.config.SourcePrefix + jobID + "/candidate-test-binary-builder.request.json"
	if err := coordinator.store.Create(ctx, requestPath, requestKey); err != nil {
		return fmt.Errorf("upload remote candidate test binary builder request: %w", err)
	}
	*objectKeys = append(*objectKeys, requestKey)
	group, err := coordinator.runtime.CreateContainerGroup(ctx, coordinator.candidateTestBinaryBuilderCreateRequest(jobID, requestKey, digest, assets.candidateCLI, input))
	if err != nil {
		return fmt.Errorf("create remote candidate test binary builder: %w", err)
	}
	*groups = append(*groups, group.ID)
	result, err := coordinator.waitCandidateTestBinaryBuilder(ctx, group.ID, request, tempRoot)
	if err != nil {
		return err
	}
	assets.candidateTestBinaryBuilds = slices.Clone(result.Builds)
	assets.candidateTestBinaryRefs = make([]CandidateTestBinaryArtifactRef, len(result.Builds))
	for index, build := range result.Builds {
		assets.candidateTestBinaryRefs[index] = build.Artifact
	}
	return nil
}

func candidateTestBinaryBuilderRequest(input RunInput, jobID string, assets *remoteAssets, targets []CandidateTestBinaryBuildTarget, sourcePrefix string) CandidateTestBinaryBuilderRequest {
	prefix := sourcePrefix + jobID + "/test-binaries/"
	return CandidateTestBinaryBuilderRequest{SchemaVersion: CandidateTestBinaryBuilderRequestSchemaVersion, JobID: jobID, CandidateTree: input.Tree, BaselineManifest: input.BaselineManifestDigest, OCIProjectCache: cloneBaselineOCIProjectCache(input.OCIProjectCache), RunnerBaseCommit: assets.artifact.Manifest.BaseCommit, RunnerBaseTree: assets.artifact.Manifest.BaseTree, PatchFormat: assets.artifact.Manifest.PatchFormat, PatchKey: assets.patchKey, PatchSHA256: assets.artifact.Manifest.PatchSHA256, PatchSize: assets.artifact.Manifest.PatchSize, ManifestKey: assets.manifestKey, ManifestSHA256: assets.manifestDigest, CandidateCLI: assets.candidateCLI, CGOEnabled: true, Targets: targets, OutputPrefix: prefix}
}

func (coordinator *Coordinator) candidateTestBinaryBuilderCreateRequest(jobID, requestKey, requestDigest string, candidateCLI CandidateCLIArtifactRef, input RunInput) eci.CreateRequest {
	class := coordinator.config.ResourcePolicy.Classes[len(coordinator.config.ResourcePolicy.Classes)-1]
	builderShard := gate.ContainerShard{Index: 0, IdentityDigest: "sha256:" + strings.Repeat("0", 64), Profile: gate.ProfileLocalFast, PlanDigest: "sha256:" + strings.Repeat("0", 64), GateIDs: []gate.GateID{"builder"}}
	request := coordinator.createRequest(jobID, builderShard, eci.Resources{CPU: class.VCPU, MemoryGiB: class.MemoryGiB}, requestKey, requestDigest, candidateCLI, input)
	request.ContainerGroupName = fmt.Sprintf("sdci-%s-builder", strings.TrimPrefix(jobID, "job-"))
	request.Command, request.Args = []string{"/bin/sh"}, []string{"-c", "exit 0"}
	request.Tags["super-dolphin-builder"] = "candidate-test-binary"
	request.InitContainer.Args = []string{"-c", remoteCandidateTestBinaryBuilderBootstrapSH}
	request.InitContainer.Environment[remoteBuilderRequestKeyEnvironment] = requestKey
	return request
}

func (coordinator *Coordinator) waitCandidateTestBinaryBuilder(ctx context.Context, groupID string, request CandidateTestBinaryBuilderRequest, tempRoot string) (CandidateTestBinaryBuilderResult, error) {
	timer := time.NewTicker(coordinator.config.PollInterval)
	defer timer.Stop()
	for {
		group, err := coordinator.shardStatus(ctx, groupID)
		if err != nil {
			return CandidateTestBinaryBuilderResult{}, fmt.Errorf("observe remote candidate test binary builder: %w", err)
		}
		if terminalECIStatus(group.Status) {
			if group.Status != "Succeeded" {
				log, logErr := coordinator.runtime.DescribeContainerLog(ctx, groupID, "materializer")
				return CandidateTestBinaryBuilderResult{}, errors.Join(fmt.Errorf("remote candidate test binary builder terminal status %q", group.Status), logErr, errors.New(remoteShardLogTail(log)))
			}
			path := filepath.Join(tempRoot, "candidate-test-binary-builder.result.json")
			found, downloadErr := coordinator.store.DownloadIfExists(ctx, request.OutputPrefix+"builder.result.json", path)
			if downloadErr != nil || !found {
				return CandidateTestBinaryBuilderResult{}, fmt.Errorf("download remote candidate test binary builder result: found=%t: %w", found, downloadErr)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return CandidateTestBinaryBuilderResult{}, fmt.Errorf("read remote candidate test binary builder result: %w", readErr)
			}
			return DecodeCandidateTestBinaryBuilderResult(data, request)
		}
		select {
		case <-ctx.Done():
			return CandidateTestBinaryBuilderResult{}, remoteCloudShardPendingError(group.Status, ctx.Err())
		case <-timer.C:
		}
	}
}

func (coordinator *Coordinator) uploadCandidateTestBinaryAssets(ctx context.Context, assets []candidateTestBinaryAsset, objectKeys *[]string) error {
	for _, asset := range assets {
		for _, item := range []struct{ path, key, label string }{{asset.binaryPath, asset.ref.BinaryKey, "candidate test binary"}, {asset.manifestPath, asset.ref.ManifestKey, "candidate test binary manifest"}} {
			if err := coordinator.store.Create(ctx, item.path, item.key); err != nil {
				return fmt.Errorf("upload remote CI %s: %w", item.label, err)
			}
			*objectKeys = append(*objectKeys, item.key)
		}
	}
	return nil
}
