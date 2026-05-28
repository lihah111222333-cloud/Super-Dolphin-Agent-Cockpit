package nodeevents

import (
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	taskdto "github.com/anthropic-ai/super-agent-v3/internal/dto/task"
	"github.com/kelindar/event"
)

func Publish(bus *event.Dispatcher, oldStatus string, node *taskdag.Node) {
	if bus == nil || node == nil {
		return
	}
	event.Publish(bus, build(oldStatus, node))
}

func PublishFields(bus *event.Dispatcher, oldStatus, newStatus, dagKey, nodeKey string, runID int64) {
	if bus == nil {
		return
	}
	Publish(bus, oldStatus, &taskdag.Node{DagKey: dagKey, NodeKey: nodeKey, RunID: &runID, Status: newStatus})
}

func PublishComplete(bus *event.Dispatcher, oldStatus string, res *taskdag.CompleteNodeWithDownstreamResult) {
	if res == nil {
		return
	}
	Publish(bus, oldStatus, res.Node)
	for _, p := range res.PromotedDownstream {
		PublishFields(bus, "pending", "ready", p.DagKey, p.NodeKey, p.RunID)
	}
}

func PublishFail(bus *event.Dispatcher, oldStatus string, res *taskdag.FailNodeResult) {
	if res == nil {
		return
	}
	if strings.TrimSpace(oldStatus) == "" {
		oldStatus = res.OldStatus
	}
	Publish(bus, oldStatus, res.Node)
	for _, c := range res.CanceledDownstream {
		PublishFields(bus, "pending", "failed", c.DagKey, c.NodeKey, c.RunID)
	}
}

func build(oldStatus string, node *taskdag.Node) taskdto.TaskNodeStatusChanged {
	dagKey, nodeKey, newStatus := strings.TrimSpace(node.DagKey), strings.TrimSpace(node.NodeKey), strings.TrimSpace(node.Status)
	runID := int64(0)
	if node.RunID != nil {
		runID = *node.RunID
	}
	if dagKey == "" || nodeKey == "" || newStatus == "" || runID <= 0 {
		panic(fmt.Sprintf("publish task node status changed: invalid identity dag=%q node=%q run_id=%d status=%q", node.DagKey, node.NodeKey, runID, node.Status))
	}
	ev := taskdto.TaskNodeStatusChanged{
		TaskNodeHeader: shareddto.TaskNodeHeader{
			TaskDAGHeader: shareddto.TaskDAGHeader{DAGHeader: shareddto.DAGHeader{
				EventHeader: shareddto.EventHeader{Timestamp: time.Now().UTC()}, DagKey: dagKey,
			}},
			NodeKey: nodeKey, RunID: runID,
		},
		AssignedTo: strings.TrimSpace(node.AssignedTo), OldStatus: strings.TrimSpace(oldStatus), NewStatus: newStatus,
	}
	if node.ActiveTurnID != nil {
		ev.ActiveTurnID = strings.TrimSpace(*node.ActiveTurnID)
	}
	if node.ActiveWakeupID != nil {
		ev.ActiveWakeupID = *node.ActiveWakeupID
	}
	return ev
}
