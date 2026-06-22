package policy

import "testing"

func TestDecidePolicyBoundaries(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want Decision
	}{
		{
			name: "workflow write is allowed with explicit permission",
			req: Request{
				Operation:  OperationWorkflowWrite,
				RiskClass:  RiskHigh,
				Permission: PermissionWorkflowWrite,
			},
			want: DecisionAllow,
		},
		{
			name: "command execution fails without high risk classification",
			req: Request{
				Operation:  OperationCommandExecution,
				RiskClass:  RiskMedium,
				Permission: PermissionCommandExecute,
			},
			want: DecisionFailFast,
		},
		{
			name: "command execution is allowed only with high risk policy",
			req: Request{
				Operation:  OperationCommandExecution,
				RiskClass:  RiskHigh,
				Permission: PermissionCommandExecute,
			},
			want: DecisionAllow,
		},
		{
			name: "shared file write requires shared file permission",
			req: Request{
				Operation:  OperationSharedFileWrite,
				RiskClass:  RiskHigh,
				Permission: PermissionWorkflowWrite,
			},
			want: DecisionDeny,
		},
		{
			name: "provider identity override requires explicit identity permission",
			req: Request{
				Operation:  OperationProviderIdentityOverride,
				RiskClass:  RiskHigh,
				Permission: PermissionWorkflowWrite,
			},
			want: DecisionDeny,
		},
		{
			name: "require approval is rejected until approval MVP exists",
			req: Request{
				Operation:        OperationWorkflowWrite,
				RiskClass:        RiskHigh,
				Permission:       PermissionWorkflowWrite,
				ApprovalRequired: true,
			},
			want: DecisionFailFast,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(tt.req)
			if got.Decision != tt.want {
				t.Fatalf("decision = %q, want %q; reason=%s", got.Decision, tt.want, got.Reason)
			}
			if got.Decision == "require_approval" {
				t.Fatal("policy must not return require_approval before approval MVP exists")
			}
		})
	}
}
