package unified

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"golang.org/x/sync/errgroup"
)

type stubThreadLookup struct {
	thread *contract.SessionThreadRef
	err    error
}

func (s stubThreadLookup) GetByThreadID(context.Context, string) (*contract.SessionThreadRef, error) {
	return s.thread, s.err
}

type mapThreadLookup struct {
	refs      map[string]*contract.SessionThreadRef
	errs      map[string]error
	firstMiss map[string]bool
	calls     map[string]int
}

func (s *mapThreadLookup) GetByThreadID(_ context.Context, threadID string) (*contract.SessionThreadRef, error) {
	if s.firstMiss[threadID] && s.calls[threadID] == 0 {
		s.calls[threadID]++
		return nil, platformdb.ErrNotFound
	}
	s.calls[threadID]++
	if err, ok := s.errs[threadID]; ok {
		return nil, err
	}
	if ref, ok := s.refs[threadID]; ok {
		return ref, nil
	}
	return nil, platformdb.ErrNotFound
}

func autoResumePromptSnapshotForTest() contract.PromptAssemblySnapshot {
	return contract.PromptAssemblySnapshot{
		DisplayName:      "resume",
		BaseInstructions: "resume system prompt",
		Provider:         "codex",
		Version:          contract.PromptAssemblySnapshotVersion,
		Hash:             "snapshot-hash",
	}
}

type stubBindingLookup struct {
	bindings           map[string]*contract.SessionBinding
	errs               map[string]error
	agentErrs          map[string]error
	recoveryHomeByPath map[string]string
}

type unifiedRecoveryTestFixture struct {
	t                  *testing.T
	recoveryHomeByPath map[string]string
}

func newUnifiedRecoveryTestFixture(t *testing.T) *unifiedRecoveryTestFixture {
	t.Helper()
	return &unifiedRecoveryTestFixture{
		t:                  t,
		recoveryHomeByPath: make(map[string]string),
	}
}

func (f *unifiedRecoveryTestFixture) registerRecoveryHome(path, home string) error {
	if f == nil || f.t == nil || f.recoveryHomeByPath == nil {
		return errors.New("unified recovery test fixture owner is required")
	}
	if path == "" || home == "" {
		return errors.New("unified recovery test rollout path and home are required")
	}
	if _, exists := f.recoveryHomeByPath[path]; exists {
		return fmt.Errorf("unified recovery test rollout path %q is already registered", path)
	}
	f.recoveryHomeByPath[path] = home
	f.t.Cleanup(func() {
		delete(f.recoveryHomeByPath, path)
	})
	return nil
}

func (f *unifiedRecoveryTestFixture) bindingLookup(lookup stubBindingLookup) stubBindingLookup {
	lookup.recoveryHomeByPath = f.recoveryHomeByPath
	return lookup
}

func (s stubBindingLookup) GetByProviderThread(_ context.Context, provider, providerThreadID string) (*contract.SessionBinding, error) {
	key := provider + ":" + providerThreadID
	if err, ok := s.errs[key]; ok {
		return nil, err
	}
	if binding, ok := s.bindings[key]; ok {
		return authorizeUnifiedRecoveryTestBinding(binding, s.recoveryHomeByPath)
	}
	return nil, platformdb.ErrNotFound
}

func (s stubBindingLookup) GetByAgentID(_ context.Context, agentID string) (*contract.SessionBinding, error) {
	if err, ok := s.agentErrs[agentID]; ok {
		return nil, err
	}
	for _, b := range s.bindings {
		if b != nil && b.AgentID == agentID {
			return authorizeUnifiedRecoveryTestBinding(b, s.recoveryHomeByPath)
		}
	}
	return nil, platformdb.ErrNotFound
}

func authorizeUnifiedRecoveryTestBinding(
	binding *contract.SessionBinding,
	recoveryHomeByPath map[string]string,
) (*contract.SessionBinding, error) {
	if binding == nil {
		return nil, nil
	}
	copy := *binding
	if copy.ProviderRecoveryHome != "" || copy.RolloutPath == "" {
		return &copy, nil
	}
	if recoveryHomeByPath == nil {
		return nil, errors.New("unified recovery test fixture owner is required")
	}
	home, ok := recoveryHomeByPath[copy.RolloutPath]
	if !ok {
		return nil, fmt.Errorf("unified recovery test rollout path %q is not registered", copy.RolloutPath)
	}
	if home == "" {
		return nil, fmt.Errorf("unified recovery test rollout path %q has an empty home", copy.RolloutPath)
	}
	copy.ProviderRecoveryHome = home
	return &copy, nil
}

