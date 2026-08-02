package remoteci

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/source"
)

func buildShardRequestsWithCandidate(
	sourcePrefix string,
	jobID string,
	shards []gate.ContainerShard,
	artifact source.Artifact,
	patchKey string,
	manifestKey string,
	manifestDigest string,
	candidateCLI CandidateCLIArtifactRef,
	candidateTestBinaries []CandidateTestBinaryArtifactRef,
	input RunInput,
) ([]ShardRequest, []string, error) {
	requests := make([]ShardRequest, len(shards))
	keys := make([]string, len(shards))
	jobPrefix := sourcePrefix + jobID + "/"
	for index, shard := range shards {
		shardBinaries, binaryErr := shardCandidateTestBinaries(shard, candidateTestBinaries)
		if binaryErr != nil {
			return nil, nil, binaryErr
		}
		keys[index] = fmt.Sprintf("%sshard-%02d.request.json", jobPrefix, index)
		requests[index] = ShardRequest{
			SchemaVersion: ShardRequestSchemaVersion, JobID: jobID, ShardIdentity: shard.IdentityDigest,
			Profile: shard.Profile, PlanDigest: shard.PlanDigest, SourceTreeSHA: shard.SourceTreeSHA,
			BaselineManifest: input.BaselineManifestDigest,
			OCIProjectCache:  cloneBaselineOCIProjectCache(input.OCIProjectCache),
			RunnerBaseCommit: artifact.Manifest.BaseCommit, RunnerBaseTree: artifact.Manifest.BaseTree,
			BaselineRuntimeImage: input.RunnerImage, BaselineToolchainDigest: input.ToolchainDigest,
			PatchFormat: artifact.Manifest.PatchFormat,
			PatchKey:    patchKey, PatchSHA256: artifact.Manifest.PatchSHA256, PatchSize: artifact.Manifest.PatchSize,
			ManifestKey: manifestKey, ManifestSHA256: manifestDigest, CandidateCLI: candidateCLI, CandidateTestBinaries: shardBinaries, GateIDs: slices.Clone(shard.GateIDs),
		}
		if err := requests[index].Validate(); err != nil {
			return nil, nil, err
		}
	}
	return requests, keys, nil
}

func shardCandidateTestBinaries(shard gate.ContainerShard, candidates []CandidateTestBinaryArtifactRef) ([]CandidateTestBinaryArtifactRef, error) {
	byPackage := make(map[string]CandidateTestBinaryArtifactRef, len(candidates))
	for _, ref := range candidates {
		byPackage[ref.Package] = ref
	}
	var selected []CandidateTestBinaryArtifactRef
	for _, id := range shard.GateIDs {
		parent, kind, target, targeted, err := gate.ParseWorkloadID(string(id))
		if err != nil {
			return nil, err
		}
		if !targeted || kind != gate.WorkloadTargetGoTest || parent != gate.GateIDBackendTestWithGuard {
			continue
		}
		goTarget, err := gate.ParseGoTestTarget(target)
		if err != nil {
			return nil, err
		}
		ref, ok := byPackage[goTarget.Package]
		if !ok || ref.Mode != "test" {
			return nil, fmt.Errorf("remote shard exact Go test %q has no candidate test binary", goTarget.Package)
		}
		if !slices.ContainsFunc(selected, func(value CandidateTestBinaryArtifactRef) bool {
			return value.Package == ref.Package && value.Mode == ref.Mode
		}) {
			selected = append(selected, ref)
		}
	}
	return selected, nil
}

