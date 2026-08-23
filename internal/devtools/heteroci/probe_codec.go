package heteroci

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
)

const maxProbeResponseBytes = 1 << 20

// DecodeProbeResponse 从输入中严格解码一个探针响应的 wire 形状。
func DecodeProbeResponse(r io.Reader) (ProbeResponse, error) {
	if isNilProbeReader(r) {
		return ProbeResponse{}, &ProbeMalformedError{Cause: errors.New("probe response reader is required")}
	}
	data, err := io.ReadAll(io.LimitReader(r, maxProbeResponseBytes+1))
	if err != nil {
		return ProbeResponse{}, ProbeTransportError{Cause: fmt.Errorf("read probe response: %w", snapshotProbeExternalError(err).cause)}
	}
	if len(data) > maxProbeResponseBytes {
		return ProbeResponse{}, &ProbeMalformedError{Cause: fmt.Errorf("probe response exceeds %d-byte limit", maxProbeResponseBytes)}
	}
	if err := validateProbeResponseJSON(data); err != nil {
		return ProbeResponse{}, &ProbeMalformedError{Cause: err}
	}
	if err := validateProbeResponseRequiredFields(data); err != nil {
		return ProbeResponse{}, &ProbeMalformedError{Cause: err}
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var response ProbeResponse
	if err := decoder.Decode(&response); err != nil {
		return ProbeResponse{}, &ProbeMalformedError{Cause: fmt.Errorf("decode probe response: %w", err)}
	}
	if err := response.PhysicalAttestation.Validate(); err != nil {
		return ProbeResponse{}, &ProbeMalformedError{Cause: err}
	}
	return response, nil
}

// isNilProbeReader 识别 nil 接口及其携带的 nil 指针读取器。
func isNilProbeReader(r io.Reader) bool {
	if r == nil {
		return true
	}
	value := reflect.ValueOf(r)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// validateProbeResponseJSON 递归检查 JSON 容器、重复键、null 和尾随值。
func validateProbeResponseJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	root, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("validate probe response JSON: %w", err)
	}
	if root == nil {
		return errors.New("validate probe response JSON: null is not allowed")
	}
	delimiter, ok := root.(json.Delim)
	if !ok || delimiter != '{' {
		return errors.New("validate probe response JSON: root must be an object")
	}
	if err := walkProbeResponseRoot(decoder); err != nil {
		return fmt.Errorf("validate probe response JSON: %w", err)
	}
	trailing, err := decoder.Token()
	if err == nil {
		return fmt.Errorf("probe response contains trailing JSON value %v", trailing)
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode probe response trailer: %w", err)
	}
	return nil
}

// walkProbeResponseRoot 读取 root 对象并确认每个键只出现一次。
func walkProbeResponseRoot(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("probe response object key is not a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("probe response object key %q is duplicated", key)
		}
		seen[key] = struct{}{}
		if err := walkSnapshotJSONValue(decoder); err != nil {
			return err
		}
	}
	return expectSnapshotJSONClosingDelimiter(decoder, '}')
}

// validateProbeResponseRequiredFields 在类型解码前确认 root 和嵌套字段齐全。
func validateProbeResponseRequiredFields(data []byte) error {
	root, err := decodeSnapshotJSONObject(json.RawMessage(data), "probe response")
	if err != nil {
		return fmt.Errorf("validate probe response required fields: %w", err)
	}
	if err := requireSnapshotFields(root, "probe response", []string{
		"schema_version", "owner_epoch", "capacity_basis", "node_id", "endpoint",
		"platform", "probe_identity", "physical_attestation", "probe_boot_id", "active_capabilities", "status",
		"heartbeat_unix_milli", "total", "available",
	}); err != nil {
		return fmt.Errorf("validate probe response required fields: %w", err)
	}
	attestation, err := decodeSnapshotJSONObject(root["physical_attestation"], "probe response.physical_attestation")
	if err != nil {
		return fmt.Errorf("validate probe response required fields: %w", err)
	}
	if err := requireSnapshotFields(attestation, "probe response.physical_attestation", []string{
		"schema_version", "os_id", "machine", "machine_id_sha256", "os_release_identity", "uname_identity", "docker_os_type", "docker_architecture", "digest",
	}); err != nil {
		return fmt.Errorf("validate probe response required fields: %w", err)
	}
	activeCapabilities, err := decodeSnapshotJSONArray(root["active_capabilities"], "probe response.active_capabilities")
	if err != nil {
		return fmt.Errorf("validate probe response required fields: %w", err)
	}
	for capabilityIndex, capability := range activeCapabilities {
		if err := validateProbeActiveCapabilityRequiredFields(capability, capabilityIndex); err != nil {
			return fmt.Errorf("validate probe response required fields: %w", err)
		}
	}
	for _, resourceName := range []string{"total", "available"} {
		resourcePath := "probe response." + resourceName
		resource, err := decodeSnapshotJSONObject(root[resourceName], resourcePath)
		if err != nil {
			return fmt.Errorf("validate probe response required fields: %w", err)
		}
		if err := requireSnapshotFields(resource, resourcePath, []string{
			"cpu_milli", "memory_bytes", "max_concurrency",
		}); err != nil {
			return fmt.Errorf("validate probe response required fields: %w", err)
		}
	}
	return nil
}

// validateProbeActiveCapabilityRequiredFields 校验能力及可选资格回执的字段形状。
func validateProbeActiveCapabilityRequiredFields(raw json.RawMessage, capabilityIndex int) error {
	capabilityPath := fmt.Sprintf("probe response.active_capabilities[%d]", capabilityIndex)
	capability, err := decodeSnapshotJSONObject(raw, capabilityPath)
	if err != nil {
		return err
	}
	if err := requireSnapshotFields(capability, capabilityPath, []string{"runtime", "image_digest"}); err != nil {
		return err
	}
	qualification := capability["qualification"]
	if qualification == nil {
		return nil
	}
	qualificationPath := capabilityPath + ".qualification"
	qualificationObject, err := decodeSnapshotJSONObject(qualification, qualificationPath)
	if err != nil {
		return err
	}
	return requireSnapshotFields(qualificationObject, qualificationPath, []string{
		"schema_version", "node_id", "runtime", "probe_identity", "probe_boot_id",
		"image_digest", "source_revision", "qualification_suite", "immutable_identity",
		"completed_at_unix_milli", "digest",
	})
}
