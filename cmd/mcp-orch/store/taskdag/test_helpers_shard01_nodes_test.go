//go:build legacy_pg_fake

package taskdag

import (
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/jackc/pgx/v5"
)

func (db *fakeTaskDAGDB) deleteTaskDagNode(args ...any) (int64, error) {
	if len(args) != 2 {
		return 0, fmt.Errorf("delete node args len = %d, want 2", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return 0, fmt.Errorf("dag key arg = %T", args[0])
	}
	nodeKey, ok := args[1].(string)
	if !ok {
		return 0, fmt.Errorf("node key arg = %T", args[1])
	}
	key := dagNodeKey(dagKey, nodeKey)
	row, ok := db.nodes[key]
	if !ok {
		return 0, nil
	}
	if row.Status != "pending" && row.Status != "ready" {
		return 0, nil
	}
	delete(db.nodes, key)
	return 1, nil
}

func (db *fakeTaskDAGDB) lockTaskDAGForDelete(args ...any) ([]any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("lock dag args len = %d, want 1", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[0])
	}
	row, ok := db.dags[dagKey]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return []any{row.ID}, nil
}

func (db *fakeTaskDAGDB) lockTaskDagRunNodeForUpdate(args ...any) ([]any, error) {
	if len(args) != 3 {
		return nil, fmt.Errorf("lock run node args len = %d, want 3", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[0])
	}
	nodeKey, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("node key arg = %T", args[1])
	}
	runID, err := fakeInt8Arg(args, 2, "run id")
	if err != nil {
		return nil, err
	}
	key := dagRunNodeKey(dagKey, nodeKey, runID)
	row, ok := db.nodes[key]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	db.locks[key] = true
	return taskDagNodeValues(row), nil
}

func (db *fakeTaskDAGDB) deleteTaskDagWakeupsByDAG(args ...any) (int64, error) {
	dagKey, err := deleteDAGKeyArg(args)
	if err != nil {
		return 0, err
	}
	var rows int64
	for key, row := range db.wakeups {
		if row.DagKey == dagKey {
			delete(db.wakeups, key)
			rows++
		}
	}
	return rows, nil
}

func (db *fakeTaskDAGDB) deleteTaskDagNodesByDAG(args ...any) (int64, error) {
	dagKey, err := deleteDAGKeyArg(args)
	if err != nil {
		return 0, err
	}
	var rows int64
	for key, row := range db.nodes {
		if row.DagKey == dagKey {
			delete(db.nodes, key)
			rows++
		}
	}
	return rows, nil
}

func (db *fakeTaskDAGDB) deleteTaskDagRunsByDAG(args ...any) (int64, error) {
	dagKey, err := deleteDAGKeyArg(args)
	if err != nil {
		return 0, err
	}
	var rows int64
	for key, row := range db.runs {
		if row.DagKey == dagKey {
			delete(db.runs, key)
			rows++
		}
	}
	return rows, nil
}

func (db *fakeTaskDAGDB) deleteTaskDAGRow(args ...any) (int64, error) {
	dagKey, err := deleteDAGKeyArg(args)
	if err != nil {
		return 0, err
	}
	if _, ok := db.dags[dagKey]; !ok {
		return 0, nil
	}
	delete(db.dags, dagKey)
	return 1, nil
}

func deleteDAGKeyArg(args []any) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("delete dag args len = %d, want 1", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return "", fmt.Errorf("dag key arg = %T", args[0])
	}
	return dagKey, nil
}

func (db *fakeTaskDAGDB) assignNode(args ...any) ([]any, error) {
	if len(args) != 4 {
		return nil, fmt.Errorf("assign node args len = %d, want 4", len(args))
	}
	assignedTo, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("assigned_to arg = %T", args[0])
	}
	dagKey, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[1])
	}
	nodeKey, ok := args[2].(string)
	if !ok {
		return nil, fmt.Errorf("node key arg = %T", args[2])
	}
	runID, err := fakeInt8Arg(args, 3, "run id")
	if err != nil {
		return nil, err
	}
	row, ok := db.nodes[dagRunNodeKey(dagKey, nodeKey, runID)]
	if !ok || (row.Status != "pending" && row.Status != "ready") {
		return nil, pgx.ErrNoRows
	}
	row.AssignedTo = assignedTo
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[dagRunNodeKey(dagKey, nodeKey, runID)] = row
	return taskDagNodeValues(row), nil
}

func (db *fakeTaskDAGDB) countActiveTaskDagRunsByKey(args ...any) ([]any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("count active runs args len = %d, want 1", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("dag key arg = %T", args[0])
	}
	var active int64
	for _, run := range db.runs {
		if run.DagKey == dagKey && run.Status == "running" {
			active++
		}
	}
	return []any{active}, nil
}

