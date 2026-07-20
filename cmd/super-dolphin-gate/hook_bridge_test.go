package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gatehook"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

func TestHookBridgeSubmitMapsAuthoritativeQueuePosition(t *testing.T) {
	request := testHookSubmitRequest(t)
	client := &recordingCoordinatorClient{status: jobStatus{
		InvocationID:     coordinatorHookInvocationID(request.Invocation),
		JobID:            "job-queued",
		QueuePosition:    4,
		State:            jobStateQueued,
		JobSourceTreeSHA: request.Source.SourceTreeSHA,
	}}
	bridge := &hookCoordinatorBridge{client: client}

	status, err := bridge.Submit(context.Background(), request)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if status.State != gatehook.JobStateQueued || status.QueuePosition != 4 {
		t.Fatalf("Submit() status = %#v", status)
	}
	if client.submitted.InvocationID != coordinatorHookInvocationID(request.Invocation) {
		t.Fatalf("coordinator invocation = %q", client.submitted.InvocationID)
	}
}

func TestSchedulerJobObservationUsesAuthoritativeFIFOOrder(t *testing.T) {
	snapshot := localci.SchedulerSnapshot{Workloads: []localci.WorkloadSnapshot{
		{Request: localci.WorkloadRequest{ID: "job-started"}, Status: localci.WorkloadStatusStarted},
		{Request: localci.WorkloadRequest{ID: "job-queued-1"}, Status: localci.WorkloadStatusQueued},
		{Request: localci.WorkloadRequest{ID: "job-queued-2"}, Status: localci.WorkloadStatusQueued},
	}}

	status, position, err := schedulerJobObservation(snapshot, "job-queued-2")
	if err != nil || status != localci.WorkloadStatusQueued || position != 2 {
		t.Fatalf("queued observation status=%q position=%d error=%v", status, position, err)
	}
	status, position, err = schedulerJobObservation(snapshot, "job-started")
	if err != nil || status != localci.WorkloadStatusStarted || position != 0 {
		t.Fatalf("started observation status=%q position=%d error=%v", status, position, err)
	}
}

func TestHookBridgePassedWithoutSignedReceiptFailsClosed(t *testing.T) {
	request := testHookSubmitRequest(t)
	fixture := newHookReceiptFixture(t, request, "job-passed")
	client := &recordingCoordinatorClient{status: jobStatus{
		InvocationID:     coordinatorHookInvocationID(request.Invocation),
		JobID:            "job-passed",
		ReceiptID:        resultReceiptID("job-passed"),
		State:            jobStatePassed,
		JobSourceTreeSHA: request.Source.SourceTreeSHA,
		Terminal:         true,
	}, receiptErr: errors.New("receipt missing")}
	bridge := &hookCoordinatorBridge{client: client, authority: fixture.authority}

	status, err := bridge.Submit(context.Background(), request)
	if !errors.Is(err, errHookReceiptInvalid) {
		t.Fatalf("Submit() error = %v", err)
	}
	if status.JobID != "job-passed" || status.SourceTreeSHA != request.Source.SourceTreeSHA || status.ReceiptID != "" {
		t.Fatalf("Submit() status = %#v", status)
	}
}

func TestHookBridgePassedWithCurrentSignedReceiptContinues(t *testing.T) {
	request := testHookSubmitRequest(t)
	fixture := newHookReceiptFixture(t, request, "job-passed")
	client := &recordingCoordinatorClient{
		status: hookPassedJobStatus(request, fixture), receipt: fixture.receipt,
	}
	bridge := &hookCoordinatorBridge{client: client, authority: fixture.authority}

	status, err := bridge.Submit(context.Background(), request)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if status.State != gatehook.JobStatePassed || status.ReceiptID != fixture.receipt.ReceiptID {
		t.Fatalf("Submit() status = %#v", status)
	}
}

