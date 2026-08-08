package gate

import (
	"fmt"
	"strings"
	"testing"
)

// TestPlanExecutionReportRejectsNonCurrentSchemasAtEncodeAndDecode 证明协议两端都不会恢复旧格式。
func TestPlanExecutionReportRejectsNonCurrentSchemasAtEncodeAndDecode(t *testing.T) {
	planDigest := "sha256:" + strings.Repeat("a", 64)
	for schemaVersion := uint32(0); schemaVersion < ExecutorPlanReportSchemaVersion; schemaVersion++ {
		t.Run(fmt.Sprintf("schema-%d", schemaVersion), func(t *testing.T) {
			report := PlanExecutionReport{SchemaVersion: schemaVersion, Profile: ProfileLocalFast, PlanDigest: planDigest}
			if _, err := EncodePlanExecutionReportChunks(report); err == nil || !strings.Contains(err.Error(), "plan report header is invalid") {
				t.Fatalf("EncodePlanExecutionReportChunks() error = %v", err)
			}

			records := []string{
				fmt.Sprintf("%s %06d %s %s %06d %s %d %s", planReportHeaderRecord, schemaVersion, ProfileLocalFast, planDigest, 1, WorkerExecutionStatusSuccess, 0, WorkerExecutionReasonNone),
				fmt.Sprintf("%s %06d", planReportEndRecord, 1),
			}
			chunks, err := framePlanReportRecords(records, planDigest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodePlanExecutionReportChunks(chunks); err == nil || !strings.Contains(err.Error(), "plan report schema is unsupported") {
				t.Fatalf("DecodePlanExecutionReportChunks() error = %v", err)
			}
		})
	}
}
