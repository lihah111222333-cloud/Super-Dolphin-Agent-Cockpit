package pidregistry

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadStableProcessIdentityRejectsGenerationDrift(t *testing.T) {
	reads := 0
	identity, err := readStableProcessIdentity(
		42,
		func(int) (string, error) {
			reads++
			if reads == 1 {
				return "generation-1", nil
			}
			return "generation-2", nil
		},
		func(int) (string, error) { return "/bin/same-executable", nil },
	)
	if !errors.Is(err, ErrStableProcessIdentityMismatch) || identity != (processIdentity{}) {
		t.Fatalf("readStableProcessIdentity() = (%+v, %v), want generation mismatch", identity, err)
	}
}

func TestReadStableProcessIdentityRejectsSwitchAtEachSampleBoundary(t *testing.T) {
	for _, boundary := range []string{"before executable", "before second start token"} {
		t.Run(boundary, func(t *testing.T) {
			generation := "generation-1"
			startReads := 0
			_, err := readStableProcessIdentity(
				42,
				func(int) (string, error) {
					startReads++
					if boundary == "before second start token" && startReads == 2 {
						generation = "generation-2"
					}
					return generation, nil
				},
				func(int) (string, error) {
					if boundary == "before executable" {
						generation = "generation-2"
					}
					return "/bin/same-executable", nil
				},
			)
			if !errors.Is(err, ErrStableProcessIdentityMismatch) {
				t.Fatalf("boundary %q error = %v, want generation mismatch", boundary, err)
			}
		})
	}
}

func TestRegisterCheckedIdentityReadFailureDoesNotRegister(t *testing.T) {
	wantErr := errors.New("identity unavailable")
	registry := New()
	registry.readIdentity = func(int) (processIdentity, error) { return processIdentity{}, wantErr }

	err := registry.RegisterChecked(4242, "test", nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RegisterChecked() error = %v, want %v", err, wantErr)
	}
	if len(registry.children) != 0 {
		t.Fatalf("registered children = %#v, want empty after identity read failure", registry.children)
	}
}

func TestSigtermOrphansRejectsIdentityMismatchAndReadFailure(t *testing.T) {
	want := ChildInfo{PID: 4242, ProcessStartToken: "start-1", ExecutableIdentity: "/bin/tool"}
	tests := []struct {
		name         string
		read         func(int) (processIdentity, error)
		wantMismatch int
		wantReadFail int
	}{
		{
			name: "identity mismatch",
			read: func(int) (processIdentity, error) {
				return processIdentity{startToken: "start-2", executable: want.ExecutableIdentity}, nil
			},
			wantMismatch: 1,
		},
		{
			name: "identity read failure",
			read: func(int) (processIdentity, error) {
				return processIdentity{}, errors.New("identity unavailable")
			},
			wantReadFail: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			termCalls := 0
			result := CleanupResult{}
			got := sigtermOrphansWithOps([]staleOrphan{{child: want}}, &result, cleanupProcessOps{
				readIdentity: tt.read,
				sendTerm: func(int) error {
					termCalls++
					return nil
				},
			})
			if len(got) != 0 || termCalls != 0 {
				t.Fatalf("sigterm result = %#v, calls = %d, want no signal", got, termCalls)
			}
			if result.IdentityMismatch != tt.wantMismatch || result.IdentityReadFailure != tt.wantReadFail {
				t.Fatalf("CleanupResult = %#v, want mismatch=%d read_failure=%d", result, tt.wantMismatch, tt.wantReadFail)
			}
			if got, want := result.hasUnresolved(), tt.wantReadFail > 0; got != want {
				t.Fatalf("CleanupResult.hasUnresolved() = %t, want %t", got, want)
			}
		})
	}
}

func TestSigkillSurvivorsRejectsPIDReuseAfterTerm(t *testing.T) {
	want := ChildInfo{PID: 4242, ProcessStartToken: "start-1", ExecutableIdentity: "/bin/tool"}
	killCalls := 0
	result := CleanupResult{}
	killed := sigkillSurvivorsWithOps([]staleOrphan{{child: want}}, &result, cleanupProcessOps{
		isAlive: func(int) bool { return true },
		readIdentity: func(int) (processIdentity, error) {
			return processIdentity{startToken: "start-2", executable: want.ExecutableIdentity}, nil
		},
		forceKill: func(int) error {
			killCalls++
			return nil
		},
	})
	if killed != 0 || killCalls != 0 {
		t.Fatalf("killed = %d, forceKill calls = %d, want no KILL after PID reuse", killed, killCalls)
	}
	if result.IdentityMismatch != 1 || result.IdentityReadFailure != 0 {
		t.Fatalf("CleanupResult = %#v, want one identity mismatch", result)
	}
}

func TestCleanupSignalsOnlyWhenIdentityMatches(t *testing.T) {
	want := ChildInfo{PID: 4242, ProcessStartToken: "start-1", ExecutableIdentity: "/bin/tool"}
	identity := processIdentity{startToken: want.ProcessStartToken, executable: want.ExecutableIdentity}
	termCalls := 0
	result := CleanupResult{}
	sigtermed := sigtermOrphansWithOps([]staleOrphan{{child: want}}, &result, cleanupProcessOps{
		readIdentity: func(int) (processIdentity, error) { return identity, nil },
		sendTerm: func(int) error {
			termCalls++
			return nil
		},
	})
	killCalls := 0
	killed := sigkillSurvivorsWithOps(sigtermed, &result, cleanupProcessOps{
		isAlive:      func(int) bool { return true },
		readIdentity: func(int) (processIdentity, error) { return identity, nil },
		forceKill: func(int) error {
			killCalls++
			return nil
		},
	})
	if termCalls != 1 || killCalls != 1 || killed != 1 {
		t.Fatalf("TERM=%d KILL=%d killed=%d, want 1/1/1", termCalls, killCalls, killed)
	}
	if result != (CleanupResult{}) {
		t.Fatalf("CleanupResult = %#v, want no refusal counters", result)
	}
}

