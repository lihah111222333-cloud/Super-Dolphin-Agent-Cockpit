package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	robcron "github.com/robfig/cron/v3"
)

var ErrRunNotFound = errors.New("orchestration: run_key not found")

func (s *service) GetRun(ctx context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error) {
	if s == nil || s.runStore == nil {
		return contract.GetRunResponse{}, ErrRunStoreUnset
	}
	runKey := strings.TrimSpace(req.RunKey)
	if runKey == "" {
		return contract.GetRunResponse{}, fmt.Errorf("orchestration: GetRun: run_key required")
	}
	run, err := s.runStore.GetRun(ctx, runKey)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return contract.GetRunResponse{}, fmt.Errorf("%w: %s", ErrRunNotFound, runKey)
		}
		return contract.GetRunResponse{}, fmt.Errorf("orchestration: GetRun(%q): %w", runKey, err)
	}
	return getRunResponse(ctx, s.runStore, runKey, run)
}

func (s *service) ListRuns(ctx context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
	if s == nil || s.runStore == nil {
		return contract.ListRunsResponse{}, ErrRunStoreUnset
	}
	dagKey := strings.TrimSpace(req.DagKey)
	if dagKey == "" {
		return contract.ListRunsResponse{}, fmt.Errorf("orchestration: ListRuns: dag_key required")
	}
	filter := taskdag.ListRunsFilter{
		DagKey: dagKey,
		Status: strings.TrimSpace(req.Status),
		Limit:  int32(shared.ClampLimit(int(req.Limit), 1, 200, 50)),
	}
	rows, err := s.runStore.ListRuns(ctx, filter)
	if err != nil {
		return contract.ListRunsResponse{}, fmt.Errorf("orchestration: ListRuns(%q): %w", dagKey, err)
	}
	return contract.ListRunsResponse{Runs: mapRuns(rows)}, nil
}

func mapRuns(items []taskdag.Run) []contract.Run {
	mapped := make([]contract.Run, 0, len(items))
	for _, item := range items {
		mapped = append(mapped, dagRunDTO(item))
	}
	return mapped
}

