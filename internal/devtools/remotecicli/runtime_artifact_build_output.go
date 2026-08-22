package remotecicli

import (
	"encoding/json"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const (
	runtimeArtifactBuildOutputSchema = "portable-runtime-artifact-build-output/v1"
	runtimeArtifactBuildOutputPrefix = "SUPER_DOLPHIN_RUNTIME_ARTIFACT_BUILD_RESULT "
)

type runtimeArtifactBuildOutput struct {
	SchemaVersion, ExecutionProvider string
	Result                           remoteci.RuntimeArtifactBuilderWorkerResult
}

// boundedRuntimeArtifactBuildJSON 编码 worker 的唯一 bounded machine output。
func boundedRuntimeArtifactBuildJSON(result remoteci.RuntimeArtifactBuilderWorkerResult) ([]byte, error) {
	output := runtimeArtifactBuildOutput{SchemaVersion: runtimeArtifactBuildOutputSchema, ExecutionProvider: remoteci.PortableExecutionProvider, Result: result}
	data, err := json.Marshal(output)
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("runtime artifact build output is invalid")
	}
	return []byte(runtimeArtifactBuildOutputPrefix + string(data) + "\n"), nil
}
