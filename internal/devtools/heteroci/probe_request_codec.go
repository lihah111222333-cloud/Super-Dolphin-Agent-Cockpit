package heteroci

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

const maxProbeRequestBytes = 4 << 10

// DecodeProbeRequest 严格解码 core 生成的 canonical probe request，供远端 node probe 验证身份。
func DecodeProbeRequest(data []byte) (ProbeRequest, error) {
	if len(data) == 0 || len(data) > maxProbeRequestBytes {
		return ProbeRequest{}, errors.New("probe request size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request ProbeRequest
	if err := decoder.Decode(&request); err != nil {
		return ProbeRequest{}, errors.New("probe request JSON is invalid")
	}
	if err := ensureProbeRequestEOF(decoder); err != nil {
		return ProbeRequest{}, err
	}
	canonical, err := json.Marshal(request)
	if err != nil || !bytes.Equal(canonical, data) {
		return ProbeRequest{}, errors.New("probe request is not canonical")
	}
	if err := validateDecodedProbeRequest(request); err != nil {
		return ProbeRequest{}, err
	}
	return request, nil
}

func ensureProbeRequestEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	}
	return errors.New("probe request has trailing JSON")
}

// validateDecodedProbeRequest 校验 schema、节点、探针、owner 与统一采集时间。
func validateDecodedProbeRequest(request ProbeRequest) error {
	if request.SchemaVersion != ProbeRequestSchemaVersion {
		return errors.New("probe request schema_version is invalid")
	}
	if !validDecodedProbeText(request.NodeID) {
		return errors.New("probe request node_id is invalid")
	}
	if !isSHA256Digest(request.ProbeIdentity) {
		return errors.New("probe request probe_identity is invalid")
	}
	if err := request.OwnerEpoch.Validate(); err != nil {
		return errors.New("probe request owner_epoch is invalid")
	}
	if request.CapturedAtUnixMilli <= 0 {
		return errors.New("probe request captured_at_unix_milli is invalid")
	}
	return nil
}

func validDecodedProbeText(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}
