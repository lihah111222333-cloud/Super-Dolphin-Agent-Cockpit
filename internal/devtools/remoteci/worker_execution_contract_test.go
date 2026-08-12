package remoteci

import (
	"errors"
	"fmt"
	"go/ast"
	"os"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestWorkerExecutionRootsAreCanonical(t *testing.T) {
	if err := validateWorkerExecutionRoots(workerExecutionRoots); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerExecutionMissingRootIsTypedUnavailable(t *testing.T) {
	index := (&remoteGitTreeSnapshot{goSources: map[string][]byte{"fixture.go": []byte("package fixture\n")}}).buildWorkerExecutionGoIndex()
	_, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "missing"})
	if !errors.Is(err, errWorkerExecutionRootUnavailable) {
		t.Fatalf("missing worker execution root error = %v", err)
	}
}

// TestWorkerExecutionCommandClassifierSeparatesPackagePath 拒绝把 Go 包路径误判为 Gate 自执行命令。
func TestWorkerExecutionCommandClassifierSeparatesPackagePath(t *testing.T) {
	if workerExecutionLooksLikeCommand([]string{"cmd/super-dolphin-gate"}) {
		t.Fatal("Go package path was classified as a super-dolphin-gate self-command")
	}
	if !workerExecutionLooksLikeCommand([]string{"super-dolphin-gate", "worker"}) {
		t.Fatal("actual super-dolphin-gate self-command was not classified")
	}
}