func dagRunDTO(row taskdag.Run) contract.Run {
	return contract.Run{
		ID:                 row.ID,
		RunKey:             row.RunKey,
		DagKey:             row.DagKey,
		DagVersionSnapshot: row.DagVersionSnapshot,
		TriggerSource:      row.TriggerSource,
		Status:             row.Status,
		StartedAt:          row.StartedAt,
		FinishedAt:         shared.CloneTime(row.FinishedAt),
		Events:             append([]byte(nil), row.Events...),
		BudgetUsed:         row.BudgetUsed,
		BudgetLimit:        cloneInt64(row.BudgetLimit),
		Metadata:           append([]byte(nil), row.Metadata...),
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}

type partitionedOps struct {
	dagUpdates nodeexec.Ops
	adds       nodeexec.Ops
	updates    nodeexec.Ops
	removes    nodeexec.Ops
}

var ErrDuplicateOpForNode = errors.New("orchestration: duplicate op for same node_key in batch")

func partitionOps(ops nodeexec.Ops) (partitionedOps, error) {
	var p partitionedOps
	seenNodeOps := make(map[string]seenNodeOp) // normalized node_key -> first op
	for i, op := range ops {
		if op == nil {
			return p, fmt.Errorf("%w: ops[%d] nil op", ErrApplyOpsInvalid, i)
		}
		if err := appendPartitionedOp(&p, seenNodeOps, i, op); err != nil {
			return p, err
		}
	}
	return p, nil
}

func appendPartitionedOp(p *partitionedOps, seenNodeOps map[string]seenNodeOp, index int, op nodeexec.Op) error {
	switch v := op.(type) {
	case nodeexec.OpUpdateDAG:
		if len(p.dagUpdates) > 0 {
			return fmt.Errorf("%w: ops[%d] duplicate update_dag; merge DAG metadata patch before calling ApplyOps", ErrApplyOpsInvalid, index)
		}
		p.dagUpdates = append(p.dagUpdates, op)
	case nodeexec.OpAddNode:
		if err := rememberNodeOp(seenNodeOps, v.Node.NodeKey, index, op.Kind()); err != nil {
			return err
		}
		p.adds = append(p.adds, op)
	case nodeexec.OpUpdateNode:
		if err := rememberNodeOp(seenNodeOps, v.NodeKey, index, op.Kind()); err != nil {
			return err
		}
		p.updates = append(p.updates, op)
	case nodeexec.OpRemoveNode:
		if err := rememberNodeOp(seenNodeOps, v.NodeKey, index, op.Kind()); err != nil {
			return err
		}
		p.removes = append(p.removes, op)
	default:
		return fmt.Errorf("%w: ops[%d] kind=%s", ErrLifecycleNotImplemented, index, op.Kind())
	}
	return nil
}

type seenNodeOp struct {
	index int
	kind  nodeexec.OpKind
}

func rememberNodeOp(seen map[string]seenNodeOp, nodeKey string, index int, kind nodeexec.OpKind) error {
	key := strings.TrimSpace(nodeKey)
	if key == "" {
		return nil
	}
	if prev, dup := seen[key]; dup {
		return fmt.Errorf("%w: %w: ops[%d] (%s) and ops[%d] (%s) both target node_key=%q",
			ErrApplyOpsInvalid, ErrDuplicateOpForNode, prev.index, prev.kind, index, kind, key)
	}
	seen[key] = seenNodeOp{index: index, kind: kind}
	return nil
}

func runOpsBatch(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, baseVersion int64, parts partitionedOps) (contract.ApplyOpsResponse, error) {
	current, existing, schedule, err := preflightOpsBatch(ctx, tx, dagKey, baseVersion, parts)
	if err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	if len(parts.dagUpdates) == 0 && len(parts.adds) == 0 && len(parts.updates) == 0 && len(parts.removes) == 0 {
		return contract.ApplyOpsResponse{NewVersion: current}, nil
	}
	plan, err := planOpsBatch(parts, existing, schedule)
	if err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	if err := persistOpsBatch(ctx, tx, dagKey, existing, plan); err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	newVersion, err := bumpDAGVersionTx(ctx, tx, dagKey, baseVersion)
	if err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	return contract.ApplyOpsResponse{NewVersion: newVersion}, nil
}

func preflightOpsBatch(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, baseVersion int64, parts partitionedOps) (int64, []taskdag.Node, taskdag.DAGSchedule, error) {
	current, err := tx.GetDAGVersionForUpdate(ctx, dagKey)
	if err != nil {
		return 0, nil, taskdag.DAGSchedule{}, fmt.Errorf("get dag version: %w", err)
	}
	if current != baseVersion {
		return 0, nil, taskdag.DAGSchedule{}, fmt.Errorf("%w: dag=%s expected=%d actual=%d", ErrVersionConflict, dagKey, baseVersion, current)
	}
	dag, err := tx.GetDAG(ctx, dagKey)
	if err != nil {
		return 0, nil, taskdag.DAGSchedule{}, fmt.Errorf("get dag: %w", err)
	}
	if dag == nil {
		return 0, nil, taskdag.DAGSchedule{}, fmt.Errorf("%w: dag=%s vanished mid-tx", ErrApplyOpsInvalid, dagKey)
	}
	activeRuns, err := tx.CountRunningRunsByDagKey(ctx, dagKey)
	if err != nil {
		return 0, nil, taskdag.DAGSchedule{}, fmt.Errorf("count running runs: %w", err)
	}
	existing, err := tx.ListNodes(ctx, dagKey)
	if err != nil {
		return 0, nil, taskdag.DAGSchedule{}, fmt.Errorf("list nodes: %w", err)
	}
	if err := enforceRunningDAGInvariants(dag.Status, activeRuns, parts, existing); err != nil {
		return 0, nil, taskdag.DAGSchedule{}, err
	}
	if err := rejectTerminalDAGOps(dag.Status); err != nil {
		return 0, nil, taskdag.DAGSchedule{}, err
	}
	schedule, err := dagScheduleForOps(ctx, tx, dagKey, parts)
	if err != nil {
		return 0, nil, taskdag.DAGSchedule{}, err
	}
	return current, existing, schedule, nil
}

func dagScheduleForOps(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, parts partitionedOps) (taskdag.DAGSchedule, error) {
	if len(parts.dagUpdates) == 0 {
		return taskdag.DAGSchedule{}, nil
	}
	schedule, err := tx.GetDAGSchedule(ctx, dagKey)
	if err != nil {
		return taskdag.DAGSchedule{}, fmt.Errorf("get dag schedule: %w", err)
	}
	return schedule, nil
}

func bumpDAGVersionTx(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, baseVersion int64) (int64, error) {
	newVersion, err := tx.BumpDAGVersion(ctx, dagKey, baseVersion)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return 0, fmt.Errorf("%w: dag=%s base_version=%d (lost lock?)", ErrVersionConflict, dagKey, baseVersion)
		}
		return 0, fmt.Errorf("bump version: %w", err)
	}
	return newVersion, nil
}

