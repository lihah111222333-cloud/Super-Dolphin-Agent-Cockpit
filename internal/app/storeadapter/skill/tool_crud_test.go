package skilladapter

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	skill "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	skilltoolstore "github.com/anthropic-ai/super-agent-v3/internal/store/skilltool"
	_ "modernc.org/sqlite"
)

func TestSkillToolRPCPersistsCRUDWithLazyTableCreation(t *testing.T) {
	t.Parallel()

	db := openSkillToolSQLite(t)
	projectRoot := filepath.Join(t.TempDir(), "repo")
	skillContent := "---\nname: backend\ndescription: Backend skill details\n---\n# Backend\nUse Go backend conventions carefully.\n"
	writeSkillToolSkill(t, projectRoot, "backend", skillContent)
	port, err := provideSkillToolPersistence(skilltoolstore.New(db))
	if err != nil {
		t.Fatalf("provide Skill tool persistence: %v", err)
	}
	svc := skill.NewServiceWithToolStore(projectRoot, port)
	server := newSkillToolRPCServer(svc)

	assertSkillToolInitialListCreatesTable(t, db, server, projectRoot)
	created := createSkillToolForTest(t, server, projectRoot)
	assertSkillToolProviderReturnsSkillContent(t, svc, projectRoot, created.MethodName, skillContent)
	updateSkillToolForTest(t, server, projectRoot, created.ID)
	getSkillToolForTest(t, server, projectRoot, created.ID)
	deleteSkillToolForTest(t, server, projectRoot, created.ID)
}

func assertSkillToolInitialListCreatesTable(t *testing.T, db *sql.DB, server *platformrpc.Server, projectRoot string) {
	t.Helper()
	if skillToolTableExists(t, db) {
		t.Fatal("skill_tools table exists before any skill tool call")
	}
	listRaw, err := server.Dispatch(context.Background(), "skills/tools/list", mustRawJSON(t, map[string]any{
		"cwd":   projectRoot,
		"limit": 20,
	}))
	if err != nil {
		t.Fatalf("Dispatch skills/tools/list before table exists: %v", err)
	}
	var initial skillToolListPayload
	if err := json.Unmarshal(listRaw, &initial); err != nil {
		t.Fatalf("unmarshal initial list: %v", err)
	}
	if len(initial.Tools) != 0 {
		t.Fatalf("initial tools = %#v, want empty", initial.Tools)
	}
	if initial.Tools == nil {
		t.Fatal("initial tools decoded as nil, want empty array")
	}
	if !skillToolTableExists(t, db) {
		t.Fatal("skill_tools table was not created lazily")
	}
}

func createSkillToolForTest(t *testing.T, server *platformrpc.Server, projectRoot string) skillToolPayload {
	t.Helper()
	createRaw, err := server.Dispatch(context.Background(), "skills/tools/create", mustRawJSON(t, map[string]any{
		"cwd":         projectRoot,
		"methodName":  "backend",
		"description": "Return backend skill details",
		"enabled":     true,
	}))
	if err != nil {
		t.Fatalf("Dispatch skills/tools/create: %v", err)
	}
	created := decodeSkillToolForTest(t, createRaw)
	if created.ID <= 0 || created.MethodName != "backend" || created.Description != "Return backend skill details" || !created.Enabled {
		t.Fatalf("created tool = %#v", created)
	}
	return created
}

func updateSkillToolForTest(t *testing.T, server *platformrpc.Server, projectRoot string, id int64) {
	t.Helper()
	updateRaw, err := server.Dispatch(context.Background(), "skills/tools/update", mustRawJSON(t, map[string]any{
		"cwd":         projectRoot,
		"id":          id,
		"methodName":  "backend_review",
		"description": "Return backend review skill details",
		"enabled":     false,
	}))
	if err != nil {
		t.Fatalf("Dispatch skills/tools/update: %v", err)
	}
	updated := decodeSkillToolForTest(t, updateRaw)
	if updated.ID != id || updated.MethodName != "backend_review" || updated.Description != "Return backend review skill details" || updated.Enabled {
		t.Fatalf("updated tool = %#v", updated)
	}
}

