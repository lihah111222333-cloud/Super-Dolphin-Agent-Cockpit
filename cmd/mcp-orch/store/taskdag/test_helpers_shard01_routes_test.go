//go:build legacy_pg_fake

package taskdag

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeTaskDAGExecRoute struct {
	token string
	run   func(*fakeTaskDAGDB, ...any) (int64, error)
}

var fakeTaskDAGExecRoutes = []fakeTaskDAGExecRoute{
	{token: "BindTaskDagWakeupTurn", run: (*fakeTaskDAGDB).bindWakeupTurn},
	{token: "MarkTaskDagWakeupSent", run: (*fakeTaskDAGDB).markWakeupSent},
	{token: "RetryTaskDagWakeup", run: (*fakeTaskDAGDB).retryWakeup},
	{token: "FailTaskDagWakeup", run: (*fakeTaskDAGDB).failWakeup},
	{token: "ReclaimStaleDispatchingTaskDagWakeups", run: func(db *fakeTaskDAGDB, _ ...any) (int64, error) { return db.reclaimStaleWakeups() }},
	{token: "EnqueueTaskDagWakeup", run: (*fakeTaskDAGDB).enqueueWakeup},
	{token: "CloneTaskDagNodesForRun", run: (*fakeTaskDAGDB).cloneTaskDagNodesForRun},
	{token: "PromoteRootNodesToReady", run: (*fakeTaskDAGDB).promoteRootNodesToReady},
	{token: "CancelTaskDagRunWakeups", run: (*fakeTaskDAGDB).cancelTaskDagRunWakeups},
	{token: "CascadeFailPendingTaskDagNode", run: (*fakeTaskDAGDB).cascadeFailPendingNode},
	{token: "PromoteSingleNodePendingToReady", run: (*fakeTaskDAGDB).promoteSingleNodePendingToReady},
	{token: "DeleteTaskDagWakeupsByDAG", run: (*fakeTaskDAGDB).deleteTaskDagWakeupsByDAG},
	{token: "DeleteTaskDagNodesByDAG", run: (*fakeTaskDAGDB).deleteTaskDagNodesByDAG},
	{token: "DeleteTaskDagRunsByDAG", run: (*fakeTaskDAGDB).deleteTaskDagRunsByDAG},
	{token: "DeleteTaskDagRow", run: (*fakeTaskDAGDB).deleteTaskDAGRow},
	{token: "DeleteTaskDagNode", run: (*fakeTaskDAGDB).deleteTaskDagNode},
}

func (db *fakeTaskDAGDB) execCommandLocked(sql string, args ...any) (pgconn.CommandTag, error) {
	for _, route := range fakeTaskDAGExecRoutes {
		if strings.Contains(sql, route.token) {
			return updateTag(route.run(db, args...))
		}
	}
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec call: %s", firstLine(sql))
}

type fakeTaskDAGQueryRoute struct {
	token string
	run   func(*fakeTaskDAGDB, ...any) ([][]any, error)
}

var fakeTaskDAGQueryRoutes = []fakeTaskDAGQueryRoute{
	{token: "ClaimDueTaskDagWakeups", run: (*fakeTaskDAGDB).claimDueWakeups},
	{token: "ListTaskDagRunNodes", run: (*fakeTaskDAGDB).listTaskDagRunNodes},
	{token: "ListTaskDagNodes", run: (*fakeTaskDAGDB).listTaskDagNodes},
	{token: "LookupNodesBySpawningThread", run: (*fakeTaskDAGDB).lookupNodesBySpawningThread},
	{token: "FinalizeTaskDagRunIfAllNodesTerminal", run: (*fakeTaskDAGDB).finalizeRunIfAllNodesTerminal},
	{token: "CancelTaskDagRunNodes", run: (*fakeTaskDAGDB).cancelTaskDagRunNodesReturningThreads},
}