func enforceRunningDAGInvariants(dagStatus string, activeRuns int64, parts partitionedOps, existing []taskdag.Node) error {
	reason, running := runningDAGReason(dagStatus, activeRuns)
	if !running {
		return nil
	}
	if err := rejectMutableOpsInRunningDAG(reason, parts); err != nil {
		return err
	}
	return enforceRunningAddNodeDeps(reason, parts.adds, existing)
}

func rejectTerminalDAGOps(dagStatus string) error {
	switch strings.TrimSpace(dagStatus) {
	case "done", "failed", "cancelled", "skipped":
		return fmt.Errorf("%w: dag status %q is terminal; apply_ops not allowed", ErrApplyOpsInvalid, dagStatus)
	default:
		return nil
	}
}

func runningDAGReason(dagStatus string, activeRuns int64) (string, bool) {
	if dagStatus == "running" {
		return "dag is running", true
	}
	if activeRuns > 0 {
		return "dag has active running run", true
	}
	return "", false
}

func rejectMutableOpsInRunningDAG(reason string, parts partitionedOps) error {
	if len(parts.dagUpdates) > 0 {
		return fmt.Errorf("%w: %s, update_dag not allowed (only add_node depends_on done nodes)", ErrApplyOpsInvalid, reason)
	}
	if len(parts.updates) > 0 {
		return fmt.Errorf("%w: %s, update_node not allowed (only add_node depends_on done nodes)", ErrApplyOpsInvalid, reason)
	}
	if len(parts.removes) > 0 {
		return fmt.Errorf("%w: %s, remove_node not allowed (only add_node depends_on done nodes)", ErrApplyOpsInvalid, reason)
	}
	return nil
}

func enforceRunningAddNodeDeps(reason string, adds nodeexec.Ops, existing []taskdag.Node) error {
	doneSet := doneNodeKeys(existing)
	for i, op := range adds {
		add, ok := op.(nodeexec.OpAddNode)
		if !ok {
			return fmt.Errorf("%w: ops[%d] expected add_node, got %s", ErrApplyOpsInvalid, i, op.Kind())
		}
		for _, dep := range nodeexec.NormalizeDependsOn(add.Node.DependsOn) {
			if _, done := doneSet[dep]; !done {
				return fmt.Errorf("%w: %s, add_node %q depends_on %q must reference a done node", ErrApplyOpsInvalid, reason, add.Node.NodeKey, dep)
			}
		}
	}
	return nil
}

func doneNodeKeys(existing []taskdag.Node) map[string]struct{} {
	out := make(map[string]struct{}, len(existing))
	for _, n := range existing {
		if n.Status == "done" {
			out[n.NodeKey] = struct{}{}
		}
	}
	return out
}

type opsBatchPlan struct {
	dagPatches []plannedDAGPatch
	addSpecs   []nodeexec.NodeSpec
	updates    []nodeexec.UpdateNodeChange
	removes    []nodeexec.RemoveNodeChange
}

type plannedDAGPatch struct {
	patch     nodeexec.DAGPatch
	nextRunAt *time.Time
}

