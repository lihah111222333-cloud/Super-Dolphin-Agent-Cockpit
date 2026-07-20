package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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
