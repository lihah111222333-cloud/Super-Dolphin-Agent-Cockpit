package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
)

func TestSchemaCompilerLateReapReturnsCapacity(t *testing.T) {
	limiter := newHelperLimiter(1)
	var completeLateReap func()
	reapFailed := func(capacity *helperCapacityTracker) (Result, error) {
		completeLateReap = capacity.registerLateReap()
		return Result{}, newDiagnostic(CodeReapFailed, "fixture did not reap", nil)
	}
	if _, err := limiter.run(context.Background(), reapFailed); ErrorCode(err) != CodeReapFailed {
		t.Fatalf("limiter.run() code = %q, want %q; error=%v", ErrorCode(err), CodeReapFailed, err)
	}
	started := false
	startedAt := time.Now()
	_, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		started = true
		return Result{}, nil
	})
	if started {
		t.Fatal("operation started after unreaped helpers consumed all capacity")
	}
	if ErrorCode(err) != CodeCapacityExhausted {
		t.Fatalf("capacity code = %q, want %q; error=%v", ErrorCode(err), CodeCapacityExhausted, err)
	}
	if elapsed := time.Since(startedAt); elapsed > capacityWait+250*time.Millisecond {
		t.Fatalf("fail-closed capacity wait took %v", elapsed)
	}
	if completeLateReap == nil {
		t.Fatal("late reap release callback was not provided")
	}
	completeLateReap()
	completeLateReap()
	if _, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		return Result{}, nil
	}); err != nil {
		t.Fatalf("limiter.run() after late reap error = %v", err)
	}
}

func TestSchemaCompilerHelperCompileAndValidate(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "mcp-schema-compiler-helper")
	build := exec.Command("go", "build", "-o", binary, "./cmd/mcp-schema-compiler-helper")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, output)
	}
	canonical, err := Canonicalize([]byte(`{"properties":{"name":{"type":"string"}},"required":["name"],"type":"object"}`))
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	compileResponse := executeBuiltHelper(t, binary, testInvocation(canonical))
	assertBuiltCompileResponse(t, compileResponse, canonical.Digest)

	validate := testInvocation(canonical)
	validate.Operation = OperationValidate
	validate.RequestID = "request-validate"
	validate.Arguments = json.RawMessage(`{"name":"reasonix"}`)
	validateResponse := executeBuiltHelper(t, binary, validate)
	assertBuiltValidateResponse(t, validateResponse)
	validate.RequestID = "request-invalid"
	validate.Arguments = json.RawMessage(`{"name":42}`)
	invalidResponse := executeBuiltHelper(t, binary, validate)
	assertBuiltInvalidResponse(t, invalidResponse)
}

func assertBuiltCompileResponse(t *testing.T, response protocolResponse, digest string) {
	t.Helper()
	if !response.OK || response.CompiledDigest != digest {
		t.Fatalf("compile response = %+v, want digest %q", response, digest)
	}
}

func assertBuiltValidateResponse(t *testing.T, response protocolResponse) {
	t.Helper()
	if !response.OK || response.ArgumentsValid == nil || !*response.ArgumentsValid {
		t.Fatalf("validate response = %+v", response)
	}
}

func assertBuiltInvalidResponse(t *testing.T, response protocolResponse) {
	t.Helper()
	if response.OK || response.Code != CodeArgumentInvalid {
		t.Fatalf("invalid response = %+v, want code %q", response, CodeArgumentInvalid)
	}
}

// executeBuiltHelper 直接验证真实 helper 二进制的一次请求、一次响应和退出行为。
func executeBuiltHelper(t *testing.T, binary string, invocation Invocation) protocolResponse {
	t.Helper()
	request, err := newProtocolRequest(invocation)
	if err != nil {
		t.Fatalf("newProtocolRequest() error = %v", err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal helper request: %v", err)
	}
	ctx, cancel := ctxutil.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Stdin = bytes.NewReader(encoded)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("run built helper: %v", err)
	}
	response, err := decodeProtocolResponse(output)
	if err != nil {
		t.Fatalf("decodeProtocolResponse() error = %v", err)
	}
	return response
}