func planOpsBatch(parts partitionedOps, existing []taskdag.Node, schedule taskdag.DAGSchedule) (opsBatchPlan, error) {
	dagPatches, err := planDAGUpdates(parts.dagUpdates, schedule)
	if err != nil {
		return opsBatchPlan{}, err
	}
	adjAdd, addSpecs, err := nodeexec.PlanAddNodes(parts.adds, existingNodesForPlan(existing))
	if err != nil {
		return opsBatchPlan{}, fmt.Errorf("%w: %w", ErrApplyOpsInvalid, err)
	}
	extended := existingFullForPlan(existing, addSpecs)
	adjUpd, changes, err := nodeexec.PlanUpdateNodes(parts.updates, extended)
	if err != nil {
		return opsBatchPlan{}, fmt.Errorf("%w: %w", ErrApplyOpsInvalid, err)
	}
	merged := mergeAdjacency(adjAdd, adjUpd)
	pruned, removes, err := nodeexec.PlanRemoveNodes(parts.removes, extended, merged)
	if err != nil {
		return opsBatchPlan{}, fmt.Errorf("%w: %w", ErrApplyOpsInvalid, err)
	}
	if cycleErr := nodeexec.DetectCycle(pruned); cycleErr != nil {
		return opsBatchPlan{}, fmt.Errorf("%w: %w", ErrApplyOpsInvalid, cycleErr)
	}
	return opsBatchPlan{dagPatches: dagPatches, addSpecs: addSpecs, updates: changes, removes: removes}, nil
}

func planDAGUpdates(ops nodeexec.Ops, current taskdag.DAGSchedule) ([]plannedDAGPatch, error) {
	patches := make([]plannedDAGPatch, 0, len(ops))
	for i, op := range ops {
		update, ok := op.(nodeexec.OpUpdateDAG)
		if !ok {
			return nil, fmt.Errorf("%w: ops[%d] expected update_dag, got %s", ErrApplyOpsInvalid, i, op.Kind())
		}
		patch := normalizeDAGPatch(update.Patch)
		finalSchedule, err := validateDAGPatch(patch, current)
		if err != nil {
			return nil, err
		}
		patches = append(patches, plannedDAGPatch{
			patch:     patch,
			nextRunAt: nextRunAtForFinalSchedule(patch, finalSchedule),
		})
	}
	return patches, nil
}

func normalizeDAGPatch(patch nodeexec.DAGPatch) nodeexec.DAGPatch {
	return nodeexec.DAGPatch{
		Title:       trimStringPtr(patch.Title),
		Description: trimStringPtr(patch.Description),
		Trigger:     trimStringPtr(patch.Trigger),
		CronExpr:    trimStringPtr(patch.CronExpr),
		OwnerID:     trimStringPtr(patch.OwnerID),
	}
}

func validateDAGPatch(patch nodeexec.DAGPatch, current taskdag.DAGSchedule) (taskdag.DAGSchedule, error) {
	if isEmptyDAGPatch(patch) {
		return taskdag.DAGSchedule{}, fmt.Errorf("%w: update_dag patch must set at least one field", ErrApplyOpsInvalid)
	}
	if err := validateDAGPatchTrigger(patch.Trigger); err != nil {
		return taskdag.DAGSchedule{}, err
	}
	if err := validateDAGPatchCronExpr(patch.CronExpr); err != nil {
		return taskdag.DAGSchedule{}, err
	}
	finalSchedule := finalDAGSchedule(current, patch)
	if err := validateDAGPatchFinalSchedule(patch, finalSchedule); err != nil {
		return taskdag.DAGSchedule{}, err
	}
	return finalSchedule, nil
}

func validateDAGPatchTrigger(trigger *string) error {
	if trigger != nil && !isValidDAGTrigger(*trigger) {
		return fmt.Errorf("%w: update_dag trigger %q must be one of manual/auto/scheduled/external", ErrApplyOpsInvalid, *trigger)
	}
	return nil
}

func validateDAGPatchCronExpr(cronExpr *string) error {
	if cronExpr != nil && *cronExpr != "" {
		if _, err := dagPatchCronParser.Parse(*cronExpr); err != nil {
			return fmt.Errorf("%w: update_dag cron_expr %q invalid: %v", ErrApplyOpsInvalid, *cronExpr, err)
		}
	}
	return nil
}

