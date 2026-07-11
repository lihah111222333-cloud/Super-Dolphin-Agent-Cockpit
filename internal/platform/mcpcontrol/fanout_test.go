package mcpcontrol

import (
	"slices"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

type intersectTargetsCase struct {
	name     string
	selector dto.Selector
	build    func(*ToolRegistry) []sendTarget
	wantNil  bool
}

func TestToolRegistry_IntersectTargets(t *testing.T) {
	t.Parallel()

	cases := []intersectTargetsCase{
		{
			name:     "empty_selector_broadcasts_all_active_targets",
			selector: dto.Selector{},
			build:    buildBroadcastTargets,
		},
		{
			name:     "subscription_only_matches_active_targets",
			selector: dto.Selector{Subscription: "topic.subscription"},
			build:    buildSubscriptionTargets,
		},
		{
			name: "intersects_subscription_agent_and_client_kind",
			selector: dto.Selector{
				Subscription: "topic.multi",
				Scope: &dto.SelectorScope{
					AgentID:    "agent-1",
					ClientKind: dto.ClientKindOrch,
				},
			},
			build: buildMultiDimensionTargets,
		},
		{
			name: "intersects_subscription_and_thread_id",
			selector: dto.Selector{
				Subscription: "topic.thread",
				Scope: &dto.SelectorScope{
					ThreadID: "thread-1",
				},
			},
			build: buildThreadTargets,
		},
		{
			name: "matches_capability_dimension",
			selector: dto.Selector{
				Capability: "cap.exec",
			},
			build: buildCapabilityTargets,
		},
		{
			name: "returns_nil_when_any_dimension_has_no_match",
			selector: dto.Selector{
				Subscription: "topic.partial",
				Scope: &dto.SelectorScope{
					AgentID: "missing-agent",
				},
			},
			build:   buildPartialMatchFailureTargets,
			wantNil: true,
		},
		{
			name:     "returns_nil_without_registered_instances",
			selector: dto.Selector{Subscription: "topic.none"},
			build:    buildEmptyTargets,
			wantNil:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			registry := NewRegistry()
			want := tc.build(registry)
			got := registry.IntersectTargets(tc.selector)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("IntersectTargets() = %#v, want nil", got)
				}
				return
			}
			assertSendTargetsEqual(t, got, want)
		})
	}
}

func TestSmallestSelectorBucket(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		buckets []selectorBucket
		want    map[LeaseKey]struct{}
	}{
		{
			name: "prefers_smallest_bucket",
			buckets: []selectorBucket{
				{leases: leaseSet(leaseKey("instance-a", 1), leaseKey("instance-b", 1), leaseKey("instance-c", 1))},
				{leases: leaseSet(leaseKey("instance-b", 1))},
				{leases: leaseSet(leaseKey("instance-a", 1), leaseKey("instance-b", 1))},
			},
			want: leaseSet(leaseKey("instance-b", 1)),
		},
		{
			name: "keeps_first_bucket_on_equal_size",
			buckets: []selectorBucket{
				{leases: leaseSet(leaseKey("instance-first", 1))},
				{leases: leaseSet(leaseKey("instance-second", 1))},
			},
			want: leaseSet(leaseKey("instance-first", 1)),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := smallestSelectorBucket(tc.buckets)
			assertLeaseSetEqual(t, got.leases, tc.want)
		})
	}
}

func buildBroadcastTargets(registry *ToolRegistry) []sendTarget {
	wantA := addFanoutTestTarget(registry, leaseKey("instance-a", 1), func(instance *ToolInstance) {
		instance.Subscriptions = []string{"topic.broadcast"}
	})
	wantB := addFanoutTestTarget(registry, leaseKey("instance-b", 1), func(instance *ToolInstance) {
		instance.AgentID = "agent-broadcast"
		instance.ClientKind = dto.ClientKindLSP
	})
	addFanoutTestTarget(registry, leaseKey("instance-stale", 1), func(instance *ToolInstance) {
		instance.Status = dto.StatusStale
		instance.Subscriptions = []string{"topic.broadcast"}
	})
	return []sendTarget{wantA, wantB}
}

func buildSubscriptionTargets(registry *ToolRegistry) []sendTarget {
	want := addFanoutTestTarget(registry, leaseKey("instance-sub", 1), func(instance *ToolInstance) {
		instance.Subscriptions = []string{"topic.subscription"}
	})
	addFanoutTestTarget(registry, leaseKey("instance-other", 1), func(instance *ToolInstance) {
		instance.Subscriptions = []string{"topic.other"}
	})
	addFanoutTestTarget(registry, leaseKey("instance-sub-stale", 1), func(instance *ToolInstance) {
		instance.Status = dto.StatusStale
		instance.Subscriptions = []string{"topic.subscription"}
	})
	return []sendTarget{want}
}

