package gate

import (
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestDeriveNormalResourceTierReservesKeyedRejectsDuplicateTuple(t *testing.T) {
	base := time.UnixMilli(10_000).UTC()
	key := shardOverheadObservationKey{jobID: "job-a", shard: "shared-shard"}
	small := RemoteCIShardResources{ClassID: "small", CPU: 2, MemoryGiB: 4}
	medium := RemoteCIShardResources{ClassID: "medium", CPU: 4, MemoryGiB: 8}
	observations := normalReserveTimingObservations(key.jobID, key.shard, base, small)
	for name, resources := range map[string][]normalReserveShardResource{
		"same resources": {
			{key: key, resources: small},
			{key: key, resources: small},
		},
		"conflicting resources and tier": {
			{key: key, resources: small},
			{key: key, resources: medium},
		},
	} {
		t.Run(name, func(t *testing.T) {
			reserves, err := deriveNormalResourceTierReservesKeyed(observations, resources, "calibration")
			if err == nil {
				t.Fatalf("duplicate keyed resource tuple was accepted: %#v", reserves)
			}
		})
	}
}

func TestDeriveNormalResourceTierReservesKeyedAggregatesCrossJobSameShardSameTier(t *testing.T) {
	base := time.UnixMilli(10_000).UTC()
	shard := "shared-shard"
	resources := RemoteCIShardResources{ClassID: "small", CPU: 2, MemoryGiB: 4}
	observations := append(
		normalReserveTimingObservations("job-a", shard, base, resources),
		normalReserveTimingObservations("job-b", shard, base.Add(time.Minute), resources)...,
	)
	shardResources := []normalReserveShardResource{
		{key: shardOverheadObservationKey{jobID: "job-a", shard: shard}, resources: resources},
		{key: shardOverheadObservationKey{jobID: "job-b", shard: shard}, resources: resources},
	}
	reserves, err := deriveNormalResourceTierReservesKeyed(observations, shardResources, "calibration")
	if err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 {
		t.Fatalf("cross-job same-tier reserves = %#v, want one bucket", reserves)
	}
	reserve := reserves[0]
	if reserve.ResourceTier != cicontract.WorkloadResourceTierFast || reserve.Measurement == nil {
		t.Fatalf("cross-job same-tier reserve = %#v, want measured fast tier", reserve)
	}
	if reserve.Measurement.SampleCount != 2 || reserve.Measurement.P95MS != 5_000 {
		t.Fatalf("cross-job same-tier measurement = %#v, want sample_count=2 p95=5000ms", reserve.Measurement)
	}
}

func TestDeriveNormalResourceTierReservesKeyedSeparatesCrossJobDifferentTier(t *testing.T) {
	base := time.UnixMilli(10_000).UTC()
	shard := "shared-shard"
	small := RemoteCIShardResources{ClassID: "small", CPU: 2, MemoryGiB: 4}
	medium := RemoteCIShardResources{ClassID: "medium", CPU: 4, MemoryGiB: 8}
	observations := append(
		normalReserveTimingObservations("job-a", shard, base, small),
		normalReserveTimingObservations("job-b", shard, base.Add(time.Minute), medium)...,
	)
	shardResources := []normalReserveShardResource{
		{key: shardOverheadObservationKey{jobID: "job-a", shard: shard}, resources: small},
		{key: shardOverheadObservationKey{jobID: "job-b", shard: shard}, resources: medium},
	}
	reserves, err := deriveNormalResourceTierReservesKeyed(observations, shardResources, "calibration")
	if err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 2 {
		t.Fatalf("cross-job different-tier reserves = %#v, want two buckets", reserves)
	}
	for index, wantTier := range []cicontract.WorkloadResourceTier{
		cicontract.WorkloadResourceTierFast,
		cicontract.WorkloadResourceTierMedium,
	} {
		reserve := reserves[index]
		if reserve.ResourceTier != wantTier || reserve.Measurement == nil {
			t.Fatalf("reserve[%d] = %#v, want measured tier %d", index, reserve, wantTier)
		}
		if reserve.Measurement.SampleCount != 1 || reserve.Measurement.P95MS != 5_000 {
			t.Fatalf("reserve[%d] measurement = %#v, want sample_count=1 p95=5000ms", index, reserve.Measurement)
		}
	}
}

func TestDeriveNormalResourceTierReservesKeyedRejectsEmptyOrNoncanonicalKeys(t *testing.T) {
	base := time.UnixMilli(10_000).UTC()
	resources := RemoteCIShardResources{ClassID: "small", CPU: 2, MemoryGiB: 4}
	cases := []struct {
		name string
		key  shardOverheadObservationKey
	}{
		{name: "empty job", key: shardOverheadObservationKey{shard: "shard-a"}},
		{name: "noncanonical job", key: shardOverheadObservationKey{jobID: " job-a", shard: "shard-a"}},
		{name: "empty shard", key: shardOverheadObservationKey{jobID: "job-a"}},
		{name: "noncanonical shard", key: shardOverheadObservationKey{jobID: "job-a", shard: " shard-a"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			observations := normalReserveTimingObservations(test.key.jobID, test.key.shard, base, resources)
			shardResources := []normalReserveShardResource{{key: test.key, resources: resources}}
			if reserves, err := deriveNormalResourceTierReservesKeyed(observations, shardResources, "calibration"); err == nil {
				t.Fatalf("invalid keyed identity was accepted: %#v", reserves)
			}
		})
	}
}

func TestDeriveNormalResourceTierReservesKeyedRejectsUnknownTimingKey(t *testing.T) {
	base := time.UnixMilli(10_000).UTC()
	resources := RemoteCIShardResources{ClassID: "small", CPU: 2, MemoryGiB: 4}
	observations := normalReserveTimingObservations("job-unknown", "shard-unknown", base, resources)
	shardResources := []normalReserveShardResource{{
		key:       shardOverheadObservationKey{jobID: "job-known", shard: "shard-known"},
		resources: resources,
	}}
	if reserves, err := deriveNormalResourceTierReservesKeyed(observations, shardResources, "calibration"); err == nil {
		t.Fatalf("unknown timing key was accepted: %#v", reserves)
	}
}

func TestDeriveNormalResourceTierReservesWrapperRejectsCrossJobShardIdentity(t *testing.T) {
	base := time.UnixMilli(10_000).UTC()
	shard := "shared-shard"
	resources := RemoteCIShardResources{ClassID: "small", CPU: 2, MemoryGiB: 4}
	observations := append(
		normalReserveTimingObservations("job-a", shard, base, resources),
		normalReserveTimingObservations("job-b", shard, base.Add(time.Minute), resources)...,
	)
	shards := []RemoteCIShardRecord{{ShardIdentity: shard, Resources: resources}}
	if reserves, err := DeriveNormalResourceTierReserves(observations, shards, "calibration"); err == nil {
		t.Fatalf("exported wrapper accepted an unkeyed multi-job shard: %#v", reserves)
	}
}
