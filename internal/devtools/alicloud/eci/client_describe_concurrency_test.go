package eci

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

// describeBatchRunner 将每个 provider 批次映射到对应响应，并可在 provider 边界暂存所有批次以验证真实 fanout。
type describeBatchRunner struct {
	mu              sync.Mutex
	calls           [][]string
	responses       map[string][]byte
	active          int
	maxActive       int
	expectedBatches int
	started         chan struct{}
	startedClosed   bool
	release         chan struct{}
}

func (runner *describeBatchRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	key, err := describeBatchKey(args)
	if err != nil {
		return nil, err
	}
	release, response := runner.recordBatch(name, args, key)
	if err := runner.waitForRelease(ctx, release); err != nil {
		runner.finishBatch()
		return nil, err
	}
	runner.finishBatch()
	if response == nil {
		return nil, fmt.Errorf("missing response for DescribeContainerGroups IDs %s", key)
	}
	return response, nil
}

func describeBatchKey(args []string) (string, error) {
	argumentIndex := slices.Index(args, "--ContainerGroupIds")
	if argumentIndex < 0 || argumentIndex+1 >= len(args) {
		return "", fmt.Errorf("DescribeContainerGroups request is missing IDs: %v", args)
	}
	return args[argumentIndex+1], nil
}

func (runner *describeBatchRunner) recordBatch(name string, args []string, key string) (chan struct{}, []byte) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.calls = append(runner.calls, append([]string{name}, args...))
	runner.active++
	if runner.active > runner.maxActive {
		runner.maxActive = runner.active
	}
	runner.signalStartedLocked()
	return runner.release, runner.responses[key]
}

func (runner *describeBatchRunner) signalStartedLocked() {
	if runner.started == nil || runner.startedClosed || runner.active != runner.expectedBatches {
		return
	}
	close(runner.started)
	runner.startedClosed = true
}

