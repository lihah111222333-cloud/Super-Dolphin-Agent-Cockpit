package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type remoteBaselineSeedRuntimeStub struct {
	groups   [][]eci.ContainerGroup
	log      string
	logErr   error
	logCalls int
}

func (stub *remoteBaselineSeedRuntimeStub) DescribeContainerGroups(context.Context, ...string) ([]eci.ContainerGroup, error) {
	if len(stub.groups) == 0 {
		return nil, errors.New("unexpected DescribeContainerGroups call")
	}
	groups := stub.groups[0]
	stub.groups = stub.groups[1:]
	return groups, nil
}

func (stub *remoteBaselineSeedRuntimeStub) DescribeContainerLog(context.Context, string, string) (string, error) {
	stub.logCalls++
	if stub.logErr != nil {
		return "", stub.logErr
	}
	return stub.log, nil
}

func TestForwardRemoteBaselineSeedLiveLogForwardsOnlyNewProgress(t *testing.T) {
	var stderr bytes.Buffer
	forwarded := make(map[string]struct{})
	log := strings.Join([]string{
		"unrelated command output",
		"seed stage start: runtime elapsed_ms=0",
		"go cache compile start: phase=normal",
		"go build cache mode: incremental refresh",
		"runtime dependency cache reused: go modules",
		"seed progress: upload 50%",
		"seed stage start: runtime elapsed_ms=0",
	}, "\n")

	if err := forwardRemoteBaselineSeedLiveLog(&stderr, log, forwarded); err != nil {
		t.Fatalf("forwardRemoteBaselineSeedLiveLog() error = %v", err)
	}
	if err := forwardRemoteBaselineSeedLiveLog(&stderr, log, forwarded); err != nil {
		t.Fatalf("forwardRemoteBaselineSeedLiveLog() second call error = %v", err)
	}
	want := "seed stage start: runtime elapsed_ms=0\ngo cache compile start: phase=normal\ngo build cache mode: incremental refresh\nruntime dependency cache reused: go modules\nseed progress: upload 50%\n"
	if got := stderr.String(); got != want {
		t.Fatalf("forwarded stderr = %q, want %q", got, want)
	}
}

func TestWaitRemoteBaselineSeedFailsFastWhenRunningLogCannotBeRead(t *testing.T) {
	runtime := &remoteBaselineSeedRuntimeStub{
		groups: [][]eci.ContainerGroup{{{ID: "eci-seed", Status: "Running"}}},
		logErr: errors.New("log API unavailable"),
	}

	err := waitRemoteBaselineSeedWithWriter(
		context.Background(),
		runtime,
		"eci-seed",
		"seed",
		1,
		remoteci.BaselineIdentity{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "read running baseline seed log") {
		t.Fatalf("waitRemoteBaselineSeedWithWriter() error = %v, want running log API failure", err)
	}
	if runtime.logCalls != 1 {
		t.Fatalf("DescribeContainerLog calls = %d, want 1", runtime.logCalls)
	}
}

func TestWaitRemoteBaselineSeedForwardsRunningTailButNotTerminalLog(t *testing.T) {
	identity := remoteci.BaselineIdentity{MainCommit: "commit", MainTree: "tree"}
	runtime := &remoteBaselineSeedRuntimeStub{
		groups: [][]eci.ContainerGroup{
			{{ID: "eci-seed", Status: "Running"}},
			{{ID: "eci-seed", Status: "Succeeded"}},
		},
		log: strings.Join([]string{
			"seed stage complete: runtime elapsed_ms=10",
			"unrelated terminal detail",
			"SUPER_DOLPHIN_BASELINE_READY generation=7 commit=commit tree=tree",
		}, "\n"),
	}
	var stderr bytes.Buffer

	if err := waitRemoteBaselineSeedWithWriterAndInterval(
		context.Background(),
		runtime,
		"eci-seed",
		"seed",
		7,
		identity,
		&stderr,
		time.Millisecond,
	); err != nil {
		t.Fatalf("waitRemoteBaselineSeedWithWriterAndInterval() error = %v", err)
	}
	if got, want := stderr.String(), "seed stage complete: runtime elapsed_ms=10\n"; got != want {
		t.Fatalf("forwarded stderr = %q, want %q", got, want)
	}
	if runtime.logCalls != 2 {
		t.Fatalf("DescribeContainerLog calls = %d, want one running tail and one terminal log", runtime.logCalls)
	}
}

func TestValidateRemoteBaselineSeedStatusReadsTerminalLogOnce(t *testing.T) {
	identity := remoteci.BaselineIdentity{MainCommit: "commit", MainTree: "tree"}
	runtime := &remoteBaselineSeedRuntimeStub{
		log: "SUPER_DOLPHIN_BASELINE_READY generation=7 commit=commit tree=tree",
	}

	if err := validateRemoteBaselineSeedStatus(context.Background(), runtime, "eci-seed", "seed", 7, identity, eci.ContainerGroup{Status: "Succeeded"}); err != nil {
		t.Fatalf("validateRemoteBaselineSeedStatus() error = %v", err)
	}
	if runtime.logCalls != 1 {
		t.Fatalf("DescribeContainerLog calls = %d, want one terminal read", runtime.logCalls)
	}
}

func TestValidateRemoteBaselineSeedStatusPreservesExitEvidence(t *testing.T) {
	exitCode := int64(23)
	runtime := &remoteBaselineSeedRuntimeStub{log: "/input/seed.sh: OK"}
	group := eci.ContainerGroup{Status: "Failed", Containers: []eci.ContainerStatus{{
		Name: "seed", CurrentState: eci.ContainerState{State: "Terminated", ExitCode: &exitCode, Reason: "Error", Message: "delta replay failed"},
	}}}
	err := validateRemoteBaselineSeedStatus(context.Background(), runtime, "eci-seed", "seed", 7, remoteci.BaselineIdentity{}, group)
	if err == nil || !strings.Contains(err.Error(), "exit_code=23") || !strings.Contains(err.Error(), "delta replay failed") {
		t.Fatalf("terminal seed error = %v", err)
	}
}