const (
	// Deprecated names remain test-only compatibility identifiers; no production request mounts or executes them.
	remoteCurrentGateMountPath      = "/current-gate"
	remoteCurrentGateDigestEnv      = "SUPER_DOLPHIN_CURRENT_GATE_SHA256"
	remoteCurrentGateVolumeName     = "current-gate"
	remoteCandidateGateSourceEnv    = "SUPER_DOLPHIN_CANDIDATE_GATE_SOURCE_SHA256"
	remoteCandidateGateToolchainEnv = "SUPER_DOLPHIN_CANDIDATE_GATE_TOOLCHAIN_SHA256"
	remoteReuseBaselineGateEnv      = "SUPER_DOLPHIN_REUSE_BASELINE_GATE_CLI"
	remoteCandidateGateBootstrapSH  = `set -eu; candidate="/candidate-bootstrap/${SUPER_DOLPHIN_REMOTE_CANDIDATE_CLI_KEY##*/}"; bootstrap_cli="$TMPDIR/candidate-super-dolphin-gate"; test "$(sha256sum "$candidate" | awk '{print $1}')" = "${SUPER_DOLPHIN_REMOTE_CANDIDATE_CLI_SHA256#sha256:}"; test "$(wc -c < "$candidate" | tr -d ' ')" = "$SUPER_DOLPHIN_REMOTE_CANDIDATE_CLI_SIZE"; cp "$candidate" "$bootstrap_cli"; chmod 0755 "$bootstrap_cli"; expected="$(printf 'gate_source_sha256=%s\nplatform=linux/amd64\ntoolchain_digest=%s' "$SUPER_DOLPHIN_CANDIDATE_GATE_SOURCE_SHA256" "$SUPER_DOLPHIN_CANDIDATE_GATE_TOOLCHAIN_SHA256")"; test "$("$bootstrap_cli" worker cli-identity)" = "$expected"; exec "$bootstrap_cli" _remote-materialize`
	remoteWritableTempMountPath     = "/tmp"
	remoteXKBCompMountPath          = "/usr/bin/xkbcomp"
	remoteXKBCompSubPath            = "runtime/rootfs/usr/bin/xkbcomp"
	remoteXKBDataMountPath          = "/usr/share/X11/xkb"
	remoteXKBDataSubPath            = "runtime/rootfs/usr/share/X11/xkb"
	remoteInitSearchPath            = gate.ExecutorRuntimeSeedRoot + "/bin:" + gate.ExecutorPortableRootFS + "/usr/bin:" + gate.ExecutorPortableRootFS + "/bin:/usr/local/bin:/usr/bin:/bin"
	remoteCandidateTestBinaryIndex  = "/opt/super-dolphin-gate/test-binaries/candidate-test-binaries.json"
)

