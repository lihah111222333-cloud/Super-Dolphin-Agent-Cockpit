package tools

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func updateNodeRequestFromInput(in UpdateNodeInput) (contract.UpdateNodeStatusRequest, error) {
	dagKey, err := resolveDAGKeyInput(in.DagKey, in.Pos)
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	nodeKey, err := resolveNodeKeyInput(in.NodeKey, in.Pos)
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	runID, err := resolveRunIDInput(in.RunID, in.Pos)
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	status, err := requireEnum(in.Status, "status", updateNodeStatusEnum)
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	result, err := encodeOptionalString(strings.TrimSpace(in.Result))
	if err != nil {
		return contract.UpdateNodeStatusRequest{}, err
	}
	return contract.UpdateNodeStatusRequest{
		DagKey:  dagKey,
		NodeKey: nodeKey,
		RunID:   runID,
		Status:  status,
		Result:  result,
	}, nil
}

func dispatchNodeRequestFromInput(in DispatchNodeInput) (contract.DispatchNodeRequest, error) {
	dagKey, err := resolveDAGKeyInput(in.DagKey, in.Pos)
	if err != nil {
		return contract.DispatchNodeRequest{}, err
	}
	nodeKey, err := resolveNodeKeyInput(in.NodeKey, in.Pos)
	if err != nil {
		return contract.DispatchNodeRequest{}, err
	}
	runID, err := resolveRunIDInput(in.RunID, in.Pos)
	if err != nil {
		return contract.DispatchNodeRequest{}, err
	}
	assignedTo, err := requireTrimmed(in.AssignedTo, "assigned_to")
	if err != nil {
		return contract.DispatchNodeRequest{}, err
	}
	return contract.DispatchNodeRequest{
		DagKey:     dagKey,
		NodeKey:    nodeKey,
		RunID:      runID,
		AssignedTo: assignedTo,
	}, nil
}
