package eventsurface

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

var frontendStringRE = regexp.MustCompile(`'([^']+)'`)

func TestTypedWireMethodsMatchFrontendList(t *testing.T) {
	root := repoRootForEventSurfaceTest(t)
	raw, err := os.ReadFile(filepath.Join(root, "frontend-app/src/shared/api/eventWireMethods.js"))
	if err != nil {
		t.Fatalf("read frontend event methods: %v", err)
	}
	assertStringSetEqual(t, "typed wire methods", AllTypedWireMethods(), frontendFrozenStringArray(t, raw, "EVENT_TYPED_WIRE_METHODS"))
}

func TestRawWireAllowlistMatchesFrontendList(t *testing.T) {
	root := repoRootForEventSurfaceTest(t)
	raw, err := os.ReadFile(filepath.Join(root, "frontend-app/src/shared/api/eventWireMethods.js"))
	if err != nil {
		t.Fatalf("read frontend event methods: %v", err)
	}
	spec := RawWireAllowlistSpec()
	assertStringSetEqual(t, "raw wire methods", spec.Methods, frontendFrozenStringArray(t, raw, "EVENT_RAW_WIRE_METHODS"))
	assertStringSetEqual(t, "raw wire prefixes", spec.Prefixes, frontendFrozenStringArray(t, raw, "EVENT_RAW_WIRE_PREFIXES"))
	assertStringSetEqual(t, "raw wire suffixes", spec.Suffixes, frontendFrozenStringArray(t, raw, "EVENT_RAW_WIRE_SUFFIXES"))
	assertStringSetEqual(t, "compat wire prefixes", CompatWirePrefixes(), frontendFrozenStringArray(t, raw, "EVENT_COMPAT_WIRE_PREFIXES"))
}

func TestWireAllowlistCoversLegacyAndRawOpenMethods(t *testing.T) {
	expanded := map[string]bool{}
	for _, notification := range ExpandNotifications(MethodThreadStarted, map[string]any{"threadId": "thread-1"}) {
		expanded[notification.Method] = true
	}
	for _, method := range []string{MethodUIThreadChanged, MethodUISidebarChanged} {
		if !expanded[method] {
			t.Fatalf("ExpandNotifications missing legacy expansion method %q", method)
		}
	}

	spec := RawWireAllowlistSpec()
	for _, method := range []string{
		"item/plan/delta",
		"turn/plan/delta",
		"account/rateLimits/updated",
		"approval/request",
		"thread/name/updated",
		"item/custom/requestApproval",
	} {
		if !RawWireAllowed(spec, method) {
			t.Fatalf("raw wire allowlist rejects %q", method)
		}
	}
	if RawWireAllowed(spec, "unknown/domain/event") {
		t.Fatalf("raw wire allowlist accepted unknown method")
	}
	if RawWireAllowed(spec, "workspace/run/created") {
		t.Fatalf("raw wire allowlist must not accept workspace run source events")
	}
	workspaceExpanded := map[string]bool{}
	for _, notification := range ExpandNotifications("workspace/run/created", map[string]any{"threadId": "thread-1"}) {
		workspaceExpanded[notification.Method] = true
	}
	if !workspaceExpanded[MethodUISidebarChanged] {
		t.Fatalf("workspace run source events must still trigger sidebar refresh")
	}
}

func frontendFrozenStringArray(t *testing.T, raw []byte, name string) []string {
	t.Helper()
	prefix := []byte("export const " + name + " = Object.freeze([")
	start := bytes.Index(raw, prefix)
	if start < 0 {
		t.Fatalf("frontend list %s not found", name)
	}
	rest := raw[start+len(prefix):]
	end := bytes.Index(rest, []byte("]);"))
	if end < 0 {
		t.Fatalf("frontend list %s missing closing ]);", name)
	}
	matches := frontendStringRE.FindAllSubmatch(rest[:end], -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, string(match[1]))
	}
	return out
}

func assertStringSetEqual(t *testing.T, label string, want, got []string) {
	t.Helper()
	want = sortedUniqueStrings(want)
	got = sortedUniqueStrings(got)
	if strings.Join(want, "\n") != strings.Join(got, "\n") {
		t.Fatalf("%s mismatch\nwant:\n%s\n\ngot:\n%s", label, strings.Join(want, "\n"), strings.Join(got, "\n"))
	}
}

func sortedUniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func repoRootForEventSurfaceTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("go.mod not found from %s: %v", root, err)
	}
	return root
}
