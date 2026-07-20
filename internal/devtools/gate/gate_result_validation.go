package gate

import (
	"errors"
	"fmt"
	"strings"
)

// Validate 校验单个 gate 的进程结果与摘要闭包。
func (r GateResult) Validate() error {
	if strings.TrimSpace(r.GateID) == "" {
		return errors.New("gate_id is required")
	}
	if r.StartedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) {
		return errors.New("gate result timestamps are invalid")
	}
	if err := validateGateExit(r.Status, r.ExitCode); err != nil {
		return err
	}
	if err := validateDigest("argv_digest", r.ArgvDigest); err != nil {
		return err
	}
	return validateDigest("log_digest", r.LogDigest)
}

// validateGateExit 约束逐 gate 公开状态与直接观测 exit code 一致。
func validateGateExit(status GateStatus, exitCode int) error {
	switch status {
	case GateStatusPassed:
		if exitCode != 0 {
			return errors.New("passed gate must have zero exit_code")
		}
	case GateStatusFailed:
		if exitCode == 0 {
			return errors.New("failed gate must have non-zero exit_code")
		}
	case GateStatusCancelled:
		if exitCode != -1 {
			return errors.New("cancelled gate must have exit_code -1")
		}
	case GateStatusTimeout:
		if exitCode != -1 {
			return errors.New("timed out gate must have exit_code -1")
		}
	default:
		return fmt.Errorf("unsupported gate status %q", status)
	}
	return nil
}
