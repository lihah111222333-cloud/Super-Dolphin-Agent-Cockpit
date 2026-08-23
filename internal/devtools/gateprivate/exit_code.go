package gateprivate

import (
	"errors"
	"fmt"
)

// ExitCode is the stable process contract for the trusted local Gate CLI.
// It is deliberately owned outside the retired remote planner package.
type ExitCode int

const (
	ExitOK                 ExitCode = 0
	ExitProtocol           ExitCode = 2
	ExitGateViolation      ExitCode = 10
	ExitEvidenceIncomplete ExitCode = 11
	ExitSourceMismatch     ExitCode = 12
	ExitInfrastructure     ExitCode = 13
	ExitRegistryInvariant  ExitCode = 14
	ExitCancelled          ExitCode = 15
	ExitTimeout            ExitCode = 16
)

// Validate verifies that the process code belongs to the versioned CLI protocol.
func (code ExitCode) Validate() error {
	switch code {
	case ExitOK, ExitProtocol, ExitGateViolation, ExitEvidenceIncomplete, ExitSourceMismatch, ExitInfrastructure, ExitRegistryInvariant, ExitCancelled, ExitTimeout:
		return nil
	default:
		return fmt.Errorf("unsupported gate exit code %d", code)
	}
}

type exitError struct {
	code ExitCode
	err  error
}

func (err *exitError) Error() string { return err.err.Error() }

func (err *exitError) Unwrap() error { return err.err }

// WithExitCode attaches a stable process exit code to a command failure.
func WithExitCode(code ExitCode, cause error) error {
	if cause == nil {
		cause = errors.New("gate command failed")
	}
	if validateErr := code.Validate(); validateErr != nil || code == ExitOK {
		return &exitError{code: ExitRegistryInvariant, err: fmt.Errorf("invalid failure exit code %d: %w", code, cause)}
	}
	return &exitError{code: code, err: cause}
}

// ExitCodeOf returns the attached code, or infrastructure failure for an
// unclassified error.
func ExitCodeOf(err error) ExitCode {
	if err == nil {
		return ExitOK
	}
	if coded, ok := errors.AsType[*exitError](err); ok {
		return coded.code
	}
	return ExitInfrastructure
}
