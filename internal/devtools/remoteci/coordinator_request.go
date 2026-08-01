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

// buildShardRequests 将 canonical 分片绑定到同一作业目录下的内容寻址对象。
func buildShardRequests(
	sourcePrefix string,
	jobID string,
	shards []gate.ContainerShard,
	artifact source.Artifact,
	patchKey string,
	manifestKey string,
	manifestDigest string,
	input RunInput,
) ([]ShardRequest, []string, error) {
	requests := make([]ShardRequest, len(shards))
	keys := make([]string, len(shards))
	jobPrefix := sourcePrefix + jobID + "/"
	for index, shard := range shards {
		keys[index] = fmt.Sprintf("%sshard-%02d.request.json", jobPrefix, index)
		requests[index] = ShardRequest{
			SchemaVersion: ShardRequestSchemaVersion, JobID: jobID, ShardIdentity: shard.IdentityDigest,
			Profile: shard.Profile, PlanDigest: shard.PlanDigest, SourceTreeSHA: shard.SourceTreeSHA,
			BaselineManifest: input.BaselineManifestDigest,
			AnchorGeneration: input.AnchorGeneration, AnchorManifest: input.AnchorManifest,
			AnchorCommit: input.AnchorCommit, AnchorTree: input.AnchorTree,
			BaselineDeltas:   slices.Clone(input.BaselineDeltas),
			RunnerBaseCommit: artifact.Manifest.BaseCommit, RunnerBaseTree: artifact.Manifest.BaseTree,
			PatchFormat: artifact.Manifest.PatchFormat,
			PatchKey:    patchKey, PatchSHA256: artifact.Manifest.PatchSHA256, PatchSize: artifact.Manifest.PatchSize,
			ManifestKey: manifestKey, ManifestSHA256: manifestDigest, GateIDs: slices.Clone(shard.GateIDs),
		}
		if err := requests[index].Validate(); err != nil {
			return nil, nil, err
		}
	}
	return requests, keys, nil
}

