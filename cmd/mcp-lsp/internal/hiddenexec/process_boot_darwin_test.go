//go:build darwin

package hiddenexec

import (
	"os"
	"testing"
)

func TestProcessStartIdentityPredatesCurrentBoot(t *testing.T) {
	preBoot, err := ProcessStartIdentityPredatesCurrentBoot("1.0")
	if err != nil {
		t.Fatalf("pre-boot identity proof: %v", err)
	}
	if !preBoot {
		t.Fatal("Unix epoch process identity was not proven older than the current Darwin boot")
	}

	current, err := ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatalf("current process identity: %v", err)
	}
	preBoot, err = ProcessStartIdentityPredatesCurrentBoot(current)
	if err != nil {
		t.Fatalf("current-boot identity proof: %v", err)
	}
	if preBoot {
		t.Fatalf("current process identity %q was misclassified as pre-boot", current)
	}
}

func TestProcessStartIdentityPredatesCurrentBootRejectsMalformedIdentity(t *testing.T) {
	if _, err := ProcessStartIdentityPredatesCurrentBoot("not-a-darwin-start-token"); err == nil {
		t.Fatal("malformed Darwin process identity unexpectedly produced boot proof")
	}
}
