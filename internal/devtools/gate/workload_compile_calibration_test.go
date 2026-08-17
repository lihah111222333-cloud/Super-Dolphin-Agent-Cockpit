package gate

import "testing"

func TestOrdinaryCompilePlanningUnitCalibrationUsesFixedResourceClass(t *testing.T) {
	context := testCalibrationPlanningContext()
	unit, err := ordinaryCompilePlanningUnit(PlannedWorkload{
		Workload:            Workload{ID: "frontend:lint"},
		EstimatedDurationMS: 1_000,
		ResourceCPU:         context.CalibrationResourceCPU,
		ResourceMemoryGiB:   context.CalibrationResourceMemoryGiB,
	}, context)
	if err != nil {
		t.Fatal(err)
	}
	if unit.resourceClassID != context.CalibrationResourceClassID {
		t.Fatalf("resource class = %q, want fixed calibration class %q", unit.resourceClassID, context.CalibrationResourceClassID)
	}
}
