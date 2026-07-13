package cron

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

type finalizeRecoveredRunInput struct {
	id, token, runID, expectedTurnID string
	expectedStatus, status           string
}

// FinalizeRecoveredRun 在一个事务中写入恢复 run 终态并释放带 fence 的 job claim。
func (s *submitStore) FinalizeRecoveredRun(ctx context.Context, p FinalizeRecoveredRunParams) error {
	if s.db == nil || s.sqlcQuery == nil {
		return wrap(errors.New("cron: explicit DB and sqlc queries are required for finalize_recovered_run"), "finalize_recovered_run")
	}
	input, err := validateFinalizeRecoveredRun(p)
	if err != nil {
		return wrap(err, "finalize_recovered_run")
	}
	err = platformdb.BoundedWriteRetry(ctx, cronClaimBusyRetryAttempts, func() error {
		return platformdb.WithImmediateTx(ctx, s.db, func(tx *sql.Tx) error {
			return finalizeRecoveredRunInTx(ctx, s.sqlcQuery.WithTx(tx), p, input)
		})
	})
	return wrap(err, "finalize_recovered_run")
}

// validateFinalizeRecoveredRun 校验并规范化恢复终态事务所需的全部 fence 字段。
func validateFinalizeRecoveredRun(p FinalizeRecoveredRunParams) (finalizeRecoveredRunInput, error) {
	id, token, err := requireClaim(p.ID, p.ClaimToken)
	if err != nil {
		return finalizeRecoveredRunInput{}, err
	}
	runID, err := requireRunID(p.RunID)
	if err != nil {
		return finalizeRecoveredRunInput{}, err
	}
	expectedTurnID, err := expectedActiveTurnIDForStore(p.LastTurnID, p.ExpectedActiveTurnID)
	if err != nil {
		return finalizeRecoveredRunInput{}, err
	}
	input := finalizeRecoveredRunInput{id: id, token: token, runID: runID, expectedTurnID: expectedTurnID, expectedStatus: strings.TrimSpace(p.ExpectedRunStatus), status: strings.TrimSpace(p.LastStatus)}
	if input.expectedStatus == "" || input.status == "" || p.NextRunAt.IsZero() {
		return finalizeRecoveredRunInput{}, errors.New("cron: recovered finalization requires expected status, terminal status, and next_run_at")
	}
	return input, nil
}

func finalizeRecoveredRunInTx(ctx context.Context, q *sqlc.Queries, p FinalizeRecoveredRunParams, input finalizeRecoveredRunInput) error {
	rows, err := q.CASCronJobRunStatus(ctx, sqlc.CASCronJobRunStatusParams{ID: input.runID, ExpectedStatus: input.expectedStatus, NextStatus: input.status, Error: p.LastError, UpdatedAt: ts(p.Now)})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrStatusTransitionRefused
	}
	rows, err = q.MarkCronJobFailed(ctx, sqlc.MarkCronJobFailedParams{LastRunAt: tsPtr(p.LastRunAt), LastTurnID: p.LastTurnID, LastStatus: input.status, LastErrorAt: tsPtr(p.LastErrorAt), LastError: p.LastError, NextRunAt: ts(p.NextRunAt), NextRetryAt: tsPtr(p.NextRetryAt), UpdatedAt: ts(p.Now), ID: input.id, ClaimToken: input.token, ExpectedActiveTurnID: input.expectedTurnID, RunID: input.runID})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrClaimTokenMismatch
	}
	return nil
}
