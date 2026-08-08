package gate

import (
	"reflect"
	"testing"
)

// TestRemoteCIRunRecordFieldRegistry 防止 run 主投影字段在 schema guard 前静默漂移。
func TestRemoteCIRunRecordFieldRegistry(t *testing.T) {
	want := []string{"JobID", "AgentTokenDigest", "Force", "Entrypoint", "Profile", "PlanDigest", "CatalogDigest", "AcceptedGeneration", "ImageCacheSnapshotID", "SourceTreeSHA", "CandidateGateSourceSHA256", "CandidateGateToolchainSHA256", "RunnerImage", "Status", "Authoritative", "StartedAt", "CompletedAt", "CleanupComplete", "ErrorText", "Shards", "Executions", "WorkloadExecutions", "WorkloadResults", "Warnings", "TimingWarnings", "TimingObservations", "CompileTimingObservations", "DurationSamples"}
	typeOf := reflect.TypeFor[RemoteCIRunRecord]()
	got := make([]string, 0, typeOf.NumField())
	for field := range typeOf.Fields() {
		got = append(got, field.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RemoteCIRunRecord fields = %v, want %v", got, want)
	}
}
