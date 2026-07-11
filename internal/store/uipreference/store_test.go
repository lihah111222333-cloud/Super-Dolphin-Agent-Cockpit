package uipreference

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

type uiPreferenceQuerierStub struct {
	getValueFn func(context.Context, sqlc.GetUIPreferenceValueParams) (json.RawMessage, error)
	upsertFn   func(context.Context, sqlc.UpsertUIPreferenceParams) error
	listFn     func(context.Context, sqlc.ListUIPreferencesParams) ([]sqlc.ListUIPreferencesRow, error)
}

func (s *uiPreferenceQuerierStub) GetUIPreferenceValue(ctx context.Context, arg sqlc.GetUIPreferenceValueParams) (json.RawMessage, error) {
	if s.getValueFn != nil {
		return s.getValueFn(ctx, arg)
	}
	return nil, nil
}

func (s *uiPreferenceQuerierStub) UpsertUIPreference(ctx context.Context, arg sqlc.UpsertUIPreferenceParams) error {
	if s.upsertFn != nil {
		return s.upsertFn(ctx, arg)
	}
	return nil
}

func (s *uiPreferenceQuerierStub) ListUIPreferences(ctx context.Context, arg sqlc.ListUIPreferencesParams) ([]sqlc.ListUIPreferencesRow, error) {
	if s.listFn != nil {
		return s.listFn(ctx, arg)
	}
	return nil, nil
}

func TestGetValueForwardsParamsAndReturnsBytes(t *testing.T) {
	t.Parallel()

	var captured sqlc.GetUIPreferenceValueParams
	s := &store{q: &uiPreferenceQuerierStub{
		getValueFn: func(_ context.Context, arg sqlc.GetUIPreferenceValueParams) (json.RawMessage, error) {
			captured = arg
			return []byte(`{"theme":"dark"}`), nil
		},
	}}

	got, err := s.GetValue(context.Background(), "/proj", "theme")
	if err != nil {
		t.Fatalf("GetValue() error = %v", err)
	}
	if captured.CWD != "/proj" || captured.Key != "theme" {
		t.Fatalf("GetValue() forwarded wrong params: %+v", captured)
	}
	if string(got) != `{"theme":"dark"}` {
		t.Fatalf("GetValue() = %s", got)
	}
}

func TestGetValueWrapsSQLErrNoRowsAsNotFound(t *testing.T) {
	t.Parallel()

	s := &store{q: &uiPreferenceQuerierStub{
		getValueFn: func(context.Context, sqlc.GetUIPreferenceValueParams) (json.RawMessage, error) {
			return nil, sql.ErrNoRows
		},
	}}

	got, err := s.GetValue(context.Background(), "/proj", "missing")
	if got != nil {
		t.Fatalf("GetValue() got = %s, want nil on error", got)
	}
	if !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("GetValue() error = %v, want wrap of ErrNotFound", err)
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) || storeErr.Operation != "get" || storeErr.Entity != "ui_preference" {
		t.Fatalf("GetValue() error metadata = %+v", err)
	}
}

func TestUpsertForwardsParamsAndReturnsNilOnSuccess(t *testing.T) {
	t.Parallel()

	var captured sqlc.UpsertUIPreferenceParams
	s := &store{q: &uiPreferenceQuerierStub{
		upsertFn: func(_ context.Context, arg sqlc.UpsertUIPreferenceParams) error {
			captured = arg
			return nil
		},
	}}

	err := s.Upsert(context.Background(), UpsertParams{
		Cwd:   "/proj",
		Key:   "theme",
		Value: []byte(`{"theme":"light"}`),
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if captured.CWD != "/proj" || captured.Key != "theme" || string(captured.Value) != `{"theme":"light"}` {
		t.Fatalf("Upsert() forwarded wrong params: %+v", captured)
	}
	if captured.UpdatedAt == 0 {
		t.Fatalf("Upsert() UpdatedAt = 0, want Go epoch milliseconds")
	}
}

func TestUpsertRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	called := false
	s := &store{q: &uiPreferenceQuerierStub{
		upsertFn: func(context.Context, sqlc.UpsertUIPreferenceParams) error {
			called = true
			return nil
		},
	}}

	err := s.Upsert(context.Background(), UpsertParams{Cwd: "/proj", Key: "theme", Value: []byte(`not-json`)})
	if err == nil {
		t.Fatal("Upsert() error = nil, want invalid JSON error")
	}
	if called {
		t.Fatal("Upsert() called query despite invalid JSON")
	}
}

func TestUpsertWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("write fail")
	s := &store{q: &uiPreferenceQuerierStub{
		upsertFn: func(context.Context, sqlc.UpsertUIPreferenceParams) error { return sentinel },
	}}

	err := s.Upsert(context.Background(), UpsertParams{Value: []byte(`{}`)})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Upsert() error = %v, want wrap of sentinel", err)
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) || storeErr.Operation != "upsert" || storeErr.Entity != "ui_preference" {
		t.Fatalf("Upsert() error metadata = %+v", err)
	}
}

