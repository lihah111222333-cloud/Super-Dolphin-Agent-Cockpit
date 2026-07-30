package remoteci

import (
	"fmt"
	"path"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	remoteCurrentGateMountPath   = "/current-gate"
	remoteCurrentGateDigestEnv   = "SUPER_DOLPHIN_CURRENT_GATE_SHA256"
	remoteCurrentGateVolumeName  = "current-gate"
	remoteXKBCompMountPath       = "/usr/bin/xkbcomp"
	remoteXKBCompSubPath         = "runtime/rootfs/usr/bin/xkbcomp"
	remoteXKBDataMountPath       = "/usr/share/X11/xkb"
	remoteXKBDataSubPath         = "runtime/rootfs/usr/share/X11/xkb"
	remoteCurrentGateBootstrapSH = `set -eu
cp /current-gate/bin/super-dolphin-gate /tmp/super-dolphin-gate-current
test "sha256:$(sha256sum /tmp/super-dolphin-gate-current | awk '{print $1}')" = "$SUPER_DOLPHIN_CURRENT_GATE_SHA256"
chmod 0755 /tmp/super-dolphin-gate-current
exec /tmp/super-dolphin-gate-current _remote-materialize`
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
		Command: []string{"/bootstrap/bin/super-dolphin-gate", "_remote-materialize"},
		Environment: map[string]string{
			"PATH":                                gate.ExecutorPortableSearchPath,
			"SSL_CERT_FILE":                       remoteDataCacheCAFile,
			"SUPER_DOLPHIN_RUNTIME_ROOT":          gate.ExecutorRuntimeSeedRoot,
			"SUPER_DOLPHIN_REMOTE_WORKER_ROLE":    coordinator.config.WorkerRoleName,
			"SUPER_DOLPHIN_REMOTE_OSS_ENDPOINT":   coordinator.config.InternalOSSEndpoint,
			"SUPER_DOLPHIN_REMOTE_OSS_BUCKET":     coordinator.config.Bucket,
			"SUPER_DOLPHIN_REMOTE_REQUEST_KEY":    requestKey,
			"SUPER_DOLPHIN_REMOTE_REQUEST_SHA256": requestDigest,
			remoteBaselineManifestEnvironment:     input.AnchorManifest,
		},
	}
	initMounts := []eci.VolumeMount{
		{Name: "base-data", MountPath: "/bootstrap", ReadOnly: true},
		{Name: "expanded-data", MountPath: "/opt/super-dolphin-gate"},
		{Name: "source-data", MountPath: gate.ExecutorSourcePath},
		{Name: "work-data", MountPath: gate.ExecutorWorkRoot},
	}
	bootstrapVolume := remoteCurrentGateVolume(coordinator.config, input)
	if bootstrapVolume != (eci.OSSVolume{}) {
		initContainer.Command = []string{"/bin/sh"}
		initContainer.Args = []string{"-c", remoteCurrentGateBootstrapSH}
		initContainer.Environment[remoteCurrentGateDigestEnv] = input.GateBinarySHA256
		initMounts = append(initMounts,
			eci.VolumeMount{Name: "temp-data", MountPath: "/tmp"},
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
		Environment: map[string]string{
			gate.ExecutorWorkloadTimeoutEnvironment: coordinator.config.WorkerTimeout.String(),
		},
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
			{Name: "temp-data", MountPath: "/tmp"},
		},
		InitVolumeMounts: initMounts,
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
