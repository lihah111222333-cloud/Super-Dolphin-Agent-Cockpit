package remoteci

import (
	"errors"
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
			BaselineManifest:     input.BaselineManifestDigest,
			ImageCacheSnapshotID: input.ImageCacheSnapshotID,
			OCIProjectCache:      cloneBaselineOCIProjectCache(input.OCIProjectCache),
			RunnerBaseCommit:     artifact.Manifest.BaseCommit, RunnerBaseTree: artifact.Manifest.BaseTree,
			BaselineRuntimeImage: input.RunnerImage, BaselineToolchainDigest: input.ToolchainDigest,
			PatchFormat: artifact.Manifest.PatchFormat,
			PatchKey:    patchKey, PatchSHA256: artifact.Manifest.PatchSHA256, PatchSize: artifact.Manifest.PatchSize,
			ManifestKey: manifestKey, ManifestSHA256: manifestDigest,
			CandidateGateSourceSHA256:    input.CandidateGateSourceSHA256,
			CandidateGateToolchainSHA256: input.CandidateGateToolchainSHA256,
			GateIDs:                      slices.Clone(shard.GateIDs),
			Calibration:                  input.Calibration,
		}
		if input.Calibration {
			class := input.CalibrationResource
			requests[index].CalibrationResource = &class
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
	remoteShardBootstrapSH      = `set -eu; accepted_gate="/opt/super-dolphin-gate/bin/super-dolphin-gate"; "$accepted_gate" _remote-materialize; private_cache="/workspace/work/go-cache"; built_gate="/workspace/work/bin/super-dolphin-gate"; mkdir -p "$private_cache" "$(dirname "$built_gate")"; started="$(date +%s%3N)"; cd /workspace/source; cache_proxy="$accepted_gate worker go-cache-proxy --seed /opt/super-dolphin/cache/go-build --private $private_cache --metrics $private_cache/shard-compile.metrics"; env GOCACHE="$private_cache" GOCACHEPROG="$cache_proxy" GOMODCACHE=/workspace/work/go-mod-cache GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local CGO_ENABLED=0 /opt/super-dolphin-gate/runtime/go/bin/go build -mod=mod -trimpath -buildvcs=false -o "$built_gate" ./cmd/super-dolphin-gate; test -x "$built_gate"; finished="$(date +%s%3N)"; printf 'SUPER_DOLPHIN_SHARD_COMPILE started_at_unix_ms=%s completed_at_unix_ms=%s duration_ms=%s cache_metrics=%s\n' "$started" "$finished" "$((finished-started))" "$private_cache/shard-compile.metrics"`
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
		ImageCacheSnapshotID: input.ImageCacheSnapshotID,
		MainImage:            input.RunnerImage,
		InitImage:            input.RunnerImage,
		Resources:            resources,
		Command:              remoteWorkerSupervisorCommand(gate.ExecutorWorkRoot + "/bin/super-dolphin-gate"),
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

func validateShardResourceBinding(resources eci.Resources, request ShardRequest) error {
	if !request.Calibration {
		if request.CalibrationResource != nil {
			return errors.New("non-calibration shard request carries calibration resources")
		}
		return nil
	}
	if request.CalibrationResource == nil {
		return errors.New("calibration shard request resources are required")
	}
	class := *request.CalibrationResource
	if class.ID == "" || class.VCPU != resources.CPU || class.MemoryGiB != resources.MemoryGiB {
		return errors.New("calibration shard request resources drifted")
	}
	return nil
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
