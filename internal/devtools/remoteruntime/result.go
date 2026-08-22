package remoteruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocinode"
)

// ProbeNodeResult 是现有 heteroci probe response 的 strict DTO 别名。
type ProbeNodeResult = heteroci.ProbeResponse

// RunNodeResult 是现有 heterocinode batch result 的 strict DTO 别名。
type RunNodeResult = heterocinode.BatchResult

// WorkerNodeResult 是现有 heterocinode batch result 的 strict DTO 别名。
type WorkerNodeResult = heterocinode.BatchResult

// DecodeNodeResult 解码现有 heteroci/heterocinode strict wire DTO，并把 binding identity 作为本地投影。
func DecodeNodeResult(data []byte, node NodeBinding, operation Operation) (NodeResult, error) {
	switch operation {
	case OperationProbe:
		result, raw, err := decodeProbeNodeResult(data, node)
		if err != nil {
			return NodeResult{}, err
		}
		return NodeResult{NodeID: result.NodeID, Identity: result.ProbeIdentity, Raw: raw}, nil
	case OperationRun:
		_, raw, err := decodeBatchNodeResult(data)
		if err != nil {
			return NodeResult{}, err
		}
		return NodeResult{NodeID: node.NodeID, Identity: node.RunnerIdentity, Raw: raw}, nil
	case OperationWorker:
		_, raw, err := decodeBatchNodeResult(data)
		if err != nil {
			return NodeResult{}, err
		}
		return NodeResult{NodeID: node.NodeID, Identity: node.WorkerIdentity, Raw: raw}, nil
	default:
		return NodeResult{}, fmt.Errorf("%w: operation", ErrInvalidTransportRequest)
	}
}

func decodeProbeNodeResult(data []byte, node NodeBinding) (ProbeNodeResult, []byte, error) {
	if len(data) == 0 || len(data) > MaxNodeResultBytes {
		return ProbeNodeResult{}, nil, ErrNodeResultInvalid
	}
	result, err := heteroci.DecodeProbeResponse(bytes.NewReader(data))
	if err != nil {
		return ProbeNodeResult{}, nil, errors.Join(ErrNodeResultInvalid, err)
	}
	canonical, err := json.Marshal(result)
	if err != nil || !bytes.Equal(canonical, data) {
		return ProbeNodeResult{}, nil, ErrNodeResultInvalid
	}
	if err := validateProbeBinding(result, node); err != nil {
		return ProbeNodeResult{}, nil, err
	}
	return result, slices.Clone(canonical), nil
}

func validateProbeBinding(result ProbeNodeResult, node NodeBinding) error {
	if result.SchemaVersion != heteroci.ProbeResponseSchemaVersion || result.Status != heteroci.NodeStatusLive || result.CapacityBasis != heteroci.SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations || result.NodeID != node.NodeID || result.Endpoint != node.Endpoint || string(result.Platform) != string(node.Platform) || result.ProbeIdentity != node.ProbeIdentity {
		return fmt.Errorf("%w: probe binding identity", ErrNodeResultInvalid)
	}
	return nil
}

func decodeBatchNodeResult(data []byte) (heterocinode.BatchResult, []byte, error) {
	if len(data) == 0 || len(data) > MaxNodeResultBytes {
		return heterocinode.BatchResult{}, nil, ErrNodeResultInvalid
	}
	result, err := heterocinode.DecodeBatchResult(data)
	if err != nil {
		return heterocinode.BatchResult{}, nil, errors.Join(ErrNodeResultInvalid, err)
	}
	return result, slices.Clone(data), nil
}
