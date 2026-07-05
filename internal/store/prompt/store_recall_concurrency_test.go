package prompt

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	platformsqlite "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func TestRecallTopicLockSerializesSameCWDTopicAcrossDBHandles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	path := filepath.Join(t.TempDir(), "prompt-recall.db")
	dbA := openPromptRecallDB(t, ctx, path, true)
	dbB := openPromptRecallDB(t, ctx, path, false)
	storeA := newStoreWithDB(dbA, sqlc.New(dbA))
	storeB := newStoreWithDB(dbB, sqlc.New(dbB))

	seedPromptRecallTemplate(t, ctx, dbA, "main/knowledge/a", "/repo/a")
	seedPromptRecallTemplate(t, ctx, dbA, "main/knowledge/b", "/repo/a")

	errs := runRecallWritesConcurrently(
		t,
		ctx,
		func() error {
			return writeRecallSectionWithBusinessScan(ctx, storeA, "/repo/a", "main/knowledge/a", "recall_sqlc_a", "sqlc-workflow", "A body")
		},
		func() error {
			return writeRecallSectionWithBusinessScan(ctx, storeB, "/repo/a", "main/knowledge/b", "recall_sqlc_b", "sqlc-workflow", "B body")
		},
	)
	assertOneSuccessOneDuplicate(t, errs)
	assertVisibleRecallSectionCount(t, ctx, storeA, "/repo/a", "sqlc-workflow", 1)
}

func TestRecallTopicLockRetriesBusyUntilConcurrentWriterCommits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	path := filepath.Join(t.TempDir(), "prompt-recall.db")
	dbA := openPromptRecallDB(t, ctx, path, true)
	dbB := openPromptRecallDB(t, ctx, path, false)
	if _, err := dbB.ExecContext(ctx, "PRAGMA busy_timeout = 0"); err != nil {
		t.Fatalf("disable dbB busy_timeout: %v", err)
	}
	storeA := newStoreWithDB(dbA, sqlc.New(dbA))
	storeB := newStoreWithDB(dbB, sqlc.New(dbB))

	seedPromptRecallTemplate(t, ctx, dbA, "main/knowledge/a", "/repo/a")
	seedPromptRecallTemplate(t, ctx, dbA, "main/knowledge/b", "/repo/a")

	lockHeld := make(chan struct{})
	releaseLock := make(chan struct{})
	leftDone := make(chan error, 1)
	leftWorkerDone := make(chan struct{})
	registerRecallGoroutineCleanup(t, leftWorkerDone, "recall lock holder")
	go func() {
		defer close(leftWorkerDone)
		leftDone <- storeA.WithTx(ctx, func(txStore Store) error {
			template, err := txStore.Get(ctx, "main/knowledge/a")
			if err != nil {
				return err
			}
			if err := txStore.LockRecallTopicInCWD(ctx, "/repo/a", "sqlc-workflow"); err != nil {
				return err
			}
			close(lockHeld)
			select {
			case <-releaseLock:
			case <-ctx.Done():
				return ctx.Err()
			}
			section, err := txStore.UpsertSection(ctx, PromptTemplateSection{
				TemplateID:  template.ID,
				SectionKey:  "recall_sqlc_a",
				Region:      "dynamic",
				Ordinal:     100,
				Body:        "A body",
				EnableWhen:  []byte(`{}`),
				Enabled:     true,
				TriggerType: "recall",
				RecallTopic: "sqlc-workflow",
			})
			if err != nil {
				return err
			}
			return txStore.UpsertRecallTopicTargetInCWD(ctx, "/repo/a", section.RecallTopic, section.TemplateID, section.SectionKey)
		})
	}()

	select {
	case <-lockHeld:
	case <-ctx.Done():
		t.Fatalf("waiting for held recall topic lock: %v", ctx.Err())
	}
	timer := time.AfterFunc(150*time.Millisecond, func() { close(releaseLock) })
	defer timer.Stop()

	rightErr := writeRecallSectionWithBusinessScan(ctx, storeB, "/repo/a", "main/knowledge/b", "recall_sqlc_b", "sqlc-workflow", "B body")
	leftErr := <-leftDone

	assertOneSuccessOneDuplicate(t, []error{leftErr, rightErr})
	assertVisibleRecallSectionCount(t, ctx, storeA, "/repo/a", "sqlc-workflow", 1)
}

