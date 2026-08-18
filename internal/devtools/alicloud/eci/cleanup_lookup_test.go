package eci

import (
	"context"
	"testing"
)

func TestFindContainerGroupsByNameAndJobTagUsesBothExactProviderFilters(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroups":[{"ContainerGroupId":"eci-cleanup","ContainerGroupName":"sdci-job-s00","Tags":[{"Key":"super-dolphin-job","Value":"job-cleanup"}]}]}`)}}
	client := newTestClient(t, runner)
	groups, err := client.FindContainerGroupsByNameAndJobTag(context.Background(), "sdci-job-s00", "job-cleanup")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].ID != "eci-cleanup" || groups[0].Name != "sdci-job-s00" {
		t.Fatalf("cleanup lookup groups = %#v", groups)
	}
	if len(runner.calls) != 1 || !containsArgumentPair(runner.calls[0], "--ContainerGroupName", "sdci-job-s00") || !containsArgumentPair(runner.calls[0], "--Tag", `[{"Key":"super-dolphin-job","Value":"job-cleanup"}]`) {
		t.Fatalf("cleanup lookup provider args = %#v", runner.calls)
	}
}
