package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Validate 校验资源 witness 版本及数值边界。
func (w ContainerResourceWitness) Validate() error {
	if w.SchemaVersion != ContainerResourceWitnessSchemaVersion {
		return fmt.Errorf("unsupported container resource witness schema version %d", w.SchemaVersion)
	}
	if w.NanoCPUs <= 0 || w.NanoCPUs > 1_000_000_000_000 {
		return errors.New("container resource witness nano_cpus is out of bounds")
	}
	if w.MemoryBytes <= 0 || w.MemoryBytes > 1<<50 {
		return errors.New("container resource witness memory_bytes is out of bounds")
	}
	if w.PidsLimit <= 0 || w.PidsLimit > 1<<30 {
		return errors.New("container resource witness pids_limit is out of bounds")
	}
	return nil
}

// Digest 返回经过校验的 typed witness canonical 摘要。
func (w ContainerResourceWitness) Digest() (string, error) {
	if err := w.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(w)
	if err != nil {
		return "", fmt.Errorf("encode container resource witness: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}

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
