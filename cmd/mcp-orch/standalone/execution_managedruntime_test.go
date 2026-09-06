package standalone

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/managedruntime"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

func TestCaptureActiveProcessReceiptFailFast(t *testing.T) {
	e := NewExecution(Config{})
	tests := []struct {
		name     string
		ctx      context.Context
		agent    string
		verifier *managedruntime.ProcessReceiptVerifier
		want     error
	}{
		{"nil context", nil, "a", &managedruntime.ProcessReceiptVerifier{Capture: func(int) (pidregistry.StableProcessIdentity, error) { return pidregistry.StableProcessIdentity{}, nil }}, errors.New("managed runtime receipt capture requires context")},
		{"nil verifier", context.Background(), "a", nil, managedruntime.ErrProductionNoGo},
		{"unknown agent", context.Background(), "a", &managedruntime.ProcessReceiptVerifier{Capture: func(int) (pidregistry.StableProcessIdentity, error) { return pidregistry.StableProcessIdentity{}, nil }}, errors.New("agent not found")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.CaptureActiveProcessReceipt(tc.ctx, tc.agent, managedruntime.PreparedReceipt{}, tc.verifier)
			if err == nil || err.Error() != tc.want.Error() {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
		})
	}
}

func TestCaptureActiveProcessReceiptRequiresActivation(t *testing.T) {
	e := NewExecution(Config{})
	e.sessions["a"] = &cliSession{readyCh: make(chan struct{})}
	v := &managedruntime.ProcessReceiptVerifier{Capture: func(int) (pidregistry.StableProcessIdentity, error) { return pidregistry.StableProcessIdentity{}, nil }}
	_, err := e.CaptureActiveProcessReceipt(context.Background(), "a", managedruntime.PreparedReceipt{}, v)
	if err == nil || err.Error() != "managed runtime receipt capture requires activated session" {
		t.Fatalf("error=%v", err)
	}
}