func TestUnifiedRecoveryTestFixtureDeletesMappingAfterSubtest(t *testing.T) {
	var fixture *unifiedRecoveryTestFixture
	var path string
	t.Run("register", func(t *testing.T) {
		fixture = newUnifiedRecoveryTestFixture(t)
		var fixtureErr error
		if path, fixtureErr = fixture.writeExistingProviderHistoryFile("11111111-aaaa-bbbb-cccc-111111111122"); fixtureErr != nil {
			t.Fatalf("write provider history fixture: %v", fixtureErr)
		}
	})
	if _, exists := fixture.recoveryHomeByPath[path]; exists {
		t.Fatalf("recovery home mapping for %q leaked after subtest cleanup", path)
	}
}

func TestUnifiedRecoveryTestFixtureRejectsInvalidState(t *testing.T) {
	wantError := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s error = nil, want fail-fast error", name)
		}
	}
	var nilFixture *unifiedRecoveryTestFixture
	wantError("nil owner", nilFixture.registerRecoveryHome("/rollout", "/home"))
	fixture := newUnifiedRecoveryTestFixture(t)
	wantError("missing path", fixture.registerRecoveryHome("", "/home"))
	wantError("empty home", fixture.registerRecoveryHome("/rollout", ""))
	if err := fixture.registerRecoveryHome("/rollout", "/home"); err != nil {
		t.Fatalf("register initial recovery home: %v", err)
	}
	wantError("duplicate path", fixture.registerRecoveryHome("/rollout", "/other-home"))
	binding := &contract.SessionBinding{RolloutPath: "/rollout"}
	for name, homes := range map[string]map[string]string{
		"nil mapping owner": nil,
		"missing mapping":   {},
		"empty mapping":     {"/rollout": ""},
	} {
		_, err := authorizeUnifiedRecoveryTestBinding(binding, homes)
		wantError(name, err)
	}
}

type sequenceThreadLookup struct {
	refs  []*contract.SessionThreadRef
	errs  []error
	calls int
}

func (s *sequenceThreadLookup) GetByThreadID(context.Context, string) (*contract.SessionThreadRef, error) {
	index := s.calls
	s.calls++
	if index >= len(s.refs) && index >= len(s.errs) {
		return nil, platformdb.ErrNotFound
	}
	var ref *contract.SessionThreadRef
	if index < len(s.refs) {
		ref = s.refs[index]
	}
	if index < len(s.errs) {
		return ref, s.errs[index]
	}
	return ref, nil
}

type keyedThreadLookup map[string]*contract.SessionThreadRef

func (s keyedThreadLookup) GetByThreadID(_ context.Context, threadID string) (*contract.SessionThreadRef, error) {
	if ref, ok := s[strings.TrimSpace(threadID)]; ok {
		return ref, nil
	}
	return nil, platformdb.ErrNotFound
}

type blockingResumeDriver struct {
	name    string
	session contract.Session
	started chan struct{}
	release chan struct{}

	mu      sync.Mutex
	resumed int
}

