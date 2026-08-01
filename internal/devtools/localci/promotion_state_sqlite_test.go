package localci

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAcceptedImageStateSQLiteImportsOnceAndRejectsStaleCAS(t *testing.T) {
	root := acceptedImageCanonicalTempDir(t)
	fixture := newAcceptedImageCryptoFixture(t)
	legacy, err := NewAcceptedImageState(root, fixture.verifier, fixture.ancestry)
	if err != nil {
		t.Fatal(err)
	}
	record := fixture.signedRecord(t, 1, "", imageStateCommit, imageStateTree)
	if err := legacy.Bootstrap(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	database := openPromotionSQLiteTestDB(t)
	state, err := NewAcceptedImageStateSQLite(database, root, fixture.verifier, fixture.ancestry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, acceptedImageStateName), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := state.Load(context.Background())
	if err != nil || loaded.Generation != 1 {
		t.Fatalf("Load() = %#v, %v", loaded, err)
	}
	promotion := fixture.promotion(t, record, imageStateNext, imageStateTree)
	if err := state.PromoteCAS(context.Background(), promotion); err != nil {
		t.Fatal(err)
	}
	if err := state.PromoteCAS(context.Background(), promotion); !errors.Is(err, ErrAcceptedImageCASConflict) {
		t.Fatalf("stale PromoteCAS() = %v", err)
	}
}

func TestAcceptedImageStateSQLiteConcurrentCASHasOneWinner(t *testing.T) {
	root := acceptedImageCanonicalTempDir(t)
	fixture := newAcceptedImageCryptoFixture(t)
	database := openPromotionSQLiteTestDB(t)
	state, err := NewAcceptedImageStateSQLite(database, root, fixture.verifier, fixture.ancestry)
	if err != nil {
		t.Fatal(err)
	}
	current := fixture.signedRecord(t, 1, "", imageStateCommit, imageStateTree)
	if err := state.Bootstrap(context.Background(), current); err != nil {
		t.Fatal(err)
	}
	promotion := fixture.promotion(t, current, imageStateNext, imageStateTree)
	var winners int
	var winnerMu sync.Mutex
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			if err := state.PromoteCAS(context.Background(), promotion); err == nil {
				winnerMu.Lock()
				winners++
				winnerMu.Unlock()
			} else if !errors.Is(err, ErrAcceptedImageCASConflict) {
				t.Errorf("PromoteCAS() = %v", err)
			}
		})
	}
	group.Wait()
	if winners != 1 {
		t.Fatalf("CAS winners = %d, want 1", winners)
	}
}

func openPromotionSQLiteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "coordinator.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsurePromotionAuthoritySQLiteSchema(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return database
}