func TestWorkerExecutionClosureSkipsAbsentOptionalFunctionNodes(t *testing.T) {
	tests := []struct {
		name             string
		source           string
		symbol           string
		wantSignatureNil bool
		wantBodyNil      bool
	}{
		{
			name:             "missing results",
			source:           "package fixture\nfunc noResults() {}\n",
			symbol:           "noResults",
			wantSignatureNil: true,
		},
		{
			name:        "missing body",
			source:      "package fixture\nfunc declarationOnly() error\n",
			symbol:      "declarationOnly",
			wantBodyNil: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := &remoteGitTreeSnapshot{
				goSources: map[string][]byte{"fixture.go": []byte(test.source)},
			}
			index := snapshot.buildWorkerExecutionGoIndex()
			unit, err := index.resolveRoot(workerExecutionRoot{
				directory: ".",
				symbol:    test.symbol,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := unit.signature == nil; got != test.wantSignatureNil {
				t.Fatalf("signature nil = %t, want %t", got, test.wantSignatureNil)
			}
			if got := unit.dependencies == nil; got != test.wantBodyNil {
				t.Fatalf("dependencies nil = %t, want %t", got, test.wantBodyNil)
			}

			closure := newWorkerExecutionGoClosure(index)
			closure.enqueue(unit)
			if err := closure.resolve(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkerExecutionClosureIgnoresUncalledReceiverMethods(t *testing.T) {
	const sourceTemplate = `package fixture

type coordinator struct{}

func (c *coordinator) root() { c.used() }
func (c *coordinator) used() { %s }
func (c *coordinator) unrelated() { %s }
`
	digest := func(usedBody, unrelatedBody string) string {
		formattedSource := fmt.Appendf(nil, sourceTemplate, usedBody, unrelatedBody)
		snapshot := &remoteGitTreeSnapshot{
			goSources: map[string][]byte{
				"fixture.go": formattedSource,
			},
		}
		index := snapshot.buildWorkerExecutionGoIndex()
		root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
		if err != nil {
			t.Fatal(err)
		}
		closure := newWorkerExecutionGoClosure(index)
		closure.enqueue(root)
		if err := closure.resolve(); err != nil {
			t.Fatal(err)
		}
		assets := &workerExecutionAssets{entries: map[string]remoteGitTreeEntry{}, fragments: map[string]workerExecutionFragment{}}
		value, err := digestWorkerExecutionClosure(closure, assets)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first := digest("", "_ = 1")
	second := digest("", "_ = 2")
	if first != second {
		t.Fatalf("unrelated receiver method changed worker execution digest: %q != %q", first, second)
	}
	used := digest("_ = 2", "_ = 1")
	if first == used {
		t.Fatal("called receiver method change was omitted from worker execution digest")
	}
}

func TestWorkerExecutionPreciseAndLegacyDigestScope(t *testing.T) {
	base := workerExecutionSyntheticSources("materializeV1", "cliV1", "gate.ExecutorWorkRoot", "resources", "cleanupV1")
	unrelated := workerExecutionSyntheticSources("materializeV1", "cliV1", "gate.ExecutorWorkRoot", "otherResources", "cleanupV2")
	semantic := workerExecutionSyntheticSources("materializeV1", "cliV1", "gate.ExecutorSourcePath", "resources", "cleanupV1")
	workerCLI := workerExecutionSyntheticSources("materializeV1", "cliV1", "gate.ExecutorWorkRoot", "resources", "cleanupV2")
	executor := workerExecutionSyntheticSources("materializeV1", "cliV2", "gate.ExecutorWorkRoot", "resources", "cleanupV1")
	materialize := workerExecutionSyntheticSources("materializeV2", "cliV1", "gate.ExecutorWorkRoot", "resources", "cleanupV1")

	precise := func(source map[string][]byte) string {
		return workerExecutionSyntheticDigest(t, source, workerExecutionRoots, false, true)
	}
	legacy := func(source map[string][]byte) string {
		return workerExecutionSyntheticDigest(t, source, workerExecutionLegacyV4Roots(), true, false)
	}
	preciseBase, preciseUnrelated := precise(base), precise(unrelated)
	if preciseBase != preciseUnrelated {
		t.Fatalf("precise worker digest changed for resource/cleanup-only request edits: %q != %q", preciseBase, preciseUnrelated)
	}
	if legacy(base) == legacy(unrelated) {
		t.Fatal("legacy v4 digest unexpectedly ignored broad createRequest edits")
	}
	if precise(workerCLI) != preciseBase {
		t.Fatal("worker CLI planning-only change invalidated precise execution digest")
	}
	for name, source := range map[string]map[string][]byte{
		"request semantic fragment": semantic,
		"canonical executor":        executor,
		"worker materialize":        materialize,
	} {
		if precise(source) == preciseBase {
			t.Fatalf("precise worker digest ignored %s change", name)
		}
	}
}

func TestWorkerExecutionExecutorConfigFragmentSurvivesOwnerExtraction(t *testing.T) {
	legacy := workerExecutionExecutorConfigFixture(t, `func ExecuteExecutor() { config := executorConfig{sourcePath: "/source"}; _ = config }`)
	extracted := workerExecutionExecutorConfigFixture(t, `
func ExecuteExecutor() { executeCanonicalGate() }
func executeCanonicalGate() { config := executorConfig{sourcePath: "/source"}; _ = config }
`)
	changed := workerExecutionExecutorConfigFixture(t, `func executeCanonicalGate() { config := executorConfig{sourcePath: "/other"}; _ = config }`)
	if legacy != extracted {
		t.Fatalf("executor config owner extraction changed semantic fragment: %q != %q", legacy, extracted)
	}
	if legacy == changed {
		t.Fatal("executor runtime config change was omitted from semantic fragment")
	}
}

func workerExecutionExecutorConfigFixture(t *testing.T, functions string) string {
	t.Helper()
	source := []byte("package gate\ntype executorConfig struct { sourcePath string }\n" + functions)
	assets := &workerExecutionAssets{snapshot: &remoteGitTreeSnapshot{goSources: map[string][]byte{
		workerExecutionExecutorSourcePath: source,
	}}}
	content, err := workerExecutionExecutorConfigContent(assets)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestWorkerExecutionDynamicReceiverFailsClosed(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

type worker struct{}

func (w *worker) used() {}
func root(value interface{}) { value.used() }
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err == nil || !strings.Contains(err.Error(), "dynamic") {
		t.Fatalf("dynamic receiver did not fail closed: %v", err)
	}
	legacy := newWorkerExecutionGoClosure(index)
	legacy.includeAllReceiverMethods = true
	legacy.enqueue(root)
	if err := legacy.resolve(); err != nil {
		t.Fatalf("legacy v4 closure rejected dynamic receiver: %v", err)
	}
}

func TestWorkerExecutionNamedInterfaceMethodResolvesImplementations(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

type Validatable interface { Validate() error }
type request struct{}
func (request) Validate() error { return nil }
func root(value Validatable) { _ = value.Validate() }
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("named interface method was misclassified as dynamic: %v", err)
	}
}

func TestWorkerExecutionExternalTypedMethodDoesNotFailClosed(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

import "strings"

func root() { var builder strings.Builder; _ = builder.String() }
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("external typed method was misclassified as dynamic: %v", err)
	}
}

func TestWorkerExecutionExternalTypeAliasMethodDoesNotFailClosed(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

import "log/slog"

type Logger = slog.Logger
func root(logger *Logger) { logger.Error("failed") }
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("external type alias method was misclassified as local: %v", err)
	}
}

func TestWorkerExecutionPredeclaredErrorMethodDoesNotFailClosed(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

func root(run func() error) string {
	err := run()
	if err != nil { return err.Error() }
	return ""
}
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("predeclared error method was misclassified as local: %v", err)
	}
}

func TestWorkerExecutionExternalSelectorNilUnitFailsClosed(t *testing.T) {
	if workerExecutionExternalSelector(nil, ast.NewIdent("external")) {
		t.Fatal("nil worker unit was treated as an external selector")
	}
}

func TestWorkerExecutionExternalCallChainDoesNotFailClosed(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

import "time"

func root() int { return int(time.Now().UnixMilli()) }
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("external call-chain method was misclassified as dynamic: %v", err)
	}
}

func TestWorkerExecutionExternalVariableCallChainDoesNotFailClosed(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

import "os"

func root(path string) {
	info, err := os.Stat(path)
	if err != nil { return }
	_ = info.Mode().Perm()
}
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("external variable call-chain method was misclassified as dynamic: %v", err)
	}
}

func TestWorkerExecutionExternalRangeValueMethodDoesNotFailClosed(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

import (
	"reflect"
	"strings"
)

type payload struct { Name string }
func root() {
	payloadType := reflect.TypeFor[payload]()
	for field := range payloadType.Fields() {
		_, _, _ = strings.Cut(field.Tag.Get("json"), ",")
	}
}
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("external range value method was misclassified as dynamic: %v", err)
	}
}

func TestWorkerExecutionExternalTypeAssertionMethodDoesNotFailClosed(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

import "net/http"

func root(roundTripper http.RoundTripper) {
	transport, ok := roundTripper.(*http.Transport)
	if !ok { return }
	_ = transport.Clone()
}
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if !workerExecutionExternalSelector(root, ast.NewIdent("transport")) {
		t.Fatal("external type assertion provenance was not recognized")
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("external type-assertion method was misclassified as dynamic: %v", err)
	}
}

func TestWorkerExecutionExternalFieldMethodDoesNotFailClosed(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

import "net/http"

type holder struct { client *http.Client }
func root(value holder) { value.client.Do(nil) }
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("external field method was misclassified as dynamic: %v", err)
	}
}