func newBlockingResumeDriver(name string, session contract.Session) *blockingResumeDriver {
	return &blockingResumeDriver{
		name:    name,
		session: session,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (d *blockingResumeDriver) Name() string { return d.name }

func (d *blockingResumeDriver) StartSession(context.Context, dto.StartSessionRequest) (contract.Session, error) {
	return d.session, nil
}

func (d *blockingResumeDriver) ResumeSession(ctx context.Context, _ dto.ResumeSessionRequest) (contract.Session, error) {
	d.mu.Lock()
	d.resumed++
	if d.resumed == 1 {
		close(d.started)
	}
	d.mu.Unlock()
	select {
	case <-d.release:
		return d.session, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (d *blockingResumeDriver) resumeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.resumed
}

type contextCaptureResumeDriver struct {
	name       string
	session    *contextBoundSession
	resumeCtx  context.Context
	resumeCall int
}

func (d *contextCaptureResumeDriver) Name() string { return d.name }

func (d *contextCaptureResumeDriver) StartSession(context.Context, dto.StartSessionRequest) (contract.Session, error) {
	return d.session, nil
}

func (d *contextCaptureResumeDriver) ResumeSession(ctx context.Context, _ dto.ResumeSessionRequest) (contract.Session, error) {
	d.resumeCtx = ctx
	d.resumeCall++
	d.session.ctx = ctx
	return d.session, nil
}

type contextBoundSession struct {
	generationTestSession
	ctx context.Context
}

func (s *contextBoundSession) usable() bool {
	return s.ctx != nil && s.ctx.Err() == nil
}

type sequencedBlockingResumeDriver struct {
	name     string
	sessions []contract.Session
	started  chan int
	release  chan struct{}

	mu      sync.Mutex
	resumed int
}

func newSequencedBlockingResumeDriver(name string, sessions ...contract.Session) *sequencedBlockingResumeDriver {
	return &sequencedBlockingResumeDriver{
		name:     name,
		sessions: sessions,
		started:  make(chan int, len(sessions)),
		release:  make(chan struct{}),
	}
}

func (d *sequencedBlockingResumeDriver) Name() string { return d.name }

func (d *sequencedBlockingResumeDriver) StartSession(context.Context, dto.StartSessionRequest) (contract.Session, error) {
	return nil, errors.New("unexpected start session")
}

func (d *sequencedBlockingResumeDriver) ResumeSession(ctx context.Context, _ dto.ResumeSessionRequest) (contract.Session, error) {
	d.mu.Lock()
	index := d.resumed
	d.resumed++
	d.mu.Unlock()
	d.started <- index + 1
	select {
	case <-d.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if index >= len(d.sessions) {
		return nil, fmt.Errorf("unexpected resume call %d", index+1)
	}
	return d.sessions[index], nil
}

func (d *sequencedBlockingResumeDriver) resumeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.resumed
}

func newAutoResumeResolverForTest(
	t *testing.T,
	driver contract.Driver,
	sessions *SessionManager,
	agentID string,
	publicThreadID string,
	providerThreadID string,
) *sessionResolver {
	t.Helper()
	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile(providerThreadID)
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	return &sessionResolver{
		threadStore: keyedThreadLookup{
			publicThreadID: {
				ThreadID:       publicThreadID,
				AgentID:        agentID,
				Status:         "running",
				PromptSnapshot: autoResumePromptSnapshotForTest(),
			},
		},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:" + providerThreadID: {
				Provider:         "codex",
				AgentID:          agentID,
				ProviderThreadID: providerThreadID,
				CodexThreadID:    publicThreadID,
				RolloutPath:      rolloutPath,
				Cwd:              "/repo",
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: sessions,
	}
}

func waitResumeCallForTest(t *testing.T, started <-chan int, want int) {
	t.Helper()
	call := <-started
	if call != want {
		t.Fatalf("ResumeSession call = %d, want %d", call, want)
	}
}

func assertNoResumeCallForTest(t *testing.T, started <-chan int) {
	t.Helper()
	select {
	case call := <-started:
		t.Fatalf("concurrent resolver started duplicate ResumeSession call %d", call)
	case <-time.After(50 * time.Millisecond):
	}
}

func waitSessionResultForTest(
	t *testing.T,
	name string,
	sessionCh <-chan contract.Session,
	errCh <-chan error,
) contract.Session {
	t.Helper()
	if err := <-errCh; err != nil {
		t.Fatalf("%s error = %v", name, err)
	}
	return <-sessionCh
}

func assertSessionResultBlockedForTest(
	t *testing.T,
	name string,
	sessionCh <-chan contract.Session,
	errCh <-chan error,
) {
	t.Helper()
	select {
	case session := <-sessionCh:
		t.Fatalf("%s returned pending session before activation: %#v", name, session)
	case err := <-errCh:
		t.Fatalf("%s returned error before activation: %v", name, err)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestResolveSessionRejectsAgentIDOnPublicThreadPath(t *testing.T) {
	t.Parallel()

	sessions := NewSessionManager(nil)
	session := &generationTestSession{threadID: "provider-thread-1"}
	sessions.Register("agent-1", session)
	resolver := &sessionResolver{
		threadStore:  stubThreadLookup{err: platformdb.ErrNotFound},
		bindingStore: stubBindingLookup{},
		registry:     NewRegistry(RegistryParams{}),
		sessions:     sessions,
	}

	got, err := resolver.ResolveSession(context.Background(), "agent-1")
	if err == nil || got == session {
		t.Fatalf("ResolveSession(agent id) = (%#v, %v), want public thread lookup failure", got, err)
	}
}

func TestAutoResumeSessionOutlivesCallerContext(t *testing.T) {
	session := &contextBoundSession{
		generationTestSession: generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111120"},
	}
	driver := &contextCaptureResumeDriver{name: "codex", session: session}
	sessions := NewSessionManager(nil)
	resolver := newAutoResumeResolverForTest(
		t,
		driver,
		sessions,
		"agent-context",
		"public-thread-context",
		session.threadID,
	)
	ctx, cancel := context.WithCancel(context.Background())

	got, err := resolver.ResolveSession(ctx, "public-thread-context")
	if err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	cancel()

	if got != session {
		t.Fatalf("ResolveSession() = %#v, want %#v", got, session)
	}
	if !session.usable() {
		t.Fatalf("auto-resumed session context canceled with caller: %v", driver.resumeCtx.Err())
	}
	registered, err := sessions.Get("agent-context")
	if err != nil || registered != session {
		t.Fatalf("registered session after caller cancel = (%#v, %v), want live session", registered, err)
	}
}

func TestClientResumeSessionOutlivesCallerContext(t *testing.T) {
	session := &contextBoundSession{
		generationTestSession: generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111122"},
	}
	driver := &contextCaptureResumeDriver{name: "codex", session: session}
	sessions := NewSessionManager(nil)
	registry := NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
		{Name: "codex", Create: func() contract.Driver { return driver }},
	}})
	client := NewClient(registry, sessions, nil)
	ctx, cancel := context.WithCancel(context.Background())

	got, err := client.ResumeSession(ctx, dto.ResumeSessionRequest{
		Provider:         "codex",
		AgentID:          "agent-client-context",
		ProviderThreadID: session.threadID,
	})
	if err != nil {
		t.Fatalf("Client.ResumeSession() error = %v", err)
	}
	cancel()

	if got != session {
		t.Fatalf("Client.ResumeSession() = %#v, want %#v", got, session)
	}
	if !session.usable() {
		t.Fatalf("client-resumed session context canceled with caller: %v", driver.resumeCtx.Err())
	}
	sessions.Activate("agent-client-context")
	registered, err := sessions.Get("agent-client-context")
	if err != nil || registered != session {
		t.Fatalf("registered session after caller cancel = (%#v, %v), want live session", registered, err)
	}
}

func TestConcurrentClientAndResolverResumeSharePendingSession(t *testing.T) {
	pending := &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111121"}
	duplicate := &generationTestSession{threadID: pending.threadID}
	driver := newSequencedBlockingResumeDriver("codex", pending, duplicate)
	t.Cleanup(func() {
		select {
		case <-driver.release:
		default:
			close(driver.release)
		}
	})
	sessions := NewSessionManager(nil)
	resolver := newAutoResumeResolverForTest(
		t,
		driver,
		sessions,
		"agent-shared-resume",
		"public-thread-shared-resume",
		pending.threadID,
	)
	client := NewClient(resolver.registry.(*Registry), sessions, nil)
	clientResult := make(chan contract.Session, 1)
	clientErr := make(chan error, 1)
	var clientGroup errgroup.Group
	clientGroup.Go(func() error {
		session, err := client.ResumeSession(context.Background(), dto.ResumeSessionRequest{
			Provider:         "codex",
			AgentID:          "agent-shared-resume",
			ThreadID:         "public-thread-shared-resume",
			ProviderThreadID: pending.threadID,
		})
		clientResult <- session
		clientErr <- err
		return nil
	})
	waitResumeCallForTest(t, driver.started, 1)

	resolverResult := make(chan contract.Session, 1)
	resolverErr := make(chan error, 1)
	var resolverGroup errgroup.Group
	resolverGroup.Go(func() error {
		session, err := resolver.ResolveSession(context.Background(), "public-thread-shared-resume")
		resolverResult <- session
		resolverErr <- err
		return nil
	})

	assertNoResumeCallForTest(t, driver.started)
	close(driver.release)
	if got := waitSessionResultForTest(t, "Client.ResumeSession()", clientResult, clientErr); got != pending {
		t.Fatalf("Client.ResumeSession() = %#v, want pending %#v", got, pending)
	}
	assertSessionResultBlockedForTest(t, "ResolveSession()", resolverResult, resolverErr)
	if pending.stopped {
		t.Fatal("pending session was ForceStop by concurrent resolver")
	}

	sessions.Activate("agent-shared-resume")
	if got := waitSessionResultForTest(t, "ResolveSession() after activation", resolverResult, resolverErr); got != pending {
		t.Fatalf("ResolveSession() = %#v, want shared pending %#v", got, pending)
	}
	if got := driver.resumeCount(); got != 1 {
		t.Fatalf("ResumeSession calls = %d, want 1 shared resume", got)
	}
	if err := errors.Join(clientGroup.Wait(), resolverGroup.Wait()); err != nil {
		t.Fatalf("resume workers error = %v", err)
	}
}

func TestConcurrentColdAutoResumeInvokesProviderResumeOnce(t *testing.T) {
	t.Parallel()

	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("11111111-aaaa-bbbb-cccc-111111111119")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	driver := newBlockingResumeDriver("codex", &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111119"})
	resolver := &sessionResolver{
		threadStore: keyedThreadLookup{
			"public-thread-concurrent": {
				ThreadID:       "public-thread-concurrent",
				AgentID:        "agent-concurrent",
				Status:         "running",
				PromptSnapshot: autoResumePromptSnapshotForTest(),
			},
		},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:11111111-aaaa-bbbb-cccc-111111111119": {
				Provider:         "codex",
				AgentID:          "agent-concurrent",
				ProviderThreadID: "11111111-aaaa-bbbb-cccc-111111111119",
				CodexThreadID:    "public-thread-concurrent",
				RolloutPath:      rolloutPath,
				Cwd:              "/repo",
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	var group errgroup.Group
	for range 8 {
		group.Go(func() error {
			_, err := resolver.ResolveSession(context.Background(), "public-thread-concurrent")
			return err
		})
	}
	<-driver.started
	time.Sleep(50 * time.Millisecond)
	close(driver.release)
	if err := group.Wait(); err != nil {
		t.Fatalf("ResolveSession() concurrent error = %v", err)
	}
	if got := driver.resumeCount(); got != 1 {
		t.Fatalf("ResumeSession calls = %d, want 1 singleflight cold resume", got)
	}
}

func TestSessionResolverResolveSessionUsesThreadStoreAgent(t *testing.T) {
	sessions := NewSessionManager(nil)
	session := &generationTestSession{threadID: "thread-1"}
	sessions.Register("agent-1", session)

	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{ThreadID: "thread-1", AgentID: "agent-1"}},
		registry:    NewRegistry(RegistryParams{}),
		sessions:    sessions,
	}

	got, err := resolver.ResolveSession(context.Background(), "thread-1")
	if err != nil || got != session {
		t.Fatalf("ResolveSession() = (%#v, %v)", got, err)
	}
}

func TestSessionResolverRejectsLegacyPromptSnapshotBeforeActiveSessionFastPath(t *testing.T) {
	t.Parallel()

	sessions := NewSessionManager(nil)
	session := &generationTestSession{threadID: "thread-legacy"}
	sessions.Register("agent-legacy", session)
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID: "thread-legacy",
			AgentID:  "agent-legacy",
			Status:   "running",
			PromptSnapshot: contract.PromptAssemblySnapshot{
				BaseInstructions: "legacy base",
				Provider:         "codex",
				Version:          contract.PromptAssemblySnapshotVersion,
				Hash:             "snapshot-hash",
				SectionSnapshot: map[string]string{
					contract.DynamicSectionMemoryContext: "legacy memory",
				},
			},
		}},
		registry: NewRegistry(RegistryParams{}),
		sessions: sessions,
	}

	got, err := resolver.ResolveSession(context.Background(), "thread-legacy")
	if !errors.Is(err, contract.ErrLegacyMemoryPromptInjection) {
		t.Fatalf("ResolveSession() error = %v, want legacy memory sentinel", err)
	}
	if got != nil {
		t.Fatalf("ResolveSession() session = %#v, want nil on legacy snapshot", got)
	}
}

