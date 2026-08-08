package gate

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
)

// preparePlanGateExecution 准备 lane 私有执行配置以及与日志绑定的测试时序证据。
func preparePlanGateExecution(
	laneIndex int,
	id GateID,
	program ExecutorProgram,
	preparedRuntimeSeeds *executorPreparedRuntimeSeeds,
	goBuildCacheRoot string,
	goBuildCacheSeedRoot string,
	now func() time.Time,
) (executorConfig, *boundedPlanLog, *testtiming.EventWriter, string, error) {
	return preparePlanGateExecutionAt(
		ExecutorWorkRoot, laneIndex, id, program, preparedRuntimeSeeds,
		goBuildCacheRoot, goBuildCacheSeedRoot, now,
	)
}

// preparePlanGateExecutionAt 为测试提供可控工作根，同时保持生产 lane 根固定为镜像约定路径。
func preparePlanGateExecutionAt(
	workRootBase string,
	laneIndex int,
	id GateID,
	program ExecutorProgram,
	preparedRuntimeSeeds *executorPreparedRuntimeSeeds,
	goBuildCacheRoot string,
	goBuildCacheSeedRoot string,
	now func() time.Time,
) (executorConfig, *boundedPlanLog, *testtiming.EventWriter, string, error) {
	workRoot := executorPlanLaneRoot(workRootBase, laneIndex)
	if err := os.MkdirAll(workRoot, 0o700); err != nil {
		return executorConfig{}, nil, nil, "", err
	}
	cacheProxy, err := executorGoBuildCacheProxyLauncher()
	if err != nil {
		return executorConfig{}, nil, nil, "", err
	}
	log := newBoundedPlanLog(executorPlanMaxLogBytes)
	stdout := io.Writer(log)
	var timingWriter *testtiming.EventWriter
	if isGoPackageTestWorkload(id) {
		timingWriter = testtiming.NewEventWriter(log)
		stdout = timingWriter
	}
	metricsPath := ""
	if needsGoCacheObservation(program) {
		metricsPath, err = GoBuildCacheProxyMetricsPathForInvocation(
			goBuildCacheRoot,
			fmt.Sprintf("lane-%d-%x", laneIndex, sha256.Sum256([]byte(string(id)))),
		)
		if err != nil {
			return executorConfig{}, nil, nil, "", err
		}
	}
	config := executorConfig{
		sourcePath: ExecutorSourcePath, workRoot: workRoot, searchPath: executorSearchPath,
		expectedUID: executorUID, requireReadOnlySource: true,
		runtimeSeedRoot: ExecutorRuntimeSeedRoot, runtimeSeedManifest: ExecutorRuntimeSeedManifestPath,
		goRoot:                  ExecutorGoRoot,
		preparedRuntimeSeeds:    preparedRuntimeSeeds,
		goBuildCacheSeedRoot:    goBuildCacheSeedRoot,
		goBuildCacheRoot:        goBuildCacheRoot,
		goBuildCacheProxy:       cacheProxy,
		goBuildCacheMetricsPath: metricsPath,
		frontendEmbedSeedRoot:   ExecutorFrontendEmbedSeedRoot,
		stdout:                  stdout, stderr: log,
		nowFunc:         now,
		executionTiming: &executorExecutionTiming{},
	}
	return config, log, timingWriter, metricsPath, nil
}
