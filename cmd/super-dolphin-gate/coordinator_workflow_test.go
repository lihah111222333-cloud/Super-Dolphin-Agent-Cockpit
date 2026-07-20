package main

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type workflowCoordinatorClient struct {
	submitted   jobStatus
	terminal    jobStatus
	calls       []string
	entrypoint  gatecontract.CIEntrypointID
	owner       gatecontract.CIEntrypointOwner
	attestation string
	invocation  string
}

func (client *workflowCoordinatorClient) Submit(_ context.Context, request submitRequest) (jobStatus, error) {
	client.calls = append(client.calls, "submit")
	client.entrypoint = request.Entrypoint
	client.owner = request.AuthorityOwner
	client.attestation = request.AuthorityAttestation
	client.invocation = request.InvocationID
	return client.submitted, nil
}

func TestWorkflowCLIForwardsBootstrapAttestationToSubmit(t *testing.T) {
	client := &workflowCoordinatorClient{
		submitted: jobStatus{JobID: "job-attested", State: jobStateQueued},
		terminal:  jobStatus{JobID: "job-attested", State: jobStatePassed, Terminal: true},
	}
	if err := runWorkflowWithConnectorAt(
		workflowCLIArgs("d"), &bytes.Buffer{}, func(context.Context) (coordinatorClient, error) { return client, nil },
		"/workspace/super-dolphin-checkout", "sha256:"+strings.Repeat("e", 64),
	); err != nil {
		t.Fatal(err)
	}
	if client.attestation != "sha256:"+strings.Repeat("e", 64) {
		t.Fatalf("workflow submit attestation = %q", client.attestation)
	}
	if client.owner != gatecontract.CIEntrypointOwnerWorkflowRequired {
		t.Fatalf("workflow submit owner = %q", client.owner)
	}
	if client.invocation != "workflow-"+strings.Repeat("e", 64) {
		t.Fatalf("workflow submit invocation = %q", client.invocation)
	}
}

func TestAuthoritativeWorkflowSubmitRejectsUnboundOIDCAttestation(t *testing.T) {
	plan, err := parsePlan(workflowCLIArgs("f"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = normalizeSubmitRequest(submitRequest{
		RepositoryRoot: t.TempDir(), Plan: plan, Entrypoint: gatecontract.CIEntrypointWorkflowRequired,
		AuthorityOwner: gatecontract.CIEntrypointOwnerWorkflowRequired,
	})
	if err == nil || !strings.Contains(err.Error(), "attestation-bound") {
		t.Fatalf("unbound workflow submit error = %v", err)
	}
}

func (client *workflowCoordinatorClient) Status(context.Context, string) (jobStatus, error) {
	return jobStatus{}, nil
}

func (client *workflowCoordinatorClient) Wait(_ context.Context, jobID string) (jobStatus, error) {
	client.calls = append(client.calls, "wait:"+jobID)
	return client.terminal, nil
}

func (client *workflowCoordinatorClient) Close() error {
	client.calls = append(client.calls, "close")
	return nil
}

func TestWorkflowCLIUsesOneCoordinatorForSubmitAndWait(t *testing.T) {
	client := &workflowCoordinatorClient{
		submitted: jobStatus{JobID: "job-1", State: jobStateQueued},
		terminal:  jobStatus{JobID: "job-1", State: jobStatePassed, Terminal: true},
	}
	connections := 0
	connector := func(context.Context) (coordinatorClient, error) {
		connections++
		return client, nil
	}
	output := &bytes.Buffer{}
	if err := runWorkflowWithConnectorAt(
		workflowCLIArgs("b"), output, connector, workflowCheckoutRoot, "sha256:"+strings.Repeat("1", 64),
	); err != nil {
		t.Fatalf("runWorkflowWithConnectorAt() error = %v", err)
	}
	if connections != 1 || len(client.calls) != 3 || client.calls[0] != "submit" ||
		!slices.Equal(client.calls[1:], []string{"wait:job-1", "close"}) {
		t.Fatalf("workflow lifecycle connections=%d calls=%#v", connections, client.calls)
	}
	if client.entrypoint != gatecontract.CIEntrypointWorkflowRequired {
		t.Fatalf("workflow submit entrypoint = %q", client.entrypoint)
	}
	var status jobStatus
	if err := json.Unmarshal(output.Bytes(), &status); err != nil || status.State != jobStatePassed || !status.Terminal {
		t.Fatalf("workflow terminal output=%s status=%#v error=%v", output.String(), status, err)
	}
}

func TestWorkflowCLIMapsFailedTerminalToGateViolation(t *testing.T) {
	client := &workflowCoordinatorClient{
		submitted: jobStatus{JobID: "job-2", State: jobStateQueued},
		terminal:  jobStatus{JobID: "job-2", State: jobStateFailed, Terminal: true},
	}
	err := runWorkflowWithConnectorAt(
		workflowCLIArgs("c"), &bytes.Buffer{}, func(context.Context) (coordinatorClient, error) { return client, nil },
		workflowCheckoutRoot, "sha256:"+strings.Repeat("2", 64),
	)
	if gatecontract.ExitCodeOf(err) != gatecontract.ExitGateViolation {
		t.Fatalf("workflow failure exit code = %d, error = %v", gatecontract.ExitCodeOf(err), err)
	}
	if !slices.Equal(client.calls[1:], []string{"wait:job-2", "close"}) {
		t.Fatalf("workflow failure lifecycle = %#v", client.calls)
	}
}

func workflowCLIArgs(character string) []string {
	return []string{
		"--profile", string(gatecontract.ProfileRemoteRequired), "--object-format", "sha1",
		"--commit", strings.Repeat("a", 40), "--source-tree", strings.Repeat(character, 40),
	}
}