func getSkillToolForTest(t *testing.T, server *platformrpc.Server, projectRoot string, id int64) {
	t.Helper()
	getRaw, err := server.Dispatch(context.Background(), "skills/tools/get", mustRawJSON(t, map[string]any{
		"cwd": projectRoot,
		"id":  id,
	}))
	if err != nil {
		t.Fatalf("Dispatch skills/tools/get: %v", err)
	}
	got := decodeSkillToolForTest(t, getRaw)
	if got.MethodName != "backend_review" || got.Description != "Return backend review skill details" {
		t.Fatalf("got tool = %#v", got)
	}
}

func assertSkillToolProviderReturnsSkillContent(t *testing.T, svc skill.Service, projectRoot, methodName, wantContent string) {
	t.Helper()
	provider, ok := svc.(contract.SkillToolProvider)
	if !ok {
		t.Fatalf("skill service does not implement contract.SkillToolProvider")
	}
	tools, err := provider.ListSkillToolsForSurface(context.Background(), projectRoot)
	if err != nil {
		t.Fatalf("ListSkillToolsForSurface: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != methodName || tools[0].Description != "Return backend skill details" {
		t.Fatalf("surface tools = %#v", tools)
	}
	gotContent, err := provider.CallSkillTool(context.Background(), contract.SkillToolCall{
		Name: methodName,
		CWD:  projectRoot,
	})
	if err != nil {
		t.Fatalf("CallSkillTool: %v", err)
	}
	if gotContent != wantContent {
		t.Fatalf("CallSkillTool content = %q, want %q", gotContent, wantContent)
	}
}

func writeSkillToolSkill(t *testing.T, projectRoot, name, content string) {
	t.Helper()
	dir := filepath.Join(projectRoot, ".agents", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill file: %v", err)
	}
}

func deleteSkillToolForTest(t *testing.T, server *platformrpc.Server, projectRoot string, id int64) {
	t.Helper()
	deleteRaw, err := server.Dispatch(context.Background(), "skills/tools/delete", mustRawJSON(t, map[string]any{
		"cwd": projectRoot,
		"id":  id,
	}))
	if err != nil {
		t.Fatalf("Dispatch skills/tools/delete: %v", err)
	}
	var deleted skillToolDeletePayload
	if err := json.Unmarshal(deleteRaw, &deleted); err != nil {
		t.Fatalf("unmarshal delete: %v", err)
	}
	if deleted.ID != id || !deleted.Deleted {
		t.Fatalf("deleted = %#v, want deleted id %d", deleted, id)
	}
}

func openSkillToolSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})
	return db
}

func skillToolTableExists(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='skill_tools'`).Scan(&name)
	if err == nil {
		return name == "skill_tools"
	}
	if err == sql.ErrNoRows {
		return false
	}
	t.Fatalf("query sqlite_master: %v", err)
	return false
}

func newSkillToolRPCServer(svc skill.Service) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(skill.NewHandlersForService(svc).Handlers)
	return server
}

func mustRawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal raw json: %v", err)
	}
	return data
}

type skillToolListPayload struct {
	Tools []skillToolPayload `json:"tools"`
}

type skillToolPayload struct {
	ID          int64  `json:"id"`
	MethodName  string `json:"methodName"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type skillToolDeletePayload struct {
	ID      int64 `json:"id"`
	Deleted bool  `json:"deleted"`
}

func decodeSkillToolForTest(t *testing.T, raw json.RawMessage) skillToolPayload {
	t.Helper()
	var got skillToolPayload
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal skill tool: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal skill tool fields: %v", err)
	}
	for _, forbidden := range []string{"command", "args"} {
		if _, ok := fields[forbidden]; ok {
			t.Fatalf("skill tool response contains %q: %s", forbidden, string(raw))
		}
	}
	return got
}
