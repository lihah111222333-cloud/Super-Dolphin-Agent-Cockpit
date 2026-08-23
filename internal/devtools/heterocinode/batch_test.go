package heterocinode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

const testBatchDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestBatchRequestCodecIsStrictAndCanonical(t *testing.T) {
	request := BatchRequest{
		SchemaVersion: BatchRequestSchemaVersion,
		GoExecutable:  os.Args[0],
		Workloads: []BatchWorkload{
			{UnitID: "unit-a", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", TargetPlatform: BatchTargetPlatformLinuxAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30, ExecutionPolicy: BatchExecutionPolicyNormal, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest},
			{UnitID: "unit-b", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", TargetPlatform: BatchTargetPlatformLinuxAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30, ExecutionPolicy: BatchExecutionPolicyRace, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest},
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
			UnitID: "unit-a", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", TargetPlatform: BatchTargetPlatformLinuxAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30, ExecutionPolicy: BatchExecutionPolicyNormal, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest,
		}},
	}
	tests := map[string]BatchRequest{
		"shell executable": func() BatchRequest { request := base; request.GoExecutable = "go;echo bad"; return request }(),
		"empty executable": func() BatchRequest { request := base; request.GoExecutable = ""; return request }(),
		"absolute module root": func() BatchRequest {
			request := base
			request.Workloads = append([]BatchWorkload(nil), base.Workloads...)
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
		UnitID: "unit-0", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", TargetPlatform: BatchTargetPlatformLinuxAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30, ExecutionPolicy: BatchExecutionPolicyNormal, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest,
	})
	if _, err := EncodeBatchRequest(unsorted); err == nil {
		t.Fatal("EncodeBatchRequest() unexpectedly accepted unsorted workloads")
	}
	canonical := base
	canonical.Workloads = append([]BatchWorkload(nil), base.Workloads...)
	canonical.Workloads[0].UnitID = "go-test/internal/devtools/heterocinode/TestExact"
	if _, err := EncodeBatchRequest(canonical); err != nil {
		t.Fatalf("canonical upstream unit ID was rejected: %v", err)
	}
}

func TestRunBatchRunsEveryExactWorkloadAndEmitsPerUnitResult(t *testing.T) {
	request := BatchRequest{
		SchemaVersion: BatchRequestSchemaVersion,
		GoExecutable:  os.Args[0],
		Workloads: []BatchWorkload{
			{UnitID: "unit-a", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", TargetPlatform: BatchTargetPlatformLinuxAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30, ExecutionPolicy: BatchExecutionPolicyNormal, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest},
			{UnitID: "unit-b", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", TargetPlatform: BatchTargetPlatformLinuxAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30, ExecutionPolicy: BatchExecutionPolicyRace, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest},
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

func TestBatchWorkloadRequiresCommandAndExecutionDigests(t *testing.T) {
	base := BatchWorkload{UnitID: "unit-a", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", TargetPlatform: BatchTargetPlatformLinuxAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30, ExecutionPolicy: BatchExecutionPolicyNormal, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest}
	for name, mutate := range map[string]func(*BatchWorkload){"command": func(value *BatchWorkload) { value.CommandDigest = "" }, "execution": func(value *BatchWorkload) { value.ExecutionDigest = "" }} {
		t.Run(name, func(t *testing.T) {
			workload := base
			mutate(&workload)
			if err := workload.Validate(); err == nil {
				t.Fatal("BatchWorkload.Validate() accepted missing digest")
			}
		})
	}
}

func TestBatchWorkloadRequiresPositiveResourceBudget(t *testing.T) {
	base := BatchWorkload{UnitID: "unit-a", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess", TargetPlatform: BatchTargetPlatformLinuxAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30, ExecutionPolicy: BatchExecutionPolicyNormal, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest}
	for name, mutate := range map[string]func(*BatchWorkload){
		"zero cpu":     func(value *BatchWorkload) { value.CPUMilli = 0 },
		"negative cpu": func(value *BatchWorkload) { value.CPUMilli = -1 },
		"zero memory":  func(value *BatchWorkload) { value.MemoryBytes = 0 },
		"negative memory": func(value *BatchWorkload) {
			value.MemoryBytes = -1
		},
	} {
		t.Run(name, func(t *testing.T) {
			workload := base
			mutate(&workload)
			if err := workload.Validate(); err == nil {
				t.Fatal("BatchWorkload.Validate() accepted invalid resource budget")
			}
		})
	}
}

func TestBatchRequestV5BindsTargetPlatformRuntimeAndResources(t *testing.T) {
	if got, want := BatchRequestSchemaVersion, "heteroci-node-batch-request/v5"; got != want {
		t.Fatalf("BatchRequestSchemaVersion = %q, want %q", got, want)
	}
	base := BatchRequest{
		SchemaVersion: BatchRequestSchemaVersion,
		GoExecutable:  os.Args[0],
		Workloads: []BatchWorkload{{
			UnitID: "unit-linux", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess",
			TargetPlatform: BatchTargetPlatformLinuxAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30,
			ExecutionPolicy: BatchExecutionPolicyNormal, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest,
		}},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("base v4 request rejected: %v", err)
	}
	missingRuntime := base
	missingRuntime.Workloads = append([]BatchWorkload(nil), base.Workloads...)
	missingRuntime.Workloads[0].RuntimeCapability = ""
	if err := missingRuntime.Validate(); err == nil {
		t.Fatal("request with missing runtime capability was accepted")
	}
	mixed := base
	mixed.Workloads = append([]BatchWorkload(nil), base.Workloads...)
	mixed.Workloads = append(mixed.Workloads, BatchWorkload{
		UnitID: "unit-windows", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess",
		TargetPlatform: BatchTargetPlatformWindowsAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuWineContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30,
		ExecutionPolicy: BatchExecutionPolicyNormal, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest,
	})
	if err := mixed.Validate(); err == nil {
		t.Fatal("mixed target platform/runtime batch was accepted")
	}
}

func TestBatchWineRejectsRaceAndRequiresExecutableIdentities(t *testing.T) {
	base := BatchRequest{
		SchemaVersion:                BatchRequestSchemaVersion,
		GoExecutable:                 os.Args[0],
		WineExecutable:               "/usr/lib/wine/wine64",
		WineExecutableIdentity:       testBatchDigest,
		WineServerExecutable:         "/usr/lib/wine/wineserver64",
		WineServerExecutableIdentity: testBatchDigest,
		WinePrefixRoot:               "/var/lib/super-dolphin/wine-prefix",
		Workloads: []BatchWorkload{{
			UnitID: "unit-windows", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess",
			TargetPlatform: BatchTargetPlatformWindowsAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuWineContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30,
			ExecutionPolicy: BatchExecutionPolicyNormal, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest,
		}},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("base Wine request rejected: %v", err)
	}
	missingIdentity := base
	missingIdentity.WineServerExecutableIdentity = ""
	if err := missingIdentity.Validate(); err == nil {
		t.Fatal("Wine request without wineserver identity was accepted")
	}
	relativeExecutable := base
	relativeExecutable.WineExecutable = "wine"
	if err := relativeExecutable.Validate(); err == nil {
		t.Fatal("Wine request with relative executable was accepted")
	}
	race := base
	race.Workloads = append([]BatchWorkload(nil), base.Workloads...)
	race.Workloads[0].ExecutionPolicy = BatchExecutionPolicyRace
	if err := race.Validate(); err == nil {
		t.Fatal("Wine race workload was accepted")
	}
}

func TestBatchGoPackageRunsWholePackageWithoutRunFilter(t *testing.T) {
	workload := BatchWorkload{
		UnitID: "unit-package", Kind: "go_package", ModuleRoot: ".", Package: ".",
		TargetPlatform: BatchTargetPlatformLinuxAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30,
		ExecutionPolicy: BatchExecutionPolicyRace, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest,
	}
	if err := workload.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	args := batchGoTestArgs(workload)
	if got, want := strings.Join(args, "\x00"), strings.Join([]string{"test", ".", "-p=1", "-count=1", "-timeout=0", "-race"}, "\x00"); got != want {
		t.Fatalf("batchGoTestArgs() = %#v", args)
	}
	t.Setenv("GOMAXPROCS", "99")
	environment := batchWorkloadEnvironment()
	if strings.Count(strings.Join(environment, "\x00"), "GOMAXPROCS=") != 1 || !slices.Contains(environment, "GOMAXPROCS=1") {
		t.Fatalf("batchWorkloadEnvironment() = %#v", environment)
	}
	workload.Name = "TestForbidden"
	if err := workload.Validate(); err == nil {
		t.Fatal("Validate() unexpectedly accepted go_package name")
	}
}

func TestRunBatchWineCrossCompilesRunsAndCleansPrivatePrefix(t *testing.T) {
	root := t.TempDir()
	goLog := filepath.Join(root, "go.log")
	wineLog := filepath.Join(root, "wine.log")
	serverLog := filepath.Join(root, "wineserver.log")
	winePrefixRoot := filepath.Join(root, "wine-prefix-root")
	if err := os.Mkdir(winePrefixRoot, 0o700); err != nil {
		t.Fatalf("create Wine prefix root: %v", err)
	}
	goExecutable := writeBatchTestExecutable(t, filepath.Join(root, "go"), fmt.Sprintf("#!/bin/sh\nset -eu\noutput=\"\"\nprevious=\"\"\nfor argument in \"$@\"; do\n  if [ \"$previous\" = \"-o\" ]; then output=\"$argument\"; fi\n  previous=\"$argument\"\ndone\ntest -n \"$output\"\nprintf '%%s/%%s/%%s\\n' \"$GOOS\" \"$GOARCH\" \"$CGO_ENABLED\" > %q\nprintf '#!/bin/sh\\nexit 0\\n' > \"$output\"\nchmod 700 \"$output\"\n", goLog))
	wineExecutable := writeBatchTestExecutable(t, filepath.Join(root, "wine"), fmt.Sprintf("#!/bin/sh\nset -eu\ntest -n \"$WINEPREFIX\"\ntest -x \"$1\"\nprintf '%%s|%%s\\n' \"$WINEPREFIX\" \"$1\" > %q\n", wineLog))
	wineServerExecutable := writeBatchTestExecutable(t, filepath.Join(root, "wineserver"), fmt.Sprintf("#!/bin/sh\nset -eu\ntest \"$1\" = \"-k\"\ntest -n \"$WINEPREFIX\"\ntest -d \"$WINEPREFIX\"\nprintf '%%s\\n' \"$WINEPREFIX\" > %q\n", serverLog))
	request := BatchRequest{
		SchemaVersion:                BatchRequestSchemaVersion,
		GoExecutable:                 goExecutable,
		WineExecutable:               wineExecutable,
		WineExecutableIdentity:       testBatchFileDigest(t, wineExecutable),
		WineServerExecutable:         wineServerExecutable,
		WineServerExecutableIdentity: testBatchFileDigest(t, wineServerExecutable),
		WinePrefixRoot:               winePrefixRoot,
		Workloads: []BatchWorkload{{
			UnitID: "unit-windows", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess",
			TargetPlatform: BatchTargetPlatformWindowsAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuWineContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30,
			ExecutionPolicy: BatchExecutionPolicyNormal, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest,
		}},
	}
	t.Setenv("CGO_ENABLED", "0")
	var stdout, stderr bytes.Buffer
	result, err := RunBatch(context.Background(), request, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunBatch() error = %v", err)
	}
	if result.Outcome != BatchOutcomeSuccess {
		t.Fatalf("Wine batch outcome = %#v", result)
	}
	if strings.HasSuffix(stdout.String(), "\n") {
		t.Fatalf("Wine stdout has trailing newline: %q", stdout.String())
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(stderr.String(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event batchEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("stderr line is not JSON event: %q: %v", line, err)
		}
		if event.SchemaVersion != batchEventSchemaVersion || event.UnitID != "unit-windows" || event.Stage == "" {
			t.Fatalf("stderr event identity = %#v", event)
		}
	}
	compileFacts, err := os.ReadFile(goLog)
	if err != nil {
		t.Fatalf("read fake Go log: %v", err)
	}
	if got, want := strings.TrimSpace(string(compileFacts)), "windows/amd64/0"; got != want {
		t.Fatalf("fake Go facts = %q, want %q", got, want)
	}
	wineFacts, err := os.ReadFile(wineLog)
	if err != nil {
		t.Fatalf("read fake Wine log: %v", err)
	}
	wineParts := strings.Split(strings.TrimSpace(string(wineFacts)), "|")
	if len(wineParts) != 2 || wineParts[0] == "" || wineParts[1] == "" {
		t.Fatalf("fake Wine facts = %q", wineFacts)
	}
	if _, err := os.Stat(wineParts[0]); !os.IsNotExist(err) {
		t.Fatalf("Wine prefix survived cleanup: %q, err=%v", wineParts[0], err)
	}
	if _, err := os.Stat(wineParts[1]); !os.IsNotExist(err) {
		t.Fatalf("compiled test executable survived cleanup: %q, err=%v", wineParts[1], err)
	}
	serverFacts, err := os.ReadFile(serverLog)
	if err != nil {
		t.Fatalf("read fake wineserver log: %v", err)
	}
	if strings.TrimSpace(string(serverFacts)) != wineParts[0] {
		t.Fatalf("wineserver prefix = %q, Wine prefix = %q", strings.TrimSpace(string(serverFacts)), wineParts[0])
	}
}

func TestRunBatchWineRejectsIdentityDriftBeforeExecution(t *testing.T) {
	root := t.TempDir()
	wineExecutable := writeBatchTestExecutable(t, filepath.Join(root, "wine"), "#!/bin/sh\nexit 0\n")
	wineServerExecutable := writeBatchTestExecutable(t, filepath.Join(root, "wineserver"), "#!/bin/sh\nexit 0\n")
	request := BatchRequest{
		SchemaVersion:                BatchRequestSchemaVersion,
		GoExecutable:                 os.Args[0],
		WineExecutable:               wineExecutable,
		WineExecutableIdentity:       testBatchDigest,
		WineServerExecutable:         wineServerExecutable,
		WineServerExecutableIdentity: testBatchFileDigest(t, wineServerExecutable),
		WinePrefixRoot:               filepath.Join(root, "wine-prefix-root"),
		Workloads: []BatchWorkload{{
			UnitID: "unit-windows", Kind: "go_test", ModuleRoot: ".", Package: "./internal/devtools/heterocinode", Name: "TestBatchHelperProcess",
			TargetPlatform: BatchTargetPlatformWindowsAMD64, RuntimeCapability: BatchRuntimeCapabilityUbuntuWineContainer, CPUMilli: 1_000, MemoryBytes: 1 << 30,
			ExecutionPolicy: BatchExecutionPolicyNormal, CommandDigest: testBatchDigest, ExecutionDigest: testBatchDigest,
		}},
	}
	var stdout, stderr bytes.Buffer
	if _, err := RunBatch(context.Background(), request, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("RunBatch() identity drift error = %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("identity drift emitted output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func writeBatchTestExecutable(t *testing.T, path, script string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
	return path
}

func testBatchFileDigest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read executable %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func assertBatchResult(t *testing.T, result BatchResult, stdout, stderr string) {
	t.Helper()
	if result.Authority != BatchAuthorityLocalNonAuthoritative || result.Status != BatchStatusNotVerified || result.Outcome != BatchOutcomeSuccess {
		t.Fatalf("result authority/status/outcome = %#v", result)
	}
	if len(result.Outcomes) != 2 || result.Outcomes[0].UnitID != "unit-a" || result.Outcomes[1].UnitID != "unit-b" {
		t.Fatalf("result outcomes = %#v", result.Outcomes)
	}
	if stdout == "" || strings.HasSuffix(stdout, "\n") || !strings.Contains(stdout, `"authority":"LOCAL_NON_AUTHORITATIVE"`) {
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
