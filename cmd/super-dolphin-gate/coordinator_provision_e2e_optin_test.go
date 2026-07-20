package main

import "testing"

func TestProductionProvisionDockerE2ERequiresExplicitOptIn(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "default or container environment", value: "", want: false},
		{name: "zero", value: "0", want: false},
		{name: "invalid nonempty value", value: "true", want: false},
		{name: "explicit opt-in", value: "1", want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(productionDockerE2EOptInEnv, test.value)
			if got := productionProvisionDockerE2EEnabled(); got != test.want {
				t.Fatalf("production Docker E2E enabled = %t, want %t", got, test.want)
			}
		})
	}
}
