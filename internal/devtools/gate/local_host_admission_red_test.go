package gate

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLocalHostAdmissionCPUWindowAt70Accepted(t *testing.T) {
	admission, err := BuildLocalHostAdmissionFromSamples(localHostAdmissionSamples(70), 8, 16)
	if err != nil {
		t.Fatal(err)
	}
	if !admission.Allowed || admission.CPUBusyAveragePercent != 70 || !localHostCPUHardAdmitted(admission) {
		t.Fatalf("70%% admission = %#v, want hard-admitted", admission)
	}
}

func TestLocalHostAdmissionCPUWindowOver70Rejected(t *testing.T) {
	admission, err := BuildLocalHostAdmissionFromSamples(localHostAdmissionSamples(71), 8, 16)
	if err != nil {
		t.Fatal(err)
	}
	if admission.Allowed || localHostCPUHardAdmitted(admission) {
		t.Fatalf("71%% admission = %#v, want rejected", admission)
	}
}

func TestLocalHostAdmissionRejectsShortWindowAndInsufficientSamples(t *testing.T) {
	short := localHostAdmissionSamples(20)
	short = short[:6]
	if _, err := BuildLocalHostAdmissionFromSamples(short, 8, 16); err == nil || !strings.Contains(err.Error(), "insufficient samples") {
		t.Fatalf("insufficient sample error = %v", err)
	}
	window := localHostAdmissionSamples(20)
	for index := range window {
		window[index].At = window[0].At.Add(time.Duration(index) * 4 * time.Second)
	}
	if _, err := BuildLocalHostAdmissionFromSamples(window, 8, 16); err == nil || !strings.Contains(err.Error(), "shorter than 30 seconds") {
		t.Fatalf("short window error = %v", err)
	}
}

func TestLocalHostAdmissionWeightsIntermediateHighLoad(t *testing.T) {
	admission, err := BuildLocalHostAdmissionFromSamples(localHostAdmissionSamples(10, 90, 90, 90, 90, 90, 10), 8, 16)
	if err != nil {
		t.Fatal(err)
	}
	if admission.CPUBusyAveragePercent <= 70 || localHostCPUHardAdmitted(admission) {
		t.Fatalf("intermediate high-load admission = %#v, want rejected", admission)
	}
}

func TestLocalSchedulerHitSkipsHostSampler(t *testing.T) {
	store, _, identity := localPassAuthorityFixture(t)
	counts := 0
	input := localSchedulerTestInput(localSchedulerTestItem(t, identity, localPassTestEnvironment(false), false), LocalWorkloadTargetAuto, &localSchedulerCounters{})
	input.SampleHost = func(context.Context) (LocalHostAdmission, error) {
		counts++
		return LocalHostAdmission{}, nil
	}
	prepared, err := PrepareLocalWorkloadSchedule(context.Background(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	if counts != 0 || len(prepared.Hits) != 1 {
		t.Fatalf("hit sampler calls=%d hits=%d, want sampler=0/hit=1", counts, len(prepared.Hits))
	}
}

func TestLocalSchedulerMissUsesInjectedHostSampler(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	identity := localSchedulerIdentity(t, GateIDBackendTestWithGuard, localPassTestEnvironment(false), "sampler-miss")
	counts := 0
	input := localSchedulerTestInput(localSchedulerTestItem(t, identity, localPassTestEnvironment(false), false), LocalWorkloadTargetLocal, &localSchedulerCounters{})
	input.SampleHost = func(context.Context) (LocalHostAdmission, error) {
		counts++
		return BuildLocalHostAdmissionFromSamples(localHostAdmissionSamples(20), 8, 16)
	}
	prepared, err := PrepareLocalWorkloadSchedule(context.Background(), store, input)
	if err != nil {
		t.Fatal(err)
	}
	if counts != 1 || len(prepared.Misses) != 1 || prepared.Admission.CPUSampleCount != 7 {
		t.Fatalf("miss sampler calls=%d misses=%d admission=%#v", counts, len(prepared.Misses), prepared.Admission)
	}
}

func localHostAdmissionSamples(values ...float64) []LocalHostCPUSample {
	if len(values) == 1 {
		values = []float64{values[0], values[0], values[0], values[0], values[0], values[0], values[0]}
	}
	start := time.Date(2026, time.August, 11, 13, 0, 0, 0, time.UTC)
	samples := make([]LocalHostCPUSample, len(values))
	for index, value := range values {
		samples[index] = LocalHostCPUSample{At: start.Add(time.Duration(index) * 5 * time.Second), BusyPercent: value}
	}
	return samples
}
