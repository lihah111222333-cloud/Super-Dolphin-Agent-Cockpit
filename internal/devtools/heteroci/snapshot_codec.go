package heteroci

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"
)

const maxSnapshotBytes = 1 << 20

// DecodeSnapshotSet 从输入中严格解码并验证一组本地异构 CI 节点快照。
func DecodeSnapshotSet(r io.Reader, manifest Manifest, now time.Time, heartbeatTTL time.Duration) (SnapshotSet, error) {
	if isNilSnapshotReader(r) {
		return SnapshotSet{}, errors.New("heterogeneous CI snapshot reader is required")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxSnapshotBytes+1))
	if err != nil {
		return SnapshotSet{}, fmt.Errorf("read heterogeneous CI snapshot: %w", err)
	}
	if len(data) > maxSnapshotBytes {
		return SnapshotSet{}, fmt.Errorf("heterogeneous CI snapshot exceeds %d-byte limit", maxSnapshotBytes)
	}
	if err := validateSnapshotJSON(data); err != nil {
		return SnapshotSet{}, err
	}
	if err := validateSnapshotRequiredFields(data); err != nil {
		return SnapshotSet{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var snapshot SnapshotSet
	if err := decoder.Decode(&snapshot); err != nil {
		return SnapshotSet{}, fmt.Errorf("decode heterogeneous CI snapshot: %w", err)
	}
	if err := snapshot.ValidateAgainst(manifest, now, heartbeatTTL); err != nil {
		return SnapshotSet{}, fmt.Errorf("validate heterogeneous CI snapshot: %w", err)
	}
	return snapshot, nil
}

// isNilSnapshotReader 识别 nil 接口及其携带的 nil 指针读取器。
func isNilSnapshotReader(r io.Reader) bool {
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

// validateSnapshotJSON 以令牌递归检查 JSON 的结构完整性和字段唯一性。
func validateSnapshotJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	root, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("validate heterogeneous CI snapshot JSON: %w", err)
	}
	if root == nil {
		return errors.New("validate heterogeneous CI snapshot JSON: null is not allowed")
	}
	delimiter, ok := root.(json.Delim)
	if !ok || delimiter != '{' {
		return errors.New("validate heterogeneous CI snapshot JSON: root must be an object")
	}
	if err := walkSnapshotJSONObject(decoder, true); err != nil {
		return fmt.Errorf("validate heterogeneous CI snapshot JSON: %w", err)
	}
	trailing, err := decoder.Token()
	if err == nil {
		return fmt.Errorf("heterogeneous CI snapshot contains trailing JSON value %v", trailing)
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode heterogeneous CI snapshot trailer: %w", err)
	}
	return nil
}

// walkSnapshotJSONValue 递归读取一个 JSON 值并拒绝 null 或未闭合容器。
func walkSnapshotJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("heterogeneous CI snapshot must not contain null")
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return walkSnapshotJSONObject(decoder, false)
	case '[':
		return walkSnapshotJSONArray(decoder)
	default:
		return fmt.Errorf("heterogeneous CI snapshot has unexpected delimiter %q", delimiter)
	}
}

// walkSnapshotJSONObject 递归检查对象 key 唯一并确认根字段齐全。
func walkSnapshotJSONObject(decoder *json.Decoder, root bool) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("heterogeneous CI snapshot object key is not a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("heterogeneous CI snapshot object key %q is duplicated", key)
		}
		seen[key] = struct{}{}
		if err := walkSnapshotJSONValue(decoder); err != nil {
			return err
		}
	}
	if err := expectSnapshotJSONClosingDelimiter(decoder, '}'); err != nil {
		return err
	}
	if !root {
		return nil
	}
	for _, field := range []string{"schema_version", "authority", "capacity_basis", "captured_at_unix_milli", "nodes", "owner_epoch"} {
		if _, present := seen[field]; !present {
			return fmt.Errorf("heterogeneous CI snapshot root field %q is required", field)
		}
	}
	return nil
}

// walkSnapshotJSONArray 递归检查数组元素并确认数组按预期闭合。
func walkSnapshotJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := walkSnapshotJSONValue(decoder); err != nil {
			return err
		}
	}
	return expectSnapshotJSONClosingDelimiter(decoder, ']')
}

// expectSnapshotJSONClosingDelimiter 确认 JSON 容器使用正确的闭合分隔符。
func expectSnapshotJSONClosingDelimiter(decoder *json.Decoder, expected json.Delim) error {
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != expected {
		return fmt.Errorf("heterogeneous CI snapshot container expected %q, got %v", expected, closing)
	}
	return nil
}