// createRequest binds one shard and its candidate request to an OCI-backed ECI group.
func (coordinator *Coordinator) createRequest(
	jobID string,
	shard gate.ContainerShard,
	resources eci.Resources,
	requestKey string,
	requestDigest string,
	candidateCLI CandidateCLIArtifactRef,
	input RunInput,
) eci.CreateRequest {
	groupName := fmt.Sprintf("sdci-%s-s%02d", strings.TrimPrefix(jobID, "job-"), shard.Index)
	initContainer := eci.InitContainer{
		Name:    "materializer",
		Command: []string{"/bin/sh"},
		Args:    []string{"-c", remoteCandidateGateBootstrapSH},
		Environment: map[string]string{
			"PATH":                                      remoteInitSearchPath,
			"SUPER_DOLPHIN_RUNTIME_ROOT":                gate.ExecutorRuntimeSeedRoot,
			"SUPER_DOLPHIN_REMOTE_WORKER_ROLE":          coordinator.config.WorkerRoleName,
			"SUPER_DOLPHIN_REMOTE_OSS_ENDPOINT":         coordinator.config.InternalOSSEndpoint,
			"SUPER_DOLPHIN_REMOTE_OSS_BUCKET":           coordinator.config.Bucket,
			"SUPER_DOLPHIN_REMOTE_REQUEST_KEY":          requestKey,
			"SUPER_DOLPHIN_REMOTE_REQUEST_SHA256":       requestDigest,
			"SUPER_DOLPHIN_REMOTE_SHARD_IDENTITY":       shard.IdentityDigest,
			"SUPER_DOLPHIN_REMOTE_CANDIDATE_CLI_KEY":    candidateCLI.BinaryKey,
			"SUPER_DOLPHIN_REMOTE_CANDIDATE_CLI_SHA256": candidateCLI.BinarySHA256,
			"SUPER_DOLPHIN_REMOTE_CANDIDATE_CLI_SIZE":   strconv.FormatInt(candidateCLI.BinarySize, 10),
			remoteCandidateGateSourceEnv:                candidateCLI.SourceSHA256,
			remoteCandidateGateToolchainEnv:             candidateCLI.ToolchainSHA256,
			"TMPDIR":                                    remoteWritableTempMountPath,
		},
	}
	initMounts := []eci.VolumeMount{
		{Name: remoteCurrentGateVolumeName, MountPath: "/candidate-bootstrap", ReadOnly: true},
		{Name: "expanded-data", MountPath: "/opt/super-dolphin-gate"},
		{Name: "source-data", MountPath: gate.ExecutorSourcePath},
		{Name: "work-data", MountPath: gate.ExecutorWorkRoot},
		{Name: "temp-data", MountPath: remoteWritableTempMountPath},
	}
	mainEnvironment := remoteWorkerEnvironment(coordinator.config.WorkerTimeout)
	mainMounts := []eci.VolumeMount{
		{Name: "expanded-data", MountPath: "/opt/super-dolphin-gate", ReadOnly: true},
		{Name: "expanded-data", MountPath: remoteXKBCompMountPath, SubPath: remoteXKBCompSubPath, ReadOnly: true},
		{Name: "expanded-data", MountPath: remoteXKBDataMountPath, SubPath: remoteXKBDataSubPath, ReadOnly: true},
		{Name: "source-data", MountPath: gate.ExecutorSourcePath, ReadOnly: true},
		{Name: "work-data", MountPath: gate.ExecutorWorkRoot},
		{Name: "temp-data", MountPath: remoteWritableTempMountPath},
	}
	return eci.CreateRequest{
		ContainerGroupName: groupName, ContainerName: "worker",
		MainImage: input.RunnerImage,
		InitImage: input.RunnerImage,
		Resources: resources,
		Command:   remoteWorkerSupervisorCommand("/opt/super-dolphin-gate/bin/super-dolphin-gate"),
		Args: []string{
			"worker", "run-shard", "--profile", string(shard.Profile), "--plan-digest", shard.PlanDigest,
			"--gates", joinGateIDs(shard.GateIDs),
		},
		Environment:      mainEnvironment,
		Tags:             map[string]string{"super-dolphin-job": jobID, "super-dolphin-shard": fmt.Sprintf("%d", shard.Index)},
		InitContainer:    initContainer,
		BootstrapVolume:  eci.OSSVolume{Bucket: coordinator.config.Bucket, Endpoint: strings.TrimPrefix(coordinator.config.InternalOSSEndpoint, "https://"), Path: "/" + path.Dir(candidateCLI.ManifestKey), RoleName: coordinator.config.WorkerRoleName},
		ExpandedVolume:   eci.EmptyDirVolume{Name: "expanded-data"},
		SourceVolume:     eci.EmptyDirVolume{Name: "source-data"},
		WorkVolume:       eci.EmptyDirVolume{Name: "work-data"},
		TempVolume:       eci.EmptyDirVolume{Name: "temp-data"},
		MainVolumeMounts: mainMounts,
		InitVolumeMounts: initMounts,
		RegistryAccess:   coordinator.config.RegistryAccess,
	}
}

// remoteWorkerEnvironment 仅绑定 worker 入口所需的运行时根与单目标超时。
func remoteWorkerEnvironment(timeout time.Duration) map[string]string {
	return map[string]string{
		gate.ExecutorWorkloadTimeoutEnvironment:     timeout.String(),
		"SUPER_DOLPHIN_RUNTIME_ROOT":                gate.ExecutorRuntimeSeedRoot,
		"SUPER_DOLPHIN_CANDIDATE_TEST_BINARY_INDEX": remoteCandidateTestBinaryIndex,
	}
}

// joinGateIDs 将一个分片的 gate 标识编码为 worker CLI 所需的稳定逗号序列。
func joinGateIDs(ids []gate.GateID) string {
	values := make([]string, len(ids))
	for index, id := range ids {
		values[index] = string(id)
	}
	return strings.Join(values, ",")
}
