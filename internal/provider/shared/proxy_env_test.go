package shared

import (
	"reflect"
	"strings"
	"testing"
)

func TestEnsureLoopbackNoProxyPreservesProxyAndMergesLoopback(t *testing.T) {
	t.Parallel()
	got := EnsureLoopbackNoProxy([]string{
		"PATH=/usr/bin",
		"HTTP_PROXY=http://127.0.0.1:7897",
		"no_proxy=example.com,localhost",
		"NO_PROXY=127.0.0.1",
	})
	text := strings.Join(got, "\n")
	for _, want := range []string{
		"PATH=/usr/bin",
		"HTTP_PROXY=http://127.0.0.1:7897",
		"NO_PROXY=example.com,localhost,127.0.0.1,::1",
		"no_proxy=example.com,localhost,127.0.0.1,::1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in env, got %#v", want, got)
		}
	}
}

func TestEnsureLoopbackNoProxyLeavesEnvWithoutProxyUnchanged(t *testing.T) {
	t.Parallel()
	env := []string{"PATH=/usr/bin", "HOME=/tmp/home"}
	got := EnsureLoopbackNoProxy(env)
	if !reflect.DeepEqual(got, env) {
		t.Fatalf("EnsureLoopbackNoProxy() = %#v, want %#v", got, env)
	}
}