func TestHookBridgeBlocksTamperedOrStaleReceipt(t *testing.T) {
	request := testHookSubmitRequest(t)
	fixture := newHookReceiptFixture(t, request, "job-passed")
	staleRequest := request
	staleSource := request.Source
	staleTree := *staleSource.Tree
	staleTree.SHA = strings.Repeat("c", 40)
	staleSource.SourceTreeSHA = staleTree.SHA
	staleSource.Tree = &staleTree
	staleRequest.Source = staleSource
	staleFixture := newHookReceiptFixture(t, staleRequest, "job-passed")
	staleReceipt := fixture.resign(t, staleFixture.receipt)
	tests := []struct {
		name   string
		mutate func(gatecontract.ResultReceipt) gatecontract.ResultReceipt
	}{
		{name: "forged_signature", mutate: func(receipt gatecontract.ResultReceipt) gatecontract.ResultReceipt {
			receipt.Signature = "forged"
			return receipt
		}},
		{name: "old_generation", mutate: func(receipt gatecontract.ResultReceipt) gatecontract.ResultReceipt {
			receipt.Generation++
			return fixture.resign(t, receipt)
		}},
		{name: "missing_evidence", mutate: func(receipt gatecontract.ResultReceipt) gatecontract.ResultReceipt {
			receipt.Evidence = nil
			return receipt
		}},
		{name: "missing_removal", mutate: func(receipt gatecontract.ResultReceipt) gatecontract.ResultReceipt {
			receipt.Container.Removed = false
			return receipt
		}},
		{name: "changed_tree", mutate: func(gatecontract.ResultReceipt) gatecontract.ResultReceipt {
			return cloneResultReceipt(staleReceipt)
		}},
		{name: "changed_shard_exited_at", mutate: func(receipt gatecontract.ResultReceipt) gatecontract.ResultReceipt {
			receipt.ShardReceipts[0].ExitedAt = receipt.ShardReceipts[0].ExitedAt.Add(time.Second)
			return receipt
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := test.mutate(cloneResultReceipt(fixture.receipt))
			client := &recordingCoordinatorClient{
				status: hookPassedJobStatus(request, fixture), receipt: receipt,
			}
			bridge := &hookCoordinatorBridge{client: client, authority: fixture.authority}
			status, err := bridge.Submit(context.Background(), request)
			if !errors.Is(err, errHookReceiptInvalid) || status.ReceiptID != "" {
				t.Fatalf("Submit() status=%#v error=%v", status, err)
			}
		})
	}
}

func TestResultReceiptMatchesHookStatusRejectsLegacyOrIncompleteShards(t *testing.T) {
	request := testHookSubmitRequest(t)
	fixture := newHookReceiptFixture(t, request, "job-passed")
	status := hookPassedJobStatus(request, fixture)
	legacy := cloneResultReceipt(fixture.receipt)
	legacy.SchemaVersion = 1
	if resultReceiptMatchesHookStatus(legacy, status, request.Source.SourceTreeSHA) {
		t.Fatal("legacy result receipt matched hook status")
	}
	incomplete := cloneResultReceipt(fixture.receipt)
	incomplete.ShardReceipts = incomplete.ShardReceipts[:gatecontract.MaxContainerShards-1]
	if resultReceiptMatchesHookStatus(incomplete, status, request.Source.SourceTreeSHA) {
		t.Fatal("incomplete shard receipt matched hook status")
	}
}

func TestCloneResultReceiptDeepCopiesShardEvidence(t *testing.T) {
	request := testHookSubmitRequest(t)
	fixture := newHookReceiptFixture(t, request, "job-passed")
	cloned := cloneResultReceipt(fixture.receipt)
	cloned.ShardReceipts[0].Shard.GateIDs[0] = "changed"
	cloned.ShardReceipts[0].GateExecutions[0].Log[0] = 'x'
	if fixture.receipt.ShardReceipts[0].Shard.GateIDs[0] == "changed" ||
		fixture.receipt.ShardReceipts[0].GateExecutions[0].Log[0] == 'x' {
		t.Fatal("cloneResultReceipt retained mutable shard evidence")
	}
}