func (runner *describeBatchRunner) waitForRelease(ctx context.Context, release chan struct{}) error {
	if release == nil {
		return nil
	}
	select {
	case <-release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (runner *describeBatchRunner) finishBatch() {
	runner.mu.Lock()
	runner.active--
	runner.mu.Unlock()
}

func (runner *describeBatchRunner) snapshot() ([][]string, int) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	calls := make([][]string, len(runner.calls))
	for index := range runner.calls {
		calls[index] = slices.Clone(runner.calls[index])
	}
	return calls, runner.maxActive
}

type describeBatchResult struct {
	groups []ContainerGroup
	err    error
}

type describeBatchCall struct {
	done    chan describeBatchResult
	workers errgroup.Group
}

func startDescribeBatchCall(ctx context.Context, client *Client, ids []string) *describeBatchCall {
	call := &describeBatchCall{done: make(chan describeBatchResult, 1)}
	call.workers.Go(func() error {
		groups, err := client.DescribeContainerGroups(ctx, ids...)
		call.done <- describeBatchResult{groups: groups, err: err}
		return nil
	})
	return call
}

func awaitDescribeBatchCall(t *testing.T, ctx context.Context, call *describeBatchCall) describeBatchResult {
	t.Helper()
	select {
	case result := <-call.done:
		if err := call.workers.Wait(); err != nil {
			t.Fatalf("DescribeContainerGroups worker error = %v", err)
		}
		return result
	case <-ctx.Done():
		t.Fatalf("DescribeContainerGroups() did not finish: %v", ctx.Err())
		return describeBatchResult{}
	}
}

func waitForDescribeBatches(t *testing.T, ctx context.Context, runner *describeBatchRunner) {
	t.Helper()
	select {
	case <-runner.started:
	case <-ctx.Done():
		t.Fatalf("DescribeContainerGroups() did not start all provider batches: %v", ctx.Err())
	}
}

func buildDescribeBatchFixture(t *testing.T, prefix string, idCount int) ([]string, map[string][]byte) {
	t.Helper()
	ids := make([]string, idCount)
	for index := range ids {
		ids[index] = prefix + strconv.Itoa(index)
	}
	responses := make(map[string][]byte, (idCount+maxDescribeContainerGroupIDs-1)/maxDescribeContainerGroupIDs)
	for start := 0; start < len(ids); start += maxDescribeContainerGroupIDs {
		end := min(start+maxDescribeContainerGroupIDs, len(ids))
		groups := containerGroupsForIDs(ids[start:end])
		responses[marshalDescribeIDs(t, ids[start:end])] = marshalDescribeGroups(t, groups)
	}
	return ids, responses
}

func marshalDescribeGroups(t *testing.T, groups []ContainerGroup) []byte {
	t.Helper()
	payload, err := json.Marshal(struct {
		ContainerGroups []ContainerGroup `json:"ContainerGroups"`
	}{ContainerGroups: groups})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func marshalDescribeIDs(t *testing.T, ids []string) string {
	t.Helper()
	payload, err := json.Marshal(ids)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func containerGroupsForIDs(ids []string) []ContainerGroup {
	groups := make([]ContainerGroup, len(ids))
	for index, id := range ids {
		groups[index] = ContainerGroup{ID: id, Status: "Running"}
	}
	return groups
}

func TestClientDescribeContainerGroupsBatchesOfficialTwentyIDLimit(t *testing.T) {
	const officialLimit = 20
	if maxDescribeContainerGroupIDs != officialLimit {
		t.Fatalf("maxDescribeContainerGroupIDs = %d, want provider limit %d", maxDescribeContainerGroupIDs, officialLimit)
	}
	ids, responses := buildDescribeBatchFixture(t, "eci-batch-", officialLimit+1)
	runner := &describeBatchRunner{responses: responses}
	groups, err := newTestClient(t, runner).DescribeContainerGroups(context.Background(), ids...)
	if err != nil {
		t.Fatalf("DescribeContainerGroups() error = %v", err)
	}
	calls, _ := runner.snapshot()
	if len(groups) != len(ids) || len(calls) != 2 {
		t.Fatalf("DescribeContainerGroups() groups=%d calls=%d, want groups=%d calls=2", len(groups), len(calls), len(ids))
	}
	assertDescribeBatches(t, calls, ids, officialLimit)
}

func assertDescribeBatches(t *testing.T, calls [][]string, ids []string, limit int) {
	t.Helper()
	queriedBatches := make(map[string]struct{}, len(calls))
	for batchIndex, call := range calls {
		queried := decodeDescribeCallIDs(t, call, batchIndex)
		if len(queried) > limit {
			t.Fatalf("DescribeContainerGroups() call %d queried %d IDs, provider limit %d", batchIndex, len(queried), limit)
		}
		queriedBatches[marshalDescribeIDs(t, queried)] = struct{}{}
	}
	for start := 0; start < len(ids); start += limit {
		end := min(start+limit, len(ids))
		if _, ok := queriedBatches[marshalDescribeIDs(t, ids[start:end])]; !ok {
			t.Fatalf("DescribeContainerGroups() missing batch IDs = %#v", ids[start:end])
		}
	}
}

func decodeDescribeCallIDs(t *testing.T, call []string, batchIndex int) []string {
	t.Helper()
	argumentIndex := slices.Index(call, "--ContainerGroupIds")
	if argumentIndex < 0 || argumentIndex+1 >= len(call) {
		t.Fatalf("DescribeContainerGroups() call %d lacks ContainerGroupIds: %#v", batchIndex, call)
	}
	var queried []string
	if err := json.Unmarshal([]byte(call[argumentIndex+1]), &queried); err != nil {
		t.Fatalf("decode call %d IDs: %v", batchIndex, err)
	}
	return queried
}

func TestClientDescribeContainerGroupsFansOutAllProviderBatches(t *testing.T) {
	const batchCount = 3
	ids, responses := buildDescribeBatchFixture(t, "eci-fanout-", maxDescribeContainerGroupIDs*2+1)
	runner := &describeBatchRunner{
		responses:       responses,
		expectedBatches: batchCount,
		started:         make(chan struct{}),
		release:         make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	call := startDescribeBatchCall(ctx, newTestClient(t, runner), ids)
	waitForDescribeBatches(t, ctx, runner)
	if _, maxActive := runner.snapshot(); maxActive != batchCount {
		t.Fatalf("DescribeContainerGroups() max concurrent batches = %d, want %d", maxActive, batchCount)
	}
	close(runner.release)
	result := awaitDescribeBatchCall(t, ctx, call)
	if result.err != nil || len(result.groups) != len(ids) {
		t.Fatalf("DescribeContainerGroups() = groups=%d, err=%v; want %d groups", len(result.groups), result.err, len(ids))
	}
	assertDescribeGroupOrder(t, result.groups, ids)
}

func assertDescribeGroupOrder(t *testing.T, groups []ContainerGroup, ids []string) {
	t.Helper()
	for index, group := range groups {
		if group.ID != ids[index] {
			t.Fatalf("DescribeContainerGroups() result order at %d = %q, want %q", index, group.ID, ids[index])
		}
	}
}

func TestClientDescribeContainerGroupsWaitsForSiblingBatchAfterError(t *testing.T) {
	const batchCount = 3
	ids, responses := buildDescribeBatchFixture(t, "eci-error-fanout-", maxDescribeContainerGroupIDs*2+1)
	delete(responses, marshalDescribeIDs(t, ids[:maxDescribeContainerGroupIDs]))
	runner := &describeBatchRunner{
		responses:       responses,
		expectedBatches: batchCount,
		started:         make(chan struct{}),
		release:         make(chan struct{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	call := startDescribeBatchCall(ctx, newTestClient(t, runner), ids)
	waitForDescribeBatches(t, ctx, runner)
	select {
	case result := <-call.done:
		t.Fatalf("DescribeContainerGroups() returned before sibling batches drained: %v", result.err)
	case <-time.After(10 * time.Millisecond):
	}
	close(runner.release)
	result := awaitDescribeBatchCall(t, ctx, call)
	if result.err == nil || !strings.Contains(result.err.Error(), "missing response") {
		t.Fatalf("DescribeContainerGroups() error = %v, want missing batch response", result.err)
	}
}
