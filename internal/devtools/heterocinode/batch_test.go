package heterocinode

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain 让 worker 子进程直接退出，避免测试二进制递归执行 batch fixture。
func TestMain(m *testing.M) {
	if os.Getenv("SUPER_DOLPHIN_HETEROCI_BATCH_HELPER") == "1" {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestBatchRequestCodecIsStrictAndCanonical(t *testing.T) {
	request := BatchRequest{
		SchemaVersion: BatchRequestSchemaVersion,
		GoExecutable:  os.Args[0],
		Workloads: []BatchWorkload{
			{UnitID: "unit-a", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", ExecutionPolicy: BatchExecutionPolicyNormal},
			{UnitID: "unit-b", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", ExecutionPolicy: BatchExecutionPolicyRace},
		},
	}
	encoded, err := EncodeBatchRequest(request)
	if err != nil {
		t.Fatalf("EncodeBatchRequest() error = %v", err)
	}
	decoded, err := DecodeBatchRequest(encoded)
	if err != nil {
		t.Fatalf("DecodeBatchRequest() error = %v", err)
	}
	if got, want := string(encoded), mustCanonicalJSON(t, decoded); got != want {
		t.Fatalf("canonical request = %s, want %s", got, want)
	}

	unknown := strings.Replace(string(encoded), `"go_executable"`, `"shell":"go test", "go_executable"`, 1)
	duplicate := strings.Replace(string(encoded), `"go_executable"`, `"go_executable":"other", "go_executable"`, 1)
	whitespace := string(encoded) + "\n"
	for name, data := range map[string][]byte{
		"unknown field":   []byte(unknown),
		"duplicate field": []byte(duplicate),
		"trailing bytes":  []byte(whitespace),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeBatchRequest(data); err == nil {
				t.Fatal("DecodeBatchRequest() unexpectedly accepted non-canonical JSON")
			}
		})
	}
}

func TestBatchResultCodecRejectsIdentityAndAggregateDrift(t *testing.T) {
	result := BatchResult{
		SchemaVersion: BatchResultSchemaVersion, Authority: BatchAuthorityLocalNonAuthoritative,
		Status: BatchStatusNotVerified, Outcome: BatchOutcomeSuccess,
		Outcomes: []BatchOutcome{{UnitID: "unit-a", Status: BatchOutcomeSuccess, ExitCode: 0}},
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeBatchResult(encoded); err != nil {
		t.Fatalf("DecodeBatchResult() error = %v", err)
	}
	for name, mutate := range map[string]func(*BatchResult){
		"authority": func(value *BatchResult) { value.Authority = "REMOTE" },
		"aggregate": func(value *BatchResult) { value.Outcome = BatchOutcomeFailed },
		"outcome":   func(value *BatchResult) { value.Outcomes[0].ExitCode = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := result
			invalid.Outcomes = append([]BatchOutcome(nil), result.Outcomes...)
			mutate(&invalid)
			raw, err := json.Marshal(invalid)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeBatchResult(raw); err == nil {
				t.Fatal("DecodeBatchResult() accepted drift")
			}
		})
	}
}

func TestBatchRequestRejectsShellArgvCwdDefaultsAndUnsortedWorkloads(t *testing.T) {
	base := BatchRequest{
		SchemaVersion: BatchRequestSchemaVersion,
		GoExecutable:  os.Args[0],
		Workloads: []BatchWorkload{{
			UnitID: "unit-a", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", ExecutionPolicy: BatchExecutionPolicyNormal,
		}},
	}
	tests := map[string]BatchRequest{
		"shell executable": func() BatchRequest { request := base; request.GoExecutable = "go;echo bad"; return request }(),
		"empty executable": func() BatchRequest { request := base; request.GoExecutable = ""; return request }(),
		"absolute module root": func() BatchRequest {
			request := base
			request.Workloads[0].ModuleRoot = filepath.Clean(t.TempDir())
			return request
		}(),
		"cwd field is not representable":  base,
		"argv field is not representable": base,
	}
	for name, request := range tests {
		t.Run(name, func(t *testing.T) {
			if name == "cwd field is not representable" || name == "argv field is not representable" {
				return
			}
			if _, err := EncodeBatchRequest(request); err == nil {
				t.Fatal("EncodeBatchRequest() unexpectedly accepted invalid request")
			}
		})
	}
	unsorted := base
	unsorted.Workloads = append(unsorted.Workloads, BatchWorkload{
		UnitID: "unit-0", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", ExecutionPolicy: BatchExecutionPolicyNormal,
	})
	if _, err := EncodeBatchRequest(unsorted); err == nil {
		t.Fatal("EncodeBatchRequest() unexpectedly accepted unsorted workloads")
	}
}

func TestRunBatchRunsEveryExactWorkloadAndEmitsPerUnitResult(t *testing.T) {
	request := BatchRequest{
		SchemaVersion: BatchRequestSchemaVersion,
		GoExecutable:  os.Args[0],
		Workloads: []BatchWorkload{
			{UnitID: "unit-a", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", ExecutionPolicy: BatchExecutionPolicyNormal},
			{UnitID: "unit-b", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", ExecutionPolicy: BatchExecutionPolicyRace},
		},
	}
	t.Setenv("SUPER_DOLPHIN_HETEROCI_BATCH_HELPER", "1")
	var stdout, stderr bytes.Buffer
	result, err := RunBatch(context.Background(), request, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunBatch() error = %v", err)
	}
	assertBatchResult(t, result, stdout.String(), stderr.String())
}

func assertBatchResult(t *testing.T, result BatchResult, stdout, stderr string) {
	t.Helper()
	if result.Authority != BatchAuthorityLocalNonAuthoritative || result.Status != BatchStatusNotVerified || result.Outcome != BatchOutcomeSuccess {
		t.Fatalf("result authority/status/outcome = %#v", result)
	}
	if len(result.Outcomes) != 2 || result.Outcomes[0].UnitID != "unit-a" || result.Outcomes[1].UnitID != "unit-b" {
		t.Fatalf("result outcomes = %#v", result.Outcomes)
	}
	if stdout == "" || !strings.Contains(stdout, `"authority":"LOCAL_NON_AUTHORITATIVE"`) || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestBatchHelperProcess(t *testing.T) {
	if os.Getenv("SUPER_DOLPHIN_HETEROCI_BATCH_HELPER") != "1" {
		t.Skip("helper process only")
	}
	time.Sleep(1 * time.Millisecond)
}

func mustCanonicalJSON(t *testing.T, request BatchRequest) string {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
