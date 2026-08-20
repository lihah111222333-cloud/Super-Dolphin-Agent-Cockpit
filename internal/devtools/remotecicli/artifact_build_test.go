package remotecicli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const artifactBuildTestBucket = "fixturebucket"

func TestArtifactBuildUsesRAMRoleOSSAndPublishesArtifactBeforeReceipt(t *testing.T) {
	fixture := newArtifactBuildTestFixture(t)
	ossServer, ossState := newArtifactBuildTestOSS(t, fixture)
	imdsServer := newArtifactBuildTestIMDS(t)

	var stdout, stderr bytes.Buffer
	err := dispatchArtifactBuildWithDependencies(
		artifactBuildTestArgs(fixture, ossServer.URL),
		&stdout,
		&stderr,
		artifactBuildDependencies{httpClient: ossServer.Client(), metadataBaseURL: imdsServer.URL, observedIdentity: artifactBuildTestObserver(fixture)},
	)
	if err != nil {
		t.Fatalf("artifact build error = %v; stderr=%q", err, stderr.String())
	}
	var output artifactBuildOutput
	resultBytes := bytes.TrimPrefix(bytes.TrimSpace(stdout.Bytes()), []byte(artifactBuildRuntimePrefix))
	if err := json.Unmarshal(resultBytes, &output); err != nil {
		t.Fatalf("decode artifact build output: %v; stdout=%q", err, stdout.String())
	}
	if output.Status != "NOT_VERIFIED" || output.ExecutionProvider != remoteci.PortableExecutionProvider {
		t.Fatalf("artifact build output identity = %#v", output)
	}
	for _, digest := range []string{output.InputDigest, output.ArtifactSHA256, output.ReceiptSHA256, output.ReceiptDigest} {
		if !isDigest(digest) {
			t.Fatalf("artifact build output digest %q is invalid", digest)
		}
	}
	if strings.Contains(stderr.String(), fixture.inputKey) || strings.Contains(stderr.String(), artifactBuildTestBucket) {
		t.Fatalf("artifact build stderr leaked object identity: %q", stderr.String())
	}
	assertArtifactBuildOSSOrder(t, ossState)
}

