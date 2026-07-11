package skilladapter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/creachadair/jrpc2"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skill/toolstore"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	skilltoolstore "github.com/anthropic-ai/super-agent-v3/internal/store/skilltool"
)

func TestSkillMutationAuditAdapterMapsDomainDTO(t *testing.T) {
	t.Parallel()

	store := &capturingAuditStore{}
	port, err := provideSkillMutationAuditStore(store)
	if err != nil {
		t.Fatalf("provide audit port: %v", err)
	}
	want := skill.MutationAuditEntry{
		EventType: "skill_mutation", Action: "create", Result: "ok", Actor: "tester",
		Target: "skill/backend", Detail: "created", Level: "info", Extra: json.RawMessage(`{"scope":"project"}`),
	}
	if err := port.Insert(context.Background(), want); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	wantStore := auditstore.InsertParams{
		EventType: want.EventType, Action: want.Action, Result: want.Result, Actor: want.Actor,
		Target: want.Target, Detail: want.Detail, Level: want.Level, Extra: json.RawMessage(want.Extra),
	}
	if !reflect.DeepEqual(store.got, wantStore) {
		t.Fatalf("mapped audit entry = %#v, want %#v", store.got, want)
	}
}

// TestModuleOwnsSkillPorts 通过真实 Fx lifecycle 证明 skill adapter module 提供两个端口。
func TestModuleOwnsSkillPorts(t *testing.T) {
	db := openSkillToolSQLite(t)
	audit := &capturingAuditStore{}
	var auditPort skill.MutationAuditStore
	var persistencePort toolstore.Persistence
	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() auditstore.Store { return audit }),
		fx.Provide(func() *skilltoolstore.Store { return skilltoolstore.New(db) }),
		Module,
		fx.Populate(&auditPort, &persistencePort),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New: %v", err)
	}
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("fx.Start: %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("fx.Stop: %v", err)
	}
	if auditPort == nil || persistencePort == nil {
		t.Fatalf("skill ports = (%T, %T), want both non-nil", auditPort, persistencePort)
	}
}

// TestSkillStoreAdaptersPreserveStoreErrorCause 固定迁移后审计错误与工具持久化错误原因继续向上传播。
func TestSkillStoreAdaptersPreserveStoreErrorCause(t *testing.T) {
	wantErr := errors.New("skill store sentinel")
	auditPort, err := provideSkillMutationAuditStore(&capturingAuditStore{err: wantErr})
	if err != nil {
		t.Fatalf("provide audit port: %v", err)
	}
	if err := auditPort.Insert(context.Background(), skill.MutationAuditEntry{}); err != wantErr {
		t.Fatalf("audit Insert error = %v, want identical %v", err, wantErr)
	}
	if err := mapSkillToolStoreError(wantErr); !errors.Is(err, wantErr) {
		t.Fatalf("tool persistence error = %v, want cause %v", err, wantErr)
	}
}

func TestSkillStoreAdaptersFailFastAndMapNotFound(t *testing.T) {
	t.Parallel()

	if _, err := provideSkillMutationAuditStore(nil); err == nil {
		t.Fatal("nil audit store error = nil")
	}
	if _, err := provideSkillToolPersistence(nil); err == nil {
		t.Fatal("nil Skill tool store error = nil")
	}
	var zero skillToolPersistenceAdapter
	_, err := zero.Get(context.Background(), toolstore.IDParams{CWD: "/repo", ID: 1})
	if !errors.Is(err, toolstore.ErrStoreNotConfigured) {
		t.Fatalf("zero adapter Get error = %v, want ErrStoreNotConfigured", err)
	}
	enabled := true
	_, err = zero.Create(context.Background(), toolstore.MutationParams{
		CWD: "/repo", MethodName: "backend", Description: "Backend skill", Enabled: &enabled,
	})
	if !errors.Is(err, toolstore.ErrStoreNotConfigured) {
		t.Fatalf("zero adapter Create error = %v, want ErrStoreNotConfigured", err)
	}

	db := openSkillToolSQLite(t)
	port, err := provideSkillToolPersistence(skilltoolstore.New(db))
	if err != nil {
		t.Fatalf("provide Skill tool persistence: %v", err)
	}
	_, err = port.Get(context.Background(), toolstore.IDParams{CWD: "/repo", ID: 99})
	if !errors.Is(err, toolstore.ErrNotFound) {
		t.Fatalf("Get missing error = %v, want ErrNotFound", err)
	}
}

func TestSkillToolRPCMapsPortErrors(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	svc := skill.NewServiceWithToolStore(projectRoot, failingSkillToolPersistence{})
	server := newSkillToolRPCServer(svc)

	_, err := server.Dispatch(context.Background(), "skills/tools/get", mustRawJSON(t, map[string]any{
		"cwd": projectRoot,
		"id":  1,
	}))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != jrpc2.Code(platformrpc.CodeNotFound) {
		t.Fatalf("get mapped error = %v, want not found RPC code", err)
	}

	_, err = server.Dispatch(context.Background(), "skills/tools/create", mustRawJSON(t, map[string]any{
		"cwd": projectRoot, "methodName": "invalid name", "description": "bad", "enabled": true,
	}))
	if !errors.As(err, &rpcErr) || rpcErr.Code != jrpc2.Code(platformrpc.CodeInvalidParams) {
		t.Fatalf("create validation error = %v, want invalid params RPC code", err)
	}
}

type capturingAuditStore struct {
	got auditstore.InsertParams
	err error
}

func (s *capturingAuditStore) List(context.Context, auditstore.ListFilter) ([]auditstore.AuditEvent, error) {
	return nil, nil
}

func (s *capturingAuditStore) Insert(_ context.Context, p auditstore.InsertParams) error {
	s.got = p
	return s.err
}

type failingSkillToolPersistence struct{}

func (failingSkillToolPersistence) Create(context.Context, toolstore.MutationParams) (toolstore.Result, error) {
	return toolstore.Result{}, toolstore.ErrNotFound
}
func (failingSkillToolPersistence) List(context.Context, toolstore.ListParams) (toolstore.ListResult, error) {
	return toolstore.ListResult{}, toolstore.ErrNotFound
}
func (failingSkillToolPersistence) Get(context.Context, toolstore.IDParams) (toolstore.Result, error) {
	return toolstore.Result{}, toolstore.ErrNotFound
}
func (failingSkillToolPersistence) GetByMethod(context.Context, toolstore.MethodParams) (toolstore.Result, error) {
	return toolstore.Result{}, toolstore.ErrNotFound
}
func (failingSkillToolPersistence) Update(context.Context, toolstore.UpdateParams) (toolstore.Result, error) {
	return toolstore.Result{}, toolstore.ErrNotFound
}
func (failingSkillToolPersistence) Delete(context.Context, toolstore.IDParams) error {
	return toolstore.ErrNotFound
}