func TestRecallTopicLockAllowsSameTopicInDifferentCWDsAcrossDBHandles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	path := filepath.Join(t.TempDir(), "prompt-recall.db")
	dbA := openPromptRecallDB(t, ctx, path, true)
	dbB := openPromptRecallDB(t, ctx, path, false)
	storeA := newStoreWithDB(dbA, sqlc.New(dbA))
	storeB := newStoreWithDB(dbB, sqlc.New(dbB))

	seedPromptRecallTemplate(t, ctx, dbA, "main/knowledge/a", "/repo/a")
	seedPromptRecallTemplate(t, ctx, dbA, "main/knowledge/b", "/repo/b")

	errs := runRecallWritesConcurrently(
		t,
		ctx,
		func() error {
			return writeRecallSectionWithBusinessScan(ctx, storeA, "/repo/a", "main/knowledge/a", "recall_sqlc", "sqlc-workflow", "A body")
		},
		func() error {
			return writeRecallSectionWithBusinessScan(ctx, storeB, "/repo/b", "main/knowledge/b", "recall_sqlc", "sqlc-workflow", "B body")
		},
	)
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent different-cwd recall write error = %v, want nil; all=%v", err, errs)
		}
	}
	assertVisibleRecallSectionCount(t, ctx, storeA, "/repo/a", "sqlc-workflow", 1)
	assertVisibleRecallSectionCount(t, ctx, storeA, "/repo/b", "sqlc-workflow", 1)
}

func openPromptRecallDB(t *testing.T, ctx context.Context, path string, migrate bool) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", promptRecallTestDSN(path))
	if err != nil {
		t.Fatalf("open sqlite prompt recall db: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite prompt recall db: %v", err)
		}
	})
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = FULL",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			t.Fatalf("apply %s: %v", pragma, err)
		}
	}
	if migrate {
		dir := filepath.Join(promptRepoRoot(t), "internal", "platform", "db", "sqlite", "migrations")
		if err := platformsqlite.RunMigrations(ctx, db, dir); err != nil {
			t.Fatalf("run sqlite migrations: %v", err)
		}
	}
	return db
}

func promptRecallTestDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout=5000")
	q.Add("_pragma", "foreign_keys=ON")
	q.Add("_pragma", "journal_mode=WAL")
	q.Add("_pragma", "synchronous=FULL")
	return path + "?" + q.Encode()
}

func promptRepoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func seedPromptRecallTemplate(t *testing.T, ctx context.Context, db *sql.DB, promptKey, cwd string) {
	t.Helper()

	now := time.Now().UTC().UnixMilli()
	_, err := db.ExecContext(ctx, `
		INSERT INTO prompt_templates (
			prompt_key, title, agent_key, tool_name, prompt_text,
			variables, tags, description, when_to_use, enabled,
			manually_edited, match_when, priority, created_by, updated_by,
			created_at, updated_at
		) VALUES (?, ?, 'main', '', '', '{}', ?, 'recall test template',
			'Use for recall topic lock tests.', 1, 0, '{}', 0, 'test', 'test', ?, ?)`,
		promptKey, promptKey, fmt.Sprintf(`["scope.cwd:%s","intent:recall"]`, cwd), now, now)
	if err != nil {
		t.Fatalf("seed prompt template %s: %v", promptKey, err)
	}
}

func runRecallWritesConcurrently(t *testing.T, ctx context.Context, left, right func() error) []error {
	t.Helper()
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	workersDone := make(chan struct{})
	registerRecallGoroutineCleanup(t, workersDone, "recall write")
	go func() {
		defer wg.Done()
		<-start
		errs[0] = left()
	}()
	go func() {
		defer wg.Done()
		<-start
		errs[1] = right()
	}()
	close(start)
	wg.Wait()
	close(workersDone)
	if err := ctx.Err(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

func registerRecallGoroutineCleanup(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s goroutines did not stop", label)
		}
	})
}