func (db *fakeTaskDAGDB) cloneTaskDagNodesForRun(args ...any) (int64, error) {
	if len(args) != 2 {
		return 0, fmt.Errorf("clone nodes args len = %d, want 2", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return 0, fmt.Errorf("dag key arg = %T", args[0])
	}
	runID, err := fakeInt8Arg(args, 1, "run id")
	if err != nil {
		return 0, err
	}
	var cloned int64
	for _, row := range db.nodes {
		if row.DagKey != dagKey || row.RunID.Valid {
			continue
		}
		key := dagRunNodeKey(dagKey, row.NodeKey, runID)
		if _, exists := db.nodes[key]; exists {
			continue
		}
		copy := cloneTaskDagNode(row)
		copy.ID = int64(len(db.nodes) + 1)
		copy.RunID = sqlc.Int8{Int64: runID, Valid: true}
		copy.Status = "pending"
		copy.Result = nil
		copy.StartedAt = sqlc.Timestamptz{}
		copy.FinishedAt = sqlc.Timestamptz{}
		copy.ActiveTurnID = sqlc.Text{}
		copy.ActiveWakeupID = sqlc.Int8{}
		copy.LastEventAt = sqlc.Timestamptz{}
		copy.SpawningThreadID = sqlc.Text{}
		copy.CreatedAt = timestamptzValue(db.now)
		copy.UpdatedAt = timestamptzValue(db.now)
		db.nodes[key] = copy
		cloned++
	}
	return cloned, nil
}

func (db *fakeTaskDAGDB) promoteRootNodesToReady(args ...any) (int64, error) {
	if len(args) != 2 {
		return 0, fmt.Errorf("promote root args len = %d, want 2", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return 0, fmt.Errorf("dag key arg = %T", args[0])
	}
	runID, err := fakeInt8Arg(args, 1, "run id")
	if err != nil {
		return 0, err
	}
	var promoted int64
	for key, row := range db.nodes {
		if row.DagKey != dagKey || fakeRunID(row) != runID || row.Status != "pending" {
			continue
		}
		deps, err := decodeDependsOn(row.DependsOn)
		if err != nil {
			return 0, err
		}
		if len(deps) != 0 {
			continue
		}
		row.Status = "ready"
		row.UpdatedAt = timestamptzValue(db.now)
		db.nodes[key] = row
		promoted++
	}
	return promoted, nil
}

func (db *fakeTaskDAGDB) promoteSingleNodePendingToReady(args ...any) (int64, error) {
	if len(args) != 3 {
		return 0, fmt.Errorf("promote args len = %d, want 3", len(args))
	}
	dagKey, ok := args[0].(string)
	if !ok {
		return 0, fmt.Errorf("dag key arg = %T", args[0])
	}
	nodeKey, ok := args[1].(string)
	if !ok {
		return 0, fmt.Errorf("node key arg = %T", args[1])
	}
	runID, err := fakeInt8Arg(args, 2, "run id")
	if err != nil {
		return 0, err
	}
	key := dagNodeLookupKey(dagKey, nodeKey, runID)
	row, ok := db.nodes[key]
	if !ok || row.Status != "pending" {
		return 0, nil
	}
	row.Status = "ready"
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	return 1, nil
}

func (db *fakeTaskDAGDB) cascadeFailPendingNode(args ...any) (int64, error) {
	if len(args) != 4 {
		return 0, fmt.Errorf("cascade fail args len = %d, want 4", len(args))
	}
	result, ok := args[0].([]byte)
	if !ok {
		return 0, fmt.Errorf("result arg = %T", args[0])
	}
	dagKey, ok := args[1].(string)
	if !ok {
		return 0, fmt.Errorf("dag key arg = %T", args[1])
	}
	nodeKey, ok := args[2].(string)
	if !ok {
		return 0, fmt.Errorf("node key arg = %T", args[2])
	}
	runID, err := fakeInt8Arg(args, 3, "run id")
	if err != nil {
		return 0, err
	}
	key := dagNodeLookupKey(dagKey, nodeKey, runID)
	if db.beforeFailNonTerminal != nil {
		db.beforeFailNonTerminal(dagKey, nodeKey)
	}
	row, ok := db.nodes[key]
	if !ok || row.Status != "pending" {
		return 0, nil
	}
	row.Status = "failed"
	row.Result = append([]byte(nil), result...)
	row.UpdatedAt = timestamptzValue(db.now)
	db.nodes[key] = row
	return 1, nil
}
