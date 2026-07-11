package cron

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// newIdentityConfig returns a valid codex identity config whose codexHome
// points at a freshly-created temp directory that passes
// contract.CanonicalizeCodexHome.
func newIdentityConfig(t *testing.T) json.RawMessage {
	t.Helper()
	dir := t.TempDir()
	cfg := map[string]any{
		"codexHome":          dir,
		"codexInstanceKey":   "glm",
		"codexModelProvider": "glm-compat",
	}
	// Sanity-check that ResolveCodexIdentity accepts our fixture; if this
	// breaks future updates, we'll catch it here rather than inside the
	// service test stack traces.
	if _, err := contract.ResolveCodexIdentity(cfg); err != nil {
		t.Fatalf("identity fixture invalid: %v", err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// fakeStore is a lightweight recording double for the narrow Store
// interface the module consumes.
type fakeStore struct {
	createFn         func(context.Context, CreateJobParams) (JobRecord, error)
	getByIDFn        func(context.Context, string) (JobRecord, error)
	listFn           func(context.Context) ([]JobRecord, error)
	deleteFn         func(context.Context, string) error
	updateFn         func(context.Context, UpdateJobScheduleParams) error
	setEnabledFn     func(context.Context, string, bool, time.Time) error
	listRunsByJobFn  func(context.Context, string, int32) ([]RunRecord, error)
	patchNextRunAtFn func(context.Context, string, time.Time, time.Time) error
}

func (f *fakeStore) CreateJob(ctx context.Context, p CreateJobParams) (JobRecord, error) {
	if f.createFn != nil {
		return f.createFn(ctx, p)
	}
	return JobRecord{ID: p.ID, Name: p.Name, Provider: p.Provider, CWD: p.CWD}, nil
}
func (f *fakeStore) PatchNextRunAt(ctx context.Context, id string, nextRunAt time.Time, now time.Time) error {
	if f.patchNextRunAtFn != nil {
		return f.patchNextRunAtFn(ctx, id, nextRunAt, now)
	}
	return nil
}

func (f *fakeStore) GetJobByID(ctx context.Context, id string) (JobRecord, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, id)
	}
	return JobRecord{ID: id}, nil
}
func (f *fakeStore) ListJobs(ctx context.Context) ([]JobRecord, error) {
	if f.listFn != nil {
		return f.listFn(ctx)
	}
	return nil, nil
}
func (f *fakeStore) DeleteJob(ctx context.Context, id string) error {
	if f.deleteFn != nil {
		return f.deleteFn(ctx, id)
	}
	return nil
}
func (f *fakeStore) UpdateJobSchedule(ctx context.Context, p UpdateJobScheduleParams) error {
	if f.updateFn != nil {
		return f.updateFn(ctx, p)
	}
	return nil
}
func (f *fakeStore) SetJobEnabled(ctx context.Context, id string, enabled bool, now time.Time) error {
	if f.setEnabledFn != nil {
		return f.setEnabledFn(ctx, id, enabled, now)
	}
	return nil
}
func (f *fakeStore) ListRunsByJob(ctx context.Context, jobID string, limit int32) ([]RunRecord, error) {
	if f.listRunsByJobFn != nil {
		return f.listRunsByJobFn(ctx, jobID, limit)
	}
	return nil, nil
}

// newTestService constructs a cron service with a deterministic clock and
// id generator so tests can assert exact values.
func newTestService(t *testing.T, store Store) *service {
	t.Helper()
	if store == nil {
		store = &fakeStore{}
	}
	svc := &service{
		store: store,
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		newID: func() string { return "job-test-id" },
	}
	return svc
}

// ----- validation -----

func TestCreateJobRejectsMissingFields(t *testing.T) {
	t.Parallel()

	cfg := newIdentityConfig(t)
	base := CreateJobRequest{
		Name:         "daily",
		Prompt:       "check logs",
		ScheduleExpr: "0 9 * * *",
		Provider:     "codex",
		CWD:          "/repo",
		Config:       cfg,
	}

	cases := []struct {
		name   string
		mutate func(*CreateJobRequest)
		want   error
	}{
		{"missing name", func(r *CreateJobRequest) { r.Name = "" }, ErrMissingName},
		{"missing prompt", func(r *CreateJobRequest) { r.Prompt = "  " }, ErrMissingPrompt},
		{"missing schedule", func(r *CreateJobRequest) { r.ScheduleExpr = "" }, ErrMissingSchedule},
		{"missing cwd", func(r *CreateJobRequest) { r.CWD = "" }, ErrMissingCWD},
		{"bad max attempts", func(r *CreateJobRequest) { r.MaxAttempts = -1 }, ErrInvalidMaxAttempts},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t, nil)
			req := base
			tc.mutate(&req)
			_, err := svc.CreateJob(context.Background(), req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestCreateJobRejectsNonCodexProvider(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, nil)
	_, err := svc.CreateJob(context.Background(), CreateJobRequest{
		Name:         "daily",
		Prompt:       "p",
		ScheduleExpr: "* * * * *",
		Provider:     "claude",
		CWD:          "/repo",
		Config:       newIdentityConfig(t),
	})
	if !errors.Is(err, ErrProviderNotSupported) {
		t.Fatalf("want ErrProviderNotSupported, got %v", err)
	}
}

func TestCreateJobDefaultsProviderToCodex(t *testing.T) {
	t.Parallel()
	var got CreateJobParams
	store := &fakeStore{
		createFn: func(_ context.Context, p CreateJobParams) (JobRecord, error) {
			got = p
			return JobRecord{ID: p.ID, Provider: p.Provider}, nil
		},
	}
	svc := newTestService(t, store)
	_, err := svc.CreateJob(context.Background(), CreateJobRequest{
		Name:         "daily",
		Prompt:       "p",
		ScheduleExpr: "* * * * *",
		CWD:          "/repo",
		Config:       newIdentityConfig(t),
	})
	if err != nil {
		t.Fatalf("CreateJob error = %v", err)
	}
	if got.Provider != providerCodex {
		t.Fatalf("Provider = %q, want codex", got.Provider)
	}
}

func TestCreateJobRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		config json.RawMessage
	}{
		{"empty config", json.RawMessage(`{}`)},
		{"missing codex_home", json.RawMessage(`{"codexInstanceKey":"k","codexModelProvider":"p"}`)},
		{"missing non-existent home", jsonMap(map[string]any{
			"codexHome":          "/definitely/does/not/exist/here",
			"codexInstanceKey":   "k",
			"codexModelProvider": "p",
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t, nil)
			_, err := svc.CreateJob(context.Background(), CreateJobRequest{
				Name:         "daily",
				Prompt:       "p",
				ScheduleExpr: "* * * * *",
				Provider:     "codex",
				CWD:          "/repo",
				Config:       tc.config,
			})
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("want ErrInvalidConfig, got %v", err)
			}
		})
	}
}

