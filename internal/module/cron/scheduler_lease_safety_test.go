package cron

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLeaseActorReturnsListClaimedFailureWithoutRetryOrCancel(t *testing.T) {
	want := errors.New("list claimed unavailable")
	var listCalls atomic.Int32
	store := &recordingCronStore{
		listJobsClaimedByFn: func(context.Context, string) ([]JobRecord, error) {
			listCalls.Add(1)
			return nil, want
		},
	}
	submitter := &leaseCancelSubmitter{}
	s := NewScheduler(nil, store, submitter, SchedulerConfig{
		ClaimedBy: "test", LeaseHeartbeat: time.Millisecond, LeaseTTL: time.Hour,
	})
	actor := NewLeaseActor(nil, s)

	_, done := startActorForTest(t, actor.Run)
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("LeaseActor.Run() error = %v, want %v", err, want)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("LeaseActor.Run() retried an unenumerated list failure")
	}
	if got := listCalls.Load(); got != 1 {
		t.Fatalf("ListJobsClaimedBy() calls = %d, want 1", got)
	}
	if len(submitter.jobs) != 0 {
		t.Fatalf("canceled jobs = %#v, want none when claimed jobs are unknown", submitter.jobs)
	}
}

func TestRenewLeasesAggregatesJobFailuresAndContinues(t *testing.T) {
	var mu sync.Mutex
	var renewed []string
	want := errors.New("sqlite busy")
	store := &recordingCronStore{
		listJobsClaimedByFn: func(context.Context, string) ([]JobRecord, error) {
			return []JobRecord{
				{ID: "job-bad", ClaimToken: "bad", LeaseExpiresAt: time.Now().Add(time.Minute)},
				{ID: "job-good", ClaimToken: "good", LeaseExpiresAt: time.Now().Add(time.Minute)},
			}, nil
		},
		renewLeaseFn: func(_ context.Context, params LeaseParams) error {
			mu.Lock()
			renewed = append(renewed, params.ID)
			mu.Unlock()
			if params.ID == "job-bad" {
				return want
			}
			return nil
		},
	}
	s := NewScheduler(nil, store, &programmableSubmitter{}, SchedulerConfig{ClaimedBy: "test"})

	err := s.RenewLeases(context.Background())
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "job-bad") {
		t.Fatalf("RenewLeases() error = %v, want job ID and cause", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(renewed) != 2 || renewed[0] != "job-bad" || renewed[1] != "job-good" {
		t.Fatalf("renewed = %v, want both jobs", renewed)
	}
}

func TestCancelLeaseFailuresAggregatesCancelErrorsByJob(t *testing.T) {
	want := errors.New("interrupt failed")
	submitter := &leaseCancelSubmitter{err: want}
	s := NewScheduler(nil, &recordingCronStore{}, submitter, SchedulerConfig{})
	failures := []leaseRenewFailure{{job: JobRecord{ID: "job-1", ThreadID: "thread-1", ActiveTurnID: "turn-1"}}}

	err := s.cancelLeaseFailures(context.Background(), failures)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "job-1") {
		t.Fatalf("cancelLeaseFailures() error = %v, want job ID and cause", err)
	}
	if len(submitter.jobs) != 1 || submitter.jobs[0].ID != "job-1" {
		t.Fatalf("canceled jobs = %#v, want job-1", submitter.jobs)
	}
}

func TestLeaseActorExitsAndCancelsWhenRenewSafetyBudgetIsExhausted(t *testing.T) {
	want := errors.New("renew unavailable")
	job := JobRecord{
		ID: "job-1", ThreadID: "thread-1", ActiveTurnID: "turn-1", ClaimToken: "token",
		LeaseExpiresAt: time.Now().Add(time.Millisecond),
	}
	store := &recordingCronStore{
		listJobsClaimedByFn: func(context.Context, string) ([]JobRecord, error) { return []JobRecord{job}, nil },
		renewLeaseFn:        func(context.Context, LeaseParams) error { return want },
	}
	submitter := &leaseCancelSubmitter{}
	s := NewScheduler(nil, store, submitter, SchedulerConfig{
		ClaimedBy: "test", LeaseHeartbeat: 10 * time.Millisecond, LeaseTTL: time.Minute,
	})
	actor := NewLeaseActor(nil, s)

	_, done := startActorForTest(t, actor.Run)
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("LeaseActor.Run() error = %v, want renew cause", err)
		}
	case <-time.After(time.Second):
		t.Fatal("LeaseActor.Run() did not exit after lease safety budget")
	}
	if len(submitter.jobs) != 1 || submitter.jobs[0].ID != job.ID {
		t.Fatalf("canceled jobs = %#v, want %s", submitter.jobs, job.ID)
	}
}

type leaseCancelSubmitter struct {
	programmableSubmitter
	err  error
	jobs []JobRecord
}

func (s *leaseCancelSubmitter) CancelLeaseLostTurn(_ context.Context, job JobRecord) error {
	s.jobs = append(s.jobs, job)
	return s.err
}
