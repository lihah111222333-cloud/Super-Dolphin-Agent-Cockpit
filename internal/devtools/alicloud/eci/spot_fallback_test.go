package eci

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestClient_SpotSchedulingFallsBackAfterThirtySecondsWithoutOverlap(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{
		[]byte(`{"ContainerGroupId":"eci-spot"}`),
		spotGroupResponse("Scheduling"),
		spotGroupResponse("Scheduling"),
		spotGroupResponse("Scheduling"),
		spotGroupResponse("Scheduling"),
		[]byte(`{"RequestId":"delete-spot"}`),
		[]byte(`{"ContainerGroups":[]}`),
		[]byte(`{"ContainerGroupId":"eci-regular"}`),
	}}
	client := newTestClientWithTokens(t, runner, "spot-token", "regular-token")
	elapsed := enableSpotFallbackClock(client)

	group, err := client.CreateContainerGroup(context.Background(), validCreateRequest())
	if err != nil || group.ID != "eci-regular" {
		t.Fatalf("CreateContainerGroup() = %#v, %v", group, err)
	}
	if got := elapsed(); got != 30*time.Second {
		t.Fatalf("spot scheduling wait = %s, want 30s", got)
	}
	if got := fallbackActionOrder(runner.calls); !reflect.DeepEqual(got, []string{
		"CreateContainerGroup:SpotAsPriceGo",
		"DescribeContainerGroups",
		"DescribeContainerGroups",
		"DescribeContainerGroups",
		"DescribeContainerGroups",
		"DeleteContainerGroup",
		"DescribeContainerGroups",
		"CreateContainerGroup:NoSpot",
	}) {
		t.Fatalf("fallback action order = %v", got)
	}
}

func TestClient_SpotPendingWithinThirtySecondsDoesNotFallBack(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{
		[]byte(`{"ContainerGroupId":"eci-spot"}`),
		spotGroupResponse("Scheduling"),
		spotGroupResponse("Pending"),
	}}
	client := newTestClientWithTokens(t, runner, "spot-token")
	elapsed := enableSpotFallbackClock(client)

	group, err := client.CreateContainerGroup(context.Background(), validCreateRequest())
	if err != nil || group.ID != "eci-spot" || group.Status != "Pending" {
		t.Fatalf("CreateContainerGroup() = %#v, %v", group, err)
	}
	if got := elapsed(); got != 10*time.Second {
		t.Fatalf("spot scheduling wait = %s, want 10s", got)
	}
	for _, call := range runner.calls {
		if containsArgumentPair(call, "--SpotStrategy", SpotStrategyNoSpot) || call[2] == "DeleteContainerGroup" {
			t.Fatalf("admitted spot instance must not fall back: %#v", runner.calls)
		}
	}
}

func TestClient_SeedUsesSameThirtySecondSpotFallback(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{
		[]byte(`{"ContainerGroupId":"eci-seed-spot"}`),
		spotGroupResponseWithID("eci-seed-spot", "Scheduling"),
		spotGroupResponseWithID("eci-seed-spot", "Scheduling"),
		spotGroupResponseWithID("eci-seed-spot", "Scheduling"),
		spotGroupResponseWithID("eci-seed-spot", "Scheduling"),
		[]byte(`{"RequestId":"delete-seed-spot"}`),
		[]byte(`{"ContainerGroups":[]}`),
		[]byte(`{"ContainerGroupId":"eci-seed-regular"}`),
	}}
	client := newTestClient(t, runner)
	elapsed := enableSpotFallbackClock(client)

	group, err := client.CreateSeedContainerGroup(context.Background(), validSeedRequest())
	if err != nil || group.ID != "eci-seed-regular" {
		t.Fatalf("CreateSeedContainerGroup() = %#v, %v", group, err)
	}
	if got := elapsed(); got != 30*time.Second {
		t.Fatalf("seed spot scheduling wait = %s, want 30s", got)
	}
	order := fallbackActionOrder(runner.calls)
	if order[len(order)-1] != "CreateContainerGroup:NoSpot" {
		t.Fatalf("seed fallback action order = %v", order)
	}
}

func TestClient_SpotScheduleFailureFallsBackImmediately(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{
		[]byte(`{"ContainerGroupId":"eci-spot"}`),
		spotGroupResponse("ScheduleFailed"),
		[]byte(`{"RequestId":"delete-spot"}`),
		[]byte(`{"ContainerGroups":[]}`),
		[]byte(`{"ContainerGroupId":"eci-regular"}`),
	}}
	client := newTestClientWithTokens(t, runner, "spot-token", "regular-token")
	enableSpotFallbackClock(client)

	group, err := client.CreateContainerGroup(context.Background(), validCreateRequest())
	if err != nil || group.ID != "eci-regular" {
		t.Fatalf("CreateContainerGroup() = %#v, %v", group, err)
	}
	if got := fallbackActionOrder(runner.calls); !reflect.DeepEqual(got, []string{
		"CreateContainerGroup:SpotAsPriceGo",
		"DescribeContainerGroups",
		"DeleteContainerGroup",
		"DescribeContainerGroups",
		"CreateContainerGroup:NoSpot",
	}) {
		t.Fatalf("fallback action order = %v", got)
	}
}

func TestClient_DoesNotCreatePayAsYouGoUntilSpotDeletionIsConfirmed(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{
		[]byte(`{"ContainerGroupId":"eci-spot"}`),
		spotGroupResponse("ScheduleFailed"),
		[]byte(`{"RequestId":"delete-spot"}`),
		spotGroupResponse("Terminating"),
		spotGroupResponse("Terminating"),
		spotGroupResponse("Terminating"),
		spotGroupResponse("Terminating"),
	}}
	client := newTestClientWithTokens(t, runner, "spot-token")
	enableSpotFallbackClock(client)

	if _, err := client.CreateContainerGroup(context.Background(), validCreateRequest()); err == nil {
		t.Fatal("CreateContainerGroup() error = nil")
	}
	for _, call := range runner.calls {
		if containsArgumentPair(call, "--SpotStrategy", SpotStrategyNoSpot) {
			t.Fatalf("pay-as-you-go instance started before deletion confirmation: %#v", runner.calls)
		}
	}
}

func enableSpotFallbackClock(client *Client) func() time.Duration {
	start := time.Unix(0, 0)
	current := start
	client.now = func() time.Time { return current }
	client.wait = func(_ context.Context, delay time.Duration) error {
		current = current.Add(delay)
		return nil
	}
	client.spotSchedulingTimeout = 30 * time.Second
	client.spotCleanupTimeout = 30 * time.Second
	client.spotPollInterval = 10 * time.Second
	return func() time.Duration { return current.Sub(start) }
}

func spotGroupResponse(status string) []byte {
	return spotGroupResponseWithID("eci-spot", status)
}

func spotGroupResponseWithID(id string, status string) []byte {
	return []byte(`{"ContainerGroups":[{"ContainerGroupId":"` + id + `","ContainerGroupName":"test","Status":"` + status + `"}]}`)
}

func fallbackActionOrder(calls [][]string) []string {
	order := make([]string, 0, len(calls))
	for _, call := range calls {
		action := call[2]
		if action == "CreateContainerGroup" {
			if containsArgumentPair(call, "--SpotStrategy", SpotStrategyNoSpot) {
				action += ":" + SpotStrategyNoSpot
			} else {
				action += ":" + SpotStrategyAsPriceGo
			}
		}
		order = append(order, action)
	}
	return order
}