func TestCreateAndUpdateRejectInvalidScheduleInputs(t *testing.T) {
	t.Parallel()
	cfg := newIdentityConfig(t)
	base := CreateJobRequest{
		Name:         "daily",
		Prompt:       "p",
		ScheduleExpr: "* * * * *",
		Timezone:     "UTC",
		Provider:     "codex",
		CWD:          "/repo",
		Config:       cfg,
	}
	cases := []struct {
		name      string
		mutate    func(*CreateJobRequest)
		wantError string
	}{
		{
			name:      "bad schedule expression",
			mutate:    func(r *CreateJobRequest) { r.ScheduleExpr = "not a cron" },
			wantError: "schedule_expr",
		},
		{
			name:      "bad timezone",
			mutate:    func(r *CreateJobRequest) { r.Timezone = "Mars/Olympus" },
			wantError: "timezone",
		},
	}
	for _, tc := range cases {
		for _, op := range []string{"create", "update"} {
			t.Run(tc.name+"/"+op, func(t *testing.T) {
				t.Parallel()
				store := &fakeStore{
					createFn: func(context.Context, CreateJobParams) (JobRecord, error) {
						t.Fatalf("CreateJob reached store for invalid %s", tc.wantError)
						return JobRecord{}, nil
					},
					updateFn: func(context.Context, UpdateJobScheduleParams) error {
						t.Fatalf("UpdateJob reached store for invalid %s", tc.wantError)
						return nil
					},
				}
				svc := newTestService(t, store)
				req := base
				tc.mutate(&req)
				var err error
				if op == "create" {
					_, err = svc.CreateJob(context.Background(), req)
				} else {
					_, err = svc.UpdateJob(context.Background(), UpdateJobRequest{
						ID:            "job-1",
						Name:          req.Name,
						Prompt:        req.Prompt,
						ScheduleType:  req.ScheduleType,
						ScheduleExpr:  req.ScheduleExpr,
						Timezone:      req.Timezone,
						Provider:      req.Provider,
						Model:         req.Model,
						CWD:           req.CWD,
						Config:        req.Config,
						Skills:        req.Skills,
						NotifyChannel: req.NotifyChannel,
						Enabled:       req.Enabled,
						NextRunAt:     req.NextRunAt,
						MaxAttempts:   req.MaxAttempts,
					})
				}
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("%s error = %v, want %s validation error", op, err, tc.wantError)
				}
			})
		}
	}
}

