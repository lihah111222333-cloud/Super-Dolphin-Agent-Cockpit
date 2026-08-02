package remoteci

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/source"
)

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
			ImageCacheID:     input.ImageCacheID,
			OCIProjectCache:  cloneBaselineOCIProjectCache(input.OCIProjectCache),
			RunnerBaseCommit: artifact.Manifest.BaseCommit, RunnerBaseTree: artifact.Manifest.BaseTree,
			BaselineRuntimeImage: input.RunnerImage, BaselineToolchainDigest: input.ToolchainDigest,
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
	// Deprecated names remain test-only compatibility identifiers; no production request mounts or executes them.
	remoteCurrentGateMountPath  = "/current-gate"
	remoteShardBootstrapSH      = `set -eu; accepted_gate="/opt/super-dolphin-gate/bin/super-dolphin-gate"; "$accepted_gate" _remote-materialize; private_cache="/workspace/work/go-cache"; built_gate="/workspace/work/bin/super-dolphin-gate"; mkdir -p "$private_cache" "$(dirname "$built_gate")"; started="$(date +%s%3N)"; cd /workspace/source; cache_proxy="$accepted_gate worker go-cache-proxy --seed /opt/super-dolphin/cache/go-build --private $private_cache --metrics $private_cache/shard-compile.metrics"; env GOCACHE="$private_cache" GOCACHEPROG="$cache_proxy" GOMODCACHE=/workspace/work/go-mod-cache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local CGO_ENABLED=0 /opt/super-dolphin-gate/runtime/go/bin/go build -mod=mod -trimpath -buildvcs=false -o "$built_gate" ./cmd/super-dolphin-gate; test -x "$built_gate"; finished="$(date +%s%3N)"; printf 'SUPER_DOLPHIN_SHARD_COMPILE duration_ms=%s cache_metrics=%s\n' "$((finished-started))" "$private_cache/shard-compile.metrics"`
	remoteWritableTempMountPath = "/tmp"
	remoteXKBCompMountPath      = "/usr/bin/xkbcomp"
	remoteXKBCompSubPath        = "runtime/rootfs/usr/bin/xkbcomp"
	remoteXKBDataMountPath      = "/usr/share/X11/xkb"
	remoteXKBDataSubPath        = "runtime/rootfs/usr/share/X11/xkb"
	remoteInitSearchPath        = gate.ExecutorRuntimeSeedRoot + "/bin:" + gate.ExecutorPortableRootFS + "/usr/bin:" + gate.ExecutorPortableRootFS + "/bin:/usr/local/bin:/usr/bin:/bin"
)

// createRequest binds one shard and its candidate request to an OCI-backed ECI group.
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
		Args:    []string{"-c", remoteShardBootstrapSH},
		Environment: map[string]string{
			"PATH":                                remoteInitSearchPath,
			"SUPER_DOLPHIN_RUNTIME_ROOT":          gate.ExecutorRuntimeSeedRoot,
			"SUPER_DOLPHIN_REMOTE_WORKER_ROLE":    coordinator.config.WorkerRoleName,
			"SUPER_DOLPHIN_REMOTE_OSS_ENDPOINT":   coordinator.config.InternalOSSEndpoint,
			"SUPER_DOLPHIN_REMOTE_OSS_BUCKET":     coordinator.config.Bucket,
			"SUPER_DOLPHIN_REMOTE_REQUEST_KEY":    requestKey,
			"SUPER_DOLPHIN_REMOTE_REQUEST_SHA256": requestDigest,
			"SUPER_DOLPHIN_REMOTE_SHARD_IDENTITY": shard.IdentityDigest,
			"TMPDIR":                              remoteWritableTempMountPath,
		},
	}
	initMounts := []eci.VolumeMount{
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
		ImageCacheID: input.ImageCacheID,
		MainImage:    input.RunnerImage,
		InitImage:    input.RunnerImage,
		Resources:    resources,
		Command:      remoteWorkerSupervisorCommand(gate.ExecutorWorkRoot + "/bin/super-dolphin-gate"),
		Args: []string{
			"worker", "run-shard", "--profile", string(shard.Profile), "--plan-digest", shard.PlanDigest,
			"--gates", joinGateIDs(shard.GateIDs),
		},
		Environment:      mainEnvironment,
		Tags:             map[string]string{"super-dolphin-job": jobID, "super-dolphin-shard": fmt.Sprintf("%d", shard.Index)},
		InitContainer:    initContainer,
		BootstrapVolume:  eci.OSSVolume{Bucket: coordinator.config.Bucket, Endpoint: strings.TrimPrefix(coordinator.config.InternalOSSEndpoint, "https://"), Path: "/" + path.Dir(requestKey), RoleName: coordinator.config.WorkerRoleName},
		ExpandedVolume:   eci.EmptyDirVolume{Name: "expanded-data"},
		SourceVolume:     eci.EmptyDirVolume{Name: "source-data"},
		WorkVolume:       eci.EmptyDirVolume{Name: "work-data"},
		TempVolume:       eci.EmptyDirVolume{Name: "temp-data"},
		MainVolumeMounts: mainMounts,
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

// joinGateIDs 将一个分片的 gate 标识编码为 worker CLI 所需的稳定逗号序列。
func joinGateIDs(ids []gate.GateID) string {
	values := make([]string, len(ids))
	for index, id := range ids {
		values[index] = string(id)
	}
	return strings.Join(values, ",")
}
