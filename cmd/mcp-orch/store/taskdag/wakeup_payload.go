package taskdag

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// WakeupPayloadSchemaVersion1 is the only payload schema accepted by the
	// dispatcher. A future schema must add an explicit decoder branch.
	WakeupPayloadSchemaVersion1 int64 = 1
	// WakeupPayloadKindDownstream identifies a DAG downstream launch payload.
	WakeupPayloadKindDownstream = "dag_downstream"
	// WakeupPayloadKindLegacyLaunch identifies the explicitly migrated legacy
	// launcher shape. Untagged legacy JSON is not accepted at runtime.
	WakeupPayloadKindLegacyLaunch = "legacy_launch"
	maxWakeupAgentIDBytes         = 256
	maxWakeupPromptBytes          = 128 << 10
	maxWakeupPathBytes            = 4096
	maxWakeupPayloadBytes         = 256 << 10
	maxWakeupUpstreamRefs         = 64
	maxWakeupInternalTextBytes    = 4096
)

var ErrInvalidWakeupPayload = errors.New("invalid wakeup payload")

// LegacyWakeupPayload is the explicitly versioned replacement for the old
// untagged LaunchRequest JSON. It intentionally mirrors the stable wire
// fields used by hand-enqueued wakeups without importing the orchestration
// contract package (which would create a dependency cycle).
type LegacyWakeupPayload struct {
	AgentID        string   `json:"agent_id"`
	Name           string   `json:"name,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`
	Instructions   string   `json:"instructions,omitempty"`
	ParentID       string   `json:"parent_id,omitempty"`
	ParentThreadID string   `json:"parent_thread_id,omitempty"`
	ContextMode    string   `json:"context_mode,omitempty"`
	AgentType      string   `json:"agent_type,omitempty"`
	AgentKey       string   `json:"agent_key,omitempty"`
	PromptKey      string   `json:"prompt_key,omitempty"`
	MemoryScope    string   `json:"memory_scope,omitempty"`
	Cwd            string   `json:"cwd,omitempty"`
	Language       string   `json:"language,omitempty"`
	Command        []string `json:"command,omitempty"`
	Env            []string `json:"env,omitempty"`
}

// MarshalJSON 为历史简写构造的 DownstreamWakeupPayload 补齐规范判别字段。
func (p DownstreamWakeupPayload) MarshalJSON() ([]byte, error) {
	type wire DownstreamWakeupPayload
	value := wire(p)
	if value.WakeupPayloadKind == "" {
		value.WakeupPayloadKind = WakeupPayloadKindDownstream
	}
	if value.SchemaVersion == 0 {
		value.SchemaVersion = WakeupPayloadSchemaVersion1
	}
	return json.Marshal(value)
}

// DecodedWakeupPayload is the validated discriminator union stored in a
// wakeup row. Exactly one branch is non-nil.
type DecodedWakeupPayload struct {
	Kind          string
	SchemaVersion int64
	Downstream    *DownstreamWakeupPayload
	Legacy        *LegacyWakeupPayload
}

// DecodeWakeupPayload 严格解码判别联合；缺失、未知、混合、损坏和不支持版本均快速失败。
func DecodeWakeupPayload(raw []byte) (DecodedWakeupPayload, error) {
	if len(raw) > maxWakeupPayloadBytes {
		return DecodedWakeupPayload{}, fmt.Errorf("%w: payload exceeds %d bytes", ErrInvalidWakeupPayload, maxWakeupPayloadBytes)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return DecodedWakeupPayload{}, fmt.Errorf("%w: payload is required", ErrInvalidWakeupPayload)
	}
	kind, schemaVersion, err := wakeupPayloadHeader(raw)
	if err != nil {
		return DecodedWakeupPayload{}, err
	}
	if kind == "" {
		return DecodedWakeupPayload{}, fmt.Errorf("%w: wakeup_payload_kind is required", ErrInvalidWakeupPayload)
	}
	if schemaVersion != WakeupPayloadSchemaVersion1 {
		return DecodedWakeupPayload{}, fmt.Errorf("%w: unsupported schema_version %d", ErrInvalidWakeupPayload, schemaVersion)
	}
	switch kind {
	case WakeupPayloadKindDownstream:
		payload, err := decodeDownstreamWakeupPayload(raw)
		if err != nil {
			return DecodedWakeupPayload{}, err
		}
		return DecodedWakeupPayload{Kind: kind, SchemaVersion: schemaVersion, Downstream: payload}, nil
	case WakeupPayloadKindLegacyLaunch:
		legacy, err := decodeLegacyWakeupPayload(raw)
		if err != nil {
			return DecodedWakeupPayload{}, err
		}
		return DecodedWakeupPayload{Kind: kind, SchemaVersion: schemaVersion, Legacy: legacy}, nil
	default:
		return DecodedWakeupPayload{}, fmt.Errorf("%w: unknown wakeup_payload_kind %q", ErrInvalidWakeupPayload, kind)
	}
}

// wakeupPayloadHeader 读取判别联合头部并保持 JSON 类型错误可见。
func wakeupPayloadHeader(raw []byte) (string, int64, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", 0, fmt.Errorf("%w: malformed JSON: %v", ErrInvalidWakeupPayload, err)
	}
	var kind string
	if value, ok := fields["wakeup_payload_kind"]; ok {
		if err := json.Unmarshal(value, &kind); err != nil {
			return "", 0, fmt.Errorf("%w: wakeup_payload_kind must be a string", ErrInvalidWakeupPayload)
		}
	}
	var schemaVersion int64
	if value, ok := fields["schema_version"]; ok {
		if err := json.Unmarshal(value, &schemaVersion); err != nil {
			return "", 0, fmt.Errorf("%w: schema_version must be an integer", ErrInvalidWakeupPayload)
		}
	}
	return strings.TrimSpace(kind), schemaVersion, nil
}

