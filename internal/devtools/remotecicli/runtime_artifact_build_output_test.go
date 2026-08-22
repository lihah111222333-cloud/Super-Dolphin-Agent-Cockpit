package remotecicli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// TestRuntimeArtifactBuilderPrivateCommandAndOutputBoundary 锁定 B 的私有命令与 machine output 链接边界。
func TestRuntimeArtifactBuilderPrivateCommandAndOutputBoundary(t *testing.T) {
	result := remoteci.RuntimeArtifactBuilderWorkerResult{ArtifactKey: "artifact", ReceiptKey: "receipt"}
	encoded, err := boundedRuntimeArtifactBuildJSON(result)
	if err != nil {
		t.Fatalf("boundedRuntimeArtifactBuildJSON() error = %v", err)
	}
	if !strings.HasPrefix(string(encoded), runtimeArtifactBuildOutputPrefix) {
		t.Fatalf("runtime artifact output = %q", encoded)
	}
	var stdout, stderr bytes.Buffer
	if err := dispatchCLI([]string{"_runtime-artifact-build"}, &stdout, &stderr); err == nil {
		t.Fatal("private runtime artifact command accepted missing strict flags")
	}
	if stdout.Len() != 0 {
		t.Fatalf("private command wrote machine output on validation failure: %q", stdout.String())
	}
}