func TestListForwardsCwdAndMapsRows(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0).UTC()
	var capturedCwd string
	s := &store{q: &uiPreferenceQuerierStub{
		listFn: func(_ context.Context, arg sqlc.ListUIPreferencesParams) ([]sqlc.ListUIPreferencesRow, error) {
			if arg.CWDFilter != "/proj" {
				t.Fatalf("List() forwarded wrong params: %+v", arg)
			}
			cwd, ok := arg.CWDFilter.(string)
			if !ok {
				t.Fatalf("List() cwd param type = %T, want string", arg.CWDFilter)
			}
			capturedCwd = cwd
			return []sqlc.ListUIPreferencesRow{
				{Key: "theme", Value: []byte(`"dark"`), CWD: "", UpdatedAt: platformdb.Millis(now)},
				{Key: "layout", Value: []byte(`"wide"`), CWD: "/proj", UpdatedAt: platformdb.Millis(now)},
			}, nil
		},
	}}

	got, err := s.List(context.Background(), "/proj")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if capturedCwd != "/proj" {
		t.Fatalf("List() forwarded cwd = %q, want /proj", capturedCwd)
	}
	assertUIPreferenceRows(t, got, now)
}

func assertUIPreferenceRows(t *testing.T, got []UIPreference, now time.Time) {
	t.Helper()

	if len(got) != 2 {
		t.Fatalf("List() len = %d, want 2", len(got))
	}
	if got[0].Key != "theme" {
		t.Fatalf("List()[0] = %+v", got[0])
	}
	if got[0].Cwd != "" {
		t.Fatalf("List()[0] = %+v", got[0])
	}
	if string(got[0].Value) != `"dark"` {
		t.Fatalf("List()[0] = %+v", got[0])
	}
	if !got[0].UpdatedAt.Equal(now) {
		t.Fatalf("List()[0] = %+v", got[0])
	}
	if got[1].Key != "layout" {
		t.Fatalf("List()[1] = %+v", got[1])
	}
	if got[1].Cwd != "/proj" {
		t.Fatalf("List()[1] = %+v", got[1])
	}
	if string(got[1].Value) != `"wide"` {
		t.Fatalf("List()[1] = %+v", got[1])
	}
}

func TestListReturnsEmptySliceWhenNoRows(t *testing.T) {
	t.Parallel()

	s := &store{q: &uiPreferenceQuerierStub{
		listFn: func(context.Context, sqlc.ListUIPreferencesParams) ([]sqlc.ListUIPreferencesRow, error) {
			return nil, nil
		},
	}}
	got, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("List() = %+v, want non-nil empty slice", got)
	}
}

func TestListWrapsQuerierError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("list fail")
	s := &store{q: &uiPreferenceQuerierStub{
		listFn: func(context.Context, sqlc.ListUIPreferencesParams) ([]sqlc.ListUIPreferencesRow, error) {
			return nil, sentinel
		},
	}}
	_, err := s.List(context.Background(), "/proj")
	if !errors.Is(err, sentinel) {
		t.Fatalf("List() error = %v, want wrap of sentinel", err)
	}
	var storeErr *platformdb.StoreError
	if !errors.As(err, &storeErr) || storeErr.Operation != "list" {
		t.Fatalf("List() error metadata = %+v", err)
	}
}
