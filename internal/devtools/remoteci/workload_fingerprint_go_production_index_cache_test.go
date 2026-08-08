package remoteci

import (
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestRemoteGoProductionIndexCacheIsScopedToProfile(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	normal, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("normal production index: %v", err)
	}
	if _, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{}); err != nil {
		t.Fatalf("repeated normal production index: %v", err)
	}
	race, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{race: true})
	if err != nil {
		t.Fatalf("race production index: %v", err)
	}
	if got := snapshot.productionIndexComputations; got != 2 {
		t.Fatalf("production index computations = %d, want 2", got)
	}
	if len(normal.byPackage) != len(race.byPackage) {
		t.Fatalf("normal/race production index package count differs: %d vs %d", len(normal.byPackage), len(race.byPackage))
	}
}

func TestRemoteGoProductionIndexCacheSerializesConcurrentBuild(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	start := make(chan struct{})
	var group errgroup.Group
	for range 8 {
		group.Go(func() error {
			<-start
			_, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
			return err
		})
	}
	close(start)
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent production index: %v", err)
	}
	if got := snapshot.productionIndexComputations; got != 1 {
		t.Fatalf("concurrent production index computations = %d, want 1", got)
	}
}

func TestRemoteGoProductionIndexCacheStoresErrors(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	snapshot.goSources["fixture/main.go"] = []byte("package fixture\nfunc broken(\n")
	first, firstErr := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
	second, secondErr := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
	if firstErr == nil || secondErr == nil {
		t.Fatal("invalid production source was accepted")
	}
	if firstErr.Error() != secondErr.Error() {
		t.Fatalf("cached production index error changed: first=%v second=%v", firstErr, secondErr)
	}
	if len(first.byPackage) != 0 || len(second.byPackage) != 0 {
		t.Fatal("failed production index returned declarations")
	}
	if got := snapshot.productionIndexComputations; got != 1 {
		t.Fatalf("error production index computations = %d, want 1", got)
	}
}

func TestRemoteGoProductionIndexCacheReturnsIndependentMaps(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	first, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("first production index: %v", err)
	}
	first.byPackage["fixture"] = nil
	second, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("second production index: %v", err)
	}
	if second.byPackage["fixture"] == nil {
		t.Fatal("mutating returned production index changed cached map")
	}
}

func BenchmarkRemoteGoProductionIndexCache(b *testing.B) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture/main.go":    []byte("package fixture\nfunc ReadFixture() {}\n"),
		"support/support.go": []byte("package support\nfunc ReadFixture() {}\n"),
	}}
	if _, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{}); err != nil {
		b.Fatalf("warm production index: %v", err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{}); err != nil {
			b.Fatalf("cached production index: %v", err)
		}
	}
	b.StopTimer()
	if got := snapshot.productionIndexComputations; got != 1 {
		b.Fatalf("benchmark production index computations = %d, want 1", got)
	}
}