func TestSessionResolverActiveFastPathRejectsForeignPromptSnapshotOwner(t *testing.T) {
	t.Parallel()

	sessions := NewSessionManager(nil)
	session := &generationTestSession{threadID: "thread-foreign-owner"}
	sessions.Register("agent-foreign-owner", session)
	snapshot := autoResumePromptSnapshotForTest()
	snapshot.Provider = "claude"
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID:       "thread-foreign-owner",
			AgentID:        "agent-foreign-owner",
			Status:         "running",
			PromptSnapshot: snapshot,
		}},
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-foreign-owner": {
				Provider: "codex",
				AgentID:  "agent-foreign-owner",
			},
		}},
		registry: NewRegistry(RegistryParams{}),
		sessions: sessions,
	}

	got, err := resolver.ResolveSession(context.Background(), "thread-foreign-owner")
	if err == nil || !strings.Contains(err.Error(), "provider mismatch") {
		t.Fatalf("ResolveSession() error = %v, want provider mismatch", err)
	}
	if got != nil {
		t.Fatalf("ResolveSession() session = %#v, want nil", got)
	}
}

func TestSessionResolverResolveSessionFallsBackToProviderThreadBinding(t *testing.T) {
	sessions := NewSessionManager(nil)
	session := &generationTestSession{threadID: "provider-thread-1"}
	sessions.Register("agent-2", session)

	resolver := &sessionResolver{
		threadStore:  stubThreadLookup{err: platformdb.ErrNotFound},
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{"codex:provider-thread-1": {AgentID: "agent-2"}}},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return nil }},
			{Name: "claude", Create: func() contract.Driver { return nil }},
		}}),
		sessions: sessions,
	}

	got, err := resolver.ResolveSession(context.Background(), "provider-thread-1")
	if err != nil || got != session {
		t.Fatalf("ResolveSession() = (%#v, %v)", got, err)
	}
}

