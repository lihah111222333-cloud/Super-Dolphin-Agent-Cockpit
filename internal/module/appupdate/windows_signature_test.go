package appupdate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestAuthenticodeInvalidResultsClassifyAsSignatureFailure(t *testing.T) {
	valid := authenticodeSignature{Status: "Valid", Subject: "CN=Expected Publisher", Thumbprint: strings.Repeat("a", 40)}
	cases := []authenticodeSignature{
		{Status: "NotSigned", Subject: valid.Subject, Thumbprint: valid.Thumbprint},
		{Status: valid.Status, Subject: "CN=Unexpected", Thumbprint: valid.Thumbprint},
		{Status: valid.Status, Subject: valid.Subject, Thumbprint: strings.Repeat("b", 40)},
	}
	for _, result := range cases {
		if err := validateAuthenticodeSignature(result, "Expected Publisher", valid.Thumbprint); !errors.Is(err, contract.ErrUpdateSignatureInvalid) {
			t.Fatalf("validateAuthenticodeSignature(%+v) error = %v, want ErrUpdateSignatureInvalid", result, err)
		}
	}
}

func TestWindowsSignatureInfrastructureFailureStaysUnknownAndSafe(t *testing.T) {
	err := windowsSignatureCommandFailure(context.DeadlineExceeded)
	if errors.Is(err, contract.ErrUpdateSignatureInvalid) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("windowsSignatureCommandFailure() error = %v, want original infrastructure category", err)
	}
	if strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Fatalf("windowsSignatureCommandFailure() leaked PowerShell output: %v", err)
	}
}