func (db *fakeTaskDAGDB) queryRowsLocked(sql string, args ...any) ([][]any, error) {
	for _, route := range fakeTaskDAGQueryRoutes {
		if strings.Contains(sql, route.token) {
			return route.run(db, args...)
		}
	}
	return nil, fmt.Errorf("unexpected Query call: %s", firstLine(sql))
}

type fakeTaskDAGQueryRowRoute struct {
	tokens []string
	run    func(*fakeTaskDAGDB, ...any) ([]any, error)
}

var fakeTaskDAGQueryRowRoutes = []fakeTaskDAGQueryRowRoute{
	{tokens: []string{"CountActiveTaskDagRunsByKey"}, run: (*fakeTaskDAGDB).countActiveTaskDagRunsByKey},
	{tokens: []string{"LockTaskDagForDelete"}, run: (*fakeTaskDAGDB).lockTaskDAGForDelete},
	{tokens: []string{"LockTaskDagRunForCompletionForUpdate"}, run: (*fakeTaskDAGDB).lockTaskDAGRunForCompletion},
	{tokens: []string{"GetTaskDagRunNodeForUpdate"}, run: (*fakeTaskDAGDB).lockTaskDagRunNodeForUpdate},
	{tokens: []string{"AssignTaskDagNode"}, run: (*fakeTaskDAGDB).assignNode},
	{tokens: []string{"BindRunningTaskDagNodeTurn"}, run: (*fakeTaskDAGDB).bindRunningNodeTurn},
	{tokens: []string{"CompleteTaskDagNode"}, run: (*fakeTaskDAGDB).completeNode},
	{tokens: []string{"FailTaskDagNodeIfNonTerminal"}, run: (*fakeTaskDAGDB).failNodeIfNonTerminal},
	{tokens: []string{"PatchTaskDagNodeConfigIfUnchanged"}, run: (*fakeTaskDAGDB).patchNodeConfigIfUnchanged},
	{tokens: []string{"ClaimTaskDagNodeOutputMaterialization"}, run: (*fakeTaskDAGDB).claimNodeOutputMaterialization},
	{tokens: []string{"UpdateTaskDagNodeStatusIfCurrent"}, run: (*fakeTaskDAGDB).updateNodeStatusIfCurrent},
	{tokens: []string{"UpdateRunningTaskDagNodeStatus"}, run: (*fakeTaskDAGDB).updateRunningNodeStatus},
	{tokens: []string{"UpdateTaskDagNodeSpawningThread"}, run: (*fakeTaskDAGDB).updateNodeSpawningThread},
	{tokens: []string{"GetTaskDagRun"}, run: (*fakeTaskDAGDB).getTaskDagRun},
	{tokens: []string{"CancelTaskDagRun"}, run: (*fakeTaskDAGDB).cancelTaskDagRun},
	{tokens: []string{"AppendTaskDagRunEvent"}, run: (*fakeTaskDAGDB).appendRunEvent},
	{tokens: []string{"jsonb_build_array($2::jsonb)"}, run: (*fakeTaskDAGDB).appendRunEvent},
	{tokens: []string{"task_dag_runs", "jsonb_array_length(events ||"}, run: (*fakeTaskDAGDB).appendRunEvent},
}

func (db *fakeTaskDAGDB) queryRowValuesLocked(sql string, args ...any) ([]any, error) {
	for _, route := range fakeTaskDAGQueryRowRoutes {
		if queryRowRouteMatches(sql, route.tokens) {
			return route.run(db, args...)
		}
	}
	return nil, fmt.Errorf("unexpected QueryRow call: %s", firstLine(sql))
}

func queryRowRouteMatches(sql string, tokens []string) bool {
	for _, token := range tokens {
		if !strings.Contains(sql, token) {
			return false
		}
	}
	return true
}

func closedTaskDAGQueryRow() pgx.Row {
	return stubTaskDAGRow{err: pgx.ErrTxClosed}
}