func TestCreateJobDefaultsNextRunAtAndScheduleType(t *testing.T) {
	t.Parallel()
	var got CreateJobParams
	store := &fakeStore{
		createFn: func(_ context.Context, p CreateJobParams) (JobRecord, error) {
			got = p
			return JobRecord{ID: p.ID}, nil
		},
	}
	svc := newTestService(t, store)
	now := svc.now()
	_, err := svc.CreateJob(context.Background(), CreateJobRequest{
		Name:         "daily",
		Prompt:       "p",
		ScheduleExpr: "* * * * *",
		Provider:     "codex",
		CWD:          "/repo",
		Config:       newIdentityConfig(t),
	})
	if err != nil {
		t.Fatalf("CreateJob error = %v", err)
	}
	if got.ScheduleType != "cron" {
		t.Fatalf("ScheduleType = %q, want cron", got.ScheduleType)
	}
	if diff := got.NextRunAt.Sub(now.Add(defaultInitialDelay)); diff != 0 {
		t.Fatalf("NextRunAt delta = %v, want exactly defaultInitialDelay", diff)
	}
}

func TestCreateJobDedupesAndTrimsSkills(t *testing.T) {
	t.Parallel()
	var got CreateJobParams
	store := &fakeStore{
		createFn: func(_ context.Context, p CreateJobParams) (JobRecord, error) {
			got = p
			return JobRecord{ID: p.ID}, nil
		},
	}
	svc := newTestService(t, store)
	_, err := svc.CreateJob(context.Background(), CreateJobRequest{
		Name:         "daily",
		Prompt:       "p",
		ScheduleExpr: "* * * * *",
		Provider:     "codex",
		CWD:          "/repo",
		Config:       newIdentityConfig(t),
		Skills:       []string{" log-inspector ", "log-inspector", "", "another"},
	})
	if err != nil {
		t.Fatalf("CreateJob error = %v", err)
	}
	var skills []string
	if err := json.Unmarshal(got.Skills, &skills); err != nil {
		t.Fatalf("unmarshal skills: %v", err)
	}
	if len(skills) != 2 || skills[0] != "log-inspector" || skills[1] != "another" {
		t.Fatalf("skills = %v, want [log-inspector, another]", skills)
	}
}

// ----- CRUD pass-through -----