func TestSessionResolverProviderThreadFallbackRejectsLegacyPromptSnapshotBeforeActiveSession(t *testing.T) {
	t.Parallel()

	sessions := NewSessionManager(nil)
	session := &generationTestSession{threadID: "provider-thread-legacy"}
	sessions.Register("agent-legacy-provider", session)
	legacyRef := &contract.SessionThreadRef{
		ThreadID: "provider-thread-legacy",
		AgentID:  "agent-legacy-provider",
		Status:   "running",
		PromptSnapshot: contract.PromptAssemblySnapshot{
			BaseInstructions: "legacy base",
			Provider:         "codex",
			Version:          contract.PromptAssemblySnapshotVersion,
			Hash:             "snapshot-hash",
			SectionSnapshot: map[string]string{
				contract.DynamicSectionMemory: "legacy memory",
			},
		},
	}
	resolver := &sessionResolver{
		threadStore: &mapThreadLookup{
			refs: map[string]*contract.SessionThreadRef{
				"provider-thread-legacy": legacyRef,
			},
			firstMiss: map[string]bool{
				"provider-thread-legacy": true,
			},
			calls: make(map[string]int),
		},
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-legacy": {
				Provider:         "codex",
				AgentID:          "agent-legacy-provider",
				ProviderThreadID: "provider-thread-legacy",
			},
		}},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return nil }},
		}}),
		sessions: sessions,
	}

	got, err := resolver.ResolveSession(context.Background(), "provider-thread-legacy")
	if !errors.Is(err, contract.ErrLegacyMemoryPromptInjection) {
		t.Fatalf("ResolveSession() error = %v, want legacy memory sentinel", err)
	}
	if got != nil {
		t.Fatalf("ResolveSession() session = %#v, want nil", got)
	}
}

