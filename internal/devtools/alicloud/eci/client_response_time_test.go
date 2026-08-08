package eci

import (
	"context"
	"strings"
	"testing"
)

// TestClientDescribeContainerGroupsAcceptsEmptyInactiveTerminalTime 覆盖 ECI 成功组返回空 FailedTime 的真实边界。
func TestClientDescribeContainerGroupsAcceptsEmptyInactiveTerminalTime(t *testing.T) {
	response := []byte(`{"ContainerGroups":[{"ContainerGroupId":"eci-success","Status":"Succeeded","CreationTime":"2026-08-04T01:12:08Z","SucceededTime":"2026-08-04T01:12:14Z","FailedTime":""}]}`)
	client := newTestClient(t, &fakeCommandRunner{responses: [][]byte{response}})
	groups, err := client.DescribeContainerGroups(context.Background(), "eci-success")
	if err != nil {
		t.Fatalf("DescribeContainerGroups() error = %v", err)
	}
	if len(groups) != 1 || groups[0].FailedTime.IsZero() == false {
		t.Fatalf("DescribeContainerGroups() = %#v, want zero FailedTime", groups)
	}
}

// TestClientDescribeContainerGroupsRejectsMalformedNonEmptyTime 保持非空畸形时间的 fail-fast 边界。
func TestClientDescribeContainerGroupsRejectsMalformedNonEmptyTime(t *testing.T) {
	response := []byte(`{"ContainerGroups":[{"ContainerGroupId":"eci-success","Status":"Succeeded","CreationTime":"not-a-time"}]}`)
	client := newTestClient(t, &fakeCommandRunner{responses: [][]byte{response}})
	_, err := client.DescribeContainerGroups(context.Background(), "eci-success")
	if err == nil || !strings.Contains(err.Error(), "parse ECI CreationTime") {
		t.Fatalf("DescribeContainerGroups() error = %v, want malformed time rejection", err)
	}
}

// TestClientDescribeContainerGroupsAcceptsEmptyRunningContainerFinishTime 覆盖轮询中 ECI 返回空 FinishTime 的真实边界。
func TestClientDescribeContainerGroupsAcceptsEmptyRunningContainerFinishTime(t *testing.T) {
	response := []byte(`{"ContainerGroups":[{"ContainerGroupId":"eci-running","Status":"Running","CreationTime":"2026-08-04T01:12:08Z","Containers":[{"Name":"worker","CurrentState":{"State":"Running","StartTime":"2026-08-04T01:12:10Z","FinishTime":""}}]}]}`)
	client := newTestClient(t, &fakeCommandRunner{responses: [][]byte{response}})
	groups, err := client.DescribeContainerGroups(context.Background(), "eci-running")
	if err != nil {
		t.Fatalf("DescribeContainerGroups() error = %v", err)
	}
	state := groups[0].Containers[0].CurrentState
	if state.StartTime.IsZero() || !state.FinishTime.IsZero() {
		t.Fatalf("CurrentState = %#v, want non-zero start and zero finish", state)
	}
}

// TestClientDescribeContainerGroupsRejectsMalformedContainerStateTime 保持嵌套非空畸形时间的 fail-fast 边界。
func TestClientDescribeContainerGroupsRejectsMalformedContainerStateTime(t *testing.T) {
	response := []byte(`{"ContainerGroups":[{"ContainerGroupId":"eci-running","Status":"Running","CreationTime":"2026-08-04T01:12:08Z","Containers":[{"Name":"worker","CurrentState":{"State":"Running","StartTime":"not-a-time","FinishTime":""}}]}]}`)
	client := newTestClient(t, &fakeCommandRunner{responses: [][]byte{response}})
	_, err := client.DescribeContainerGroups(context.Background(), "eci-running")
	if err == nil || !strings.Contains(err.Error(), "parse ECI ContainerState.StartTime") {
		t.Fatalf("DescribeContainerGroups() error = %v, want malformed container state time rejection", err)
	}
}
