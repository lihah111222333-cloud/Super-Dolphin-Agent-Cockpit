package acpnode

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

const (
	configProvenanceSchema       = "super-dolphin.acpnode-config-provenance"
	configProvenanceVersion      = 1
	configProvenanceSourcePath   = "/Users/l4place/Documents/Super-Dolphin/.codex/config.toml"
	configProvenanceConfigSHA256 = "6fe66a4533873c522e4f35af58632c2a29be8534742d149d1e6d062e71a64fb5"
)

//go:embed config_provenance.json
var embeddedConfigProvenance string

type configProvenanceReceipt struct {
	Schema                string `json:"schema"`
	Version               int    `json:"version"`
	SourcePath            string `json:"source_path"`
	CanonicalConfigBase64 string `json:"canonical_config_bytes_base64"`
	CanonicalConfigSHA256 string `json:"canonical_config_sha256"`
}

// validateConfigProvenance 在任何子进程启动前验证内置配置收据，不读取外部可变配置。
func validateConfigProvenance() error {
	return validateConfigProvenanceBytes([]byte(embeddedConfigProvenance))
}

// validateConfigProvenanceBytes 校验收据结构、来源、版本和内置字节摘要。
func validateConfigProvenanceBytes(payload []byte) error {
	receipt, err := decodeConfigProvenanceReceipt(payload)
	if err != nil {
		return err
	}
	if err := validateConfigProvenanceMetadata(receipt); err != nil {
		return err
	}
	configBytes, err := base64.StdEncoding.DecodeString(receipt.CanonicalConfigBase64)
	if err != nil {
		return fmt.Errorf("acp: decode canonical config bytes: %w", err)
	}
	digest := sha256.Sum256(configBytes)
	if hex.EncodeToString(digest[:]) != configProvenanceConfigSHA256 {
		return fmt.Errorf("acp: canonical config bytes do not match receipt digest")
	}
	return nil
}

// decodeConfigProvenanceReceipt 严格解码单个 JSON 收据并拒绝尾随值。
func decodeConfigProvenanceReceipt(payload []byte) (configProvenanceReceipt, error) {
	if len(payload) == 0 {
		return configProvenanceReceipt{}, fmt.Errorf("acp: config provenance receipt is missing")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var receipt configProvenanceReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return configProvenanceReceipt{}, fmt.Errorf("acp: decode config provenance receipt: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return configProvenanceReceipt{}, fmt.Errorf("acp: config provenance receipt has trailing data")
		}
		return configProvenanceReceipt{}, fmt.Errorf("acp: decode trailing config provenance data: %w", err)
	}
	return receipt, nil
}

// validateConfigProvenanceMetadata 校验收据的固定来源、schema、版本和摘要声明。
func validateConfigProvenanceMetadata(receipt configProvenanceReceipt) error {
	if receipt.Schema != configProvenanceSchema || receipt.Version != configProvenanceVersion {
		return fmt.Errorf("acp: config provenance schema/version mismatch")
	}
	if receipt.SourcePath != configProvenanceSourcePath {
		return fmt.Errorf("acp: config provenance source path mismatch")
	}
	if receipt.CanonicalConfigSHA256 != configProvenanceConfigSHA256 {
		return fmt.Errorf("acp: config provenance digest mismatch")
	}
	return nil
}