func writeRecallSectionWithBusinessScan(ctx context.Context, store Store, cwd, promptKey, sectionKey, topic, body string) error {
	return store.WithTx(ctx, func(txStore Store) error {
		template, err := txStore.Get(ctx, promptKey)
		if err != nil {
			return err
		}
		if err := txStore.LockRecallTopicInCWD(ctx, cwd, topic); err != nil {
			return err
		}
		templates, err := txStore.List(ctx, ListFilter{CWD: cwd, Limit: 100})
		if err != nil {
			return err
		}
		sections, err := recallSectionsByTemplateID(ctx, txStore, templates)
		if err != nil {
			return err
		}
		if recallTopicExistsForAnotherTarget(templates, sections, cwd, topic, template.ID, sectionKey) {
			return fmt.Errorf("dashboard: duplicate recall_topic %q in cwd", topic)
		}
		section, err := txStore.UpsertSection(ctx, PromptTemplateSection{
			TemplateID:  template.ID,
			SectionKey:  sectionKey,
			Region:      "dynamic",
			Ordinal:     100,
			Body:        body,
			EnableWhen:  []byte(`{}`),
			Enabled:     true,
			TriggerType: "recall",
			RecallTopic: topic,
		})
		if err != nil {
			return err
		}
		return txStore.UpsertRecallTopicTargetInCWD(ctx, cwd, section.RecallTopic, section.TemplateID, section.SectionKey)
	})
}

func recallSectionsByTemplateID(ctx context.Context, store Store, templates []PromptTemplate) (map[int64][]PromptTemplateSection, error) {
	ids := make([]int64, 0, len(templates))
	for _, template := range templates {
		if template.ID != 0 {
			ids = append(ids, template.ID)
		}
	}
	out := map[int64][]PromptTemplateSection{}
	if len(ids) == 0 {
		return out, nil
	}
	sections, err := store.ListSectionsByTemplateIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, section := range sections {
		out[section.TemplateID] = append(out[section.TemplateID], section)
	}
	return out, nil
}

func recallTopicExistsForAnotherTarget(
	templates []PromptTemplate,
	sections map[int64][]PromptTemplateSection,
	cwd, topic string,
	templateID int64,
	sectionKey string,
) bool {
	for _, template := range templates {
		if !template.Enabled || !promptRecallTestTemplateVisibleForCWD(template, cwd) {
			continue
		}
		for _, section := range sections[template.ID] {
			if !section.Enabled || strings.TrimSpace(section.TriggerType) != "recall" || strings.TrimSpace(section.RecallTopic) != topic {
				continue
			}
			if section.TemplateID != templateID || strings.TrimSpace(section.SectionKey) != sectionKey {
				return true
			}
		}
	}
	return false
}

func promptRecallTestTemplateVisibleForCWD(template PromptTemplate, cwd string) bool {
	tags := string(template.Tags)
	return strings.Contains(tags, `"scope.global"`) || strings.Contains(tags, `"scope.cwd:`+strings.TrimSpace(cwd)+`"`)
}

func assertOneSuccessOneDuplicate(t *testing.T, errs []error) {
	t.Helper()

	successes := 0
	duplicates := 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case strings.Contains(err.Error(), "duplicate recall_topic"):
			duplicates++
		default:
			t.Fatalf("concurrent recall write unexpected error = %v; all=%v", err, errs)
		}
	}
	if successes != 1 || duplicates != 1 {
		t.Fatalf("concurrent recall writes successes=%d duplicates=%d, want 1/1; all=%v", successes, duplicates, errs)
	}
}

func assertVisibleRecallSectionCount(t *testing.T, ctx context.Context, store Store, cwd, topic string, want int) {
	t.Helper()

	sections, err := store.ListRecallSections(ctx, cwd)
	if err != nil {
		t.Fatalf("ListRecallSections(%s): %v", cwd, err)
	}
	got := 0
	for _, section := range sections {
		if strings.TrimSpace(section.RecallTopic) == topic {
			got++
		}
	}
	if got != want {
		t.Fatalf("visible recall section count for cwd=%q topic=%q = %d, want %d; sections=%+v", cwd, topic, got, want, sections)
	}
}