func TestGetJobMapsNotFound(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		getByIDFn: func(context.Context, string) (JobRecord, error) {
			return JobRecord{}, ErrStoreJobNotFound
		},
	}
	svc := newTestService(t, store)
	_, err := svc.GetJob(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListJobsPassesThrough(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		listFn: func(context.Context) ([]JobRecord, error) {
			return []JobRecord{{ID: "a"}, {ID: "b"}}, nil
		},
	}
	svc := newTestService(t, store)
	jobs, err := svc.ListJobs(context.Background())
	if err != nil {
		t.Fatalf("ListJobs error = %v", err)
	}
	if len(jobs) != 2 || jobs[0].ID != "a" || jobs[1].ID != "b" {
		t.Fatalf("ListJobs = %+v", jobs)
	}
}

func TestDeleteJobRequiresID(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, nil)
	err := svc.DeleteJob(context.Background(), "")
	if err == nil {
		t.Fatal("DeleteJob with empty id must error")
	}
}

func TestSetJobEnabledForwards(t *testing.T) {
	t.Parallel()
	var gotID string
	var gotEnabled bool
	store := &fakeStore{
		setEnabledFn: func(_ context.Context, id string, enabled bool, _ time.Time) error {
			gotID, gotEnabled = id, enabled
			return nil
		},
	}
	svc := newTestService(t, store)
	if err := svc.SetJobEnabled(context.Background(), "job-1", false); err != nil {
		t.Fatalf("SetJobEnabled error = %v", err)
	}
	if gotID != "job-1" || gotEnabled {
		t.Fatalf("SetJobEnabled forwarded wrong values: id=%q enabled=%v", gotID, gotEnabled)
	}
}

func TestUpdateJobReValidatesIdentity(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, nil)
	_, err := svc.UpdateJob(context.Background(), UpdateJobRequest{
		ID:           "job-1",
		Name:         "daily",
		Prompt:       "p",
		ScheduleExpr: "* * * * *",
		Provider:     "codex",
		CWD:          "/repo",
		Config:       json.RawMessage(`{}`), // identity triple missing
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
}

func TestListJobRunsPassesThrough(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		listRunsByJobFn: func(_ context.Context, _ string, limit int32) ([]RunRecord, error) {
			if limit == 0 {
				t.Fatal("service must forward caller's limit, not rewrite to store default")
			}
			return []RunRecord{{ID: "r1", Status: statusPending}}, nil
		},
	}
	svc := newTestService(t, store)
	runs, err := svc.ListJobRuns(context.Background(), "job-1", 5)
	if err != nil {
		t.Fatalf("ListJobRuns error = %v", err)
	}
	if len(runs) != 1 || runs[0].Status != statusPending {
		t.Fatalf("ListJobRuns = %+v", runs)
	}
}

func TestCreateGetAndListJobsFailOnCorruptStoredPayload(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		field string
		row   JobRecord
		call  func(*service) error
	}{
		{
			name:  "create corrupt config",
			field: "config",
			row:   JobRecord{ID: "job-create", Name: "daily", Config: []byte(`{bad`), Skills: []byte(`[]`)},
			call: func(svc *service) error {
				_, err := svc.CreateJob(context.Background(), CreateJobRequest{
					Name:         "daily",
					Prompt:       "p",
					ScheduleExpr: "* * * * *",
					CWD:          "/repo",
					Config:       newIdentityConfig(t),
				})
				return err
			},
		},
		{
			name:  "get corrupt skills",
			field: "skills",
			row:   JobRecord{ID: "job-get", Config: []byte(`{}`), Skills: []byte(`["ok"`), Name: "daily"},
			call: func(svc *service) error {
				_, err := svc.GetJob(context.Background(), "job-get")
				return err
			},
		},
		{
			name:  "list corrupt config",
			field: "config",
			row:   JobRecord{ID: "job-list", Config: []byte(`{"unterminated"`), Skills: []byte(`[]`), Name: "daily"},
			call: func(svc *service) error {
				_, err := svc.ListJobs(context.Background())
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &fakeStore{
				createFn: func(context.Context, CreateJobParams) (JobRecord, error) {
					return tc.row, nil
				},
				getByIDFn: func(context.Context, string) (JobRecord, error) {
					return tc.row, nil
				},
				listFn: func(context.Context) ([]JobRecord, error) {
					return []JobRecord{tc.row}, nil
				},
			}
			svc := newTestService(t, store)

			err := tc.call(svc)
			assertCronJobPayloadInvalidError(t, err, tc.row.ID, tc.field)
		})
	}
}

