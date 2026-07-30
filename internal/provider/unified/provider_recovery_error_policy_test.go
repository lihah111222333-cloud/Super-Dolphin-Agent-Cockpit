package unified

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"
)

func TestAutoResumeRecoveryErrorPolicyMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		input            error
		wantSessionGone  bool
		wantRecoveryKind providerrecovery.ErrorKind
	}{
		{
			name:            "missing maps to session not found",
			input:           &providerrecovery.Error{Kind: providerrecovery.ErrorKindNotFound, Provider: "claude", Cause: providerrecovery.ErrNotFound},
			wantSessionGone: true,
		},
		{name: "permission propagates", input: &providerrecovery.Error{Kind: providerrecovery.ErrorKindPermission, Provider: "claude", Cause: errors.New("permission")}, wantRecoveryKind: providerrecovery.ErrorKindPermission},
		{name: "io propagates", input: &providerrecovery.Error{Kind: providerrecovery.ErrorKindIO, Provider: "claude", Cause: errors.New("io")}, wantRecoveryKind: providerrecovery.ErrorKindIO},
		{name: "parse propagates", input: &providerrecovery.Error{Kind: providerrecovery.ErrorKindParse, Provider: "claude", Cause: errors.New("parse")}, wantRecoveryKind: providerrecovery.ErrorKindParse},
		{name: "unknown provider propagates", input: &providerrecovery.Error{Kind: providerrecovery.ErrorKindUnknownProvider, Provider: "future", Cause: errors.New("unknown")}, wantRecoveryKind: providerrecovery.ErrorKindUnknownProvider},
		{name: "invalid identity propagates", input: &providerrecovery.Error{Kind: providerrecovery.ErrorKindInvalidIdentity, Provider: "codex", Cause: errors.New("identity")}, wantRecoveryKind: providerrecovery.ErrorKindInvalidIdentity},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := autoResumeRecoveryError(fmt.Errorf("recover provider identity: %w", tc.input))
			if tc.wantSessionGone {
				if !errors.Is(got, contract.ErrSessionNotFound) {
					t.Fatalf("autoResumeRecoveryError() = %v, want ErrSessionNotFound", got)
				}
				return
			}
			if errors.Is(got, contract.ErrSessionNotFound) {
				t.Fatalf("autoResumeRecoveryError() = %v, must not map to ErrSessionNotFound", got)
			}
			if !providerrecovery.IsKind(got, tc.wantRecoveryKind) {
				t.Fatalf("autoResumeRecoveryError() = %v, want recovery kind %q", got, tc.wantRecoveryKind)
			}
		})
	}
}
