//go:build legacy_pg_fake

package taskdag

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

type fakeFinalizeRunNodeTotals struct {
	total       int
	nonTerminal int
	failed      int
	cancelled   int
}

func (db *fakeTaskDAGDB) finalizeRunNodeTotals(dagKey string, runID int64) fakeFinalizeRunNodeTotals {
	var totals fakeFinalizeRunNodeTotals
	for _, node := range db.nodes {
		if node.DagKey != dagKey || fakeRunID(node) != runID {
			continue
		}
		totals.total++
		accumulateFinalizeStatus(&totals, node.Status)
	}
	return totals
}

func accumulateFinalizeStatus(totals *fakeFinalizeRunNodeTotals, status string) {
	switch status {
	case "done", "skipped":
		return
	case "failed":
		totals.failed++
	case "cancelled":
		totals.cancelled++
	default:
		totals.nonTerminal++
	}
}

func (totals fakeFinalizeRunNodeTotals) ready() bool {
	return totals.total > 0 && totals.nonTerminal == 0
}

func (totals fakeFinalizeRunNodeTotals) status() string {
	switch {
	case totals.failed > 0:
		return "failed"
	case totals.cancelled > 0:
		return "cancelled"
	default:
		return "succeeded"
	}
}

func (db *fakeTaskDAGDB) finalizeMatchingRuns(dagKey string, runID int64, finalStatus string) ([][]any, error) {
	rows := make([][]any, 0, 1)
	for runKey, run := range db.runs {
		if !isMatchingRunningRun(run, dagKey, runID) {
			continue
		}
		updated, err := db.finalizedRunRow(dagKey, runID, finalStatus, run)
		if err != nil {
			return nil, err
		}
		db.runs[runKey] = updated
		rows = append(rows, []any{updated.RunKey, updated.Status})
	}
	return rows, nil
}

func (db *fakeTaskDAGDB) finalizedRunRow(dagKey string, runID int64, finalStatus string, run sqlc.TaskDagRun) (sqlc.TaskDagRun, error) {
	run.Status = finalStatus
	run.FinishedAt = timestamptzValue(db.now)
	run.UpdatedAt = timestamptzValue(db.now)
	if finalStatus == "succeeded" {
		metadata, err := db.metadataWithFinalOutput(dagKey, runID, run.Metadata)
		if err != nil {
			return run, err
		}
		run.Metadata = metadata
	}
	return run, nil
}

func (db *fakeTaskDAGDB) finalNodeKeyForDAG(dagKey string) (string, bool, error) {
	dag, ok := db.dags[dagKey]
	if !ok || len(dag.Metadata) == 0 {
		return "", false, nil
	}
	var dagMeta struct {
		FinalNodeKey string `json:"final_node_key"`
	}
	if err := json.Unmarshal(dag.Metadata, &dagMeta); err != nil {
		return "", false, fmt.Errorf("decode dag metadata: %w", err)
	}
	if dagMeta.FinalNodeKey == "" {
		return "", false, nil
	}
	return dagMeta.FinalNodeKey, true, nil
}

func mergeFinalOutputMetadata(metadata []byte, finalOutput map[string]any) ([]byte, error) {
	runMetadata, err := decodeRunMetadata(metadata)
	if err != nil {
		return nil, err
	}
	runMetadata["final_output"] = finalOutput
	encoded, err := json.Marshal(runMetadata)
	if err != nil {
		return nil, fmt.Errorf("encode run metadata: %w", err)
	}
	return encoded, nil
}

func decodeRunMetadata(metadata []byte) (map[string]any, error) {
	runMetadata := map[string]any{}
	if len(metadata) == 0 {
		return runMetadata, nil
	}
	var raw any
	if err := json.Unmarshal(metadata, &raw); err != nil {
		return nil, fmt.Errorf("decode run metadata: %w", err)
	}
	if obj, ok := raw.(map[string]any); ok {
		runMetadata = obj
	}
	return runMetadata, nil
}

func baseFinalOutput(node sqlc.TaskDagNode) map[string]any {
	title := node.Title
	if title == "" {
		title = "Final output"
	}
	return map[string]any{
		"role":            "final_output",
		"title":           title,
		"source_node_key": node.NodeKey,
	}
}

