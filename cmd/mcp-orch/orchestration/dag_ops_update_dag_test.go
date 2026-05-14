package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// F4.4 update_dag 真实业务实现单测。覆盖矩阵：
//   - happy: 更新 DAG 元数据字段并推进 version
//   - reject: running DAG / active run 下不允许 update_dag
//   - reject: trigger 非枚举值
//   - reject: 同批多个 update_dag 语义歧义

func TestApplyOps_UpdateDAG_Happy(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{currentVersion: 7}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 7,
		Ops: json.RawMessage(`[
			{"op":"update_dag","patch":{
				"title":"  Daily Report  ",
				"description":"  Morning summary  ",
				"trigger":" scheduled ",
				"cron_expr":" 0 8 * * * ",
				"owner_id":" owner-1 "
			}}
		]`),
	}

	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyOps update_dag err = %v, want nil", err)
	}
	if resp.NewVersion != 8 {
		t.Fatalf("NewVersion = %d, want 8", resp.NewVersion)
	}
	if len(stub.dagPatchCalls) != 1 {
		t.Fatalf("dagPatchCalls = %d, want 1", len(stub.dagPatchCalls))
	}
	got := stub.dagPatchCalls[0]
	if got.Title == nil || *got.Title != "Daily Report" {
		t.Fatalf("Title patch = %#v, want Daily Report", got.Title)
	}
	if got.Description == nil || *got.Description != "Morning summary" {
		t.Fatalf("Description patch = %#v, want Morning summary", got.Description)
	}
	if got.Trigger == nil || *got.Trigger != "scheduled" {
		t.Fatalf("Trigger patch = %#v, want scheduled", got.Trigger)
	}
	if got.CronExpr == nil || *got.CronExpr != "0 8 * * *" {
		t.Fatalf("CronExpr patch = %#v, want cron expression", got.CronExpr)
	}
	if got.OwnerID == nil || *got.OwnerID != "owner-1" {
		t.Fatalf("OwnerID patch = %#v, want owner-1", got.OwnerID)
	}
	if got.NextRunAt == nil || !got.NextRunAt.After(time.Now()) {
		t.Fatalf("NextRunAt patch = %#v, want future cron trigger time", got.NextRunAt)
	}
	if len(stub.upsertCalls) != 0 || len(stub.deleteCalls) != 0 {
		t.Fatalf("update_dag should not touch nodes: upsert=%d delete=%d", len(stub.upsertCalls), len(stub.deleteCalls))
	}
}

func TestApplyOps_UpdateDAG_ActiveRunRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{currentVersion: 3, activeRuns: 1}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 3,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{"title":"x"}}]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("active run update_dag: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "running") {
		t.Fatalf("err = %v, want mention running/active run", err)
	}
	if len(stub.dagPatchCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("dagPatchCalls=%v bumpCalls=%v, want no writes", stub.dagPatchCalls, stub.bumpCalls)
	}
}

func TestApplyOps_UpdateDAG_RunningDAGRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{
		currentVersion: 3,
		dagStatus:      "running",
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 3,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{"title":"x"}}]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("running DAG update_dag: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "running") {
		t.Fatalf("err = %v, want mention running DAG", err)
	}
	if len(stub.dagPatchCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("dagPatchCalls=%v bumpCalls=%v, want no writes", stub.dagPatchCalls, stub.bumpCalls)
	}
}

func TestApplyOps_UpdateDAG_TerminalDAGRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{
		currentVersion: 3,
		dagStatus:      "done",
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 3,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{"title":"x"}}]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("terminal DAG update_dag: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("err = %v, want mention terminal status", err)
	}
	if len(stub.dagPatchCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("dagPatchCalls=%v bumpCalls=%v, want no writes", stub.dagPatchCalls, stub.bumpCalls)
	}
}

func TestApplyOps_UpdateDAG_InvalidTriggerRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{currentVersion: 1}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{"trigger":"cron"}}]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("invalid trigger: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "trigger") {
		t.Fatalf("err = %v, want mention trigger", err)
	}
	if len(stub.dagPatchCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("dagPatchCalls=%v bumpCalls=%v, want fail before writes", stub.dagPatchCalls, stub.bumpCalls)
	}
}

func TestApplyOps_UpdateDAG_InvalidCronExprRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{currentVersion: 1}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{"trigger":"scheduled","cron_expr":"not-a-cron"}}]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("invalid cron_expr: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "cron_expr") {
		t.Fatalf("err = %v, want mention cron_expr", err)
	}
	if len(stub.dagPatchCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("dagPatchCalls=%v bumpCalls=%v, want fail before writes", stub.dagPatchCalls, stub.bumpCalls)
	}
}

