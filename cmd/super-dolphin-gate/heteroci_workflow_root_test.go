package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
)

func TestRunHeterociWorkflowRejectsMissingExplicitRootInputs(t *testing.T) {
	_, err := RunHeterociWorkflow(context.Background(), HeterociRootInput{})
	if err == nil {
		t.Fatal("RunHeterociWorkflow accepted missing explicit root inputs")
	}
	if !errors.Is(err, ErrHeterociRootInput) {
		t.Fatalf("RunHeterociWorkflow error = %v, want ErrHeterociRootInput", err)
	}
}

func TestRunHeterociWorkflowRunsLocalAuthorityWithoutRemoteFallback(t *testing.T) {
	fixture := newHeterociRootFixture(t)
	result, err := RunHeterociWorkflow(context.Background(), fixture.input)
	if err != nil {
		t.Fatalf("RunHeterociWorkflow: %v", err)
	}
	if result.Authority != heteroci.AuthorityLocalNonAuthoritative || result.Status != heterociworkflow.ResultStatusNotVerified {
		t.Fatalf("result authority/status = %q/%q", result.Authority, result.Status)
	}
	if len(result.Units) != 1 || result.Units[0].State != heteroci.LeaseStateSucceeded {
		t.Fatalf("result units = %+v", result.Units)
	}
	fixture.binder.mu.Lock()
	defer fixture.binder.mu.Unlock()
	if len(fixture.binder.keys) != 1 || fixture.binder.keys[0].Kind != heteroci.ExecutorKindLocalNode {
		t.Fatalf("executor keys = %+v, want one local node and no remote fallback", fixture.binder.keys)
	}
	if fixture.binder.executeCount != 1 {
		t.Fatalf("executor calls = %d, want one batch execution", fixture.binder.executeCount)
	}
	if fixture.transport.callCount != 1 {
		t.Fatalf("probe calls = %d, want exactly one fresh dynamic-node probe", fixture.transport.callCount)
	}
}

func TestRunHeterociWorkflowRejectsMissingInputsBeforeOwnerMutation(t *testing.T) {
	fixture := newHeterociRootFixture(t)
	cases := []struct {
		name   string
		mutate func(*HeterociRootInput)
	}{
		{name: "manifest path", mutate: func(input *HeterociRootInput) { input.ManifestPath = "" }},
		{name: "owner path", mutate: func(input *HeterociRootInput) { input.OwnerDBPath = "" }},
		{name: "scope", mutate: func(input *HeterociRootInput) { input.Scope = "" }},
		{name: "repository identity", mutate: func(input *HeterociRootInput) { input.RepositoryID = "" }},
		{name: "source tree", mutate: func(input *HeterociRootInput) { input.SourceTreeSHA = "ABC" }},
		{name: "units", mutate: func(input *HeterociRootInput) { input.Units = nil }},
		{name: "probe transport", mutate: func(input *HeterociRootInput) { input.ProbeTransport = nil }},
		{name: "executor binder", mutate: func(input *HeterociRootInput) { input.ExecutorBinder = nil }},
		{name: "cleanup timeout", mutate: func(input *HeterociRootInput) { input.CleanupTimeout = 0 }},
		{name: "clock", mutate: func(input *HeterociRootInput) { input.Now = nil }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			input := fixture.input
			test.mutate(&input)
			_, err := RunHeterociWorkflow(context.Background(), input)
			if !errors.Is(err, ErrHeterociRootInput) {
				t.Fatalf("RunHeterociWorkflow error = %v, want ErrHeterociRootInput", err)
			}
			if _, statErr := os.Stat(fixture.input.OwnerDBPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid %s input created owner database: stat err=%v", test.name, statErr)
			}
		})
	}
}

func TestRunHeterociWorkflowClosesOwnerDatabaseAfterExecutionError(t *testing.T) {
	fixture := newHeterociRootFixture(t)
	fixture.binder.executeErr = errors.New("local execution failed")
	_, err := RunHeterociWorkflow(context.Background(), fixture.input)
	if err == nil {
		t.Fatal("RunHeterociWorkflow unexpectedly succeeded")
	}
	opened, openErr := os.Open(fixture.input.OwnerDBPath)
	if openErr != nil {
		t.Fatalf("owner database cannot be reopened after workflow cleanup: %v", openErr)
	}
	if closeErr := opened.Close(); closeErr != nil {
		t.Fatalf("close reopened owner database: %v", closeErr)
	}
	entries, readErr := os.ReadDir(filepath.Dir(fixture.input.OwnerDBPath))
	if readErr != nil {
		t.Fatalf("read owner database directory: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "-wal") || strings.HasSuffix(entry.Name(), "-shm") {
			t.Fatalf("owner database left journal artifact %q", entry.Name())
		}
	}
}

func TestRunHeterociWorkflowRejectsSymlinkedPathComponentsBeforeMutation(t *testing.T) {
	t.Run("manifest file", testHeterociRootRejectsManifestFileSymlink)
	t.Run("manifest parent", testHeterociRootRejectsManifestParentSymlink)
	t.Run("owner file", testHeterociRootRejectsOwnerFileSymlink)
	t.Run("owner parent", testHeterociRootRejectsOwnerParentSymlink)
}