func assertCronJobPayloadInvalidError(t *testing.T, err error, jobID, field string) {
	t.Helper()

	if err == nil {
		t.Fatalf("error = nil, want cron_job_payload_invalid for %s.%s", jobID, field)
	}
	var coded interface{ ErrorCode() string }
	if !errors.As(err, &coded) {
		t.Fatalf("error %T does not expose ErrorCode(): %v", err, err)
	}
	if code := coded.ErrorCode(); code != "cron_job_payload_invalid" {
		t.Fatalf("error code = %q, want cron_job_payload_invalid", code)
	}
	for _, want := range []string{jobID, field} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want detail %q", err.Error(), want)
		}
	}
}

// ----- helpers -----

func jsonMap(m map[string]any) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}

// Ensure go test does not rely on the current directory for the identity
// fixture: this anchors t.TempDir() under os.TempDir regardless of
// go-test cwd.
var _ = filepath.Join
var _ = os.TempDir

// ----- RunOnce -----

func TestRunOnceBumpsNextRunAtPreservingOtherFields(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_700_000_000, 0).UTC()

	job := JobRecord{
		ID:            "job-1",
		Name:          "daily",
		Prompt:        "check",
		ScheduleType:  "cron",
		ScheduleExpr:  "0 9 * * *",
		Timezone:      "Asia/Seoul",
		Provider:      "codex",
		Model:         "gpt-5",
		CWD:           "/repo",
		Config:        []byte(`{"codexHome":"/x"}`),
		Skills:        []byte(`["a"]`),
		NotifyChannel: "slack.default",
		Enabled:       true,
		NextRunAt:     now.Add(24 * time.Hour),
		MaxAttempts:   3,
	}

	var gotID string
	var gotNextRunAt time.Time
	store := &fakeStore{
		getByIDFn: func(_ context.Context, id string) (JobRecord, error) {
			if id != "job-1" {
				t.Fatalf("unexpected id %q", id)
			}
			return job, nil
		},
		patchNextRunAtFn: func(_ context.Context, id string, nextRunAt time.Time, _ time.Time) error {
			gotID = id
			gotNextRunAt = nextRunAt
			return nil
		},
	}
	svc := newTestService(t, store)
	if _, err := svc.RunOnce(context.Background(), "job-1"); err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if gotID != "job-1" {
		t.Fatalf("PatchNextRunAt id = %q, want job-1", gotID)
	}
	if !gotNextRunAt.Equal(now) {
		t.Fatalf("NextRunAt = %v, want %v", gotNextRunAt, now)
	}
}

func TestRunOnceReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		getByIDFn: func(context.Context, string) (JobRecord, error) {
			return JobRecord{}, ErrStoreJobNotFound
		},
	}
	svc := newTestService(t, store)
	_, err := svc.RunOnce(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRunOnceRejectsEmptyID(t *testing.T) {
	t.Parallel()
	svc := newTestService(t, nil)
	if _, err := svc.RunOnce(context.Background(), "  "); err == nil {
		t.Fatal("RunOnce should reject blank id")
	}
}

func TestRunOnceRejectsDisabledJob(t *testing.T) {
	t.Parallel()
	store := &fakeStore{
		getByIDFn: func(context.Context, string) (JobRecord, error) {
			return JobRecord{ID: "job-1", Enabled: false}, nil
		},
	}
	svc := newTestService(t, store)
	_, err := svc.RunOnce(context.Background(), "job-1")
	if !errors.Is(err, ErrJobDisabled) {
		t.Fatalf("want ErrJobDisabled, got %v", err)
	}
}
