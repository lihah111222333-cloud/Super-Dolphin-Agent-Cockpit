package thread

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

func TestPromptSnapshotRoundTripNormalizesNilSectionMap(t *testing.T) {
	t.Parallel()

	var saved []byte
	s := &store{q: &threadQuerierStub{
		savePromptSnapshotFn: func(_ context.Context, arg sqlc.UpdateAgentThreadPromptSnapshotParams) (int64, error) {
			saved = append([]byte(nil), arg.PromptSnapshot...)
			return 1, nil
		},
		loadPromptSnapshotFn: func(context.Context, string) ([]byte, error) {
			return append([]byte(nil), saved...), nil
		},
	}}
	want := PromptSnapshot{
		DisplayName:           "thread-1",
		BaseInstructions:      "base",
		DeveloperInstructions: "dev",
		Provider:              "codex",
		Version:               1,
		Hash:                  "hash-1",
		Generation:            7,
	}

	if err := s.SavePromptSnapshot(context.Background(), "thread-1", want); err != nil {
		t.Fatalf("SavePromptSnapshot() error = %v", err)
	}
	got, err := s.LoadPromptSnapshot(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("LoadPromptSnapshot() error = %v", err)
	}
	if got == nil {
		t.Fatal("LoadPromptSnapshot() = nil, want snapshot")
	}
	if got.SectionSnapshot == nil || len(got.SectionSnapshot) != 0 {
		t.Fatalf("SectionSnapshot = %#v, want non-nil empty map", got.SectionSnapshot)
	}
	assertPromptSnapshotEqual(t, *got, want)
}

func TestLoadPromptSnapshotLiteralNullReturnsNil(t *testing.T) {
	t.Parallel()

	s := &store{q: &threadQuerierStub{
		loadPromptSnapshotFn: func(context.Context, string) ([]byte, error) {
			return []byte(" \n null \t"), nil
		},
	}}

	got, err := s.LoadPromptSnapshot(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("LoadPromptSnapshot() error = %v", err)
	}
	if got != nil {
		t.Fatalf("LoadPromptSnapshot() = %#v, want nil", got)
	}
}

func TestSavePromptSnapshotConcurrentSafety(t *testing.T) {
	t.Parallel()

	snapshot := PromptSnapshot{
		DisplayName:           "thread-1",
		BaseInstructions:      "base",
		DeveloperInstructions: "dev",
		Provider:              "codex",
		Version:               1,
		Hash:                  "hash-1",
		SectionSnapshot:       map[string]string{"cwd": "/repo"},
		Generation:            3,
	}
	var (
		mu     sync.Mutex
		saved  [][]byte
		errCh  = make(chan error, 8)
		storeQ = &threadQuerierStub{
			savePromptSnapshotFn: func(_ context.Context, arg sqlc.UpdateAgentThreadPromptSnapshotParams) (int64, error) {
				mu.Lock()
				defer mu.Unlock()
				saved = append(saved, append([]byte(nil), arg.PromptSnapshot...))
				return 1, nil
			},
		}
	)
	s := &store{q: storeQ}

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := s.SavePromptSnapshot(context.Background(), "thread-1", snapshot); err != nil {
				errCh <- err
			}
		})
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if len(saved) != 8 {
		t.Fatalf("saved payload count = %d, want 8", len(saved))
	}
	for _, payload := range saved {
		var got PromptSnapshot
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		assertPromptSnapshotEqual(t, got, snapshot)
	}
}

func assertPromptSnapshotEqual(t *testing.T, got, want PromptSnapshot) {
	t.Helper()
	assertPromptSnapshotFieldsEqual(t, got, want)
	assertSectionSnapshotEqual(t, got.SectionSnapshot, want.SectionSnapshot)
}

func assertPromptSnapshotFieldsEqual(t *testing.T, got, want PromptSnapshot) {
	t.Helper()
	if got.BaseInstructions != want.BaseInstructions ||
		got.DeveloperInstructions != want.DeveloperInstructions ||
		got.DisplayName != want.DisplayName ||
		got.Provider != want.Provider ||
		got.Version != want.Version ||
		got.Hash != want.Hash ||
		got.Generation != want.Generation ||
		len(got.SectionSnapshot) != len(want.SectionSnapshot) {
		t.Fatalf("PromptSnapshot = %#v, want %#v", got, want)
	}
}

func assertSectionSnapshotEqual(t *testing.T, got, want map[string]string) {
	t.Helper()
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("SectionSnapshot[%q] = %q, want %q", key, got[key], value)
		}
	}
}