func finalOutputFromConfiguredPath(out map[string]any, configuredPath string) (map[string]any, bool, error) {
	if configuredPath == "" {
		return nil, false, nil
	}
	return finalOutputWithFile(out, configuredPath), true, nil
}

func finalOutputFromResultMap(out, typed map[string]any, configuredPath string, result any) map[string]any {
	if path := sharedfilePathFromResultMap(typed); path != "" {
		return finalOutputWithFile(out, path)
	}
	if configuredPath != "" {
		return finalOutputWithFile(out, configuredPath)
	}
	out["kind"] = "json"
	out["result"] = result
	return out
}

func sharedfilePathFromResultMap(typed map[string]any) string {
	sf, ok := typed["sharedfile"].(map[string]any)
	if !ok {
		return ""
	}
	path, ok := sf["path"].(string)
	if !ok {
		return ""
	}
	return path
}

func finalOutputFromResultString(out map[string]any, text, configuredPath string) map[string]any {
	if configuredPath != "" {
		return finalOutputWithFile(out, configuredPath)
	}
	out["kind"] = "text"
	out["text"] = text
	return out
}

func finalOutputFromFallback(out map[string]any, configuredPath string, result any) map[string]any {
	if configuredPath != "" {
		return finalOutputWithFile(out, configuredPath)
	}
	out["kind"] = "json"
	out["result"] = result
	return out
}

func finalOutputWithFile(out map[string]any, path string) map[string]any {
	out["kind"] = "file"
	out["path"] = path
	return out
}

func isMatchingRunningRun(run sqlc.TaskDagRun, dagKey string, runID int64) bool {
	return run.DagKey == dagKey && run.Status == "running" && run.ID == runID
}

func (db *fakeTaskDAGDB) finalizeRunIfAllNodesTerminal(args ...any) ([][]any, error) {
	if err := requireFakeTaskDAGArgs(args, 2, "finalize",
		fakeTaskDAGTypedArg[string](0, "finalize dag_key"),
		fakeTaskDAGInt8Arg(1, "finalize run_id")); err != nil {
		return nil, err
	}
	dagKey := args[0].(string)
	runID, err := fakeInt8Arg(args, 1, "finalize run_id")
	if err != nil {
		return nil, err
	}
	totals := db.finalizeRunNodeTotals(dagKey, runID)
	if !totals.ready() {
		return nil, nil
	}
	return db.finalizeMatchingRuns(dagKey, runID, totals.status())
}

func (db *fakeTaskDAGDB) metadataWithFinalOutput(dagKey string, runID int64, metadata []byte) ([]byte, error) {
	finalNodeKey, ok, err := db.finalNodeKeyForDAG(dagKey)
	if err != nil || !ok {
		return metadata, err
	}
	node, ok := db.nodes[dagNodeLookupKey(dagKey, finalNodeKey, runID)]
	if !ok {
		return metadata, nil
	}
	finalOutput, ok, err := finalOutputFromNodeResult(node)
	if err != nil || !ok {
		return metadata, err
	}
	return mergeFinalOutputMetadata(metadata, finalOutput)
}

func finalOutputFromNodeResult(node sqlc.TaskDagNode) (map[string]any, bool, error) {
	out := baseFinalOutput(node)
	configuredPath := configuredSharedfilePathFromNodeConfig(node.Config)
	if len(node.Result) == 0 {
		return finalOutputFromConfiguredPath(out, configuredPath)
	}
	var result any
	if err := json.Unmarshal(node.Result, &result); err != nil {
		return nil, false, fmt.Errorf("decode final node result: %w", err)
	}
	switch typed := result.(type) {
	case map[string]any:
		return finalOutputFromResultMap(out, typed, configuredPath, result), true, nil
	case string:
		return finalOutputFromResultString(out, typed, configuredPath), true, nil
	default:
		return finalOutputFromFallback(out, configuredPath, result), true, nil
	}
}

func configuredSharedfilePathFromNodeConfig(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var cfg struct {
		Outputs struct {
			ToSharedfile *struct {
				Path string `json:"path"`
			} `json:"to_sharedfile"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg.Outputs.ToSharedfile == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Outputs.ToSharedfile.Path)
}
