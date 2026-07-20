package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type terminalSubmitClient struct {
	submitted jobStatus
	terminal  jobStatus
	waitJob   string
}

func (client *terminalSubmitClient) Submit(context.Context, submitRequest) (jobStatus, error) {
	return client.submitted, nil
}

func (*terminalSubmitClient) Status(context.Context, string) (jobStatus, error) {
	return jobStatus{}, nil
}

func (client *terminalSubmitClient) Wait(_ context.Context, jobID string) (jobStatus, error) {
	client.waitJob = jobID
	return client.terminal, nil
}

func (*terminalSubmitClient) Close() error { return nil }

func TestRunSubmitWithConnectorWaitsAndRejectsFailedTerminalState(t *testing.T) {
	client := &terminalSubmitClient{
		submitted: jobStatus{JobID: "job-submitted", State: jobStateQueued},
		terminal:  jobStatus{JobID: "job-submitted", State: jobStateFailed, Terminal: true},
	}
	connector := func(context.Context) (coordinatorClient, error) { return client, nil }
	output := &bytes.Buffer{}

	err := runSubmitWithConnector(append([]string{"--wait"}, testPlanArgs("b")...), output, connector)
	if got := gatecontract.ExitCodeOf(err); got != gatecontract.ExitGateViolation {
		t.Fatalf("runSubmitWithConnector() exit code = %d, want failed gate exit %d; error = %v", got, gatecontract.ExitGateViolation, err)
	}
	if client.waitJob != "job-submitted" {
		t.Fatalf("Wait() job = %q, want submitted job", client.waitJob)
	}
	var status jobStatus
	if err := json.Unmarshal(output.Bytes(), &status); err != nil {
		t.Fatalf("decode terminal status: %v; output = %s", err, output.String())
	}
	if status.State != jobStateFailed || !status.Terminal {
		t.Fatalf("terminal status = %#v, want failed terminal status", status)
	}
}

func TestParseSubmitArgsRejectsRepeatedWait(t *testing.T) {
	if _, _, err := parseSubmitArgs([]string{"--wait", "--wait"}); err == nil {
		t.Fatal("parseSubmitArgs() accepted repeated --wait")
	}
}

func TestRunSubmitWithConnectorRejectsReleaseBeforeConnecting(t *testing.T) {
	called := false
	err := runSubmitWithConnector(
		[]string{
			"--profile", string(gatecontract.ProfileRelease),
			"--object-format", string(gatecontract.GitObjectFormatSHA1),
			"--source-tree", strings.Repeat("b", 40), "--commit", strings.Repeat("a", 40),
		},
		&bytes.Buffer{},
		func(context.Context) (coordinatorClient, error) {
			called = true
			return nil, errors.New("manual release must not connect")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "authoritative release entrypoint") || called {
		t.Fatalf("manual release error=%v connected=%t", err, called)
	}
}

type authoritativeReleaseWaitClient struct {
	terminalSubmitClient
	request submitRequest
}

func (client *authoritativeReleaseWaitClient) Submit(ctx context.Context, request submitRequest) (jobStatus, error) {
	client.request = request
	return client.terminalSubmitClient.Submit(ctx, request)
}

func TestProductionLauncherReleaseWaitsForFailedAuthorityJob(t *testing.T) {
	fixture := newProductionTestFixture(t)
	client := &authoritativeReleaseWaitClient{terminalSubmitClient: terminalSubmitClient{
		submitted: jobStatus{JobID: "release-submitted", State: jobStateQueued},
		terminal:  jobStatus{JobID: "release-submitted", State: jobStateFailed, Terminal: true},
	}}
	output := &bytes.Buffer{}
	err := runProductionLauncherWithConnector(
		[]string{
			"submit", "--wait", "--profile", string(gatecontract.ProfileRelease),
			"--object-format", string(gatecontract.GitObjectFormatSHA1),
			"--source-tree", fixture.tree, "--commit", fixture.commit,
		},
		output,
		func() (productionCoordinatorConfig, error) { return fixture.config, nil },
		func() (string, error) { return fixture.sourceRepo, nil },
		func(context.Context) (coordinatorClient, error) { return client, nil },
		func([]string, io.Writer) error { return errors.New("unexpected launcher fallback") },
	)
	if got := gatecontract.ExitCodeOf(err); got != gatecontract.ExitGateViolation {
		t.Fatalf("production launcher exit code = %d, want failed gate exit %d; error = %v", got, gatecontract.ExitGateViolation, err)
	}
	if client.waitJob != "release-submitted" || client.request.Plan.Profile != gatecontract.ProfileRelease {
		t.Fatalf("authority release request=%#v wait job=%q", client.request, client.waitJob)
	}
}

func TestProductionLauncherRejectsRepeatedWait(t *testing.T) {
	err := runProductionLauncherWithConnector(
		[]string{"submit", "--wait", "--wait"},
		&bytes.Buffer{},
		func() (productionCoordinatorConfig, error) {
			return productionCoordinatorConfig{}, errors.New("unexpected config load")
		},
		func() (string, error) { return "", errors.New("unexpected repository root") },
		nil,
		func([]string, io.Writer) error { return errors.New("unexpected launcher fallback") },
	)
	if err == nil || !strings.Contains(err.Error(), "at most once") {
		t.Fatalf("repeated production launcher --wait error = %v", err)
	}
}
