package contract

import "testing"

func TestIsActiveAgentState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state string
		want  bool
	}{
		{name: "empty", state: "", want: false},
		{name: "stopped", state: "stopped", want: false},
		{name: "failed", state: "failed", want: false},
		{name: "archived thread status", state: "archived", want: false},
		{name: "archived thread status with whitespace", state: " archived ", want: false},
		{name: "stopped with whitespace", state: "\tstopped\n", want: false},
		{name: "created thread status", state: "created", want: true},
		{name: "running agent state", state: "turn_running", want: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsActiveAgentState(tt.state); got != tt.want {
				t.Fatalf("IsActiveAgentState(%q) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}