func TestWorkerExecutionIndexedLocalStructExternalFieldMethodResolves(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

import "time"

type score struct{}
func (score) less(score) bool { return false }
type result struct { started time.Time; completed time.Time; score score }
func (result) Validate() error { return nil }
type runner func() (result, error)
func load() ([]result, error) { return nil, nil }
func root(results []result, run runner) {
	made := make([]result, 1)
	_ = made[0].Validate()
	current := results[0]
	_ = current.started.Equal(current.completed)
	for _, current := range results {
		_ = current.started.Equal(current.completed)
	}
	value, err := run()
	if err == nil { _ = value.Validate() }
	loaded, err := load()
	loaded = loaded[:len(loaded)]
	if err == nil {
		_ = loaded[0].started.Equal(loaded[0].completed)
		_ = loaded[0].score.less(loaded[0].score)
	}
}
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("indexed local struct field method was not resolved: %v", err)
	}
}

func TestWorkerExecutionImportedLocalFactoryMethodResolves(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{
		moduleMappings: []remoteGoModuleMapping{{importPath: "example.invalid/fixture", directory: "."}},
		goSources: map[string][]byte{
			"fixture.go": []byte(`package fixture

import "example.invalid/fixture/workerio"

func root() {
	client, err := workerio.NewClient()
	if err != nil { return }
	client.Download()
	envelope, err := workerio.NewEnvelope()
	if err != nil { return }
	envelope.Client.Download()
	_, writer, err := workerio.NewExecution()
	if err != nil { return }
	_ = writer.Close()
}
`),
			"workerio/workerio.go": []byte(`package workerio

type Client struct { wait func() }
type Envelope struct { Client *Client }
type Config struct{}
type Writer struct{}
func NewClient() (*Client, error) { return &Client{wait: wait}, nil }
func NewEnvelope() (*Envelope, error) { return &Envelope{}, nil }
func NewExecution() (Config, *Writer, error) { return Config{}, &Writer{}, nil }
func wait() {}
func (client *Client) Download() { client.wait() }
func (writer *Writer) Close() error { return nil }
`),
		},
	}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("imported-local factory method was not resolved precisely: %v", err)
	}
}

