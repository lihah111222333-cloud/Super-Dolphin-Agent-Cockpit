package uistate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

func TestAddProjectConcurrentPreservesAllAdds(t *testing.T) {
	store := &slowProjectPreferenceStore{
		delay:  5 * time.Millisecond,
		values: map[string]json.RawMessage{},
	}
	svc, _, err := NewService(nil, nil, nil, store, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	root := t.TempDir()
	paths := make([]string, 12)
	for i := range paths {
		paths[i] = filepath.Join(root, fmt.Sprintf("project-%02d", i))
		if err := os.MkdirAll(paths[i], 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", paths[i], err)
		}
	}
	var wg sync.WaitGroup
	for _, path := range paths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			if _, err := svc.AddProject(context.Background(), path); err != nil {
				t.Errorf("AddProject(%q) error = %v", path, err)
			}
		}(path)
	}
	wg.Wait()
	state, err := svc.GetProjects(context.Background())
	if err != nil {
		t.Fatalf("GetProjects() error = %v", err)
	}
	if len(state.Projects) != len(paths) {
		t.Fatalf("len(projects) = %d, want %d (%#v)", len(state.Projects), len(paths), state.Projects)
	}
}

type slowProjectPreferenceStore struct {
	delay  time.Duration
	mu     sync.Mutex
	values map[string]json.RawMessage
}

func (s *slowProjectPreferenceStore) GetValue(_ context.Context, cwd, key string) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, ok := s.values[projectPrefKey(cwd, key)]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	return append(json.RawMessage(nil), raw...), nil
}

func (s *slowProjectPreferenceStore) Upsert(_ context.Context, params preferenceUpsertParams) error {
	time.Sleep(s.delay)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[projectPrefKey(params.Cwd, params.Key)] = append(json.RawMessage(nil), params.Value...)
	return nil
}

func (s *slowProjectPreferenceStore) List(_ context.Context, cwd string) ([]preferenceEntry, error) {
	time.Sleep(s.delay)
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]preferenceEntry, 0, len(s.values))
	for rawKey, value := range s.values {
		rowCwd, rowKey := splitProjectPrefKey(rawKey)
		if rowCwd != cwd {
			continue
		}
		rows = append(rows, preferenceEntry{
			Cwd:   rowCwd,
			Key:   rowKey,
			Value: append(json.RawMessage(nil), value...),
		})
	}
	return rows, nil
}

func projectPrefKey(cwd, key string) string {
	return cwd + "\x00" + key
}

func splitProjectPrefKey(value string) (string, string) {
	for i := 0; i < len(value); i++ {
		if value[i] == 0 {
			return value[:i], value[i+1:]
		}
	}
	return "", value
}
