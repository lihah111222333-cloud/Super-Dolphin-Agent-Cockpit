package nodeevents

import (
	"strings"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	taskdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/task"
)

// Publish 发布编排。
func Publish(bus *event.Dispatcher, oldStatus string, node *taskdag.Node) {
	if bus == nil || node == nil {
		return
	}
	ev, ok := build(oldStatus, node)
	if !ok {
		return
	}
	event.Publish(bus, ev)
}

// PublishFields 发布字段。
func PublishFields(bus *event.Dispatcher, oldStatus, newStatus, dagKey, nodeKey string, runID int64) {
	if bus == nil {
		return
	}
	Publish(bus, oldStatus, &taskdag.Node{DagKey: dagKey, NodeKey: nodeKey, RunID: &runID, Status: newStatus})
}

// PublishComplete 发布complete。
func PublishComplete(bus *event.Dispatcher, oldStatus string, res *taskdag.CompleteNodeWithDownstreamResult) {
	if res == nil {
		return
	}
	Publish(bus, oldStatus, res.Node)
	for _, p := range res.PromotedDownstream {
		PublishFields(bus, "pending", "ready", p.DagKey, p.NodeKey, p.RunID)
	}
}

// PublishFail 发布fail。
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

// build 构建编排。
func build(oldStatus string, node *taskdag.Node) (taskdto.TaskNodeStatusChanged, bool) {
	dagKey, nodeKey, newStatus := strings.TrimSpace(node.DagKey), strings.TrimSpace(node.NodeKey), strings.TrimSpace(node.Status)
	runID := int64(0)
	if node.RunID != nil {
		runID = *node.RunID
	}
	if dagKey == "" || nodeKey == "" || newStatus == "" || runID <= 0 {
		return taskdto.TaskNodeStatusChanged{}, false
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
	return ev, true
}