// decodeDownstreamWakeupPayload 解码 DAG 下游分支并检查字段边界。
func decodeDownstreamWakeupPayload(raw []byte) (*DownstreamWakeupPayload, error) {
	if err := rejectNullFields(raw, "wakeup_payload_kind", "schema_version", "agent_id", "upstream_outputs"); err != nil {
		return nil, fmt.Errorf("%w: downstream: %v", ErrInvalidWakeupPayload, err)
	}
	if err := rejectAnyJSONNull(raw); err != nil {
		return nil, fmt.Errorf("%w: downstream: %v", ErrInvalidWakeupPayload, err)
	}
	var payload DownstreamWakeupPayload
	if err := decodeStrict(raw, &payload); err != nil {
		return nil, fmt.Errorf("%w: downstream: %v", ErrInvalidWakeupPayload, err)
	}
	if strings.TrimSpace(payload.AgentID) == "" {
		return nil, fmt.Errorf("%w: downstream agent_id is required", ErrInvalidWakeupPayload)
	}
	if payload.AgentID != strings.TrimSpace(payload.AgentID) {
		return nil, fmt.Errorf("%w: downstream agent_id must not have surrounding whitespace", ErrInvalidWakeupPayload)
	}
	if err := validateDownstreamFields(payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// validateDownstreamFields 检查 agent 与上游引用的数量和长度边界。
func validateDownstreamFields(payload DownstreamWakeupPayload) error {
	if len(payload.AgentID) > maxWakeupAgentIDBytes {
		return fmt.Errorf("%w: downstream agent_id exceeds %d bytes", ErrInvalidWakeupPayload, maxWakeupAgentIDBytes)
	}
	if len(payload.UpstreamOutputs) > maxWakeupUpstreamRefs {
		return fmt.Errorf("%w: downstream upstream_outputs exceeds %d entries", ErrInvalidWakeupPayload, maxWakeupUpstreamRefs)
	}
	for _, ref := range payload.UpstreamOutputs {
		if strings.TrimSpace(ref.NodeKey) == "" || strings.TrimSpace(ref.Path) == "" {
			return fmt.Errorf("%w: downstream upstream_outputs entries require node_key and path", ErrInvalidWakeupPayload)
		}
		if len(ref.NodeKey) > maxWakeupAgentIDBytes || len(ref.Path) > maxWakeupPathBytes {
			return fmt.Errorf("%w: downstream upstream_outputs entry exceeds field bounds", ErrInvalidWakeupPayload)
		}
	}
	return nil
}

// decodeLegacyWakeupPayload 解码显式迁移的 legacy launch 分支。
func decodeLegacyWakeupPayload(raw []byte) (*LegacyWakeupPayload, error) {
	if err := rejectNullFields(raw, "wakeup_payload_kind", "schema_version", "agent_id", "prompt"); err != nil {
		return nil, fmt.Errorf("%w: legacy: %v", ErrInvalidWakeupPayload, err)
	}
	if err := rejectAnyJSONNull(raw); err != nil {
		return nil, fmt.Errorf("%w: legacy: %v", ErrInvalidWakeupPayload, err)
	}
	var payload struct {
		WakeupPayloadKind string `json:"wakeup_payload_kind"`
		SchemaVersion     int64  `json:"schema_version"`
		LegacyWakeupPayload
	}
	if err := decodeStrict(raw, &payload); err != nil {
		return nil, fmt.Errorf("%w: legacy: %v", ErrInvalidWakeupPayload, err)
	}
	if strings.TrimSpace(payload.AgentID) == "" {
		return nil, fmt.Errorf("%w: legacy agent_id is required", ErrInvalidWakeupPayload)
	}
	if payload.AgentID != strings.TrimSpace(payload.AgentID) {
		return nil, fmt.Errorf("%w: legacy agent_id must not have surrounding whitespace", ErrInvalidWakeupPayload)
	}
	if len(payload.AgentID) > maxWakeupAgentIDBytes || len(payload.Prompt) > maxWakeupPromptBytes {
		return nil, fmt.Errorf("%w: legacy payload exceeds field bounds", ErrInvalidWakeupPayload)
	}
	legacy := payload.LegacyWakeupPayload
	return &legacy, nil
}

func rejectNullFields(raw []byte, names ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for _, name := range names {
		if value, ok := fields[name]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("field %q must not be null", name)
		}
	}
	return nil
}

// rejectAnyJSONNull 拒绝 launch payload 任意层级的 null，避免 encoding/json
// 把可选字段静默还原成零值后再被 omitempty 丢失。
func rejectAnyJSONNull(raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if containsJSONNull(value) {
		return errors.New("JSON null is not allowed")
	}
	return nil
}

func containsJSONNull(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case []any:
		for _, item := range typed {
			if containsJSONNull(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsJSONNull(item) {
				return true
			}
		}
	}
	return false
}

func decodeStrict(raw []byte, target any) error {
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// rejectDuplicateJSONKeys 在 strict decoder 前扫描对象键，避免 JSON 重复键被静默覆盖。
func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			return scanJSONObject(decoder)
		case '[':
			return scanJSONArray(decoder)
		}
	}
	return nil
}

// scanJSONObject 递归扫描对象并拒绝重复键。
func scanJSONObject(decoder *json.Decoder) error {
	seen := map[string]struct{}{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("object key must be a string")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate JSON field %q", key)
		}
		seen[key] = struct{}{}
		if err := scanJSONValue(decoder); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

func scanJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := scanJSONValue(decoder); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}
