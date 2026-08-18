//go:build darwin || linux

package gate

import "testing"

func TestCompiledSelectorBatchReportsMeasuredCPUAndRSS(t *testing.T) {
	group, results := runCompileGroupBatchFixture(t)
	for _, id := range group.WorkloadIDs {
		resource := results[id].ExecutionProfile.BatchResource
		if resource == nil || resource.BatchID != "batch-000" || resource.CPUTimeNS <= 0 ||
			resource.AverageCPUMilliCores <= 0 || resource.PeakRSSBytes <= 0 {
			t.Fatalf("batch result lacks measured CPU/RSS evidence for %q: %#v", id, resource)
		}
	}
}