func buildMultiDimensionTargets(registry *ToolRegistry) []sendTarget {
	want := addFanoutTestTarget(registry, leaseKey("instance-match", 1), func(instance *ToolInstance) {
		instance.AgentID = "agent-1"
		instance.ClientKind = dto.ClientKindOrch
		instance.Subscriptions = []string{"topic.multi"}
	})
	addFanoutTestTarget(registry, leaseKey("instance-wrong-client", 1), func(instance *ToolInstance) {
		instance.AgentID = "agent-1"
		instance.ClientKind = dto.ClientKindLSP
		instance.Subscriptions = []string{"topic.multi"}
	})
	addFanoutTestTarget(registry, leaseKey("instance-wrong-agent", 1), func(instance *ToolInstance) {
		instance.AgentID = "agent-2"
		instance.ClientKind = dto.ClientKindOrch
		instance.Subscriptions = []string{"topic.multi"}
	})
	addFanoutTestTarget(registry, leaseKey("instance-wrong-subscription", 1), func(instance *ToolInstance) {
		instance.AgentID = "agent-1"
		instance.ClientKind = dto.ClientKindOrch
		instance.Subscriptions = []string{"topic.other"}
	})
	return []sendTarget{want}
}

func buildThreadTargets(registry *ToolRegistry) []sendTarget {
	wantA := addFanoutTestTarget(registry, leaseKey("instance-thread-a", 1), func(instance *ToolInstance) {
		instance.ThreadID = "thread-1"
		instance.Subscriptions = []string{"topic.thread"}
	})
	wantB := addFanoutTestTarget(registry, leaseKey("instance-thread-b", 1), func(instance *ToolInstance) {
		instance.ThreadID = "thread-1"
		instance.Subscriptions = []string{"topic.thread"}
	})
	addFanoutTestTarget(registry, leaseKey("instance-thread-wrong-thread", 1), func(instance *ToolInstance) {
		instance.ThreadID = "thread-2"
		instance.Subscriptions = []string{"topic.thread"}
	})
	addFanoutTestTarget(registry, leaseKey("instance-thread-stale", 1), func(instance *ToolInstance) {
		instance.Status = dto.StatusStale
		instance.ThreadID = "thread-1"
		instance.Subscriptions = []string{"topic.thread"}
	})
	return []sendTarget{wantA, wantB}
}

func buildCapabilityTargets(registry *ToolRegistry) []sendTarget {
	wantA := addFanoutTestTarget(registry, leaseKey("instance-cap-a", 1), func(instance *ToolInstance) {
		instance.Capabilities = []string{"cap.exec"}
	})
	wantB := addFanoutTestTarget(registry, leaseKey("instance-cap-b", 1), func(instance *ToolInstance) {
		instance.Capabilities = []string{"cap.exec", "cap.extra"}
	})
	addFanoutTestTarget(registry, leaseKey("instance-cap-other", 1), func(instance *ToolInstance) {
		instance.Capabilities = []string{"cap.other"}
	})
	addFanoutTestTarget(registry, leaseKey("instance-cap-stale", 1), func(instance *ToolInstance) {
		instance.Status = dto.StatusStale
		instance.Capabilities = []string{"cap.exec"}
	})
	return []sendTarget{wantA, wantB}
}

func buildPartialMatchFailureTargets(registry *ToolRegistry) []sendTarget {
	addFanoutTestTarget(registry, leaseKey("instance-partial", 1), func(instance *ToolInstance) {
		instance.AgentID = "agent-1"
		instance.Subscriptions = []string{"topic.partial"}
	})
	return nil
}

func buildEmptyTargets(*ToolRegistry) []sendTarget {
	return nil
}

func addFanoutTestTarget(
	registry *ToolRegistry,
	lease LeaseKey,
	configure func(*ToolInstance),
) sendTarget {
	instance := &ToolInstance{
		Lease: lease,
		Peer:  &stubCallbackPeer{},
	}
	if configure != nil {
		configure(instance)
	}
	addIndexedInstance(registry, instance)
	return sendTarget{key: lease, peer: instance.Peer}
}

func assertSendTargetsEqual(t *testing.T, got, want []sendTarget) {
	t.Helper()

	got = sortedSendTargets(got)
	want = sortedSendTargets(want)
	if len(got) != len(want) {
		t.Fatalf("IntersectTargets() = %#v, want %#v", got, want)
	}
	if !slices.EqualFunc(got, want, equalSendTarget) {
		t.Fatalf("IntersectTargets() = %#v, want %#v", got, want)
	}
}

func sortedSendTargets(targets []sendTarget) []sendTarget {
	sorted := slices.Clone(targets)
	slices.SortFunc(sorted, compareSendTarget)
	return sorted
}

func equalSendTarget(left, right sendTarget) bool {
	return left.key == right.key && left.peer == right.peer
}

func compareSendTarget(left, right sendTarget) int {
	if left.key.InstanceID < right.key.InstanceID {
		return -1
	}
	if left.key.InstanceID > right.key.InstanceID {
		return 1
	}
	if left.key.Generation < right.key.Generation {
		return -1
	}
	if left.key.Generation > right.key.Generation {
		return 1
	}
	return 0
}

func assertLeaseSetEqual(t *testing.T, got, want map[LeaseKey]struct{}) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("lease set = %#v, want %#v", got, want)
	}
	for key := range want {
		if _, ok := got[key]; !ok {
			t.Fatalf("lease set = %#v, want %#v", got, want)
		}
	}
}

func leaseSet(keys ...LeaseKey) map[LeaseKey]struct{} {
	set := make(map[LeaseKey]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

func leaseKey(instanceID string, generation uint64) LeaseKey {
	return LeaseKey{InstanceID: instanceID, Generation: generation}
}
