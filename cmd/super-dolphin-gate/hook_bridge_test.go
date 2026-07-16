package main

import (
	"context"
	"crypto/ed25519"
	"errors"
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
		{name: "changed_tree", mutate: func(receipt gatecontract.ResultReceipt) gatecontract.ResultReceipt {
			receipt.Source.SourceTreeSHA = strings.Repeat("c", 40)
			receipt.Source.Tree.SHA = receipt.Source.SourceTreeSHA
			return fixture.resign(t, receipt)
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

func TestCoordinatorJobStatusFieldRegistryTracksQueuePosition(t *testing.T) {
	registry := map[string]string{
		"InvocationID": "durable invocation identity",
		"JobID":        "durable job identity", "EnqueueSequence": "scheduler FIFO identity",
		"QueuePosition": "authoritative queued observation", "State": "durable state",
		"Profile": "timeout contract", "JobSourceTreeSHA": "submitted source binding",
		"ImageProvenanceSourceTreeSHA": "image provenance", "SubmittedAt": "submit timestamp",
		"StartedAt": "start timestamp", "CompletedAt": "completion timestamp",
		"GateResults": "structured gate evidence", "ReceiptID": "authoritative signed receipt",
		"Error": "terminal failure detail", "Terminal": "terminal marker",
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
	if receipt.RepoID != authority.accepted.RepoID ||
		receipt.PolicyDigest != authority.accepted.PolicyDigest ||
		receipt.Generation != authority.accepted.Generation ||
		!reflect.DeepEqual(receipt.Image, authority.accepted.Image) ||
		!reflect.DeepEqual(receipt.Runner, authority.accepted.Runner) {
		return errors.New("receipt does not match current accepted generation")
	}
	return nil
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
	execution := receiptExecution{Accepted: accepted, Deadline: time.Now().UTC().Add(2 * time.Minute)}
	for _, gateSpec := range plan.Gates {
		if err := execution.appendResult(successfulFreshContainerResult(freshContainerRequest{
			GateID: gateSpec.ID,
		})); err != nil {
			t.Fatal(err)
		}
	}
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
	receipt, err := buildPassedResultReceipt(coordinatorJobRecord{
		JobID: jobID, InvocationID: coordinatorHookInvocationID(request.Invocation), Plan: plan,
	}, execution, signer)
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