func TestArtifactBuildRejectsWrongInputAndReceiptMetadata(t *testing.T) {
	fixture := newArtifactBuildTestFixture(t)
	imdsServer := newArtifactBuildTestIMDS(t)

	t.Run("wrong input digest", func(t *testing.T) {
		ossServer, _ := newArtifactBuildTestOSS(t, fixture)
		args := artifactBuildTestArgs(fixture, ossServer.URL)
		replaceArtifactBuildArg(args, "input-sha256", "sha256:"+strings.Repeat("0", 64))
		var stdout, stderr bytes.Buffer
		if err := dispatchArtifactBuildWithDependencies(args, &stdout, &stderr, artifactBuildDependencies{httpClient: ossServer.Client(), metadataBaseURL: imdsServer.URL, observedIdentity: artifactBuildTestObserver(fixture)}); err == nil {
			t.Fatal("artifact build accepted wrong input digest")
		}
		if stdout.Len() != 0 || strings.Contains(stderr.String(), fixture.inputKey) {
			t.Fatalf("wrong input result leaked output: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	})

	t.Run("wrong artifact readback metadata", func(t *testing.T) {
		ossServer, state := newArtifactBuildTestOSS(t, fixture)
		state.mu.Lock()
		state.wrongArtifactMetadata = true
		state.mu.Unlock()
		var stdout, stderr bytes.Buffer
		if err := dispatchArtifactBuildWithDependencies(artifactBuildTestArgs(fixture, ossServer.URL), &stdout, &stderr, artifactBuildDependencies{httpClient: ossServer.Client(), metadataBaseURL: imdsServer.URL, observedIdentity: artifactBuildTestObserver(fixture)}); err == nil {
			t.Fatal("artifact build accepted wrong artifact metadata")
		}
		if stdout.Len() != 0 || strings.Contains(stderr.String(), fixture.inputKey) {
			t.Fatalf("wrong metadata result leaked output: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
		if !strings.Contains(stderr.String(), "progress: artifact build failed stage=upload_artifact\n") {
			t.Fatalf("artifact failure stage was not observable: stderr=%q", stderr.String())
		}
		if strings.Contains(stderr.String(), "wrong artifact") {
			t.Fatalf("artifact failure leaked raw cause: stderr=%q", stderr.String())
		}
		events := state.eventsSnapshot()
		if strings.Contains(strings.Join(events, ","), "receipt") {
			t.Fatalf("receipt upload started after artifact metadata rejection: %v", events)
		}
	})
}

func TestArtifactBuildRejectsUnknownMissingDuplicateAndIMDSFlags(t *testing.T) {
	fixture := newArtifactBuildTestFixture(t)
	base := artifactBuildTestArgs(fixture, "https://oss.example.invalid")
	cases := []struct {
		name   string
		mutate func([]string) []string
	}{
		{name: "unknown", mutate: func(args []string) []string { return append(args, "--unknown", "value") }},
		{name: "missing", mutate: func(args []string) []string { return removeArtifactBuildArg(args, "expected-toolchain-digest") }},
		{name: "missing compiler path", mutate: func(args []string) []string { return removeArtifactBuildArg(args, "compiler-path") }},
		{name: "duplicate compiler path", mutate: func(args []string) []string { return append(args, "--compiler-path", "/usr/local/go/bin/go") }},
		{name: "unclean compiler path", mutate: func(args []string) []string {
			replaceArtifactBuildArg(args, "compiler-path", "/usr/local/go/../go/bin/go")
			return args
		}},
		{name: "duplicate", mutate: func(args []string) []string { return append(args, "--input-key", fixture.inputKey) }},
		{name: "metadata endpoint injection", mutate: func(args []string) []string { return append(args, "--metadata-base-url", "http://attacker.invalid") }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run(append([]string{"_artifact-build"}, testCase.mutate(append([]string(nil), base...))...), &stdout, &stderr)
			if code == 0 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("invalid args result = code %d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), fixture.inputKey) {
				t.Fatalf("invalid args diagnostic leaked input key: %q", stderr.String())
			}
		})
	}
}

type artifactBuildTestFixture struct {
	bundle                    []byte
	inputKey                  string
	artifactPrefix            string
	inputSHA256               string
	inputSizeBytes            int64
	repositoryID              string
	objectFormat              string
	commitSHA                 string
	treeSHA                   string
	expectedBuilderDigest     string
	expectedToolchainDigest   string
	protocolABIDigest         string
	workloadManifestABIDigest string
	compilerPath              string
}

func newArtifactBuildTestFixture(t *testing.T) artifactBuildTestFixture {
	t.Helper()
	root := t.TempDir()
	repository := filepath.Join(root, "input.git")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatalf("create input repository directory: %v", err)
	}
	runArtifactBuildGit(t, repository, nil, "init", "--bare", "--object-format=sha1", "--quiet")
	tree := runArtifactBuildGit(t, repository, nil, "hash-object", "-t", "tree", "-w", "--stdin")
	commitPayload := fmt.Appendf(nil, "tree %s\nauthor Fixture <fixture@example.invalid> 0 +0000\ncommitter Fixture <fixture@example.invalid> 0 +0000\n\nfixture input\n", tree)
	commit := runArtifactBuildGit(t, repository, commitPayload, "hash-object", "-t", "commit", "-w", "--stdin")
	runArtifactBuildGit(t, repository, nil, "update-ref", "refs/source/input", commit)
	bundlePath := filepath.Join(root, "input.bundle")
	runArtifactBuildGit(t, repository, nil, "bundle", "create", bundlePath, "--end-of-options", "refs/source/input")
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read input bundle: %v", err)
	}
	digest := sha256.Sum256(bundle)
	protocolABI, err := remoteci.BuilderSimulationProtocolABIDigest()
	if err != nil {
		t.Fatalf("builder protocol ABI fixture: %v", err)
	}
	workloadABI, err := remoteci.RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatalf("workload manifest ABI fixture: %v", err)
	}
	return artifactBuildTestFixture{
		bundle: bundle, artifactPrefix: "cli/", inputKey: "cli/inputs/source.bundle", inputSHA256: "sha256:" + hex.EncodeToString(digest[:]), inputSizeBytes: int64(len(bundle)),
		repositoryID: "repository-1", objectFormat: "sha1", commitSHA: commit, treeSHA: tree,
		expectedBuilderDigest: "sha256:" + strings.Repeat("b", 64), expectedToolchainDigest: "sha256:" + strings.Repeat("c", 64),
		protocolABIDigest: protocolABI, workloadManifestABIDigest: workloadABI, compilerPath: "/usr/local/go/bin/go",
	}
}

func artifactBuildTestArgs(fixture artifactBuildTestFixture, ossEndpoint string) []string {
	return []string{
		"--compiler-path", fixture.compilerPath,
		"--execution-provider", remoteci.PortableExecutionProvider,
		"--oss-endpoint", ossEndpoint, "--oss-bucket", artifactBuildTestBucket, "--ram-role-name", "AliyunECIArtifactBuilderRole",
		"--artifact-kind", remoteci.PortableSourceBaselineArtifactKind, "--artifact-ref", remoteci.PortableSourceBaselineArtifactRef,
		"--artifact-prefix", fixture.artifactPrefix,
		"--input-key", fixture.inputKey, "--input-sha256", fixture.inputSHA256, "--input-size-bytes", strconv.FormatInt(fixture.inputSizeBytes, 10),
		"--repository-id", fixture.repositoryID, "--object-format", fixture.objectFormat, "--source-commit-sha", fixture.commitSHA, "--source-tree-sha", fixture.treeSHA,
		"--expected-builder-digest", fixture.expectedBuilderDigest, "--expected-toolchain-digest", fixture.expectedToolchainDigest,
		"--protocol-abi-digest", fixture.protocolABIDigest, "--workload-manifest-abi-digest", fixture.workloadManifestABIDigest,
	}
}

func artifactBuildTestObserver(fixture artifactBuildTestFixture) func(context.Context, string) (remoteci.ObservedArtifactBuilderIdentity, error) {
	return func(_ context.Context, compilerPath string) (remoteci.ObservedArtifactBuilderIdentity, error) {
		if compilerPath != fixture.compilerPath {
			return remoteci.ObservedArtifactBuilderIdentity{}, fmt.Errorf("compiler path = %q", compilerPath)
		}
		return remoteci.ObservedArtifactBuilderIdentity{BuilderDigest: fixture.expectedBuilderDigest, ToolchainDigest: fixture.expectedToolchainDigest}, nil
	}
}

func replaceArtifactBuildArg(args []string, name, value string) {
	for index := range args {
		if args[index] == "--"+name && index+1 < len(args) {
			args[index+1] = value
			return
		}
	}
}

func removeArtifactBuildArg(args []string, name string) []string {
	for index := range args {
		if args[index] == "--"+name && index+1 < len(args) {
			return append(args[:index], args[index+2:]...)
		}
	}
	return args
}

func runArtifactBuildGit(t *testing.T, repository string, input []byte, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", repository}, args...)
	command := exec.Command("git", commandArgs...)
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

type artifactBuildTestObject struct {
	data       []byte
	sha256     string
	size       int64
	forbidOnly bool
}

type artifactBuildTestOSSState struct {
	mu                    sync.Mutex
	fixture               artifactBuildTestFixture
	objects               map[string]artifactBuildTestObject
	events                []string
	wrongArtifactMetadata bool
}

func newArtifactBuildTestOSS(t *testing.T, fixture artifactBuildTestFixture) (*httptest.Server, *artifactBuildTestOSSState) {
	t.Helper()
	state := &artifactBuildTestOSSState{fixture: fixture, objects: make(map[string]artifactBuildTestObject)}
	server := httptest.NewTLSServer(state)
	t.Cleanup(server.Close)
	return server, state
}

func (state *artifactBuildTestOSSState) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	key, kind, ok := artifactBuildTestRoute(request.URL.Path)
	if !ok {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	switch request.Method {
	case http.MethodGet:
		state.serveGET(response, key, kind)
	case http.MethodPut:
		state.servePUT(response, request, key, kind)
	case http.MethodHead:
		state.serveHEAD(response, key, kind)
	default:
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func artifactBuildTestRoute(path string) (string, string, bool) {
	prefix := "/" + artifactBuildTestBucket + "/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	key := strings.TrimPrefix(path, prefix)
	switch {
	case strings.HasPrefix(key, "cli/inputs/"):
		return key, "input", true
	case strings.HasPrefix(key, "cli/artifacts/"):
		return key, "artifact", true
	case strings.HasPrefix(key, "cli/receipts/"):
		return key, "receipt", true
	default:
		return key, "other", true
	}
}

func (state *artifactBuildTestOSSState) serveGET(response http.ResponseWriter, key, kind string) {
	state.events = append(state.events, kind+"-get")
	object, ok := state.objects[key]
	if !ok && key == state.fixture.inputKey {
		digest := sha256.Sum256(state.fixture.bundle)
		object = artifactBuildTestObject{data: state.fixture.bundle, sha256: "sha256:" + hex.EncodeToString(digest[:]), size: int64(len(state.fixture.bundle))}
		ok = true
	}
	if !ok {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	writeArtifactBuildTestObject(response, object, false)
}

func (state *artifactBuildTestOSSState) servePUT(response http.ResponseWriter, request *http.Request, key, kind string) {
	state.events = append(state.events, kind+"-put")
	if request.Header.Get("x-oss-forbid-overwrite") != "true" {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	if _, exists := state.objects[key]; exists {
		response.WriteHeader(http.StatusConflict)
		return
	}
	data, err := io.ReadAll(request.Body)
	if err != nil {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	state.objects[key] = artifactBuildTestObject{data: data, sha256: request.Header.Get("x-oss-meta-sha256"), size: int64(len(data)), forbidOnly: true}
	response.WriteHeader(http.StatusOK)
}

func (state *artifactBuildTestOSSState) serveHEAD(response http.ResponseWriter, key, kind string) {
	state.events = append(state.events, kind+"-head")
	object, ok := state.objects[key]
	if !ok {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	if state.wrongArtifactMetadata && kind == "artifact" {
		object.sha256 = "sha256:" + strings.Repeat("0", 64)
	}
	writeArtifactBuildTestObject(response, object, true)
}

func writeArtifactBuildTestObject(response http.ResponseWriter, object artifactBuildTestObject, headOnly bool) {
	response.Header().Set("Content-Length", strconv.FormatInt(object.size, 10))
	response.Header().Set("x-oss-meta-sha256", object.sha256)
	response.Header().Set("x-oss-meta-size", strconv.FormatInt(object.size, 10))
	if !headOnly {
		_, _ = response.Write(object.data)
	}
}

func (state *artifactBuildTestOSSState) eventsSnapshot() []string {
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]string(nil), state.events...)
}

func assertArtifactBuildOSSOrder(t *testing.T, state *artifactBuildTestOSSState) {
	t.Helper()
	events := state.eventsSnapshot()
	want := []string{"input-get", "artifact-put", "artifact-head", "artifact-get", "receipt-put", "receipt-head", "receipt-get"}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("OSS call order = %v, want %v", events, want)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for key, object := range state.objects {
		if strings.HasPrefix(key, "cli/artifacts/") || strings.HasPrefix(key, "cli/receipts/") {
			if !object.forbidOnly {
				t.Fatalf("object %q did not use create-only upload", key)
			}
		}
	}
}

func newArtifactBuildTestIMDS(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPut && request.URL.Path == "/latest/api/token":
			_, _ = response.Write([]byte("fixture-imds-token"))
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/latest/meta-data/ram/security-credentials/"):
			response.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(response, `{"Code":"Success","AccessKeyId":"fixture-access-key","AccessKeySecret":"fixture-access-secret","SecurityToken":"fixture-security-token","Expiration":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestArtifactBuildUploadRejectsNonCanonicalMetadataBeforeNetwork(t *testing.T) {
	fixture := newArtifactBuildTestFixture(t)
	flags, err := parseArtifactBuildFlags(artifactBuildTestArgs(fixture, "https://oss.example.invalid"))
	if err != nil {
		t.Fatalf("parseArtifactBuildFlags() error = %v", err)
	}
	upload := newArtifactBuildUpload(flags, artifactBuildDependencies{}, artifactBuildArtifactUpload, &artifactBuildUploadState{})
	err = upload(context.Background(), "/not-used", "wrong-key", remoteci.SourceBaselineArtifactMetadata{SHA256: "sha256:" + strings.Repeat("a", 64), SizeBytes: 1})
	if err == nil {
		t.Fatal("upload accepted a non-canonical artifact key")
	}
}