func TestWorkerExecutionLocalCallChainFailsClosed(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

type value struct{}

func (value) method() {}
func factory() value { return value{} }
func root()         { factory().method() }
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err == nil || !strings.Contains(err.Error(), "dynamic") {
		t.Fatalf("local call chain did not fail closed: %v", err)
	}
}

func TestWorkerExecutionLocalVariableCallChainResolves(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture.go": []byte(`package fixture

type value struct{}

func (value) method() {}
func factory() value { return value{} }
func root() {
	value := factory()
	value.method()
}
`),
	}}
	index := snapshot.buildWorkerExecutionGoIndex()
	root, err := index.resolveRoot(workerExecutionRoot{directory: ".", symbol: "root"})
	if err != nil {
		t.Fatal(err)
	}
	closure := newWorkerExecutionGoClosure(index)
	closure.enqueue(root)
	if err := closure.resolve(); err != nil {
		t.Fatalf("statically typed local variable call chain was not resolved: %v", err)
	}
}

func workerExecutionSyntheticDigest(
	t *testing.T,
	sources map[string][]byte,
	roots []workerExecutionRoot,
	includeAllReceiverMethods bool,
	includeRequestFragment bool,
) string {
	t.Helper()
	snapshot := &remoteGitTreeSnapshot{goSources: sources}
	closure, err := snapshot.resolveWorkerExecutionClosureWithRoots(roots, includeAllReceiverMethods)
	if err != nil {
		t.Fatal(err)
	}
	assets := &workerExecutionAssets{
		snapshot:  snapshot,
		entries:   make(map[string]remoteGitTreeEntry),
		fragments: make(map[string]workerExecutionFragment),
	}
	if includeRequestFragment {
		if err := assets.addWorkerExecutionRequestSemanticFragment(); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := digestWorkerExecutionClosure(closure, assets)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func workerExecutionSyntheticSources(materialize, executor, commandRoot, resourceExpr, cleanup string) map[string][]byte {
	mainSource := strings.ReplaceAll(`package main

func runRemoteMaterialize() { materializeMarker() }
func installAcceptedBootstrapManifest() {}
func stageRemoteSourceObjects() { materializeMarker() }
func verifyRemoteMaterializedGateCLICompileClosure() {}
func verifyRemoteOCIProjectCache() {}
func verifyRemoteSourceManifestBinding() {}
func materializeMarker() {}
func runWorkerCLI() { planningMarker() }
func planningMarker() {}
`, "materializeMarker", materialize)
	mainSource = strings.ReplaceAll(mainSource, "planningMarker", cleanup)
	gateSource := strings.ReplaceAll(`package gate
func ensureJSONEOF() {}
func executeProgram() { executorMarker() }
func executorMarker() {}
func sourceVariantCount() {}
func validateCommitSource() {}
func validateOID() {}
func validateRangeSource() {}
func validateTreeSource() {}
`, "executorMarker", executor)
	requestSource := strings.NewReplacer(
		"$COMMAND_ROOT$", commandRoot,
		"$RESOURCE_EXPR$", resourceExpr,
		"$CLEANUP$", cleanup,
	).Replace(`package remoteci

func remoteShardBootstrapSH() string { return "bootstrap-v1" }
func checkoutMaterializedSource() {}
func importSourceWorktreeBundle() {}
func materializeSourceWorktree() { materializeMarker() }
func materializeMarker() {}
func remoteWorkerEnvironment() map[string]string { return map[string]string{"RUNTIME": "v1"} }
func remoteWorkerSupervisorCommand(binary string) []string { return []string{"python", binary} }
func validateCanonicalDirectory() {}
func validateManifestCommitIdentity() {}
func validateManifestIdentityFields() {}
func validateManifestSyntheticBase() {}
func validatePublishedArtifacts() {}
func validateSourceBaseline() {}
func verifyPublishedSourceBundle() {}
func orchestrationCleanup() string { return "$CLEANUP$" }

func createRequest(jobID string) eci.CreateRequest {
	_ = orchestrationCleanup()
	initContainer := eci.InitContainer{
		Command: []string{"/bin/sh"},
		Args: []string{remoteShardBootstrapPath},
		Environment: map[string]string{"PATH": remoteInitSearchPath},
	}
	mainMounts := []eci.VolumeMount{{Name: "source-data", MountPath: gate.ExecutorSourcePath, ReadOnly: true}}
	initMounts := []eci.VolumeMount{{Name: "source-data", MountPath: gate.ExecutorSourcePath, ReadOnly: false}}
	return eci.CreateRequest{
		ContainerGroupName: jobID,
		Resources: $RESOURCE_EXPR$,
		Command: remoteWorkerSupervisorCommand($COMMAND_ROOT$ + "/bin/super-dolphin-gate"),
		Args: []string{"worker", "run-shard", "--manifest-path", gate.ExecutorShardExecutionManifestPath},
		Environment: mainEnvironment,
		InitContainer: initContainer,
		MainVolumeMounts: mainMounts,
		InitVolumeMounts: initMounts,
		ConfigFileVolumes: []eci.ConfigFileVolume{{Name: remoteShardBootstrapVolumeName, DefaultMode: 0600, ConfigFileToPath: []eci.ConfigFileToPath{{Path: remoteShardBootstrapFilePath, Content: remoteShardBootstrapSH(), Mode: 0600}}}},
	}
}
`)
	requestSource = strings.ReplaceAll(requestSource, "materializeMarker", materialize)
	return map[string][]byte{
		"cmd/super-dolphin-gate/main.go":     []byte(mainSource),
		"internal/devtools/gate/executor.go": []byte(gateSource),
		workerExecutionRequestSourcePath:     []byte(requestSource),
	}
}

func TestWorkerExecutionRequestSemanticFragmentExcludesOrchestrationFields(t *testing.T) {
	const sourceTemplate = `package remoteci

func createRequest() eci.CreateRequest {
	initContainer := eci.InitContainer{
		Command: []string{"/bin/sh"},
		Args: []string{remoteShardBootstrapPath},
		Environment: map[string]string{
			"PATH": remoteInitSearchPath,
			"SUPER_DOLPHIN_REMOTE_REQUEST_KEY": bootstrapRequestKey,
		},
	}
	mainMounts := []eci.VolumeMount{{Name: "source-data", MountPath: gate.ExecutorSourcePath, ReadOnly: true}}
	initMounts := []eci.VolumeMount{{Name: "source-data", MountPath: gate.ExecutorSourcePath, ReadOnly: false}}
	return eci.CreateRequest{
		ContainerGroupName: %s,
		Resources: resources,
		Command: remoteWorkerSupervisorCommand(gate.ExecutorWorkRoot + "/bin/super-dolphin-gate"),
		Args: []string{"worker", "run-shard", "--plan-digest", shard.PlanDigest, "--manifest-path", gate.ExecutorShardExecutionManifestPath},
		Environment: mainEnvironment,
		InitContainer: initContainer,
		MainVolumeMounts: mainMounts,
		InitVolumeMounts: initMounts,
		ConfigFileVolumes: []eci.ConfigFileVolume{{Name: remoteShardBootstrapVolumeName, DefaultMode: 0600, ConfigFileToPath: []eci.ConfigFileToPath{{Path: remoteShardBootstrapFilePath, Content: remoteShardBootstrapSH(), Mode: 0600}}}},
		Tags: map[string]string{"job": jobID},
	}
}
`
	fragment := func(groupName, commandMount string) string {
		source := fmt.Sprintf(sourceTemplate, groupName)
		if commandMount != "" {
			source = strings.Replace(source, "gate.ExecutorWorkRoot", commandMount, 1)
		}
		snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
			workerExecutionRequestSourcePath: []byte(source),
		}}
		assets := &workerExecutionAssets{snapshot: snapshot, fragments: map[string]workerExecutionFragment{}}
		if err := assets.addWorkerExecutionRequestSemanticFragment(); err != nil {
			t.Fatal(err)
		}
		return string(assets.fragments["request-runtime"].content)
	}
	base := fragment("fmt.Sprintf(\"group-base\", jobID)", "")
	unrelated := fragment("fmt.Sprintf(\"group-other\", jobID)", "")
	if base != unrelated {
		t.Fatalf("orchestration-only request fields changed canonical worker fragment:\n%s\n---\n%s", base, unrelated)
	}
	semantic := fragment("fmt.Sprintf(\"group-%s\", jobID)", "gate.ExecutorSourcePath")
	if semantic == base {
		t.Fatal("worker command mount change was not represented in canonical request fragment")
	}
}

func TestWorkerExecutionRequestSemanticFragmentParsesProductionShape(t *testing.T) {
	source, err := os.ReadFile("coordinator_request.go")
	if err != nil {
		t.Fatal(err)
	}
	assets := &workerExecutionAssets{
		snapshot:  &remoteGitTreeSnapshot{goSources: map[string][]byte{workerExecutionRequestSourcePath: source}},
		fragments: make(map[string]workerExecutionFragment),
	}
	if err := assets.addWorkerExecutionRequestSemanticFragment(); err != nil {
		t.Fatalf("production createRequest shape was not canonical: %v", err)
	}
	if len(assets.fragments["request-runtime"].content) == 0 {
		t.Fatal("production createRequest produced an empty worker request fragment")
	}
}

func TestWorkerExecutionRequestDynamicCommandFailsClosed(t *testing.T) {
	source, err := os.ReadFile("coordinator_request.go")
	if err != nil {
		t.Fatal(err)
	}
	source = []byte(strings.Replace(string(source), "remoteWorkerSupervisorCommand(", "unknownWorkerCommand(", 1))
	assets := &workerExecutionAssets{
		snapshot:  &remoteGitTreeSnapshot{goSources: map[string][]byte{workerExecutionRequestSourcePath: source}},
		fragments: make(map[string]workerExecutionFragment),
	}
	if err := assets.addWorkerExecutionRequestSemanticFragment(); err == nil {
		t.Fatal("dynamic worker command unexpectedly entered canonical request fragment")
	}
}

func TestWorkerExecutionDigestIncludesLocalReplacementModuleMetadata(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{
		moduleMappings: []remoteGoModuleMapping{{importPath: "example.invalid/replaced", directory: "internal/replaced"}},
		byPath: map[string]remoteGitTreeEntry{
			"internal/replaced/go.mod": {mode: "100644", kind: "blob", objectID: "go-mod-v1", path: "internal/replaced/go.mod"},
			"internal/replaced/go.sum": {mode: "100644", kind: "blob", objectID: "go-sum-v1", path: "internal/replaced/go.sum"},
		},
	}
	assets := &workerExecutionAssets{snapshot: snapshot, entries: make(map[string]remoteGitTreeEntry)}
	if err := assets.addLocalGoModuleMetadata(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"internal/replaced/go.mod", "internal/replaced/go.sum"} {
		if _, ok := assets.entries[path]; !ok {
			t.Fatalf("worker execution assets omitted local module metadata %q: %#v", path, assets.entries)
		}
	}
	closure := &workerExecutionGoClosure{selected: map[string]*workerExecutionGoUnit{}, usedImports: map[string]map[string]struct{}{}}
	first, err := digestWorkerExecutionClosure(closure, assets)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.byPath["internal/replaced/go.mod"] = remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: "go-mod-v2", path: "internal/replaced/go.mod"}
	assets.entries = make(map[string]remoteGitTreeEntry)
	if err := assets.addLocalGoModuleMetadata(); err != nil {
		t.Fatal(err)
	}
	second, err := digestWorkerExecutionClosure(closure, assets)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("local replacement go.mod metadata change did not change worker execution digest: %q", first)
	}
}

func TestWorkerExecutionDigestBindsSemanticEnvironmentProvenance(t *testing.T) {
	closure := &workerExecutionGoClosure{selected: map[string]*workerExecutionGoUnit{}, usedImports: map[string]map[string]struct{}{}}
	assets := &workerExecutionAssets{entries: make(map[string]remoteGitTreeEntry), fragments: make(map[string]workerExecutionFragment)}
	base, err := digestWorkerExecutionClosure(closure, assets)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name string
		fn   func(string, string, []string) (string, string, []string)
	}{
		{name: "schema", fn: func(schema, provenance string, environment []string) (string, string, []string) {
			return schema + "/bump", provenance, environment
		}},
		{name: "provenance", fn: func(schema, provenance string, environment []string) (string, string, []string) {
			return schema, provenance + "/bump", environment
		}},
		{name: "assignment", fn: func(schema, provenance string, environment []string) (string, string, []string) {
			return schema, provenance, append(environment, "LANG=C")
		}},
		{name: "assignment removal", fn: func(schema, provenance string, environment []string) (string, string, []string) {
			return schema, provenance, environment[1:]
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			schema, provenance, environment := mutation.fn(
				cicontract.WorkerExecutionEnvironmentSchemaVersion,
				cicontract.WorkerExecutionProvenanceID,
				cicontract.CanonicalWorkerExecutionEnvironment(),
			)
			changed, err := digestWorkerExecutionClosureWithSemanticEnvironment(closure, assets, schema, provenance, environment)
			if err != nil {
				t.Fatal(err)
			}
			if changed == base {
				t.Fatalf("semantic environment %s mutation did not change digest %q", mutation.name, changed)
			}
		})
	}
}