const (
	remoteCurrentGateMountPath      = "/current-gate"
	remoteCurrentGateDigestEnv      = "SUPER_DOLPHIN_CURRENT_GATE_SHA256"
	remoteCurrentGateVolumeName     = "current-gate"
	remoteCandidateGateSourceEnv    = "SUPER_DOLPHIN_CANDIDATE_GATE_SOURCE_SHA256"
	remoteCandidateGateToolchainEnv = "SUPER_DOLPHIN_CANDIDATE_GATE_TOOLCHAIN_SHA256"
	remoteReuseBaselineGateEnv      = "SUPER_DOLPHIN_REUSE_BASELINE_GATE_CLI"
	remoteWritableTempMountPath     = "/tmp"
	remoteXKBCompMountPath          = "/usr/bin/xkbcomp"
	remoteXKBCompSubPath            = "runtime/rootfs/usr/bin/xkbcomp"
	remoteXKBDataMountPath          = "/usr/share/X11/xkb"
	remoteXKBDataSubPath            = "runtime/rootfs/usr/share/X11/xkb"
	remoteInitSearchPath            = gate.ExecutorRuntimeSeedRoot + "/bin:" + gate.ExecutorPortableRootFS + "/usr/bin:" + gate.ExecutorPortableRootFS + "/bin:/usr/local/bin:/usr/bin:/bin"
	remoteCandidateGateBootstrapSH  = `set -eu
materializer=/bootstrap/bin/super-dolphin-gate
if test -f /current-gate/bin/super-dolphin-gate; then
  cp /current-gate/bin/super-dolphin-gate /tmp/super-dolphin-gate-current
  test "sha256:$(sha256sum /tmp/super-dolphin-gate-current | awk '{print $1}')" = "$SUPER_DOLPHIN_CURRENT_GATE_SHA256"
  chmod 0755 /tmp/super-dolphin-gate-current
  materializer=/tmp/super-dolphin-gate-current
fi
"$materializer" _remote-materialize

candidate_binary=/opt/super-dolphin-gate/bin/super-dolphin-gate
expected_identity=$(printf 'gate_source_sha256=%s\nplatform=linux/amd64\ntoolchain_digest=%s' \
  "$SUPER_DOLPHIN_CANDIDATE_GATE_SOURCE_SHA256" "$SUPER_DOLPHIN_CANDIDATE_GATE_TOOLCHAIN_SHA256")
verify_candidate_gate() {
  test "$("$1" worker cli-identity)" = "$expected_identity"
}
if test "$SUPER_DOLPHIN_REUSE_BASELINE_GATE_CLI" = true; then
  verify_candidate_gate "$candidate_binary"
  printf 'candidate gate CLI mode: baseline-reuse; source=%s\n' "$SUPER_DOLPHIN_CANDIDATE_GATE_SOURCE_SHA256"
  exit 0
fi

temporary=$(mktemp /opt/super-dolphin-gate/bin/.super-dolphin-gate-candidate-XXXXXX)
trap 'rm -f "$temporary"' EXIT HUP INT TERM
cd /workspace/source
env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOENV=off GOWORK=off GOTOOLCHAIN=local \
  GOPROXY=off GOSUMDB=off GOMODCACHE=/opt/super-dolphin-gate/runtime/go-mod-cache \
  GOCACHE=/opt/super-dolphin-gate/cache-seed/go-build GOTMPDIR=/tmp HOME=/tmp \
  /opt/super-dolphin-gate/runtime/go/bin/go build -mod=readonly -trimpath -buildvcs=false \
  -ldflags="-X main.gateSourceDigest=$SUPER_DOLPHIN_CANDIDATE_GATE_SOURCE_SHA256 -X main.gateToolchainDigest=$SUPER_DOLPHIN_CANDIDATE_GATE_TOOLCHAIN_SHA256" \
  -o "$temporary" ./cmd/super-dolphin-gate
verify_candidate_gate "$temporary"
chmod 0755 "$temporary"
mv "$temporary" "$candidate_binary"
trap - EXIT HUP INT TERM
printf 'candidate gate CLI mode: compiled; source=%s\n' "$SUPER_DOLPHIN_CANDIDATE_GATE_SOURCE_SHA256"`
)