func TestHookBridgeStatusBindsInvocationAndWorktree(t *testing.T) {
	request := testHookSubmitRequest(t)
	client := &recordingCoordinatorClient{status: jobStatus{
		InvocationID:     coordinatorHookInvocationID(request.Invocation),
		JobID:            "job-running",
		State:            jobStateStarted,
		JobSourceTreeSHA: request.Source.SourceTreeSHA,
	}}
	bridge := &hookCoordinatorBridge{client: client}
	statusRequest := gatehook.StatusRequest{
		Repository:            request.Repository,
		Invocation:            request.Invocation,
		ExpectedSourceTreeSHA: request.Source.SourceTreeSHA,
		ParentInvocationOnly:  true,
	}

	status, err := bridge.Status(context.Background(), statusRequest)
	if err != nil || status.State != gatehook.JobStateRunning {
		t.Fatalf("Status() status=%#v error=%v", status, err)
	}
	if client.statusInvocationID != coordinatorHookInvocationID(request.Invocation) ||
		client.statusRepositoryRoot != request.Repository.WorktreeRoot {
		t.Fatalf("StatusInvocation() id=%q root=%q", client.statusInvocationID, client.statusRepositoryRoot)
	}
}

func TestHookBridgeMapsCancelledAndTimeoutAsActionableTerminalStates(t *testing.T) {
	request := testHookSubmitRequest(t)
	tests := []struct {
		name        string
		state       jobState
		wantState   gatehook.JobState
		wantSummary string
	}{
		{name: "cancelled", state: jobStateCancelled, wantState: gatehook.JobStateCancelled, wantSummary: "submit the same source again"},
		{name: "timeout", state: jobStateTimeout, wantState: gatehook.JobStateTimeout, wantSummary: "inspect status before retrying"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingCoordinatorClient{status: jobStatus{
				InvocationID:     coordinatorHookInvocationID(request.Invocation),
				JobID:            "job-" + test.name,
				State:            test.state,
				JobSourceTreeSHA: request.Source.SourceTreeSHA,
				Terminal:         true,
			}}
			bridge := &hookCoordinatorBridge{client: client}

			status, err := bridge.Submit(context.Background(), request)
			if err != nil {
				t.Fatalf("Submit() error = %v", err)
			}
			if status.State != test.wantState || !strings.Contains(status.Summary, test.wantSummary) {
				t.Fatalf("Submit() status = %#v", status)
			}
		})
	}
}

func TestCoordinatorJobStatusFieldRegistryTracksQueuePosition(t *testing.T) {
	registry := map[string]string{
		"InvocationID": "durable invocation identity",
		"JobID":        "durable job identity", "EnqueueSequence": "scheduler FIFO identity",
		"QueuePosition": "authoritative queued observation", "State": "durable state",
		"Profile": "timeout contract", "JobSourceTreeSHA": "submitted source binding",
		"ImageProvenanceSourceTreeSHA": "image provenance", "SubmittedAt": "submit timestamp",
		"StartedAt": "start timestamp", "CompletedAt": "completion timestamp",
		"GateResults": "structured gate evidence", "ReceiptID": "authoritative signed receipt",
		"ContainerHostConfigDigest":        "verified Docker HostConfig digest",
		"ContainerResourceWitness":         "typed verified container resource evidence",
		"ContainerResourceWitnessDigest":   "canonical typed resource evidence digest",
		"ContainerResourceWitnessVerified": "pre-start resource verification marker",
		"Error":                            "terminal failure detail", "Terminal": "terminal marker",
	}
	producer := reflect.TypeFor[jobStatus]()
	for index := range producer.NumField() {
		field := producer.Field(index).Name
		if registry[field] == "" {
			t.Fatalf("jobStatus field %q is not registered", field)
		}
		delete(registry, field)
	}
	if len(registry) != 0 {
		t.Fatalf("jobStatus field registry has stale entries: %v", registry)
	}
}

type recordingCoordinatorClient struct {
	status               jobStatus
	receipt              gatecontract.ResultReceipt
	receiptErr           error
	submitted            submitRequest
	statusInvocationID   string
	statusRepositoryRoot string
}

type hookReceiptFixture struct {
	jobID     string
	receipt   gatecontract.ResultReceipt
	signer    resultReceiptSigner
	authority *staticHookResultReceiptAuthority
}

