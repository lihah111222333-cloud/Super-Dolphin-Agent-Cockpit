package gate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRemoteCIUncreatedShardCannotBePassedOrExecuted(t *testing.T) {
	shard := RemoteCIShardRecord{
		ShardIdentity:   "sha256:" + strings.Repeat("a", 64),
		ContainerStatus: "Unknown",
		Workloads:       []GateID{"guard:uncreated"},
		MaterializationTiming: ShardMaterializationTiming{
			Measurement: MaterializationMeasurementNotMeasured,
		},
	}

	for _, status := range []ResultStatus{ResultStatusPassed, ResultStatusPassedStalePolicy} {
		t.Run(string(status), func(t *testing.T) {
			err := validateRemoteCIRunShards(RemoteCIRunRecord{
				Status: status,
				Shards: []RemoteCIShardRecord{shard},
			})
			if err == nil || !strings.Contains(err.Error(), "cannot contain uncreated shard") {
				t.Fatalf("validateRemoteCIRunShards() error = %v, want uncreated shard rejection", err)
			}
		})
	}

	err := validateRemoteCIRunShards(RemoteCIRunRecord{
		Status:             ResultStatusFailed,
		Shards:             []RemoteCIShardRecord{shard},
		WorkloadExecutions: []PlanGateExecution{{GateID: "guard:uncreated"}},
	})
	if err == nil || !strings.Contains(err.Error(), "bound to uncreated shard") {
		t.Fatalf("validateRemoteCIRunShards() error = %v, want execution rejection", err)
	}
}

func TestRemoteCIPassedRunRequiresMeasuredCreatedShardTiming(t *testing.T) {
	shard := RemoteCIShardRecord{
		ShardIdentity:   "sha256:" + strings.Repeat("c", 64),
		ContainerGroup:  "eci-created",
		ContainerStatus: "Failed",
		MaterializationTiming: ShardMaterializationTiming{
			Measurement: MaterializationMeasurementUnavailable,
		},
	}
	for _, status := range []ResultStatus{ResultStatusPassed, ResultStatusPassedStalePolicy} {
		t.Run(string(status), func(t *testing.T) {
			err := validateRemoteCIRunShards(RemoteCIRunRecord{Status: status, Shards: []RemoteCIShardRecord{shard}})
			if err == nil || !strings.Contains(err.Error(), "remote CI shard materialization timing is invalid") {
				t.Fatalf("validateRemoteCIRunShards() error = %v, want measured timing rejection", err)
			}
		})
	}
}

func TestRemoteCIPassedRunRequiresCreatedShardResources(t *testing.T) {
	shard := RemoteCIShardRecord{ShardIdentity: "sha256:" + strings.Repeat("d", 64), ContainerGroup: "eci-created", ContainerStatus: "Failed"}
	if err := validateRemoteCIShardResources(ResultStatusPassed, false, shard); err == nil || !strings.Contains(err.Error(), "remote CI shard resources are invalid") {
		t.Fatalf("validateRemoteCIShardResources() error = %v, want PASS resource rejection", err)
	}
	if err := validateRemoteCIShardResources(ResultStatusFailed, false, shard); err != nil {
		t.Fatalf("validateRemoteCIShardResources() failed provisional error = %v, want truthful missing-resource acceptance", err)
	}
}

func TestDecodeStoredRemoteCIShardEvidenceAllowsFailedCreatedMissingResources(t *testing.T) {
	timingJSON, err := json.Marshal(ShardMaterializationTiming{Measurement: MaterializationMeasurementUnavailable})
	if err != nil {
		t.Fatal(err)
	}
	shard := RemoteCIShardRecord{ShardIdentity: "sha256:" + strings.Repeat("e", 64), ContainerGroup: "eci-created", ContainerStatus: "Failed"}
	if err := decodeStoredRemoteCIShardEvidence(&shard, string(timingJSON), "", ResultStatusFailed, false); err != nil {
		t.Fatalf("decodeStoredRemoteCIShardEvidence() failed missing resources = %v, want truthful acceptance", err)
	}
	if shard.Resources != (RemoteCIShardResources{}) {
		t.Fatalf("decodeStoredRemoteCIShardEvidence() resources = %#v, want zero unknown evidence", shard.Resources)
	}
}

func TestDecodeStoredRemoteCIShardEvidenceDoesNotFabricateZeroResources(t *testing.T) {
	timingJSON, err := json.Marshal(ShardMaterializationTiming{Measurement: MaterializationMeasurementNotMeasured})
	if err != nil {
		t.Fatal(err)
	}
	placeholder := RemoteCIShardRecord{ShardIdentity: "sha256:" + strings.Repeat("b", 64), ContainerStatus: "Unknown"}
	if err := decodeStoredRemoteCIShardEvidence(&placeholder, string(timingJSON), "", ResultStatusFailed, false); err != nil {
		t.Fatalf("decodeStoredRemoteCIShardEvidence() missing resources = %v, want placeholder acceptance", err)
	}
	placeholder = RemoteCIShardRecord{ShardIdentity: "sha256:" + strings.Repeat("b", 64), ContainerStatus: "Unknown"}
	if err := decodeStoredRemoteCIShardEvidence(&placeholder, string(timingJSON), "{}", ResultStatusFailed, false); err == nil || !strings.Contains(err.Error(), "decode stored remote CI shard resources") {
		t.Fatalf("decodeStoredRemoteCIShardEvidence() zero resource object error = %v, want strict rejection", err)
	}
}
