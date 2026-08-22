package remoteruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	// ProbeNodeResultSchemaVersion 是 probe result 的 strict DTO schema。
	ProbeNodeResultSchemaVersion = "remote-ci-node-probe-result/v1"
	// RunNodeResultSchemaVersion 是 runner result 的 strict DTO schema。
	RunNodeResultSchemaVersion = "remote-ci-node-run-result/v1"
	// WorkerNodeResultSchemaVersion 是 worker result 的 strict DTO schema。
	WorkerNodeResultSchemaVersion = "remote-ci-node-worker-result/v1"
)

// ProbeNodeResult 是 probe stdout 唯一允许的字段集合。
type ProbeNodeResult struct {
	// SchemaVersion 是 probe DTO 的 ABI schema。
	SchemaVersion string `json:"schema_version"`
	// NodeID 是返回事实所属的固定节点。
	NodeID string `json:"node_id"`
	// ProbeIdentity 是与 binding 精确匹配的 probe identity。
	ProbeIdentity string `json:"probe_identity"`
	// Status 是不含路径或底层日志的稳定状态。
	Status string `json:"status"`
}

// RunNodeResult 是 runner stdout 唯一允许的字段集合。
type RunNodeResult struct {
	// SchemaVersion 是 runner DTO 的 ABI schema。
	SchemaVersion string `json:"schema_version"`
	// NodeID 是返回事实所属的固定节点。
	NodeID string `json:"node_id"`
	// RunnerIdentity 是与 binding 精确匹配的 runner identity。
	RunnerIdentity string `json:"runner_identity"`
	// Status 是不含路径或底层日志的稳定状态。
	Status string `json:"status"`
}

// WorkerNodeResult 是 worker stdout 唯一允许的字段集合。
type WorkerNodeResult struct {
	// SchemaVersion 是 worker DTO 的 ABI schema。
	SchemaVersion string `json:"schema_version"`
	// NodeID 是返回事实所属的固定节点。
	NodeID string `json:"node_id"`
	// WorkerIdentity 是与 binding 精确匹配的 worker identity。
	WorkerIdentity string `json:"worker_identity"`
	// Status 是不含路径或底层日志的稳定状态。
	Status string `json:"status"`
}

// DecodeNodeResult 严格解码 operation-specific typed DTO，拒绝 unknown、generic identity 和路径字段。
func DecodeNodeResult(data []byte, node NodeBinding, operation Operation) (NodeResult, error) {
	switch operation {
	case OperationProbe:
		result, raw, err := decodeProbeNodeResult(data, node)
		if err != nil {
			return NodeResult{}, err
		}
		return NodeResult{NodeID: result.NodeID, Identity: result.ProbeIdentity, Raw: raw}, nil
	case OperationRun:
		result, raw, err := decodeRunNodeResult(data, node)
		if err != nil {
			return NodeResult{}, err
		}
		return NodeResult{NodeID: result.NodeID, Identity: result.RunnerIdentity, Raw: raw}, nil
	case OperationWorker:
		result, raw, err := decodeWorkerNodeResult(data, node)
		if err != nil {
			return NodeResult{}, err
		}
		return NodeResult{NodeID: result.NodeID, Identity: result.WorkerIdentity, Raw: raw}, nil
	default:
		return NodeResult{}, fmt.Errorf("%w: operation", ErrInvalidTransportRequest)
	}
}

func decodeProbeNodeResult(data []byte, node NodeBinding) (ProbeNodeResult, []byte, error) {
	var result ProbeNodeResult
	raw, err := decodeTypedNodeResult(data, &result)
	if err != nil {
		return ProbeNodeResult{}, nil, err
	}
	if result.SchemaVersion != ProbeNodeResultSchemaVersion || result.NodeID != node.NodeID || result.ProbeIdentity != node.ProbeIdentity {
		return ProbeNodeResult{}, nil, fmt.Errorf("%w: probe identity", ErrNodeResultInvalid)
	}
	if err := validateResultStatus(result.Status); err != nil {
		return ProbeNodeResult{}, nil, err
	}
	return result, raw, nil
}

func decodeRunNodeResult(data []byte, node NodeBinding) (RunNodeResult, []byte, error) {
	var result RunNodeResult
	raw, err := decodeTypedNodeResult(data, &result)
	if err != nil {
		return RunNodeResult{}, nil, err
	}
	if result.SchemaVersion != RunNodeResultSchemaVersion || result.NodeID != node.NodeID || result.RunnerIdentity != node.RunnerIdentity {
		return RunNodeResult{}, nil, fmt.Errorf("%w: runner identity", ErrNodeResultInvalid)
	}
	if err := validateResultStatus(result.Status); err != nil {
		return RunNodeResult{}, nil, err
	}
	return result, raw, nil
}

func decodeWorkerNodeResult(data []byte, node NodeBinding) (WorkerNodeResult, []byte, error) {
	var result WorkerNodeResult
	raw, err := decodeTypedNodeResult(data, &result)
	if err != nil {
		return WorkerNodeResult{}, nil, err
	}
	if result.SchemaVersion != WorkerNodeResultSchemaVersion || result.NodeID != node.NodeID || result.WorkerIdentity != node.WorkerIdentity {
		return WorkerNodeResult{}, nil, fmt.Errorf("%w: worker identity", ErrNodeResultInvalid)
	}
	if err := validateResultStatus(result.Status); err != nil {
		return WorkerNodeResult{}, nil, err
	}
	return result, raw, nil
}

func decodeTypedNodeResult(data []byte, target any) ([]byte, error) {
	if len(data) == 0 || len(data) > MaxNodeResultBytes {
		return nil, ErrNodeResultInvalid
	}
	if err := walkStrictJSON(data); err != nil {
		return nil, errors.Join(ErrNodeResultInvalid, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, errors.Join(ErrNodeResultInvalid, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, errors.Join(ErrNodeResultInvalid, err)
	}
	canonical, err := json.Marshal(target)
	if err != nil || !bytes.Equal(canonical, data) {
		return nil, ErrNodeResultInvalid
	}
	return canonical, nil
}

func validateResultStatus(value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: status", ErrNodeResultInvalid)
	}
	return nil
}
