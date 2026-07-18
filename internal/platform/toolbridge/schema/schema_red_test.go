package schema

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const helperFixtureTimeout = 10 * time.Second

func TestMain(m *testing.M) {
	if runBlockingFilesystemWorkerFixture() {
		os.Exit(0)
	}
	if handled, err := RunFilesystemWorkerIfRequested(os.Stdin, os.Stdout); handled {
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	if runCancellationFixture() {
		os.Exit(0)
	}
	if mode := os.Getenv("REASONIX_SCHEMA_MALICIOUS_HELPER"); mode != "" {
		time.Sleep(25 * time.Millisecond)
		if runImmediateMaliciousMode(mode) {
			os.Exit(0)
		}
		request := readMaliciousRequest()
		writeMaliciousResponse(mode, maliciousResponse(mode, request))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testHelperIdentity() HelperIdentity {
	return HelperIdentity{AppCommit: "test-commit", GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

func newSchemaTestClient(t *testing.T, source string) *Client {
	t.Helper()
	dir := t.TempDir()
	helper := filepath.Join(dir, HelperFileName(runtime.GOOS))
	image, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read helper fixture: %v", err)
	}
	if err := os.WriteFile(helper, image, 0o700); err != nil {
		t.Fatalf("write helper fixture: %v", err)
	}
	manifest := helper + HelperManifestSuffix
	if err := WriteHelperManifest(helper, manifest, testHelperIdentity()); err != nil {
		t.Fatalf("write helper manifest: %v", err)
	}
	client, err := NewClient(context.Background(), ClientConfig{
		HelperPath: helper, ManifestPath: manifest, FilesystemWorkerPath: os.Args[0], Identity: testHelperIdentity(),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func TestSchemaCompilerRejectsReferencesAndBudgets(t *testing.T) {
	t.Parallel()

	deep := bytes.Repeat([]byte(`{"x":`), maxNestingDepth+1)
	deep = append(deep, bytes.Repeat([]byte("}"), maxNestingDepth+1)...)
	tests := []struct {
		name   string
		schema []byte
		code   Code
	}{
		{name: "external reference", schema: []byte(`{"type":"object","$ref":"https://example.com/schema"}`), code: CodeExternalRefForbidden},
		{name: "relative document reference", schema: []byte(`{"type":"object","$ref":"other.json"}`), code: CodeExternalRefForbidden},
		{name: "identifier", schema: []byte(`{"type":"object","$id":"https://example.com/schema"}`), code: CodeExternalRefForbidden},
		{name: "duplicate key", schema: []byte(`{"type":"object","type":"object"}`), code: CodeInvalidEnvelope},
		{name: "nesting depth", schema: append([]byte(`{"type":"object","properties":`), append(deep, '}')...), code: CodeBudgetExceeded},
		{name: "oversize raw input", schema: bytes.Repeat([]byte(" "), maxRawSchemaBytes+1), code: CodeInputTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Canonicalize(tc.schema)
			if err == nil {
				t.Fatal("Canonicalize() error = nil")
			}
			if got := ErrorCode(err); got != tc.code {
				t.Fatalf("Canonicalize() code = %q, want %q; error=%v", got, tc.code, err)
			}
		})
	}

	canonical, err := Canonicalize([]byte(`{"properties":{"child":{"$ref":"#"}},"type":"object"}`))
	if err != nil {
		t.Fatalf("Canonicalize(local reference) error = %v", err)
	}
	if got, want := string(canonical.Bytes), `{"properties":{"child":{"$ref":"#"}},"type":"object"}`; got != want {
		t.Fatalf("canonical bytes = %s, want %s", got, want)
	}
	_, err = Canonicalize([]byte(`{"type":"object","properties":{"type":{"type":"string"},"$ref":{"type":"string"},"pattern":{"type":"string"}}}`))
	if err != nil {
		t.Fatalf("Canonicalize(structural property names) error = %v", err)
	}
}

func TestSchemaCompilerCancellationOrIsolationIsBounded(t *testing.T) {
	if runCancellationFixture() {
		return
	}

	snapshotRoot := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, snapshotRoot)
	}
	snapshotsBefore := filesystemSnapshotDirectoryNames(t, snapshotRoot)
	canonical, err := Canonicalize([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	dir := t.TempDir()
	helper := filepath.Join(dir, HelperFileName(runtime.GOOS))
	image, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, image, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := helper + HelperManifestSuffix
	if err := WriteHelperManifest(helper, manifest, testHelperIdentity()); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(context.Background(), ClientConfig{
		HelperPath: helper, ManifestPath: manifest, FilesystemWorkerPath: os.Args[0], Identity: testHelperIdentity(),
	})
	if err != nil {
		t.Fatal(err)
	}
	client.operationTimeout = 2 * helperFixtureTimeout
	if err := os.WriteFile(helper, []byte("replaced-after-verification"), 0o700); err != nil {
		t.Fatal(err)
	}
	markerDir := t.TempDir()
	marker := filepath.Join(markerDir, "started")
	doneMarker := filepath.Join(markerDir, "finished")
	client.workerEnv = []string{
		"REASONIX_SCHEMA_HELPER_FIXTURE=sleep",
		"REASONIX_SCHEMA_HELPER_MARKER=" + marker,
		"REASONIX_SCHEMA_HELPER_DONE_MARKER=" + doneMarker,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	safego.Go(ctx, nil, "toolbridge.schema-helper.cancellation-test", func(context.Context) {
		_, executeErr := client.Execute(ctx, testInvocation(canonical), allowFence)
		result <- executeErr
	})
	waitForHelperMarker(t, marker)
	helperIdentity := captureFixtureProcessIdentity(t, marker)
	cancel()
	assertBoundedCancellation(t, result)
	assertStableProcessIdentityGone(t, helperIdentity)
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(doneMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("late helper write at %s: %v", doneMarker, err)
	}
	assertFilesystemSnapshotSetUnchanged(t, snapshotRoot, snapshotsBefore)
}

func runCancellationFixture() bool {
	if os.Getenv("REASONIX_SCHEMA_HELPER_FIXTURE") != "sleep" {
		return false
	}
	marker := os.Getenv("REASONIX_SCHEMA_HELPER_MARKER")
	if marker == "" {
		os.Exit(2)
	}
	if err := os.WriteFile(marker, fmt.Appendf(nil, "%d", os.Getpid()), 0o600); err != nil {
		os.Exit(3)
	}
	time.Sleep(30 * time.Second)
	if err := os.WriteFile(os.Getenv("REASONIX_SCHEMA_HELPER_DONE_MARKER"), []byte("finished"), 0o600); err != nil {
		os.Exit(4)
	}
	return true
}

func waitForHelperMarker(t *testing.T, marker string) {
	t.Helper()
	end := time.Now().Add(helperFixtureTimeout)
	for {
		if raw, err := os.ReadFile(marker); err == nil {
			if _, err := strconv.Atoi(string(raw)); err == nil {
				return
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read PID: %v", err)
		}
		if time.Now().After(end) {
			t.Fatal("helper wait timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func assertBoundedCancellation(t *testing.T, result <-chan error) {
	t.Helper()
	// Cancellation reaps the execute worker, then bounds cleanup and its worker reap.
	shutdownDeadline := 2*reapDeadline + filesystemSnapshotCleanupTimeout + time.Second
	timer := time.NewTimer(shutdownDeadline)
	defer timer.Stop()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || ErrorCode(err) != CodeCancelled {
			t.Fatalf("Execute(cancelled) error = %v, code=%q", err, ErrorCode(err))
		}
	case <-timer.C:
		t.Fatalf("cancelled helper cleanup exceeded %v", shutdownDeadline)
	}
}

func filesystemSnapshotDirectoryNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read schema snapshot root: %v", err)
	}
	names := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), filesystemSnapshotPrefix) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

func assertFilesystemSnapshotSetUnchanged(t *testing.T, root string, before []string) {
	t.Helper()
	after := filesystemSnapshotDirectoryNames(t, root)
	if strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("schema snapshot set changed: before=%v after=%v", before, after)
	}
}

func captureFixtureProcessIdentity(t *testing.T, marker string) pidregistry.StableProcessIdentity {
	t.Helper()
	rawPID, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(rawPID))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := pidregistry.CaptureStableProcessIdentity(pid)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func assertStableProcessIdentityGone(t *testing.T, identity pidregistry.StableProcessIdentity) {
	t.Helper()
	current, err := pidregistry.CaptureStableProcessIdentity(identity.PID)
	if errors.Is(err, pidregistry.ErrStableProcessNotFound) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if current.ProcessStartToken == identity.ProcessStartToken &&
		current.ExecutableIdentity == identity.ExecutableIdentity {
		t.Fatalf("schema helper identity remains alive: %+v", identity)
	}
}

func testInvocation(canonical CanonicalSchema) Invocation {
	return Invocation{
		Operation:           OperationCompile,
		RequestID:           "request-1",
		ServerID:            "server-1",
		ToolName:            "tool-1",
		AuthorityGeneration: 7,
		Schema:              canonical,
	}
}

func allowFence(context.Context, FenceStage, FenceIdentity) error {
	return nil
}

func TestSchemaProtocolStrictIdentityDigestAndFieldGuard(t *testing.T) {
	canonical, err := Canonicalize([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	req, err := newProtocolRequest(testInvocation(canonical))
	if err != nil {
		t.Fatalf("newProtocolRequest() error = %v", err)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	assertJSONFieldsMatchProducer(t, raw, req)

	unknown := bytes.TrimSuffix(raw, []byte("}"))
	unknown = append(unknown, []byte(`,"unknown":true}`)...)
	if _, err := decodeProtocolRequest(unknown); ErrorCode(err) != CodeInvalidEnvelope {
		t.Fatalf("unknown request field code = %q, want %q; error=%v", ErrorCode(err), CodeInvalidEnvelope, err)
	}
	duplicate := bytes.Replace(raw, []byte(`"protocol":`), []byte(`"protocol":"duplicate","protocol":`), 1)
	if _, err := decodeProtocolRequest(duplicate); ErrorCode(err) != CodeInvalidEnvelope {
		t.Fatalf("duplicate request field code = %q, want %q; error=%v", ErrorCode(err), CodeInvalidEnvelope, err)
	}
}

func TestFilesystemWorkerProtocolFieldsAndStrictDecoding(t *testing.T) {
	request := filesystemWorkerRequest{
		Version: filesystemWorkerVersion, Operation: filesystemWorkerVerify,
		HelperPath: "/tmp/helper", ManifestPath: "/tmp/helper.manifest",
		Identity: testHelperIdentity(), HelperGOOS: runtime.GOOS,
		ImageBytes: 1, RequestBytes: 1, DeadlineUnixNano: 1,
		Snapshot: filesystemSnapshotIdentity{
			Version: filesystemSnapshotVersion, Directory: "/tmp/reasonix-schema-helper.00112233445566778899aabbccddeeff",
			Token: "00112233445566778899aabbccddeeff", HelperGOOS: runtime.GOOS,
			OwnerPID: 1, OwnerStartToken: "start", OwnerExecutable: "executable",
		},
	}
	assertProducerFieldsAcceptedByConsumer(t, request, &filesystemWorkerRequest{})
	assertProducerFieldsAcceptedByConsumer(t, request.Snapshot, &filesystemSnapshotIdentity{})
	rawRequest, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	unknownRequest := append(bytes.TrimSuffix(rawRequest, []byte("}")), []byte(`,"unknown":true}`)...)
	unknownRequest = append(unknownRequest, '\n')
	if _, err := decodeFilesystemWorkerRequest(bufio.NewReader(bytes.NewReader(unknownRequest))); err == nil {
		t.Fatal("filesystem worker request accepted an unknown field")
	}

	response := filesystemWorkerResponse{
		Version: filesystemWorkerVersion, Operation: filesystemWorkerVerify, PayloadBytes: 1,
		Error: &filesystemWorkerError{
			Code: CodeProcessExited, Message: "fixture", FailureClass: InitializationFailureTransient,
		},
	}
	assertProducerFieldsAcceptedByConsumer(t, response, &filesystemWorkerResponse{})
	assertProducerFieldsAcceptedByConsumer(t, *response.Error, &filesystemWorkerError{})
	rawResponse, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	unknownResponse := append(bytes.TrimSuffix(rawResponse, []byte("}")), []byte(`,"unknown":true}`)...)
	unknownResponse = append(unknownResponse, '\n')
	if _, err := decodeFilesystemWorkerResponse(unknownResponse, filesystemWorkerVerify, 1); ErrorCode(err) != CodeProtocolViolation {
		t.Fatalf("unknown filesystem worker response field error = %v", err)
	}
}

func assertJSONFieldsMatchProducer(t *testing.T, raw []byte, producer any) {
	t.Helper()
	got, err := strictObjectFieldNames(raw)
	if err != nil {
		t.Fatalf("strictObjectFieldNames() error = %v", err)
	}
	want, err := encodedJSONFieldNames(producer)
	if err != nil {
		t.Fatalf("encodedJSONFieldNames() error = %v", err)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("protocol fields = %v, producer fields = %v", got, want)
	}
}

func TestSchemaCompilerStaleFencePreventsExecution(t *testing.T) {
	canonical, err := Canonicalize([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	client := newSchemaTestClient(t, os.Args[0])
	client.workerCommand = func(string) *exec.Cmd {
		t.Fatal("helper started after stale pre-launch fence")
		return nil
	}
	_, err = client.Execute(context.Background(), testInvocation(canonical), func(context.Context, FenceStage, FenceIdentity) error {
		return fmt.Errorf("generation changed")
	})
	if ErrorCode(err) != CodeGenerationStale {
		t.Fatalf("stale fence code = %q, want %q; error=%v", ErrorCode(err), CodeGenerationStale, err)
	}
}

func TestSchemaHelperProcessFixture(t *testing.T) {
	mode := os.Getenv("REASONIX_SCHEMA_MALICIOUS_HELPER")
	if mode == "" {
		return
	}
	time.Sleep(25 * time.Millisecond)
	if runImmediateMaliciousMode(mode) {
		return
	}
	request := readMaliciousRequest()
	response := maliciousResponse(mode, request)
	writeMaliciousResponse(mode, response)
	os.Exit(0)
}

func runImmediateMaliciousMode(mode string) bool {
	switch mode {
	case "stdout_overflow":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), maxStdoutBytes+1))
		return true
	case "nonzero":
		os.Exit(7)
	case "timeout":
		time.Sleep(30 * time.Second)
		os.Exit(12)
	}
	return false
}

func readMaliciousRequest() protocolRequest {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(8)
	}
	request, err := decodeProtocolRequest(raw)
	if err != nil {
		os.Exit(9)
	}
	return request
}

func maliciousResponse(mode string, request protocolRequest) protocolResponse {
	response := baseResponse(request)
	response.OK = true
	response.CompiledDigest = request.SchemaDigest
	switch mode {
	case "stderr_overflow":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("e"), maxStderrBytes+1))
	case "identity":
		response.RequestID = "wrong-request"
	case "digest":
		response.SchemaDigest = strings.Repeat("0", 64)
	case "compiled_digest":
		response.CompiledDigest = strings.Repeat("0", 64)
	case "trailing", "success":
	default:
		os.Exit(10)
	}
	return response
}

func writeMaliciousResponse(mode string, response protocolResponse) {
	encoded, err := json.Marshal(response)
	if err != nil {
		os.Exit(11)
	}
	if mode == "trailing" {
		encoded = append(encoded, 'x')
	}
	_, _ = os.Stdout.Write(encoded)
}

func TestSchemaCompilerRejectsMaliciousHelperOutputs(t *testing.T) {
	canonical, err := Canonicalize([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	tests := []struct {
		mode string
		code Code
	}{
		{mode: "stdout_overflow", code: CodeOutputTooLarge},
		{mode: "stderr_overflow", code: CodeOutputTooLarge},
		{mode: "nonzero", code: CodeProcessExited},
		{mode: "identity", code: CodeProtocolViolation},
		{mode: "digest", code: CodeDigestMismatch},
		{mode: "compiled_digest", code: CodeDigestMismatch},
		{mode: "trailing", code: CodeProtocolViolation},
		{mode: "timeout", code: CodeTimeout},
	}
	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			client := newSchemaTestClient(t, os.Args[0])
			if tc.mode != "timeout" {
				client.operationTimeout = helperFixtureTimeout
			}
			client.workerEnv = []string{"REASONIX_SCHEMA_MALICIOUS_HELPER=" + tc.mode}
			_, err = client.Execute(context.Background(), testInvocation(canonical), allowFence)
			if ErrorCode(err) != tc.code {
				t.Fatalf("Execute(%s) code = %q, want %q; error=%v", tc.mode, ErrorCode(err), tc.code, err)
			}
		})
	}
}

func TestSchemaCompilerPostSuccessStaleFence(t *testing.T) {
	canonical, err := Canonicalize([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	client := newSchemaTestClient(t, os.Args[0])
	client.operationTimeout = helperFixtureTimeout
	client.workerEnv = []string{"REASONIX_SCHEMA_MALICIOUS_HELPER=success"}
	stages := make([]FenceStage, 0, 2)
	_, err = client.Execute(context.Background(), testInvocation(canonical), func(_ context.Context, stage FenceStage, _ FenceIdentity) error {
		stages = append(stages, stage)
		if stage == FenceAfterSuccess {
			return errors.New("generation changed")
		}
		return nil
	})
	if ErrorCode(err) != CodeGenerationStale {
		t.Fatalf("post-success stale code = %q, want %q; error=%v", ErrorCode(err), CodeGenerationStale, err)
	}
	if got := fmt.Sprint(stages); got != fmt.Sprint([]FenceStage{FenceBeforeLaunch, FenceAfterSuccess}) {
		t.Fatalf("fence stages = %v", stages)
	}
}

func TestSchemaCompilerExecutesPinnedImageAfterPackagePathReplacement(t *testing.T) {
	canonical, err := Canonicalize([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	helper := filepath.Join(dir, HelperFileName(runtime.GOOS))
	image, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, image, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := helper + HelperManifestSuffix
	if err := WriteHelperManifest(helper, manifest, testHelperIdentity()); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(context.Background(), ClientConfig{
		HelperPath: helper, ManifestPath: manifest, FilesystemWorkerPath: os.Args[0], Identity: testHelperIdentity(),
	})
	if err != nil {
		t.Fatal(err)
	}
	client.operationTimeout = helperFixtureTimeout
	if err := os.WriteFile(helper, []byte("replaced-after-verification"), 0o700); err != nil {
		t.Fatal(err)
	}
	client.workerEnv = []string{"REASONIX_SCHEMA_MALICIOUS_HELPER=success"}
	result, err := client.Execute(context.Background(), testInvocation(canonical), allowFence)
	if err != nil || result.CompiledDigest != canonical.Digest {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
}

func TestSchemaProtocolProducerFieldsAreDynamicallyGuarded(t *testing.T) {
	canonical, err := Canonicalize([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	compileRequest, err := newProtocolRequest(testInvocation(canonical))
	if err != nil {
		t.Fatalf("newProtocolRequest() error = %v", err)
	}
	compileResponse := executeLocal(compileRequest)
	assertProducerFieldsAcceptedByConsumer(t, compileRequest, &protocolRequest{})
	assertProducerFieldsAcceptedByConsumer(t, compileResponse, &protocolResponse{})

	validateInvocation := testInvocation(canonical)
	validateInvocation.Operation = OperationValidate
	validateInvocation.Arguments = json.RawMessage(`{}`)
	validateRequest, err := newProtocolRequest(validateInvocation)
	if err != nil {
		t.Fatalf("newProtocolRequest(validate) error = %v", err)
	}
	validateResponse := executeLocal(validateRequest)
	assertProducerFieldsAcceptedByConsumer(t, validateRequest, &protocolRequest{})
	assertProducerFieldsAcceptedByConsumer(t, validateResponse, &protocolResponse{})
}

func assertProducerFieldsAcceptedByConsumer(t *testing.T, producer, consumer any) {
	t.Helper()
	encoded, err := json.Marshal(producer)
	if err != nil {
		t.Fatalf("marshal producer: %v", err)
	}
	producerFields, err := strictObjectFieldNames(encoded)
	if err != nil {
		t.Fatalf("enumerate producer fields: %v", err)
	}
	allowed, required, err := jsonTypeFields(consumer)
	if err != nil {
		t.Fatalf("enumerate consumer fields: %v", err)
	}
	for _, field := range producerFields {
		if _, ok := allowed[field]; !ok {
			t.Errorf("producer field %q is missing from consumer %T", field, consumer)
		}
		delete(allowed, field)
		delete(required, field)
	}
	for field := range required {
		t.Errorf("producer %T omitted required consumer field %q", producer, field)
	}
	for field := range allowed {
		if field != "arguments" && field != "arguments_valid" {
			t.Errorf("consumer field %q is stale for producer %T", field, producer)
		}
	}
}

func TestSchemaCompilerRejectsNonAbsoluteOrUncleanHelperPaths(t *testing.T) {
	directory := t.TempDir()
	helper := filepath.Join(directory, "mcp-schema-compiler-helper")
	marker := filepath.Join(directory, "started")
	script := "#!/bin/sh\nprintf started >" + marker + "\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatalf("write helper fixture: %v", err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	paths := []string{
		"mcp-schema-compiler-helper",
		"./mcp-schema-compiler-helper",
		filepath.Join("relative", "mcp-schema-compiler-helper"),
		directory + string(os.PathSeparator) + ".." + string(os.PathSeparator) +
			filepath.Base(directory) + string(os.PathSeparator) + "mcp-schema-compiler-helper",
	}
	for _, helperPath := range paths {
		client, err := NewClient(context.Background(), ClientConfig{
			HelperPath: helperPath, ManifestPath: helper + HelperManifestSuffix,
			FilesystemWorkerPath: os.Args[0], Identity: testHelperIdentity(),
		})
		if err == nil || client != nil {
			t.Errorf("NewClient(%q) = (%v, %v), want rejection", helperPath, client, err)
		}
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-pinned helper was started: stat error = %v", err)
	}
	if err := WriteHelperManifest(helper, helper+HelperManifestSuffix, testHelperIdentity()); err != nil {
		t.Fatalf("write helper manifest: %v", err)
	}
	client, err := NewClient(context.Background(), ClientConfig{
		HelperPath: helper, ManifestPath: helper + HelperManifestSuffix,
		FilesystemWorkerPath: os.Args[0], Identity: testHelperIdentity(),
	})
	if err != nil || client == nil {
		t.Fatalf("NewClient(absolute clean) = (%v, %v)", client, err)
	}
}

func TestSchemaCompilerCapacityWaitIsBounded(t *testing.T) {
	canonical, err := Canonicalize([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	for range maxLiveHelpers {
		globalHelperLimiter.slots <- struct{}{}
	}
	defer func() {
		for range maxLiveHelpers {
			<-globalHelperLimiter.slots
		}
	}()
	client := newSchemaTestClient(t, os.Args[0])
	started := time.Now()
	_, err = client.Execute(context.Background(), testInvocation(canonical), allowFence)
	if ErrorCode(err) != CodeCapacityExhausted {
		t.Fatalf("capacity code = %q, want %q; error=%v", ErrorCode(err), CodeCapacityExhausted, err)
	}
	if elapsed := time.Since(started); elapsed > capacityWait+250*time.Millisecond {
		t.Fatalf("capacity wait took %v", elapsed)
	}
}

func TestSchemaCompilerReapFailurePermanentlyConsumesCapacity(t *testing.T) {
	limiter := newHelperLimiter(maxLiveHelpers)
	reapFailed := func() (Result, error) {
		return Result{}, newDiagnostic(CodeReapFailed, "fixture did not reap", nil)
	}
	for range maxLiveHelpers {
		if _, err := limiter.run(context.Background(), reapFailed); ErrorCode(err) != CodeReapFailed {
			t.Fatalf("limiter.run() code = %q, want %q; error=%v", ErrorCode(err), CodeReapFailed, err)
		}
	}
	started := false
	startedAt := time.Now()
	_, err := limiter.run(context.Background(), func() (Result, error) {
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
