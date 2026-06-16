package cron

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// TestCreateJobDefaultsAndTrims verifies service-level validation and persistence mapping.
func TestCreateJobDefaultsAndTrims(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	store := newFakeCronServiceStore()
	svc := NewService(nil, store).(*service)
	svc.now = func() time.Time { return now }
	svc.newID = func() string { return "job-1" }

	config := validCronCodexConfig(t)
	job, err := svc.CreateJob(context.Background(), CreateJobRequest{
		Name:         " nightly ",
		Prompt:       "run report",
		ScheduleExpr: " */5 * * * * ",
		Provider:     " ",
		CWD:          " /repo ",
		Config:       config,
		Skills:       []string{" go ", "go", "", "test"},
		Enabled:      true,
		MaxAttempts:  2,
	})
	if err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}

	if job.ID != "job-1" || job.Provider != contract.CronProviderCodex || job.ScheduleType != "cron" {
		t.Fatalf("job = %#v, want id job-1 provider codex schedule_type cron", job)
	}
	params := store.created
	if params.ID != "job-1" || params.Name != "nightly" || params.Provider != contract.CronProviderCodex {
		t.Fatalf("created params = %#v", params)
	}
	if params.CWD != "/repo" || params.ScheduleExpr != "*/5 * * * *" {
		t.Fatalf("created cwd/schedule = %q/%q", params.CWD, params.ScheduleExpr)
	}
	if !params.NextRunAt.Equal(now.Add(defaultInitialDelay)) {
		t.Fatalf("NextRunAt = %s, want %s", params.NextRunAt, now.Add(defaultInitialDelay))
	}
	if !params.CreatedAt.Equal(now) || !params.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps = created %s updated %s, want %s", params.CreatedAt, params.UpdatedAt, now)
	}
	var skills []string
	if err := json.Unmarshal(params.Skills, &skills); err != nil {
		t.Fatalf("json.Unmarshal(skills) error = %v", err)
	}
	if len(skills) != 2 || skills[0] != "go" || skills[1] != "test" {
		t.Fatalf("skills = %#v, want deduplicated [go test]", skills)
	}
}

// TestRunOncePatchesEnabledJobAndRejectsDisabledJob verifies manual trigger semantics.
func TestRunOncePatchesEnabledJobAndRejectsDisabledJob(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 16, 12, 30, 0, 0, time.UTC)
	store := newFakeCronServiceStore()
	store.jobs["enabled"] = contract.CronJob{ID: "enabled", Enabled: true}
	store.jobs["disabled"] = contract.CronJob{ID: "disabled", Enabled: false}
	svc := NewService(nil, store).(*service)
	svc.now = func() time.Time { return now }

	job, err := svc.RunOnce(context.Background(), " enabled ")
	if err != nil {
		t.Fatalf("RunOnce(enabled) error = %v", err)
	}
	if job.ID != "enabled" || !store.jobs["enabled"].NextRunAt.Equal(now) {
		t.Fatalf("enabled job after RunOnce = %#v, want next_run_at %s", store.jobs["enabled"], now)
	}
	if store.patched != 1 {
		t.Fatalf("patched = %d, want 1", store.patched)
	}

	_, err = svc.RunOnce(context.Background(), "disabled")
	if !errors.Is(err, ErrJobDisabled) {
		t.Fatalf("RunOnce(disabled) error = %v, want ErrJobDisabled", err)
	}
	if store.patched != 1 {
		t.Fatalf("patched after disabled = %d, want unchanged 1", store.patched)
	}
}

func validCronCodexConfig(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		contract.CodexHomeKey:          t.TempDir(),
		contract.CodexInstanceKeyKey:   "default",
		contract.CodexModelProviderKey: "super-dolphin-relay",
	})
	if err != nil {
		t.Fatalf("json.Marshal(config) error = %v", err)
	}
	return raw
}

type fakeCronServiceStore struct {
	created contract.CronCreateJobParams
	jobs    map[string]contract.CronJob
	patched int
}