func finalDAGSchedule(current taskdag.DAGSchedule, patch nodeexec.DAGPatch) taskdag.DAGSchedule {
	schedule := taskdag.DAGSchedule{
		Trigger:  strings.TrimSpace(current.Trigger),
		CronExpr: strings.TrimSpace(current.CronExpr),
	}
	if schedule.Trigger == "" {
		schedule.Trigger = "manual"
	}
	if patch.Trigger != nil {
		schedule.Trigger = *patch.Trigger
	}
	if patch.CronExpr != nil {
		schedule.CronExpr = *patch.CronExpr
	}
	return schedule
}

func validateDAGPatchFinalSchedule(patch nodeexec.DAGPatch, final taskdag.DAGSchedule) error {
	if patch.Trigger == nil && patch.CronExpr == nil {
		return nil
	}
	if final.Trigger == "scheduled" {
		if final.CronExpr == "" {
			return fmt.Errorf("%w: update_dag final trigger=scheduled requires non-empty cron_expr", ErrApplyOpsInvalid)
		}
		if _, err := dagPatchCronParser.Parse(final.CronExpr); err != nil {
			return fmt.Errorf("%w: update_dag final cron_expr %q invalid: %v", ErrApplyOpsInvalid, final.CronExpr, err)
		}
		return nil
	}
	if final.CronExpr != "" {
		return fmt.Errorf("%w: update_dag cron_expr is allowed only when final trigger=scheduled; clear cron_expr when changing trigger away from scheduled", ErrApplyOpsInvalid)
	}
	return nil
}

func isEmptyDAGPatch(patch nodeexec.DAGPatch) bool {
	return patch.Title == nil &&
		patch.Description == nil &&
		patch.Trigger == nil &&
		patch.CronExpr == nil &&
		patch.OwnerID == nil
}

func isValidDAGTrigger(trigger string) bool {
	return trigger == "manual" || trigger == "auto" || trigger == "scheduled" || trigger == "external"
}

var dagPatchCronParser = robcron.NewParser(
	robcron.Minute | robcron.Hour | robcron.Dom | robcron.Month | robcron.Dow | robcron.Descriptor,
)

func trimStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func existingNodesForPlan(nodes []taskdag.Node) []nodeexec.ExistingNode {
	out := make([]nodeexec.ExistingNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeexec.ExistingNode{
			NodeKey:   n.NodeKey,
			DependsOn: decodeDependsOn(n.DependsOn),
		})
	}
	return out
}

func existingFullForPlan(nodes []taskdag.Node, addSpecs []nodeexec.NodeSpec) []nodeexec.ExistingNodeFull {
	out := make([]nodeexec.ExistingNodeFull, 0, len(nodes)+len(addSpecs))
	for _, n := range nodes {
		out = append(out, nodeexec.ExistingNodeFull{
			NodeKey:   n.NodeKey,
			DependsOn: decodeDependsOn(n.DependsOn),
			Status:    n.Status,
		})
	}
	for _, spec := range addSpecs {
		out = append(out, nodeexec.ExistingNodeFull{
			NodeKey:   spec.NodeKey,
			DependsOn: spec.DependsOn,
			Status:    string(nodeexec.NodeStatusPending),
		})
	}
	return out
}

func mergeAdjacency(adjAdd, adjUpd map[string][]string) map[string][]string {
	merged := make(map[string][]string, len(adjAdd)+len(adjUpd))
	maps.Copy(merged, adjUpd)
	for k, v := range adjAdd {
		if _, ok := merged[k]; !ok {
			merged[k] = v
		}
	}
	return merged
}

func persistOpsBatch(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, existing []taskdag.Node, plan opsBatchPlan) error {
	if err := persistDAGPatches(ctx, tx, dagKey, plan.dagPatches); err != nil {
		return err
	}
	if err := persistAddNodeSpecs(ctx, tx, dagKey, plan.addSpecs); err != nil {
		return err
	}
	if err := persistUpdateChanges(ctx, tx, dagKey, existing, plan.updates); err != nil {
		return err
	}
	return persistRemoveChanges(ctx, tx, dagKey, plan.removes)
}

