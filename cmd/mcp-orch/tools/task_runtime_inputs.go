package tools

import (
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// pos 可以补 dag/node/run，但最终必须有明确 run_id。
// 没有 run_id 就拒绝，避免把 task_update_node 写到模板节点上。
func updateNodeRequestFromInput(in UpdateNodeInput) (contract.UpdateNodeStatusRequest, error) {
	return updateNodeRequestFromInputWithRuntimeState(newToolRuntimeState(), in)
}

func updateNodeRequestFromInputWithRuntimeState(state *toolRuntimeState, in UpdateNodeInput) (contract.UpdateNodeStatusRequest, error) {
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
	status, err := requireEnum(in.Status, "status", state.updateNodeStatusEnum)
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

// dispatch 也必须带 run_id；它不改 status，只补 assigned_to 并入队。
// assigned_to 为空要立即拒绝，否则 dispatcher 会把“没人可派”当运行失败。
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
