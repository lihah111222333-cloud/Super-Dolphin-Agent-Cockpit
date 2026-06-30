package toolpolicy

import "testing"

func TestPlanSafeImpliesReadOnly(t *testing.T) {
	capability := CapabilityPlanSafe
	if !capability.ReadOnly() {
		t.Fatalf("CapabilityPlanSafe.ReadOnly() = false, want true")
	}

	got := Decide(Assessment{
		Stage:        StagePlan,
		Trust:        TrustInternal,
		Capabilities: capability,
	})
	assertAllowed(t, got)
}

func TestReadOnlyDoesNotImplyPlanSafe(t *testing.T) {
	capability := CapabilityReadOnly
	if capability.PlanSafe() {
		t.Fatalf("CapabilityReadOnly.PlanSafe() = true, want false")
	}

	got := Decide(Assessment{
		Stage:        StagePlan,
		Trust:        TrustInternal,
		Capabilities: capability,
	})
	assertDenied(t, got, CodeCapabilityDenied, "toolpolicy: plan stage requires plan-safe read-only capability")

	readOnly := Decide(Assessment{
		Stage:        StageReadOnly,
		Trust:        TrustInternal,
		Capabilities: capability,
	})
	assertAllowed(t, readOnly)
}

func TestTrustFailsClosedForUnknownExternalAndExternalReadOnlyHint(t *testing.T) {
	tests := []struct {
		name string
		in   Assessment
		code DecisionCode
		want string
	}{
		{
			name: "unknown trust",
			in: Assessment{
				Stage:        StageReadOnly,
				Trust:        TrustUnknown,
				Capabilities: CapabilityReadOnly,
			},
			code: CodeUntrustedSource,
			want: "toolpolicy: trust source is unknown or external",
		},
		{
			name: "external trust",
			in: Assessment{
				Stage:        StageReadOnly,
				Trust:        TrustExternal,
				Capabilities: CapabilityReadOnly,
			},
			code: CodeUntrustedSource,
			want: "toolpolicy: trust source is unknown or external",
		},
		{
			name: "external read-only hint",
			in: Assessment{
				Stage:             StageReadOnly,
				Trust:             TrustInternal,
				Capabilities:      CapabilityReadOnly,
				ReadOnlyHint:      true,
				ReadOnlyHintTrust: TrustExternal,
			},
			code: CodeExternalHint,
			want: "toolpolicy: external read-only hint is not trusted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(tt.in)
			assertDenied(t, got, tt.code, tt.want)
		})
	}
}

func TestShellClassificationAllowsReadOnlyCommandTable(t *testing.T) {
	tests := []string{
		"pwd",
		"git status --short",
		"git branch",
		"git branch --show-current",
		"git branch --list 'feature/*'",
		"git branch -a -v",
		"git branch --merged",
		"git branch --contains HEAD",
		"git branch --points-at HEAD",
		"git branch --format '%(refname)'",
		"git diff -- README.md",
		"rg -n toolpolicy internal/platform",
		"sed -n '1,20p' README.md",
	}

	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			got := ClassifyShell(command)
			assertAllowed(t, got)
		})
	}
}

func TestShellClassificationRejectsBackgroundProcessControlDangerousArgsAndSyntax(t *testing.T) {
	tests := []struct {
		name    string
		command string
		code    DecisionCode
	}{
		{name: "background operator", command: "sleep 10 &", code: CodeShellSyntaxDenied},
		{name: "process control command", command: "wait 123", code: CodeShellCommandDenied},
		{name: "dangerous git argument", command: "git -c core.pager=cat status", code: CodeShellArgumentDenied},
		{name: "git branch creates branch", command: "git branch feature/new", code: CodeShellCommandDenied},
		{name: "git branch deletes branch", command: "git branch -d feature/old", code: CodeShellCommandDenied},
		{name: "git branch force deletes branch", command: "git branch -D feature/old", code: CodeShellCommandDenied},
		{name: "git branch renames branch", command: "git branch -m old new", code: CodeShellCommandDenied},
		{name: "git branch sets upstream", command: "git branch --set-upstream-to origin/main main", code: CodeShellCommandDenied},
		{name: "git diff writes output file", command: "git diff --output patch.diff", code: CodeShellArgumentDenied},
		{name: "complex shell syntax", command: "git status && rm -rf .", code: CodeShellSyntaxDenied},
		{name: "shell wrapper", command: "bash -lc 'git status'", code: CodeShellCommandDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyShell(tt.command)
			assertDenied(t, got, tt.code, "")
		})
	}
}

func assertAllowed(t *testing.T, got Decision) {
	t.Helper()
	if !got.Allow {
		t.Fatalf("decision allowed = false, code=%q reason=%q", got.Code, got.Reason)
	}
	if got.Code != CodeAllowed {
		t.Fatalf("decision code = %q, want %q", got.Code, CodeAllowed)
	}
}

func assertDenied(t *testing.T, got Decision, code DecisionCode, reason string) {
	t.Helper()
	if got.Allow {
		t.Fatalf("decision allowed = true, want false")
	}
	if got.Code != code {
		t.Fatalf("decision code = %q, want %q (reason=%q)", got.Code, code, got.Reason)
	}
	if reason != "" && got.Reason != reason {
		t.Fatalf("decision reason = %q, want %q", got.Reason, reason)
	}
	if got.Reason == "" {
		t.Fatalf("decision reason is empty")
	}
}
