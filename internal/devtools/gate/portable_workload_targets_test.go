package gate

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestPortableGoWorkloadTargetsAreSealedAndExecutable 锁定 portable Go target 的封闭执行映射。
func TestPortableGoWorkloadTargetsAreSealedAndExecutable(t *testing.T) {
	workload := mustNewPortableGoTestWorkload(t, PortableGoTarget{
		ModuleRoot:      "tools/checker",
		Package:         "./internal/check",
		Name:            "TestContract",
		ExecutionPolicy: PortableGoExecutionPolicyRace,
	})
	if err := ValidateExecutableWorkloadID(GateID(workload.ID)); err != nil {
		t.Fatalf("ValidateExecutableWorkloadID() error = %v", err)
	}
	assertPortableGoTestIdentity(t, workload)
	assertPortableGoTestProgram(t, workload)
	assertPortableGoDigestRoundTrip(t, workload)
	assertPortableGoCallerCommandControlRejected(t)
}

func mustNewPortableGoTestWorkload(t *testing.T, target PortableGoTarget) Workload {
	t.Helper()
	workload, err := NewPortableGoTestWorkload(target, 1_250)
	if err != nil {
		t.Fatalf("NewPortableGoTestWorkload() error = %v", err)
	}
	return workload
}

func assertPortableGoTestIdentity(t *testing.T, workload Workload) {
	t.Helper()
	parent, kind, payload, targeted, err := ParseWorkloadID(workload.ID)
	if err != nil {
		t.Fatalf("ParseWorkloadID() error = %v", err)
	}
	if parent != GateIDBackendTestGuardWithRace || kind != WorkloadTargetPortableGoTest || !targeted {
		t.Fatalf("parsed workload = (%q, %q, targeted=%t)", parent, kind, targeted)
	}
	target, err := ParsePortableGoTestTarget(payload)
	if err != nil {
		t.Fatalf("ParsePortableGoTestTarget() error = %v", err)
	}
	if target.ModuleRoot != "tools/checker" || target.Package != "./internal/check" || target.Name != "TestContract" ||
		target.ExecutionPolicy != PortableGoExecutionPolicyRace {
		t.Fatalf("portable target changed: %#v", target)
	}
}

func assertPortableGoTestProgram(t *testing.T, workload Workload) {
	t.Helper()
	_, program, err := executorProgramForWorkload(GateID(workload.ID))
	if err != nil {
		t.Fatalf("executorProgramForWorkload() error = %v", err)
	}
	if len(program.Steps) != 1 || program.Steps[0].Directory != "tools/checker" {
		t.Fatalf("portable executor directory = %#v", program.Steps)
	}
	if got := strings.Join(program.Steps[0].Argv, " "); got != "go test ./internal/check -run ^TestContract$ -count=1" {
		t.Fatalf("portable executor argv = %q", got)
	}
}

func assertPortableGoDigestRoundTrip(t *testing.T, workload Workload) {
	t.Helper()
	if digest, err := WorkloadExecutionDigest(workload.ID); err != nil || digest != workload.CommandDigest {
		t.Fatalf("execution digest round trip = (%q, %v), workload=%q", digest, err, workload.CommandDigest)
	}
}

// assertPortableGoCallerCommandControlRejected 拒绝 caller 自报 argv、shell、parent gate 或 policy 漂移。
func assertPortableGoCallerCommandControlRejected(t *testing.T) {
	t.Helper()
	target := PortableGoTarget{ModuleRoot: ".", Package: "./internal/check", ExecutionPolicy: PortableGoExecutionPolicyNormal}
	workload := mustNewPortableGoPackageWorkload(t, target)
	assertPortableGoRejectsCallerArgv(t, workload)
	assertPortableGoRejectsWrongParent(t, target)
	assertPortableGoRejectsPolicyDrift(t, target)
}

func mustNewPortableGoPackageWorkload(t *testing.T, target PortableGoTarget) Workload {
	t.Helper()
	workload, err := NewPortableGoPackageWorkload(target, 1_000)
	if err != nil {
		t.Fatalf("NewPortableGoPackageWorkload() error = %v", err)
	}
	return workload
}

func assertPortableGoRejectsCallerArgv(t *testing.T, workload Workload) {
	t.Helper()
	_, kind, payload, _, err := ParseWorkloadID(workload.ID)
	if err != nil {
		t.Fatalf("ParseWorkloadID() error = %v", err)
	}
	forgedPayload := strings.TrimSuffix(payload, "}") + `,"argv":["sh","-c","exit 0"]}`
	forgedID := string(GateIDBackendTestWithGuard) + workloadTargetDelimiter + string(kind) + workloadTargetDelimiter +
		base64.RawURLEncoding.EncodeToString([]byte(forgedPayload))
	if _, _, _, _, err := ParseWorkloadID(forgedID); err == nil {
		t.Fatal("ParseWorkloadID() accepted caller-controlled argv")
	}
}

func assertPortableGoRejectsWrongParent(t *testing.T, target PortableGoTarget) {
	t.Helper()
	rawTarget, err := encodePortableGoPackageTarget(target)
	if err != nil {
		t.Fatalf("encodePortableGoPackageTarget() error = %v", err)
	}
	wrongParentID := string(GateIDFrontendTest) + workloadTargetDelimiter + string(WorkloadTargetPortableGoPackage) + workloadTargetDelimiter +
		base64.RawURLEncoding.EncodeToString([]byte(rawTarget))
	if _, _, _, _, err := ParseWorkloadID(wrongParentID); err == nil {
		t.Fatal("ParseWorkloadID() accepted an arbitrary parent gate")
	}
}

func assertPortableGoRejectsPolicyDrift(t *testing.T, target PortableGoTarget) {
	t.Helper()
	racyTarget := target
	racyTarget.ExecutionPolicy = PortableGoExecutionPolicyRace
	rawTarget, err := encodePortableGoPackageTarget(racyTarget)
	if err != nil {
		t.Fatalf("encodePortableGoPackageTarget(race) error = %v", err)
	}
	policyDriftID := string(GateIDBackendTestWithGuard) + workloadTargetDelimiter + string(WorkloadTargetPortableGoPackage) + workloadTargetDelimiter +
		base64.RawURLEncoding.EncodeToString([]byte(rawTarget))
	if _, _, _, _, err := ParseWorkloadID(policyDriftID); err == nil {
		t.Fatal("ParseWorkloadID() accepted parent/policy drift")
	}
}