// validateSnapshotRequiredFields 在类型解码前拒绝缺失嵌套字段和错误容器类型。
func validateSnapshotRequiredFields(data []byte) error {
	root, err := decodeSnapshotJSONObject(json.RawMessage(data), "root")
	if err != nil {
		return fmt.Errorf("validate heterogeneous CI snapshot required fields: %w", err)
	}
	if err := requireSnapshotFields(root, "root", []string{"schema_version", "authority", "capacity_basis", "captured_at_unix_milli", "nodes", "owner_epoch"}); err != nil {
		return fmt.Errorf("validate heterogeneous CI snapshot required fields: %w", err)
	}
	nodes, err := decodeSnapshotJSONArray(root["nodes"], "nodes")
	if err != nil {
		return fmt.Errorf("validate heterogeneous CI snapshot required fields: %w", err)
	}
	for nodeIndex, node := range nodes {
		if err := validateSnapshotNodeRequiredFields(node, nodeIndex); err != nil {
			return fmt.Errorf("validate heterogeneous CI snapshot required fields: %w", err)
		}
	}
	return nil
}

// validateSnapshotNodeRequiredFields 校验节点、容量和活动能力的 wire 字段。
func validateSnapshotNodeRequiredFields(raw json.RawMessage, nodeIndex int) error {
	nodePath := fmt.Sprintf("nodes[%d]", nodeIndex)
	node, err := decodeSnapshotJSONObject(raw, nodePath)
	if err != nil {
		return err
	}
	if err := requireSnapshotFields(node, nodePath, []string{
		"node_id", "endpoint", "platform", "probe_identity", "probe_boot_id",
		"active_capabilities", "status", "heartbeat_unix_milli", "total", "available",
	}); err != nil {
		return err
	}
	activeCapabilities, err := decodeSnapshotJSONArray(node["active_capabilities"], nodePath+".active_capabilities")
	if err != nil {
		return err
	}
	for capabilityIndex, capability := range activeCapabilities {
		if err := validateSnapshotActiveCapabilityRequiredFields(capability, nodePath, capabilityIndex); err != nil {
			return err
		}
	}
	for _, resourceName := range []string{"total", "available"} {
		resourcePath := nodePath + "." + resourceName
		resource, err := decodeSnapshotJSONObject(node[resourceName], resourcePath)
		if err != nil {
			return err
		}
		if err := requireSnapshotFields(resource, resourcePath, []string{"cpu_milli", "memory_bytes", "max_concurrency"}); err != nil {
			return err
		}
	}
	return nil
}

// validateSnapshotActiveCapabilityRequiredFields 校验能力字段并按运行时要求资格回执。
func validateSnapshotActiveCapabilityRequiredFields(raw json.RawMessage, nodePath string, capabilityIndex int) error {
	capabilityPath := fmt.Sprintf("%s.active_capabilities[%d]", nodePath, capabilityIndex)
	capability, err := decodeSnapshotJSONObject(raw, capabilityPath)
	if err != nil {
		return err
	}
	if err := requireSnapshotFields(capability, capabilityPath, []string{"runtime", "image_digest"}); err != nil {
		return err
	}
	var runtime RuntimeKind
	if err := json.Unmarshal(capability["runtime"], &runtime); err != nil {
		return fmt.Errorf("heterogeneous CI snapshot %s.runtime must be a string: %w", capabilityPath, err)
	}
	qualification, hasQualification := capability["qualification"]
	if runtime == RuntimeDarlingCompatRuntime || runtime == RuntimeDockerOSXExperimental {
		if !hasQualification {
			return fmt.Errorf("heterogeneous CI snapshot %s field %q is required", capabilityPath, "qualification")
		}
	}
	if hasQualification {
		return validateSnapshotQualificationRequiredFields(qualification, capabilityPath+".qualification")
	}
	return nil
}

// validateSnapshotQualificationRequiredFields 校验资格对象中的全部显式字段。
func validateSnapshotQualificationRequiredFields(raw json.RawMessage, qualificationPath string) error {
	qualification, err := decodeSnapshotJSONObject(raw, qualificationPath)
	if err != nil {
		return err
	}
	return requireSnapshotFields(qualification, qualificationPath, []string{
		"schema_version", "node_id", "runtime", "probe_identity", "probe_boot_id",
		"image_digest", "source_revision", "qualification_suite", "immutable_identity",
		"completed_at_unix_milli", "digest",
	})
}

// decodeSnapshotJSONObject 将 JSON 值严格解码为非空对象。
func decodeSnapshotJSONObject(raw json.RawMessage, path string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("heterogeneous CI snapshot %s must be an object: %w", path, err)
	}
	if object == nil {
		return nil, fmt.Errorf("heterogeneous CI snapshot %s must be an object", path)
	}
	return object, nil
}

// decodeSnapshotJSONArray 将 JSON 值严格解码为非空数组。
func decodeSnapshotJSONArray(raw json.RawMessage, path string) ([]json.RawMessage, error) {
	var array []json.RawMessage
	if err := json.Unmarshal(raw, &array); err != nil {
		return nil, fmt.Errorf("heterogeneous CI snapshot %s must be an array: %w", path, err)
	}
	if array == nil {
		return nil, fmt.Errorf("heterogeneous CI snapshot %s must be an array", path)
	}
	return array, nil
}

// requireSnapshotFields 确认对象显式携带每一个协议字段。
func requireSnapshotFields(object map[string]json.RawMessage, path string, required []string) error {
	for _, field := range required {
		if _, present := object[field]; !present {
			return fmt.Errorf("heterogeneous CI snapshot %s field %q is required", path, field)
		}
	}
	return nil
}
