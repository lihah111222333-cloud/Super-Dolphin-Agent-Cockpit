package remoteciruntime

import (
	"fmt"
	"slices"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/oss"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const (
	coordinatorPollInterval   = 2 * time.Second
	coordinatorCleanupTimeout = 2 * time.Minute
)

// ErrorClass 标识 composition 失败应映射到协议错误还是基础设施错误。
type ErrorClass uint8

const (
	// ErrorClassProtocol 表示调用参数或协议形状错误。
	ErrorClassProtocol ErrorClass = iota + 1
	// ErrorClassInfrastructure 表示 OSS、ECI 或 Coordinator 构造失败。
	ErrorClassInfrastructure
)

// CompositionError 保留共享 runtime 装配阶段和原始错误链。
type CompositionError struct {
	Class     ErrorClass
	Operation string
	Cause     error
}

// Error 返回不暴露凭据的稳定装配错误文本。
func (err *CompositionError) Error() string {
	if err.Cause == nil {
		return err.Operation
	}
	return fmt.Sprintf("%s: %v", err.Operation, err.Cause)
}

// Unwrap 保留底层构造失败供 Gate adapter 分类。
func (err *CompositionError) Unwrap() error { return err.Cause }

// NewCoordinator 是 OSS、ECI 与唯一 remoteci Coordinator 的共享 composition owner。
func NewCoordinator(
	config Config,
	input remoteci.RunInput,
	resolveWorkerTimeout func(gatecontract.Profile, bool) (time.Duration, error),
	resolveContainerDeadline func(time.Duration) (time.Duration, error),
	registryCredentialLoader func() (eci.RegistryCredential, error),
	progressObservers ...remoteci.ProgressObserver,
) (*remoteci.Coordinator, time.Duration, error) {
	store, err := oss.NewCLI(oss.Config{
		Binary: config.AliyunCLI, Bucket: config.OSS.Bucket, Endpoint: config.OSS.Endpoint,
		Profile: config.CredentialProfile, Prefix: config.OSS.SourcePrefix,
	})
	if err != nil {
		return nil, 0, compositionError(ErrorClassInfrastructure, "create remote CI OSS client", err)
	}
	if resolveWorkerTimeout == nil || resolveContainerDeadline == nil {
		return nil, 0, compositionError(ErrorClassProtocol, "remote CI deadline resolver is required", nil)
	}
	workerTimeout, err := resolveWorkerTimeout(input.Profile, input.Calibration)
	if err != nil {
		return nil, 0, compositionError(ErrorClassProtocol, "resolve remote CI deadline", err)
	}
	containerDeadline, err := resolveContainerDeadline(workerTimeout)
	if err != nil {
		return nil, 0, compositionError(ErrorClassProtocol, "resolve remote CI container deadline", err)
	}
	if len(progressObservers) > 1 {
		return nil, 0, compositionError(ErrorClassProtocol, "remote CI accepts at most one progress observer", nil)
	}
	var progressObserver remoteci.ProgressObserver
	if len(progressObservers) == 1 {
		progressObserver = progressObservers[0]
	}
	runtime, err := eci.New(eci.Config{
		Binary:   config.AliyunCLI,
		RegionID: config.RegionID, VSwitches: slices.Clone(config.VSwitches), SecurityGroupID: config.SecurityGroupID,
		WorkerRoleName: config.WorkerRoleName, Profile: config.CredentialProfile,
		Deadline:     containerDeadline,
		SpotStrategy: eci.SpotStrategyAsPriceGo, SpotDurationHours: 1,
		RegistryCredentialLoader: registryCredentialLoader,
	})
	if err != nil {
		return nil, 0, compositionError(ErrorClassInfrastructure, "create remote CI ECI client", err)
	}
	coordinator, err := remoteci.NewCoordinator(remoteci.CoordinatorConfig{
		Bucket: config.OSS.Bucket, SourcePrefix: config.OSS.SourcePrefix,
		InternalOSSEndpoint: config.OSS.InternalEndpoint,
		WorkerRoleName:      config.WorkerRoleName, WorkerTimeout: workerTimeout,
		ImageCacheSnapshotID: input.ImageCacheSnapshotID,
		PollInterval:         coordinatorPollInterval, CleanupTimeout: coordinatorCleanupTimeout,
		ResourcePolicy:   config.Capacity.ResourcePolicy,
		ProgressObserver: progressObserver,
	}, store, runtime)
	if err != nil {
		return nil, 0, compositionError(ErrorClassInfrastructure, "create remote CI coordinator", err)
	}
	return coordinator, containerDeadline, nil
}

func compositionError(class ErrorClass, operation string, cause error) error {
	return &CompositionError{Class: class, Operation: operation, Cause: cause}
}
