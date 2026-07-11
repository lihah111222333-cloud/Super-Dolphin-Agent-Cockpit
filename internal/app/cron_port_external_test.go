package app_test

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/cron"
)

type externalCronStore struct{}

func (externalCronStore) CreateJob(context.Context, cron.CreateJobParams) (cron.JobRecord, error) {
	return cron.JobRecord{}, nil
}

func (externalCronStore) GetJobByID(context.Context, string) (cron.JobRecord, error) {
	return cron.JobRecord{}, nil
}

func (externalCronStore) ListJobs(context.Context) ([]cron.JobRecord, error) { return nil, nil }
func (externalCronStore) DeleteJob(context.Context, string) error            { return nil }
func (externalCronStore) UpdateJobSchedule(context.Context, cron.UpdateJobScheduleParams) error {
	return nil
}
func (externalCronStore) SetJobEnabled(context.Context, string, bool, time.Time) error { return nil }
func (externalCronStore) PatchNextRunAt(context.Context, string, time.Time, time.Time) error {
	return nil
}
func (externalCronStore) ListRunsByJob(context.Context, string, int32) ([]cron.RunRecord, error) {
	return nil, nil
}

type externalCronSchedulerStore struct {
	externalCronSchedulerClaimStore
	externalCronSchedulerRunStore
	externalCronSchedulerLookupStore
}

type externalCronSchedulerClaimStore struct{}

func (externalCronSchedulerClaimStore) ClaimDueJobsForUpdate(context.Context, cron.ClaimDueJobsForUpdateParams) ([]cron.JobRecord, error) {
	return nil, nil
}
func (externalCronSchedulerClaimStore) RenewLease(context.Context, cron.LeaseParams) error {
	return nil
}
func (externalCronSchedulerClaimStore) ExtendClaim(context.Context, cron.LeaseParams) error {
	return nil
}
func (externalCronSchedulerClaimStore) MarkFinished(context.Context, cron.MarkFinishedParams) error {
	return nil
}
func (externalCronSchedulerClaimStore) MarkFailed(context.Context, cron.MarkFailedParams) error {
	return nil
}
func (externalCronSchedulerClaimStore) SetActiveTurn(context.Context, cron.SetActiveTurnParams) error {
	return nil
}

type externalCronSchedulerRunStore struct{}

func (externalCronSchedulerRunStore) InsertRun(context.Context, cron.InsertRunParams) (cron.RunRecord, error) {
	return cron.RunRecord{}, nil
}
func (externalCronSchedulerRunStore) CASRunStatus(context.Context, cron.CASRunStatusParams) error {
	return nil
}
func (externalCronSchedulerRunStore) SetRunTurn(context.Context, cron.SetRunTurnParams) error {
	return nil
}
func (externalCronSchedulerRunStore) GetRunningRunByTurnID(context.Context, string) (cron.RunRecord, error) {
	return cron.RunRecord{}, nil
}
func (externalCronSchedulerRunStore) ListUnresolvedRuns(context.Context) ([]cron.RunRecord, error) {
	return nil, nil
}

type externalCronSchedulerLookupStore struct{}

func (externalCronSchedulerLookupStore) GetJobByID(context.Context, string) (cron.JobRecord, error) {
	return cron.JobRecord{}, nil
}
func (externalCronSchedulerLookupStore) ListJobsClaimedBy(context.Context, string) ([]cron.JobRecord, error) {
	return nil, nil
}

var _ cron.Store = externalCronStore{}
var _ cron.SchedulerStore = externalCronSchedulerStore{}
var _ = cron.SubmitRunWithActiveTurnParams{}
var _ = []error{
	cron.ErrStoreJobNotFound,
	cron.ErrStoreJobRunNotFound,
	cron.ErrStoreClaimTokenMismatch,
	cron.ErrStoreStatusTransitionRefused,
	cron.ErrStoreEmptyID,
	cron.ErrStoreEmptyCWD,
	cron.ErrStoreEmptyProvider,
	cron.ErrStoreEmptyScheduleExpr,
}