func TestApplyOps_UpdateDAG_ScheduledWithoutCronRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{currentVersion: 1}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{"trigger":"scheduled"}}]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("scheduled without cron_expr: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "cron_expr") {
		t.Fatalf("err = %v, want mention cron_expr", err)
	}
	if len(stub.dagPatchCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("dagPatchCalls=%v bumpCalls=%v, want fail before writes", stub.dagPatchCalls, stub.bumpCalls)
	}
}

func TestApplyOps_UpdateDAG_ScheduledReusesExistingCron(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{
		currentVersion: 1,
		dagTrigger:     "manual",
		dagCronExpr:    "0 8 * * *",
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{"trigger":"scheduled"}}]`),
	}

	resp, err := s.ApplyOps(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyOps scheduled with existing cron err = %v, want nil", err)
	}
	if resp.NewVersion != 2 {
		t.Fatalf("NewVersion = %d, want 2", resp.NewVersion)
	}
	if len(stub.dagPatchCalls) != 1 {
		t.Fatalf("dagPatchCalls = %d, want 1", len(stub.dagPatchCalls))
	}
	if got := stub.dagPatchCalls[0]; got.NextRunAt == nil || !got.NextRunAt.After(time.Now()) {
		t.Fatalf("NextRunAt = %#v, want future time based on existing cron", got.NextRunAt)
	}
}

func TestApplyOps_UpdateDAG_ScheduledRejectsInvalidExistingCron(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{
		currentVersion: 1,
		dagTrigger:     "manual",
		dagCronExpr:    "not-a-cron",
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{"trigger":"scheduled"}}]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("scheduled with invalid existing cron: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "cron_expr") {
		t.Fatalf("err = %v, want mention cron_expr", err)
	}
	if len(stub.dagPatchCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("dagPatchCalls=%v bumpCalls=%v, want fail before writes", stub.dagPatchCalls, stub.bumpCalls)
	}
}

func TestApplyOps_UpdateDAG_ClearCronWithoutTriggerChangeRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{
		currentVersion: 1,
		dagTrigger:     "scheduled",
		dagCronExpr:    "0 8 * * *",
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{"cron_expr":""}}]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("clear cron without trigger change: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "cron_expr") {
		t.Fatalf("err = %v, want mention cron_expr", err)
	}
	if len(stub.dagPatchCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("dagPatchCalls=%v bumpCalls=%v, want fail before writes", stub.dagPatchCalls, stub.bumpCalls)
	}
}

func TestApplyOps_UpdateDAG_CronExprOnManualDAGRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{
		currentVersion: 1,
		dagTrigger:     "manual",
	}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{"cron_expr":"0 8 * * *"}}]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("cron_expr on manual DAG: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if !strings.Contains(err.Error(), "trigger=scheduled") {
		t.Fatalf("err = %v, want mention trigger=scheduled", err)
	}
	if len(stub.dagPatchCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("dagPatchCalls=%v bumpCalls=%v, want fail before writes", stub.dagPatchCalls, stub.bumpCalls)
	}
}

func TestApplyOps_UpdateDAG_EmptyPatchRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{currentVersion: 1}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{}}]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("empty patch: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if len(stub.dagPatchCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("dagPatchCalls=%v bumpCalls=%v, want fail before writes", stub.dagPatchCalls, stub.bumpCalls)
	}
}

func TestApplyOps_UpdateDAG_DuplicateRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{currentVersion: 1}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 1,
		Ops: json.RawMessage(`[
			{"op":"update_dag","patch":{"title":"a"}},
			{"op":"update_dag","patch":{"description":"b"}}
		]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("duplicate update_dag: want err, got nil")
	}
	if !errors.Is(err, ErrApplyOpsInvalid) {
		t.Fatalf("err = %v, want errors.Is ErrApplyOpsInvalid", err)
	}
	if len(stub.dagPatchCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("dagPatchCalls=%v bumpCalls=%v, want fail-fast before writes", stub.dagPatchCalls, stub.bumpCalls)
	}
}

func TestApplyOps_UpdateDAG_StaleBaseVersionRejected(t *testing.T) {
	t.Parallel()

	stub := &stubDAGOpsStore{currentVersion: 5}
	s := makeApplyOpsService(stub)
	req := contract.ApplyOpsRequest{
		DagKey:      "dag-a",
		BaseVersion: 4,
		Ops:         json.RawMessage(`[{"op":"update_dag","patch":{"title":"x"}}]`),
	}

	_, err := s.ApplyOps(context.Background(), req)
	if err == nil {
		t.Fatal("stale base_version: want err, got nil")
	}
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("err = %v, want ErrVersionConflict", err)
	}
	if len(stub.dagPatchCalls) != 0 || len(stub.bumpCalls) != 0 {
		t.Fatalf("dagPatchCalls=%v bumpCalls=%v, want no writes", stub.dagPatchCalls, stub.bumpCalls)
	}
}
