package cron

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestCronListCursorCanonicalAndFailFast(t *testing.T) {
	cursor := cronListCursor{Version: 1, CreatedAt: 1720000000000, ID: "job-b"}
	raw, err := encodeCronListCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeCronListCursor(raw)
	if err != nil || got != cursor {
		t.Fatalf("decode = %#v, %v", got, err)
	}
	for _, invalid := range []string{
		"not-base64",
		raw + "=",
		"eyJ2IjoxLCJjIjowLCJpIjoieCJ9",
		encodeRawCronCursor(`{"v":1,"c":1720000000000,"i":"job-b","unknown":true}`),
		encodeRawCronCursor(`{"v":1,"c":1720000000000,"i":"job-b","i":"job-c"}`),
		encodeRawCronCursor(` { "v" : 1 , "c" : 1720000000000 , "i" : "job-b" } `),
		encodeRawCronCursor(`{"i":"job-b","c":1720000000000,"v":1}`),
	} {
		if _, err := decodeCronListCursor(invalid); err == nil {
			t.Fatalf("cursor %q accepted", invalid)
		}
	}
}

func TestCronListPageSQLiteKeysetLimitPlusOneAndStableSameTimestamp(t *testing.T) {
	now := time.UnixMilli(1_720_000_000_000).UTC()
	ctx, store := newCronListPageFixture(t, "list-page-keyset", now, "job-a", "job-b", "job-c", "job-d", "job-e")
	first := mustListCronPage(t, ctx, store, ListJobsPageParams{Limit: 2})
	assertCronPage(t, first, []string{"job-e", "job-d"}, true)
	assertCronCursor(t, first.NextCursor, now, "job-d")
	second := mustListCronPage(t, ctx, store, ListJobsPageParams{Limit: 2, Cursor: first.NextCursor})
	assertCronPage(t, second, []string{"job-c", "job-b"}, true)
	assertCronCursor(t, second.NextCursor, now, "job-b")
	third := mustListCronPage(t, ctx, store, ListJobsPageParams{Limit: 2, Cursor: second.NextCursor})
	assertCronPage(t, third, []string{"job-a"}, false)
}

func TestCronListPageSQLiteExcludesNewRowsAndToleratesDeletionBetweenPages(t *testing.T) {
	now := time.UnixMilli(1_720_000_000_000).UTC()
	ctx, store := newCronListPageFixture(t, "list-page-mutation", now, "job-a", "job-b", "job-c", "job-d")
	first := mustListCronPage(t, ctx, store, ListJobsPageParams{Limit: 2})
	assertCronPage(t, first, []string{"job-d", "job-c"}, true)
	seedCronListJob(t, ctx, store, "job-new", now.Add(time.Millisecond))
	if err := store.DeleteJob(ctx, "job-b"); err != nil {
		t.Fatalf("delete between pages: %v", err)
	}
	next := mustListCronPage(t, ctx, store, ListJobsPageParams{Limit: 2, Cursor: first.NextCursor})
	assertCronPage(t, next, []string{"job-a"}, false)
	fresh := mustListCronPage(t, ctx, store, ListJobsPageParams{Limit: 2})
	assertCronPage(t, fresh, []string{"job-new", "job-d"}, true)
}

func TestCronListPageRejectsUnboundedValuesAndInvalidCursor(t *testing.T) {
	ctx := context.Background()
	store, db := openSubmitRunStore(t, "list-page-invalid")
	t.Cleanup(func() { _ = db.Close() })
	for _, limit := range []int32{0, -1, maxCronListLimit + 1} {
		if _, err := store.ListJobsPage(ctx, ListJobsPageParams{Limit: limit}); !errors.Is(err, ErrInvalidListLimit) {
			t.Fatalf("limit %d error = %v, want ErrInvalidListLimit", limit, err)
		}
	}
	if _, err := store.ListJobsPage(ctx, ListJobsPageParams{Limit: 1, Cursor: "not-a-cursor"}); !errors.Is(err, ErrInvalidListCursor) {
		t.Fatalf("invalid cursor error = %v, want ErrInvalidListCursor", err)
	}
}

func TestCronJobMutationsReturnNotFoundWhenTargetDoesNotExist(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, db := openSubmitRunStore(t, "mutation-not-found")
	t.Cleanup(func() { _ = db.Close() })

	if err := store.DeleteJob(ctx, "missing-job"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("DeleteJob(missing) error = %v, want ErrJobNotFound", err)
	}
	if err := store.SetJobEnabled(ctx, "missing-job", true, time.Now().UTC()); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("SetJobEnabled(missing) error = %v, want ErrJobNotFound", err)
	}
}

func seedCronListJob(t *testing.T, ctx context.Context, store Store, id string, createdAt time.Time) {
	t.Helper()
	if _, err := store.CreateJob(ctx, CreateJobParams{
		ID: id, Name: id, Prompt: "page", ScheduleExpr: "0 9 * * *", Timezone: "UTC", Provider: ProviderCodex,
		CWD: "/repo", Enabled: true, NextRunAt: createdAt.Add(time.Hour), CreatedAt: createdAt, UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func newCronListPageFixture(t *testing.T, name string, createdAt time.Time, ids ...string) (context.Context, Store) {
	t.Helper()
	ctx := context.Background()
	store, db := openSubmitRunStore(t, name)
	t.Cleanup(func() { _ = db.Close() })
	for _, id := range ids {
		seedCronListJob(t, ctx, store, id, createdAt)
	}
	return ctx, store
}

func mustListCronPage(t *testing.T, ctx context.Context, store Store, params ListJobsPageParams) JobPage {
	t.Helper()
	page, err := store.ListJobsPage(ctx, params)
	if err != nil {
		t.Fatalf("ListJobsPage(%+v): %v", params, err)
	}
	return page
}

func assertCronPage(t *testing.T, page JobPage, ids []string, hasMore bool) {
	t.Helper()
	assertCronJobIDs(t, page.Jobs, ids)
	if page.HasMore != hasMore {
		t.Fatalf("page has_more = %v, want %v", page.HasMore, hasMore)
	}
	if hasMore && page.NextCursor == "" {
		t.Fatal("continuing page has empty cursor")
	}
	if !hasMore && page.NextCursor != "" {
		t.Fatalf("final page cursor = %q", page.NextCursor)
	}
}

func assertCronCursor(t *testing.T, raw string, createdAt time.Time, id string) {
	t.Helper()
	cursor, err := decodeCronListCursor(raw)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if cursor.CreatedAt != createdAt.UnixMilli() {
		t.Fatalf("cursor created_at = %d", cursor.CreatedAt)
	}
	if cursor.ID != id {
		t.Fatalf("cursor id = %q, want %q", cursor.ID, id)
	}
}

func cronJobIDs(jobs []Job) []string {
	ids := make([]string, len(jobs))
	for i, job := range jobs {
		ids[i] = job.ID
	}
	return ids
}

func assertCronJobIDs(t *testing.T, jobs []Job, want []string) {
	t.Helper()
	if got := cronJobIDs(jobs); !reflect.DeepEqual(got, want) {
		t.Fatalf("cron job IDs = %v, want %v", got, want)
	}
}

func encodeRawCronCursor(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