func TestProcessIdentityMatchesBothStableComponents(t *testing.T) {
	want := processIdentity{startToken: "start-1", executable: "/bin/tool"}
	tests := []struct {
		name string
		got  processIdentity
		ok   bool
	}{
		{name: "complete match", got: want, ok: true},
		{name: "pid reused", got: processIdentity{startToken: "start-2", executable: want.executable}},
		{name: "executable changed", got: processIdentity{startToken: want.startToken, executable: "/bin/other"}},
		{name: "missing start token", got: processIdentity{executable: want.executable}},
		{name: "missing executable", got: processIdentity{startToken: want.startToken}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := processIdentityMatches(want, tt.got); got != tt.ok {
				t.Fatalf("processIdentityMatches() = %v, want %v", got, tt.ok)
			}
		})
	}
}

func TestChildInfoJSONFieldGuardAndRoundTrip(t *testing.T) {
	identity, err := readProcessIdentity(os.Getpid())
	if err != nil {
		t.Fatalf("readProcessIdentity(current) error = %v", err)
	}
	want := ChildInfo{
		PID:                os.Getpid(),
		Kind:               "test",
		StartedAt:          "2026-07-14T00:00:00Z",
		ProcessStartToken:  identity.startToken,
		ExecutableIdentity: identity.executable,
		Meta:               map[string]string{"owner": "test"},
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("json.Unmarshal(fields) error = %v", err)
	}
	producer := reflect.TypeFor[ChildInfo]()
	for i := 0; i < producer.NumField(); i++ {
		tag, _, _ := strings.Cut(producer.Field(i).Tag.Get("json"), ",")
		if tag == "" || tag == "-" {
			t.Fatalf("ChildInfo field %s has no JSON producer tag", producer.Field(i).Name)
		}
		if _, ok := fields[tag]; !ok {
			t.Fatalf("serialized ChildInfo missing producer field %q", tag)
		}
	}
	var got ChildInfo
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal(roundtrip) error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChildInfo roundtrip = %#v, want %#v", got, want)
	}
}

func TestChildInfoMissingIdentityNeverMatchesLiveProcess(t *testing.T) {
	current := processIdentity{startToken: "start-1", executable: "/bin/tool"}
	children := []ChildInfo{
		{PID: 42, ExecutableIdentity: current.executable},
		{PID: 42, ProcessStartToken: current.startToken},
		{PID: 42},
	}
	for _, child := range children {
		if childIdentityMatches(child, current) {
			t.Fatalf("childIdentityMatches(%#v) = true for legacy/incomplete identity", child)
		}
	}
}

func TestCollectStaleOrphansRejectsLegacyIdentity(t *testing.T) {
	orphans, result := collectStaleOrphans([]staleFile{{registryFile: registryFile{
		Children: []ChildInfo{{PID: os.Getpid(), Kind: "legacy"}},
	}}}, nil)
	if len(orphans) != 0 {
		t.Fatalf("collectStaleOrphans() = %#v, want no legacy orphan", orphans)
	}
	if result.MissingIdentity != 1 {
		t.Fatalf("MissingIdentity = %d, want 1", result.MissingIdentity)
	}
}

func TestCleanupSignalFailuresRemainRetryable(t *testing.T) {
	want := ChildInfo{PID: 4242, ProcessStartToken: "start-1", ExecutableIdentity: "/bin/tool"}
	identity := processIdentity{startToken: want.ProcessStartToken, executable: want.ExecutableIdentity}

	termResult := CleanupResult{}
	sigtermed := sigtermOrphansWithOps([]staleOrphan{{child: want}}, &termResult, cleanupProcessOps{
		readIdentity: func(int) (processIdentity, error) { return identity, nil },
		sendTerm:     func(int) error { return errors.New("term unavailable") },
	})
	if len(sigtermed) != 0 || !termResult.hasUnresolved() {
		t.Fatalf("TERM result = %#v, cleanup = %#v, want unresolved retry", sigtermed, termResult)
	}

	killResult := CleanupResult{}
	killed := sigkillSurvivorsWithOps([]staleOrphan{{child: want}}, &killResult, cleanupProcessOps{
		isAlive:      func(int) bool { return true },
		readIdentity: func(int) (processIdentity, error) { return identity, nil },
		forceKill:    func(int) error { return errors.New("kill unavailable") },
	})
	if killed != 0 || !killResult.hasUnresolved() {
		t.Fatalf("KILL count = %d, cleanup = %#v, want unresolved retry", killed, killResult)
	}
}

func TestFinalizeStaleRegistryFilesRetainsUnresolvedCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write stale registry: %v", err)
	}
	files := []staleFile{{path: path}}
	result := CleanupResult{}
	result.markUnresolved()

	finalizeStaleRegistryFiles(files, result)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unresolved stale registry was removed: %v", err)
	}

	finalizeStaleRegistryFiles(files, CleanupResult{})
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resolved stale registry still exists: %v", err)
	}
}
