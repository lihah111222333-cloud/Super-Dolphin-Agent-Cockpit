package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

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
	client := &recordingCoordinatorClient{status: jobStatus{
		InvocationID:     coordinatorHookInvocationID(request.Invocation),
		JobID:            "job-passed",
		State:            jobStatePassed,
		JobSourceTreeSHA: request.Source.SourceTreeSHA,
		Terminal:         true,
	}}
	bridge := &hookCoordinatorBridge{client: client}

	status, err := bridge.Submit(context.Background(), request)
	if !errors.Is(err, errHookReceiptUnavailable) {
		t.Fatalf("Submit() error = %v", err)
	}
	if status.JobID != "job-passed" || status.SourceTreeSHA != request.Source.SourceTreeSHA || status.ReceiptID != "" {
		t.Fatalf("Submit() status = %#v", status)
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
		"GateResults": "structured gate evidence", "Error": "terminal failure detail", "Terminal": "terminal marker",
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
	submitted            submitRequest
	statusInvocationID   string
	statusRepositoryRoot string
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