func testHeterociRootRejectsManifestFileSymlink(t *testing.T) {
	fixture := newHeterociRootFixture(t)
	link := filepath.Join(canonicalHeterociRootTestDir(t), "manifest-link.json")
	requireHeterociRootSymlink(t, fixture.input.ManifestPath, link)
	fixture.input.ManifestPath = link
	assertHeterociRootPathRejectedWithoutExecution(t, fixture, fixture.input.OwnerDBPath)
}

func testHeterociRootRejectsManifestParentSymlink(t *testing.T) {
	fixture := newHeterociRootFixture(t)
	link := filepath.Join(canonicalHeterociRootTestDir(t), "manifest-parent-link")
	requireHeterociRootSymlink(t, filepath.Dir(fixture.input.ManifestPath), link)
	fixture.input.ManifestPath = filepath.Join(link, filepath.Base(fixture.input.ManifestPath))
	assertHeterociRootPathRejectedWithoutExecution(t, fixture, fixture.input.OwnerDBPath)
}

func testHeterociRootRejectsOwnerFileSymlink(t *testing.T) {
	fixture := newHeterociRootFixture(t)
	target := filepath.Join(canonicalHeterociRootTestDir(t), "owner-target.sqlite")
	link := filepath.Join(canonicalHeterociRootTestDir(t), "owner-link.sqlite")
	requireHeterociRootSymlink(t, target, link)
	fixture.input.OwnerDBPath = link
	assertHeterociRootPathRejectedWithoutExecution(t, fixture, target)
}

func testHeterociRootRejectsOwnerParentSymlink(t *testing.T) {
	fixture := newHeterociRootFixture(t)
	targetParent := canonicalHeterociRootTestDir(t)
	link := filepath.Join(canonicalHeterociRootTestDir(t), "owner-parent-link")
	requireHeterociRootSymlink(t, targetParent, link)
	target := filepath.Join(targetParent, "owner-target.sqlite")
	fixture.input.OwnerDBPath = filepath.Join(link, filepath.Base(target))
	assertHeterociRootPathRejectedWithoutExecution(t, fixture, target)
}

func requireHeterociRootSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink fixture is unavailable: %v", err)
	}
}

func assertHeterociRootPathRejectedWithoutExecution(t *testing.T, fixture heterociRootFixture, mutationPath string) {
	t.Helper()
	_, err := RunHeterociWorkflow(context.Background(), fixture.input)
	if !errors.Is(err, ErrHeterociRootInput) {
		t.Fatalf("RunHeterociWorkflow error = %v, want ErrHeterociRootInput", err)
	}
	if _, statErr := os.Lstat(mutationPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("symlinked root input mutated target: stat err=%v", statErr)
	}
	if fixture.transport.callCount != 0 || fixture.binder.executeCount != 0 {
		t.Fatalf("symlinked root input reached probe/executor: probes=%d executes=%d", fixture.transport.callCount, fixture.binder.executeCount)
	}
}

type heterociRootFixture struct {
	input     HeterociRootInput
	transport *rootProbeTransport
	binder    *rootExecutorBinder
}