// createRequest 将分片、请求摘要和 DataCache 身份绑定为 ECI 创建请求。
func (coordinator *Coordinator) createRequest(
	jobID string,
	shard gate.ContainerShard,
	resources eci.Resources,
	requestKey string,
	requestDigest string,
	input RunInput,
) eci.CreateRequest {
	groupName := fmt.Sprintf("sdci-%s-s%02d", strings.TrimPrefix(jobID, "job-"), shard.Index)
	initContainer := eci.InitContainer{
		Name:    "materializer",
		Command: []string{"/bin/sh"},
		Args:    []string{"-c", remoteCandidateGateBootstrapSH},
		Environment: map[string]string{
			"PATH":                                remoteInitSearchPath,
			"SSL_CERT_FILE":                       remoteDataCacheCAFile,
			"SUPER_DOLPHIN_RUNTIME_ROOT":          gate.ExecutorRuntimeSeedRoot,
			"SUPER_DOLPHIN_REMOTE_WORKER_ROLE":    coordinator.config.WorkerRoleName,
			"SUPER_DOLPHIN_REMOTE_OSS_ENDPOINT":   coordinator.config.InternalOSSEndpoint,
			"SUPER_DOLPHIN_REMOTE_OSS_BUCKET":     coordinator.config.Bucket,
			"SUPER_DOLPHIN_REMOTE_REQUEST_KEY":    requestKey,
			"SUPER_DOLPHIN_REMOTE_REQUEST_SHA256": requestDigest,
			"TMPDIR":                              remoteWritableTempMountPath,
			remoteBaselineManifestEnvironment:     input.AnchorManifest,
			remoteCurrentGateDigestEnv:            input.GateBinarySHA256,
			remoteCandidateGateSourceEnv:          input.CandidateGateSourceSHA256,
			remoteCandidateGateToolchainEnv:       input.CandidateGateToolchainSHA256,
			remoteReuseBaselineGateEnv:            strconv.FormatBool(input.ReuseBaselineGateCLI),
		},
	}
	initMounts := []eci.VolumeMount{
		{Name: "base-data", MountPath: "/bootstrap", ReadOnly: true},
		{Name: "expanded-data", MountPath: "/opt/super-dolphin-gate"},
		{Name: "source-data", MountPath: gate.ExecutorSourcePath},
		{Name: "work-data", MountPath: gate.ExecutorWorkRoot},
		{Name: "temp-data", MountPath: remoteWritableTempMountPath},
	}
	bootstrapVolume := remoteCurrentGateVolume(coordinator.config, input)
	if bootstrapVolume != (eci.OSSVolume{}) {
		initMounts = append(initMounts,
			eci.VolumeMount{Name: remoteCurrentGateVolumeName, MountPath: remoteCurrentGateMountPath, ReadOnly: true},
		)
	}
	return eci.CreateRequest{
		ContainerGroupName: groupName, ContainerName: "worker",
		Resources: resources,
		Command:   remoteWorkerSupervisorCommand("/opt/super-dolphin-gate/bin/super-dolphin-gate"),
		Args: []string{
			"worker", "run-shard", "--profile", string(shard.Profile), "--plan-digest", shard.PlanDigest,
			"--gates", joinGateIDs(shard.GateIDs),
		},
		Environment:     remoteWorkerEnvironment(coordinator.config.WorkerTimeout),
		Tags:            map[string]string{"super-dolphin-job": jobID, "super-dolphin-shard": fmt.Sprintf("%d", shard.Index)},
		DataCacheBucket: input.DataCacheBucket,
		InitContainer:   initContainer,
		BaseVolume:      eci.HostPathVolume{Name: "base-data", Path: input.DataCachePath, Type: "Directory"},
		BootstrapVolume: bootstrapVolume,
		ExpandedVolume:  eci.EmptyDirVolume{Name: "expanded-data"},
		SourceVolume:    eci.EmptyDirVolume{Name: "source-data"},
		WorkVolume:      eci.EmptyDirVolume{Name: "work-data"},
		TempVolume:      eci.EmptyDirVolume{Name: "temp-data"},
		MainVolumeMounts: []eci.VolumeMount{
			{Name: "base-data", MountPath: "/bootstrap", ReadOnly: true},
			{Name: "expanded-data", MountPath: "/opt/super-dolphin-gate", ReadOnly: true},
			{Name: "expanded-data", MountPath: remoteXKBCompMountPath, SubPath: remoteXKBCompSubPath, ReadOnly: true},
			{Name: "expanded-data", MountPath: remoteXKBDataMountPath, SubPath: remoteXKBDataSubPath, ReadOnly: true},
			{Name: "source-data", MountPath: gate.ExecutorSourcePath, ReadOnly: true},
			{Name: "work-data", MountPath: gate.ExecutorWorkRoot},
			{Name: "temp-data", MountPath: remoteWritableTempMountPath},
		},
		InitVolumeMounts: initMounts,
	}
}

// remoteWorkerEnvironment 仅绑定 worker 入口所需的运行时根与单目标超时。
func remoteWorkerEnvironment(timeout time.Duration) map[string]string {
	return map[string]string{
		gate.ExecutorWorkloadTimeoutEnvironment: timeout.String(),
		"SUPER_DOLPHIN_RUNTIME_ROOT":            gate.ExecutorRuntimeSeedRoot,
	}
}

func remoteCurrentGateVolume(config CoordinatorConfig, input RunInput) eci.OSSVolume {
	if len(input.BaselineDeltas) == 0 {
		return eci.OSSVolume{}
	}
	latest := input.BaselineDeltas[len(input.BaselineDeltas)-1]
	return eci.OSSVolume{
		Bucket: config.Bucket, Endpoint: strings.TrimPrefix(config.InternalOSSEndpoint, "https://"),
		Path: path.Join("/", latest.ObjectPrefix, "output"), RoleName: config.WorkerRoleName,
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
