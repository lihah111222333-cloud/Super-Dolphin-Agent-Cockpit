package localci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
)

const dockerInfoJSONFormat = "{{json .}}"

type dockerDaemonCapacityInspector struct {
	runner dockerRunner
	now    func() time.Time
}

// dockerInfoPayload 固化受支持的 Docker info 顶层字段合同。
// 未消费字段保留原始 JSON，使顶层字段漂移仍在此边界失败。
type dockerInfoPayload struct {
	ID       string `json:"ID"`
	NCPU     int64  `json:"NCPU"`
	MemTotal int64  `json:"MemTotal"`

	Containers          json.RawMessage
	ContainersRunning   json.RawMessage
	ContainersPaused    json.RawMessage
	ContainersStopped   json.RawMessage
	Images              json.RawMessage
	Driver              json.RawMessage
	DriverStatus        json.RawMessage
	Plugins             json.RawMessage
	MemoryLimit         json.RawMessage
	SwapLimit           json.RawMessage
	KernelMemoryTCP     json.RawMessage
	CpuCfsPeriod        json.RawMessage
	CpuCfsQuota         json.RawMessage
	CPUShares           json.RawMessage
	CPUSet              json.RawMessage
	PidsLimit           json.RawMessage
	IPv4Forwarding      json.RawMessage
	BridgeNfIptables    json.RawMessage
	BridgeNfIP6tables   json.RawMessage
	Debug               json.RawMessage
	NFd                 json.RawMessage
	OomKillDisable      json.RawMessage
	NGoroutines         json.RawMessage
	SystemTime          json.RawMessage
	LoggingDriver       json.RawMessage
	CgroupDriver        json.RawMessage
	CgroupVersion       json.RawMessage
	NEventsListener     json.RawMessage
	KernelVersion       json.RawMessage
	OperatingSystem     json.RawMessage
	OSVersion           json.RawMessage
	OSType              json.RawMessage
	Architecture        json.RawMessage
	IndexServerAddress  json.RawMessage
	RegistryConfig      json.RawMessage
	GenericResources    json.RawMessage
	DockerRootDir       json.RawMessage
	HttpProxy           json.RawMessage
	HttpsProxy          json.RawMessage
	NoProxy             json.RawMessage
	Name                json.RawMessage
	Labels              json.RawMessage
	ExperimentalBuild   json.RawMessage
	ServerVersion       json.RawMessage
	ClusterStore        json.RawMessage
	ClusterAdvertise    json.RawMessage
	Runtimes            json.RawMessage
	DefaultRuntime      json.RawMessage
	Swarm               json.RawMessage
	LiveRestoreEnabled  json.RawMessage
	Isolation           json.RawMessage
	InitBinary          json.RawMessage
	ContainerdCommit    json.RawMessage
	RuncCommit          json.RawMessage
	InitCommit          json.RawMessage
	SecurityOptions     json.RawMessage
	ProductLicense      json.RawMessage
	DefaultAddressPools json.RawMessage
	FirewallBackend     json.RawMessage
	CDISpecDirs         json.RawMessage
	DiscoveredDevices   json.RawMessage
	Containerd          json.RawMessage
	Warnings            json.RawMessage
	ClientInfo          json.RawMessage
}

func newDockerDaemonCapacityInspector(runner dockerRunner) (*dockerDaemonCapacityInspector, error) {
	if isNilDockerRunner(runner) {
		return nil, errors.New("docker daemon capacity runner is nil")
	}
	return &dockerDaemonCapacityInspector{runner: runner, now: time.Now}, nil
}

// InspectDaemonCapacity 仅通过固定 CLI 命令读取当前 active Docker daemon。
func (inspector *dockerDaemonCapacityInspector) InspectDaemonCapacity(
	ctx context.Context,
	daemonID string,
) (DaemonCapacity, error) {
	if err := inspector.validateInputs(ctx, daemonID); err != nil {
		return DaemonCapacity{}, err
	}
	output, err := inspector.runner.Run(ctx, "info", "--format", dockerInfoJSONFormat)
	if err != nil {
		return DaemonCapacity{}, fmt.Errorf("read docker daemon info: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return DaemonCapacity{}, fmt.Errorf("read docker daemon info: %w", err)
	}
	info, err := decodeDockerInfo(output)
	if err != nil {
		return DaemonCapacity{}, err
	}
	if info.ID != daemonID {
		return DaemonCapacity{}, fmt.Errorf(
			"docker daemon identity mismatch: requested %q, inspected %q",
			daemonID,
			info.ID,
		)
	}
	capacity := DaemonCapacity{
		DaemonID:    info.ID,
		ObservedAt:  inspector.now().UTC(),
		LogicalCPUs: info.NCPU,
		MemoryBytes: info.MemTotal,
	}
	if err := capacity.Validate(); err != nil {
		return DaemonCapacity{}, fmt.Errorf("validate docker daemon info capacity: %w", err)
	}
	return capacity, nil
}

// validateInputs 在执行 Docker CLI 前校验 inspector、请求身份和上下文。
func (inspector *dockerDaemonCapacityInspector) validateInputs(ctx context.Context, daemonID string) error {
	if inspector == nil {
		return errors.New("docker daemon capacity inspector is nil")
	}
	if ctx == nil {
		return errors.New("docker daemon capacity context is nil")
	}
	if err := validateDaemonID(daemonID); err != nil {
		return err
	}
	if isNilDockerRunner(inspector.runner) {
		return errors.New("docker daemon capacity runner is nil")
	}
	if inspector.now == nil {
		return errors.New("docker daemon capacity clock is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("read docker daemon info: %w", err)
	}
	return nil
}

func decodeDockerInfo(output string) (dockerInfoPayload, error) {
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	var info dockerInfoPayload
	if err := decoder.Decode(&info); err != nil {
		return dockerInfoPayload{}, fmt.Errorf("decode docker daemon info JSON: %w", err)
	}
	var trailing json.RawMessage
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return info, nil
	}
	if err == nil {
		return dockerInfoPayload{}, errors.New("docker daemon info contains trailing JSON")
	}
	return dockerInfoPayload{}, fmt.Errorf("docker daemon info contains trailing output: %w", err)
}

func isNilDockerRunner(runner dockerRunner) bool {
	if runner == nil {
		return true
	}
	value := reflect.ValueOf(runner)
	kind := value.Kind()
	return kind >= reflect.Chan && kind <= reflect.Slice && value.IsNil()
}