type staticHookResultReceiptAuthority struct {
	verifier resultReceiptVerifier
	accepted gatecontract.AcceptedImageRecord
}

func (authority *staticHookResultReceiptAuthority) VerifyCurrentResultReceipt(
	_ context.Context,
	receipt gatecontract.ResultReceipt,
) error {
	if authority == nil || authority.verifier == nil {
		return errors.New("test receipt authority is missing")
	}
	if err := authority.verifier.VerifyResultReceipt(receipt); err != nil {
		return err
	}
	return validateCurrentAcceptedReceipt(receipt, authority.accepted)
}

func newHookReceiptFixture(
	t *testing.T,
	request gatehook.SubmitRequest,
	jobID string,
) hookReceiptFixture {
	t.Helper()
	plan, err := gatecontract.BuildGatePlan(request.Profile, request.Source)
	if err != nil {
		t.Fatal(err)
	}
	accepted := testAcceptedImageRecord(plan)
	record := coordinatorJobRecord{
		JobID: jobID, InvocationID: coordinatorHookInvocationID(request.Invocation),
		RepositoryRoot: request.Repository.WorktreeRoot, Plan: plan, Profile: plan.Profile,
		JobSourceTreeSHA: plan.Source.SourceTreeSHA, ImageProvenanceSourceTreeSHA: plan.Source.SourceTreeSHA,
		Authority: submissionAuthority{
			Entrypoint: request.Entrypoint, Owner: authorityOwnerForHook(request),
			Attestation: authorityAttestationForHook(request),
		},
	}
	execution := mustTestCanonicalShardReceiptExecution(t, record)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	identity := gatecontract.SignerIdentity{
		KeyID: "hook-receipt-test", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}
	signer, err := newEd25519ResultReceiptSigner(identity, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := newEd25519ResultReceiptVerifier(identity, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := buildPassedResultReceipt(record, execution, signer)
	if err != nil {
		t.Fatal(err)
	}
	return hookReceiptFixture{
		jobID: jobID, receipt: receipt, signer: signer,
		authority: &staticHookResultReceiptAuthority{verifier: verifier, accepted: accepted},
	}
}

func (fixture hookReceiptFixture) resign(
	t *testing.T,
	receipt gatecontract.ResultReceipt,
) gatecontract.ResultReceipt {
	t.Helper()
	signed, err := fixture.signer.SignResultReceipt(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func hookPassedJobStatus(request gatehook.SubmitRequest, fixture hookReceiptFixture) jobStatus {
	return jobStatus{
		InvocationID: coordinatorHookInvocationID(request.Invocation),
		JobID:        fixture.jobID, ReceiptID: fixture.receipt.ReceiptID,
		State: jobStatePassed, JobSourceTreeSHA: request.Source.SourceTreeSHA, Terminal: true,
	}
}

func (client *recordingCoordinatorClient) Submit(_ context.Context, request submitRequest) (jobStatus, error) {
	client.submitted = request
	return client.status, nil
}

func (client *recordingCoordinatorClient) Status(context.Context, string) (jobStatus, error) {
	return client.status, nil
}

func (client *recordingCoordinatorClient) StatusInvocation(
	_ context.Context,
	invocationID string,
	repositoryRoot string,
) (jobStatus, error) {
	client.statusInvocationID = invocationID
	client.statusRepositoryRoot = repositoryRoot
	return client.status, nil
}

func (client *recordingCoordinatorClient) Wait(context.Context, string) (jobStatus, error) {
	return client.status, nil
}

func (client *recordingCoordinatorClient) ResultReceipt(
	context.Context,
	string,
) (gatecontract.ResultReceipt, error) {
	return cloneResultReceipt(client.receipt), client.receiptErr
}

func (*recordingCoordinatorClient) Close() error { return nil }

func testHookSubmitRequest(t *testing.T) gatehook.SubmitRequest {
	t.Helper()
	source := gatecontract.SourceSpec{
		Kind:         gatecontract.SourceKindTree,
		ObjectFormat: gatecontract.GitObjectFormatSHA1,
		Tree: &gatecontract.TreeSource{
			SHA:             strings.Repeat("a", 40),
			ParentCommitSHA: strings.Repeat("b", 40),
		},
		SourceTreeSHA: strings.Repeat("a", 40),
	}
	request := gatehook.SubmitRequest{
		Entrypoint: gatecontract.CIEntrypointGitPreCommit,
		Profile:    gatecontract.ProfileLocalFast,
		Repository: gatehook.RepositoryIdentity{
			WorktreeRoot: "/tmp/worktree",
			GitDir:       "/tmp/common/worktrees/topic",
			CommonDir:    "/tmp/common",
			ObjectFormat: gatecontract.GitObjectFormatSHA1,
		},
		Invocation: gatehook.InvocationIdentity{
			Owner: "sha256:" + strings.Repeat("1", 64),
			Key:   "sha256:" + strings.Repeat("2", 64),
		},
		Source: source,
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("test request invalid: %v", err)
	}
	return request
}

func successfulFreshContainerResult(
	ctx context.Context,
	request freshContainerRequest,
) (localci.FreshContainerResult, error) {
	observedAt := time.Now().UTC()
	deadline, removalProof, err := emitFakeContainerLifecycle(ctx, request, observedAt)
	if err != nil {
		return localci.FreshContainerResult{}, err
	}
	return testSuccessfulFreshContainerResult(request, deadline, removalProof)
}

// testSuccessfulFreshContainerResult 生成与已记录 lifecycle 一致的成功容器结果。
func testSuccessfulFreshContainerResult(
	request freshContainerRequest,
	deadline time.Time,
	removalProof string,
) (localci.FreshContainerResult, error) {
	startedAt := deadline.Add(-coordinatorTimeout(request.Profile))
	exitedAt := startedAt
	completedAt := exitedAt
	logOutput := []byte("gate passed\n")
	logDigest := coordinatorLogDigest(logOutput)
	gateResult := testPassedGateResult(request.GateID, startedAt, completedAt, logDigest)
	result := testSuccessfulContainerResult(request, deadline, exitedAt, completedAt, removalProof, logOutput, logDigest, gateResult)
	planResults, err := testSuccessfulPlanGateResults(request, gateResult, logOutput)
	if err != nil {
		return localci.FreshContainerResult{}, err
	}
	if len(planResults) != 0 {
		result.GateResult = nil
		result.PlanGateResults = planResults
		return result, nil
	}
	gateResult, err = testGateResultWithCanonicalDigest(gateResult, request.GateID)
	if err != nil {
		return localci.FreshContainerResult{}, err
	}
	result.GateResult = &gateResult
	return result, nil
}

// testSuccessfulContainerResult 组装成功容器的不可变证据和合法生命周期时间。
func testSuccessfulContainerResult(
	request freshContainerRequest,
	deadline time.Time,
	exitedAt time.Time,
	completedAt time.Time,
	removalProof string,
	logOutput []byte,
	logDigest string,
	gateResult gatecontract.GateResult,
) localci.FreshContainerResult {
	resourceWitness, resourceWitnessDigest := testContainerResourceWitness()
	containerID := fakeFreshContainerID(request)
	container := gatecontract.ContainerEvidence{
		ContainerID:      containerID,
		NetworkID:        "network-" + containerID[:12],
		HostConfigDigest: coordinatorDigest("5"),
		ResourceWitness:  resourceWitness, ResourceWitnessDigest: resourceWitnessDigest,
		NetworkPolicyDigest: coordinatorDigest("6"),
		Removed:             true, NetworkRemoved: true,
	}
	return localci.FreshContainerResult{
		Status: gatecontract.ResultStatusPassed, ExitCode: 0, GateResult: &gateResult,
		Container: container, StartedAt: gateResult.StartedAt, ExitedAt: exitedAt, CompletedAt: completedAt,
		Deadline: deadline, LogOutput: logOutput, LogDigest: logDigest, RemovalProofDigest: removalProof,
		Evidence: []gatecontract.Evidence{
			{Kind: gatecontract.EvidenceKindLog, Digest: logDigest},
			{Kind: gatecontract.EvidenceKindDocker, Digest: removalProof},
		},
	}
}

// testSuccessfulPlanGateResults 为 shard 或整计划执行生成带规范摘要的 gate 结果。
func testSuccessfulPlanGateResults(
	request freshContainerRequest,
	template gatecontract.GateResult,
	logOutput []byte,
) ([]localci.FreshPlanGateResult, error) {
	gateIDs := testFreshContainerGateIDs(request)
	if len(gateIDs) == 0 {
		return nil, nil
	}
	results := make([]localci.FreshPlanGateResult, 0, len(gateIDs))
	for _, gateID := range gateIDs {
		gateResult, err := testGateResultWithCanonicalDigest(template, gateID)
		if err != nil {
			return nil, err
		}
		results = append(results, localci.FreshPlanGateResult{
			GateResult: gateResult, Status: gatecontract.ResultStatusPassed,
			LogOutput: append([]byte(nil), logOutput...),
		})
	}
	return results, nil
}

// testFreshContainerGateIDs 返回 shard 或整计划执行应观察到的规范 gate 列表。
func testFreshContainerGateIDs(request freshContainerRequest) []gatecontract.GateID {
	if len(request.ShardGateIDs) != 0 {
		return request.ShardGateIDs
	}
	if !request.PlanExecution {
		return nil
	}
	gateIDs := make([]gatecontract.GateID, 0, len(request.Plan.Gates))
	for _, spec := range request.Plan.Gates {
		gateIDs = append(gateIDs, spec.ID)
	}
	return gateIDs
}

// testPassedGateResult 创建尚未填充规范参数摘要的成功 gate 观察结果。
func testPassedGateResult(
	gateID gatecontract.GateID,
	startedAt time.Time,
	completedAt time.Time,
	logDigest string,
) gatecontract.GateResult {
	return gatecontract.GateResult{
		GateID: string(gateID), Status: gatecontract.GateStatusPassed, ExitCode: 0,
		StartedAt: startedAt, CompletedAt: completedAt, LogDigest: logDigest,
	}
}

// testGateResultWithCanonicalDigest 复制 gate 结果并写入对应 GateSpec.Argv 的规范摘要。
func testGateResultWithCanonicalDigest(
	result gatecontract.GateResult,
	gateID gatecontract.GateID,
) (gatecontract.GateResult, error) {
	argvDigest, err := testCanonicalGateArgvDigest(gateID)
	if err != nil {
		return gatecontract.GateResult{}, err
	}
	result.GateID = string(gateID)
	result.ArgvDigest = argvDigest
	return result, nil
}

// testCanonicalGateArgvDigest 按 gate 注册表使用的规范 JSON 编码计算 ArgvDigest。
func testCanonicalGateArgvDigest(gateID gatecontract.GateID) (string, error) {
	for _, spec := range gatecontract.GateRegistry() {
		if spec.ID == gateID {
			return testCanonicalJSONDigest(spec.Argv)
		}
	}
	return "", fmt.Errorf("canonical gate spec %q is missing", gateID)
}

// testCanonicalJSONDigest 与容器执行器保持相同的 JSON 摘要规则。
func testCanonicalJSONDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func testAcceptedImageRecord(plan gatecontract.GatePlan) gatecontract.AcceptedImageRecord {
	signer := gatecontract.SignerIdentity{
		KeyID: "coordinator-test", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}
	return gatecontract.AcceptedImageRecord{
		SchemaVersion: gatecontract.AcceptedImageRecordSchemaVersion,
		RepoID:        "example/repository", TrustedRef: "refs/heads/main",
		TrustedCommit: strings.Repeat("e", 40), SourceTree: strings.Repeat("f", len(plan.Source.SourceTreeSHA)),
		PolicyDigest: plan.PolicyDigest, ImageInputDigest: coordinatorDigest("3"),
		Image: gatecontract.ImageIdentity{
			Registry: "registry.invalid/super-dolphin/gate", OCIIndexDigest: coordinatorDigest("7"),
			PlatformManifestDigest: coordinatorDigest("8"), ConfigDigest: coordinatorDigest("9"),
			RootFSDiffIDs: []string{coordinatorDigest("a")}, OS: "linux", Architecture: "arm64",
		},
		Runner: gatecontract.TrustedRunnerIdentity{
			BinaryDigest: coordinatorDigest("b"), Signer: signer, PolicyDigest: plan.PolicyDigest,
		},
		Generation: 1, AcceptedAt: time.Date(2026, time.July, 17, 0, 0, 0, 0, time.UTC),
		Signer: signer, Signature: "signed-accepted-image-test-record",
	}
}

func mustTestResultReceiptSigner(t *testing.T) resultReceiptSigner {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := newEd25519ResultReceiptSigner(gatecontract.SignerIdentity{
		KeyID: "result-receipt-test", KeyEpoch: 1, Algorithm: gatecontract.SignatureAlgorithmEd25519,
	}, privateKey, publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func mustTestCanonicalShardReceiptExecution(t *testing.T, record coordinatorJobRecord) receiptExecution {
	t.Helper()
	accepted := testAcceptedImageRecord(record.Plan)
	set, err := gatecontract.BuildContainerShardSet(record.Plan, accepted.Image.PlatformManifestDigest, accepted.Image.ConfigDigest)
	if err != nil {
		t.Fatal(err)
	}
	deadline := testReceiptDeadline(record)
	receipts := make([]gatecontract.ContainerShardReceipt, 0, len(set.Shards))
	for _, shard := range set.Shards {
		result, err := successfulFreshContainerResult(context.Background(), freshContainerRequest{
			Image: accepted.Image, Plan: record.Plan, Profile: record.Profile, ShardGateIDs: shard.GateIDs,
			JobSourceTreeSHA: record.JobSourceTreeSHA, Deadline: deadline,
		})
		if err != nil {
			t.Fatal(err)
		}
		receipts = append(receipts, shardReceipt(shard, result))
	}
	aggregate, err := gatecontract.AggregateContainerShards(set, receipts)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := shardReceiptExecution(accepted, set, receipts, aggregate)
	if err != nil {
		t.Fatal(err)
	}
	return execution
}

// alignTestShardReceiptTimeline 用 durable shard 生命周期重建测试回执的父子时间线。
func alignTestShardReceiptTimeline(
	t *testing.T,
	receipt *gatecontract.ContainerShardReceipt,
	durable coordinatorShardRecord,
) {
	t.Helper()
	if durable.StartedAt == nil || durable.ExitedAt == nil || durable.CompletedAt == nil || durable.Deadline == nil {
		t.Fatalf("durable shard %q has incomplete lifecycle evidence", durable.Shard.IdentityDigest)
	}
	receipt.StartedAt = durable.StartedAt.UTC()
	receipt.ExitedAt = durable.ExitedAt.UTC()
	receipt.CompletedAt = durable.CompletedAt.UTC()
	receipt.Deadline = durable.Deadline.UTC()
	for index := range receipt.GateExecutions {
		receipt.GateExecutions[index].StartedAt = receipt.StartedAt
		receipt.GateExecutions[index].CompletedAt = receipt.ExitedAt
	}
}

func testReceiptDeadline(record coordinatorJobRecord) time.Time {
	if record.Deadline != nil {
		return record.Deadline.UTC()
	}
	return time.Now().UTC().Add(2 * time.Minute)
}

func coordinatorDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func TestCoordinatorReceiptSigningFailurePersistsInfraFailed(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	owner := startTestCoordinatorOwnerWithSigner(
		t, checkpoint, immediateFreshRunner{}, failingResultReceiptSigner{},
	)
	client := dialTestCoordinator(t, checkpoint)
	submitted := submitTestPlan(t, client, "c")
	status, err := client.Wait(context.Background(), submitted.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != jobStateInfraFailed || !status.Terminal || status.ReceiptID != "" {
		t.Fatalf("signing failure status = %#v", status)
	}
	record, err := owner.store.job(context.Background(), submitted.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if record.State != jobStateInfraFailed || record.Receipt != nil ||
		!strings.Contains(record.Error, "sign canonical result receipt") {
		t.Fatalf("signing failure durable record = %#v", record)
	}
}