func newFakeCronServiceStore() *fakeCronServiceStore {
	return &fakeCronServiceStore{jobs: map[string]contract.CronJob{}}
}

// CreateJob records and returns a cron job row for service tests.
func (s *fakeCronServiceStore) CreateJob(_ context.Context, params contract.CronCreateJobParams) (contract.CronJob, error) {
	s.created = params
	row := contract.CronJob{
		ID:            params.ID,
		Name:          params.Name,
		Prompt:        params.Prompt,
		ScheduleType:  params.ScheduleType,
		ScheduleExpr:  params.ScheduleExpr,
		Timezone:      params.Timezone,
		Provider:      params.Provider,
		Model:         params.Model,
		CWD:           params.CWD,
		Config:        params.Config,
		Skills:        params.Skills,
		NotifyChannel: params.NotifyChannel,
		Enabled:       params.Enabled,
		NextRunAt:     params.NextRunAt,
		MaxAttempts:   params.MaxAttempts,
		CreatedAt:     params.CreatedAt,
		UpdatedAt:     params.UpdatedAt,
	}
	s.jobs[row.ID] = row
	return row, nil
}

// GetJobByID returns a seeded cron job row for service tests.
func (s *fakeCronServiceStore) GetJobByID(_ context.Context, id string) (contract.CronJob, error) {
	row, ok := s.jobs[id]
	if !ok {
		return contract.CronJob{}, contract.ErrCronJobNotFound
	}
	return row, nil
}

// ListJobs returns all seeded cron job rows for service tests.
func (s *fakeCronServiceStore) ListJobs(context.Context) ([]contract.CronJob, error) {
	rows := make([]contract.CronJob, 0, len(s.jobs))
	for _, row := range s.jobs {
		rows = append(rows, row)
	}
	return rows, nil
}

// DeleteJob removes a seeded cron job row for service tests.
func (s *fakeCronServiceStore) DeleteJob(_ context.Context, id string) error {
	delete(s.jobs, id)
	return nil
}

// UpdateJobSchedule updates a seeded cron job row for service tests.
func (s *fakeCronServiceStore) UpdateJobSchedule(_ context.Context, params contract.CronUpdateJobScheduleParams) error {
	row := s.jobs[params.ID]
	row.Name = params.Name
	row.Prompt = params.Prompt
	row.ScheduleType = params.ScheduleType
	row.ScheduleExpr = params.ScheduleExpr
	row.Timezone = params.Timezone
	row.Provider = params.Provider
	row.Model = params.Model
	row.CWD = params.CWD
	row.Config = params.Config
	row.Skills = params.Skills
	row.NotifyChannel = params.NotifyChannel
	row.Enabled = params.Enabled
	row.NextRunAt = params.NextRunAt
	row.MaxAttempts = params.MaxAttempts
	row.UpdatedAt = params.UpdatedAt
	s.jobs[params.ID] = row
	return nil
}

// SetJobEnabled updates enabled state on a seeded cron job row for service tests.
func (s *fakeCronServiceStore) SetJobEnabled(_ context.Context, id string, enabled bool, now time.Time) error {
	row, ok := s.jobs[id]
	if !ok {
		return contract.ErrCronJobNotFound
	}
	row.Enabled = enabled
	row.UpdatedAt = now
	s.jobs[id] = row
	return nil
}

// PatchNextRunAt updates next_run_at on a seeded cron job row for service tests.
func (s *fakeCronServiceStore) PatchNextRunAt(_ context.Context, id string, nextRunAt time.Time, now time.Time) error {
	row, ok := s.jobs[id]
	if !ok {
		return contract.ErrCronJobNotFound
	}
	row.NextRunAt = nextRunAt
	row.UpdatedAt = now
	s.jobs[id] = row
	s.patched++
	return nil
}

// ListRunsByJob returns no runs because service tests do not exercise run listing.
func (s *fakeCronServiceStore) ListRunsByJob(context.Context, string, int32) ([]contract.CronRun, error) {
	return nil, nil
}
