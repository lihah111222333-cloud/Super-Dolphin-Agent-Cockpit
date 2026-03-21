package rpc

import "testing"

func TestProvideWSRouteExposesDefaultPath(t *testing.T) {
	t.Parallel()

	result := provideWSRoute(newTestServer())
	if result.Route.Path != "/ws" {
		t.Fatalf("Path = %q, want %q", result.Route.Path, "/ws")
	}
	if result.Route.Handler == nil {
		t.Fatal("Handler = nil, want non-nil")
	}
}