func newHeterociRootFixture(t *testing.T) heterociRootFixture {
	t.Helper()
	const imageDigest = "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	const probeIdentity = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	now := time.Now().UTC()
	manifest := heteroci.Manifest{
		SchemaVersion: heteroci.ManifestSchemaVersion,
		Authority:     heteroci.AuthorityLocalNonAuthoritative,
		Nodes: []heteroci.NodeConfig{{
			NodeID:        "root-node-1",
			Endpoint:      "https://root-node-1.invalid/runner",
			ProbeIdentity: probeIdentity,
			Platform:      heteroci.PlatformLinuxAMD64,
			Capabilities: []heteroci.CapabilityConfig{{
				Runtime:     heteroci.RuntimeUbuntuContainer,
				Image:       "ubuntu:24.04",
				ImageDigest: imageDigest,
			}},
		}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	manifestPath := filepath.Join(canonicalHeterociRootTestDir(t), "heteroci-manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	transport := &rootProbeTransport{manifest: manifest, imageDigest: imageDigest, now: now}
	binder := &rootExecutorBinder{}
	units := []heteroci.ExecutionUnit{{
		UnitID:              "root-unit-1",
		TargetPlatform:      heteroci.PlatformLinuxAMD64,
		RequiredRuntime:     heteroci.RuntimeUbuntuContainer,
		EstimatedDurationMS: 100,
		CPUMilli:            500,
		MemoryBytes:         256 << 20,
	}}
	return heterociRootFixture{
		input: HeterociRootInput{
			ManifestPath:   manifestPath,
			OwnerDBPath:    filepath.Join(canonicalHeterociRootTestDir(t), "heteroci-owner.sqlite"),
			Scope:          heteroci.RoutingOwnerScope("sha256:" + strings.Repeat("a", 64)),
			RepositoryID:   "root-repository",
			SourceTreeSHA:  strings.Repeat("b", 40),
			RunID:          "root-run-1",
			Units:          units,
			HeartbeatTTL:   time.Minute,
			ProbeTransport: transport,
			ExecutorBinder: binder,
			CleanupTimeout: time.Second,
			Now:            func() time.Time { return now },
		},
		transport: transport,
		binder:    binder,
	}
}

func canonicalHeterociRootTestDir(t *testing.T) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve canonical test directory: %v", err)
	}
	return canonical
}

type rootProbeTransport struct {
	mu          sync.Mutex
	manifest    heteroci.Manifest
	imageDigest string
	now         time.Time
	callCount   int
}

func (transport *rootProbeTransport) Probe(ctx context.Context, endpoint string, request []byte) (heteroci.ProbeResponseBody, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var probeRequest heteroci.ProbeRequest
	if err := json.Unmarshal(request, &probeRequest); err != nil {
		return nil, fmt.Errorf("decode probe request: %w", err)
	}
	if len(transport.manifest.Nodes) != 1 || endpoint != transport.manifest.Nodes[0].Endpoint || probeRequest.NodeID != transport.manifest.Nodes[0].NodeID {
		return nil, errors.New("probe request identity mismatch")
	}
	transport.mu.Lock()
	transport.callCount++
	transport.mu.Unlock()
	response, err := json.Marshal(heteroci.ProbeResponse{
		SchemaVersion: heteroci.ProbeResponseSchemaVersion,
		OwnerEpoch:    probeRequest.OwnerEpoch,
		CapacityBasis: heteroci.SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations,
		NodeID:        probeRequest.NodeID,
		Endpoint:      endpoint,
		Platform:      heteroci.PlatformLinuxAMD64,
		ProbeIdentity: probeRequest.ProbeIdentity,
		ProbeBootID:   "root-boot-1",
		ActiveCapabilities: []heteroci.ActiveCapabilitySnapshot{{
			Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: transport.imageDigest,
		}},
		Status:             heteroci.NodeStatusLive,
		HeartbeatUnixMilli: transport.now.UnixMilli(),
		Total:              heteroci.ResourceCapacity{CPUMilli: 4000, MemoryBytes: 8 << 30, MaxConcurrency: 4},
		Available:          heteroci.ResourceCapacity{CPUMilli: 4000, MemoryBytes: 8 << 30, MaxConcurrency: 4},
	})
	if err != nil {
		return nil, err
	}
	return &rootProbeBody{reader: bytes.NewReader(response)}, nil
}

type rootProbeBody struct {
	mu     sync.Mutex
	reader *bytes.Reader
	closed bool
}

func (body *rootProbeBody) Read(ctx context.Context, buffer []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.closed {
		return 0, errors.New("probe body is closed")
	}
	return body.reader.Read(buffer)
}

func (body *rootProbeBody) Close() error {
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.closed {
		return errors.New("probe body closed twice")
	}
	body.closed = true
	return nil
}

type rootExecutorBinder struct {
	mu           sync.Mutex
	keys         []heterociworkflow.ExecutorKey
	executeCount int
	executeErr   error
}

func (binder *rootExecutorBinder) Bind(plan heterociworkflow.ExecutorBindingPlan) (map[heterociworkflow.ExecutorKey]heterociworkflow.BatchExecutor, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	binder.mu.Lock()
	executeErr := binder.executeErr
	bound := make(map[heterociworkflow.ExecutorKey]heterociworkflow.BatchExecutor, len(plan.Bindings))
	for _, binding := range plan.Bindings {
		binder.keys = append(binder.keys, binding.Key)
		bound[binding.Key] = &rootBatchExecutor{binder: binder, executeErr: executeErr}
	}
	binder.mu.Unlock()
	return bound, nil
}

type rootBatchExecutor struct {
	binder     *rootExecutorBinder
	executeErr error
}

func (executor *rootBatchExecutor) ExecuteBatch(ctx context.Context, batch heterociworkflow.ExecutionBatch) (heterociworkflow.BatchResult, error) {
	if err := ctx.Err(); err != nil {
		return heterociworkflow.BatchResult{}, err
	}
	executor.binder.mu.Lock()
	executor.binder.executeCount++
	executor.binder.mu.Unlock()
	if executor.executeErr != nil {
		return heterociworkflow.BatchResult{}, executor.executeErr
	}
	outcomes := make([]heterociworkflow.BatchOutcome, 0, len(batch.Units))
	for _, unit := range batch.Units {
		outcomes = append(outcomes, heterociworkflow.BatchOutcome{UnitID: unit.UnitID, State: heteroci.LeaseStateSucceeded})
	}
	materialDigest, err := heterociworkflow.DeriveExecutionMaterialDigest(batch)
	if err != nil {
		return heterociworkflow.BatchResult{}, err
	}
	return heterociworkflow.BatchResult{
		BatchID: batch.BatchID, RunID: batch.RunID, Executor: batch.Key, MaterialDigest: materialDigest,
		CommandDigest: "sha256:" + strings.Repeat("c", 64), Authority: heteroci.AuthorityLocalNonAuthoritative,
		Status: heterociworkflow.ResultStatusNotVerified, Outcomes: outcomes,
	}, nil
}
