package thread

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsBindingConflictError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "unrelated", err: errors.New("timeout"), want: false},
		{name: "provider_thread_bound", err: fmt.Errorf("provider thread %q is already bound to agent %q", "019e1713", "agent_999"), want: true},
		{name: "public_thread_bound", err: fmt.Errorf("public thread id %q is already bound to agent %q", "tid", "agent_1"), want: true},
		{name: "already_bound_to_provider", err: fmt.Errorf("agent %q is already bound to provider %q", "agent_1", "claude"), want: true},
		{name: "already_bound_to_public_thread", err: fmt.Errorf("agent %q is already bound to public thread %q", "agent_1", "tid"), want: true},
		{name: "wrapped", err: fmt.Errorf("persist failed: %w", fmt.Errorf("provider thread %q is already bound to agent %q", "x", "y")), want: true},
		{name: "case_insensitive", err: errors.New("ALREADY BOUND TO AGENT foo"), want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBindingConflictError(tc.err); got != tc.want {
				t.Errorf("isBindingConflictError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