func TestSessionResolverResolveSessionReturnsLookupErrors(t *testing.T) {
	sessions := NewSessionManager(nil)
	wantErr := errors.New("db unavailable")
	resolver := &sessionResolver{
		threadStore:  stubThreadLookup{err: wantErr},
		bindingStore: stubBindingLookup{errs: map[string]error{"codex:thread-404": wantErr}},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return nil }},
		}}),
		sessions: sessions,
	}

	_, err := resolver.ResolveSession(context.Background(), "thread-404")
	if err == nil || !strings.Contains(err.Error(), "db unavailable") {
		t.Fatalf("ResolveSession() error = %v", err)
	}
}

func TestSessionResolverReturnsBindingLookupErrorForKnownThread(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("binding db unavailable")
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID: "public-thread-1",
			AgentID:  "agent-1",
			Status:   "running",
		}},
		bindingStore: stubBindingLookup{
			agentErrs: map[string]error{"agent-1": wantErr},
		},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return nil }},
		}}),
		sessions: NewSessionManager(nil),
	}

	_, err := resolver.ResolveSession(context.Background(), "public-thread-1")
	if err == nil || !strings.Contains(err.Error(), "binding db unavailable") {
		t.Fatalf("ResolveSession() error = %v, want binding lookup error", err)
	}
}
