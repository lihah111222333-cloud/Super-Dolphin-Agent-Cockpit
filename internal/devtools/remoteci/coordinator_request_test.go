package remoteci

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestShardCandidateTestBinariesSelectsOnlyExactPackages(t *testing.T) {
	first, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/first", "TestOne", 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/first", "TestTwo", 1)
	if err != nil {
		t.Fatal(err)
	}
	other, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/other", "TestOther", 1)
	if err != nil {
		t.Fatal(err)
	}
	candidates := []CandidateTestBinaryArtifactRef{
		{Package: "./internal/first", Mode: "test"},
		{Package: "./internal/other", Mode: "test"},
	}
	shards := []gate.ContainerShard{
		{GateIDs: []gate.GateID{gate.GateID(first.ID), gate.GateID(second.ID)}},
		{GateIDs: []gate.GateID{gate.GateID(other.ID)}},
		{GateIDs: []gate.GateID{gate.GateIDBackendTestWithGuard}},
	}
	want := []string{"./internal/first", "./internal/other", ""}
	for index, shard := range shards {
		got, selectErr := shardCandidateTestBinaries(shard, candidates)
		if selectErr != nil {
			t.Fatalf("shardCandidateTestBinaries(%d) error = %v", index, selectErr)
		}
		if want[index] == "" {
			if len(got) != 0 {
				t.Fatalf("guard-only shard binaries = %#v, want none", got)
			}
			continue
		}
		if len(got) != 1 || got[0].Package != want[index] || got[0].Mode != "test" {
			t.Fatalf("shard %d binaries = %#v, want only %q", index, got, want[index])
		}
	}
}

func TestShardCandidateTestBinariesFailsWhenExactPackageIsMissing(t *testing.T) {
	workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/missing", "TestMissing", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = shardCandidateTestBinaries(
		gate.ContainerShard{GateIDs: []gate.GateID{gate.GateID(workload.ID)}},
		[]CandidateTestBinaryArtifactRef{{Package: "./internal/other", Mode: "test"}},
	)
	if err == nil || !strings.Contains(err.Error(), "has no candidate test binary") {
		t.Fatalf("missing exact package error = %v", err)
	}
}