func persistDAGPatches(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, patches []plannedDAGPatch) error {
	for _, patch := range patches {
		rows, err := tx.UpdateDAGPatch(ctx, dagPatchInput(dagKey, patch))
		if err != nil {
			return fmt.Errorf("update dag patch %s: %w", dagKey, err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: update_dag %s: no rows affected", ErrApplyOpsInvalid, dagKey)
		}
	}
	return nil
}

func dagPatchInput(dagKey string, planned plannedDAGPatch) taskdag.UpdateDAGPatchInput {
	patch := planned.patch
	return taskdag.UpdateDAGPatchInput{
		DagKey:      dagKey,
		Title:       cloneStringPtr(patch.Title),
		Description: cloneStringPtr(patch.Description),
		Trigger:     cloneStringPtr(patch.Trigger),
		CronExpr:    cloneStringPtr(patch.CronExpr),
		OwnerID:     cloneStringPtr(patch.OwnerID),
		NextRunAt:   planned.nextRunAt,
	}
}

func nextRunAtForFinalSchedule(patch nodeexec.DAGPatch, schedule taskdag.DAGSchedule) *time.Time {
	if patch.Trigger == nil && patch.CronExpr == nil {
		return nil
	}
	if schedule.Trigger != "scheduled" || schedule.CronExpr == "" {
		return nil
	}
	parsed, err := dagPatchCronParser.Parse(schedule.CronExpr)
	if err != nil {
		return nil
	}
	next := parsed.Next(time.Now().UTC())
	return &next
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func persistAddNodeSpecs(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, specs []nodeexec.NodeSpec) error {
	for _, spec := range specs {
		node := taskdag.Node{
			DagKey:    dagKey,
			NodeKey:   strings.TrimSpace(spec.NodeKey),
			Title:     strings.TrimSpace(spec.Title),
			NodeType:  strings.TrimSpace(spec.NodeType),
			DependsOn: dependsOnJSON(spec.DependsOn),
			Config:    append(json.RawMessage(nil), spec.Config...),
		}
		if _, err := tx.UpsertNode(ctx, node); err != nil {
			return fmt.Errorf("upsert node %s: %w", node.NodeKey, err)
		}
	}
	return nil
}

func persistUpdateChanges(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, existing []taskdag.Node, changes []nodeexec.UpdateNodeChange) error {
	byKey := indexExistingByKey(existing)
	for _, c := range changes {
		old, ok := byKey[c.NodeKey]
		if !ok {
			return fmt.Errorf("update_node: node %q vanished between plan and persist", c.NodeKey)
		}
		merged := mergeNodePatch(old, c.Patch, dagKey)
		if _, err := tx.UpsertNode(ctx, merged); err != nil {
			return fmt.Errorf("upsert (update) node %s: %w", c.NodeKey, err)
		}
	}
	return nil
}

func persistRemoveChanges(ctx context.Context, tx taskdag.DAGOpsStore, dagKey string, changes []nodeexec.RemoveNodeChange) error {
	for _, c := range changes {
		rows, err := tx.DeleteNode(ctx, dagKey, c.NodeKey)
		if err != nil {
			return fmt.Errorf("delete node %s: %w", c.NodeKey, err)
		}
		if rows == 0 {
			return fmt.Errorf("%w: delete node %s: no rows affected (node status changed or node missing)", ErrApplyOpsInvalid, c.NodeKey)
		}
	}
	return nil
}

func indexExistingByKey(nodes []taskdag.Node) map[string]taskdag.Node {
	out := make(map[string]taskdag.Node, len(nodes))
	for _, n := range nodes {
		out[n.NodeKey] = n
	}
	return out
}

func mergeNodePatch(old taskdag.Node, patch nodeexec.NodePatch, dagKey string) taskdag.Node {
	merged := old
	merged.DagKey = dagKey
	if patch.Title != nil {
		merged.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.AssignedTo != nil {
		merged.AssignedTo = strings.TrimSpace(*patch.AssignedTo)
	}
	if patch.DependsOn != nil {
		merged.DependsOn = dependsOnJSON(nodeexec.NormalizeDependsOn(*patch.DependsOn))
	}
	if !isEmptyRawJSON(patch.Config) {
		merged.Config = append(json.RawMessage(nil), patch.Config...)
	}
	return merged
}

func isEmptyRawJSON(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	return strings.TrimSpace(string(raw)) == "null"
}
