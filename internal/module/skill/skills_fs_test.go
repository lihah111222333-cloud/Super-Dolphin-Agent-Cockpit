package skill

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadConfigReturnsExplicitStubBindingState(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	out, err := svc.ReadConfig(context.Background(), " agent-1 ")
	if err != nil {
		t.Fatalf("ReadConfig returned error: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ReadConfig result type mismatch: %T", out)
	}
	if got, _ := result["agent_id"].(string); got != "agent-1" {
		t.Fatalf("agent_id mismatch: got %q", got)
	}
	if got, _ := result["configured"].(bool); got {
		t.Fatal("configured mismatch: got true want false")
	}
	if got, _ := result["binding_count"].(int); got != 0 {
		t.Fatalf("binding_count mismatch: got %d", got)
	}
	if got, _ := result["binding_source"].(string); got != "stub" {
		t.Fatalf("binding_source mismatch: got %q", got)
	}
}

func TestWriteSkillContentWritesNamedSkillContent(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	out, err := svc.WriteSkillContent(context.Background(), "demo-skill", "# demo")
	if err != nil {
		t.Fatalf("WriteSkillContent returned error: %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("WriteSkillContent result type mismatch: %T", out)
	}
	path, _ := result["path"].(string)
	if path == "" {
		t.Fatal("WriteSkillContent path is empty")
	}
	if want := filepath.Join(svc.root, "demo-skill", skillMainFile); path != want {
		t.Fatalf("WriteSkillContent path mismatch: got %q want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if string(data) != "# demo" {
		t.Fatalf("WriteSkillContent content mismatch: got %q", string(data))
	}
}

func TestReadLocalRejectsPathOutsideSkillsRoot(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "SKILL.md")
	if err := os.WriteFile(outsidePath, []byte("# outside"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{projectRoot: t.TempDir(), root: skillsRoot, http: &http.Client{}}

	_, err := svc.ReadLocal(context.Background(), outsidePath)
	if err == nil || err.Error() != "path escapes skills root: "+outsidePath {
		t.Fatalf("ReadLocal() error = %v, want path escapes skills root", err)
	}
}

func TestListLocalFilesRejectsDirOutsideSkillsRoot(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	outsideRoot := t.TempDir()
	svc := &service{projectRoot: t.TempDir(), root: skillsRoot, http: &http.Client{}}

	_, err := svc.ListLocalFiles(context.Background(), listSkillFilesParams{Dir: outsideRoot})
	if err == nil || err.Error() != "path escapes skills root: "+outsideRoot {
		t.Fatalf("ListLocalFiles() error = %v, want path escapes skills root", err)
	}
}

func TestWriteLocalRejectsPathOutsideSkillsRoot(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "note.md")
	if err := os.WriteFile(outsidePath, []byte("before"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{projectRoot: t.TempDir(), root: skillsRoot, http: &http.Client{}}

	_, err := svc.WriteLocal(context.Background(), outsidePath, "after")
	if err == nil || err.Error() != "path escapes skills root: "+outsidePath {
		t.Fatalf("WriteLocal() error = %v, want path escapes skills root", err)
	}
}

func TestReadLocalAcceptsPathInsideProjectSkillsRoot(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectSkillsRoot := t.TempDir()
	skillPath := filepath.Join(projectSkillsRoot, "demo", skillMainFile)
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{root: systemRoot, projectSkillsRoot: projectSkillsRoot, http: &http.Client{}}

	out, err := svc.ReadLocal(context.Background(), skillPath)
	if err != nil {
		t.Fatalf("ReadLocal() error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ReadLocal() result type = %T", out)
	}
	skill, ok := result["skill"].(map[string]any)
	if !ok {
		t.Fatalf("ReadLocal() skill type = %T", result["skill"])
	}
	if got, _ := skill["content"].(string); got != "# demo" {
		t.Fatalf("ReadLocal() content = %q, want # demo", got)
	}
}

func TestListSkillsMergesProjectAndSystemRoots(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectSkillsRoot := t.TempDir()
	writeTestSkill(t, systemRoot, "from-system", "# system")
	writeTestSkill(t, projectSkillsRoot, "from-project", "# project")
	svc := &service{root: systemRoot, projectSkillsRoot: projectSkillsRoot, http: &http.Client{}}

	skills, err := svc.ListSkills(context.Background())
	if err != nil {
		t.Fatalf("ListSkills() error = %v", err)
	}
	names := make(map[string]bool, len(skills))
	for _, skill := range skills {
		names[skill.Name] = true
	}
	if !names["from-system"] || !names["from-project"] {
		t.Fatalf("ListSkills() names = %#v, want both roots covered", names)
	}
}

func TestImportLocalDirRejectsSourceInsideProjectSkillsRoot(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectSkillsRoot := t.TempDir()
	sourceDir := filepath.Join(projectSkillsRoot, "demo-skill")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, skillMainFile), []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{root: systemRoot, projectSkillsRoot: projectSkillsRoot, http: &http.Client{}}

	out, err := svc.ImportLocalDir(context.Background(), importSkillDirParams{Path: sourceDir})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ImportLocalDir() result type = %T", out)
	}
	failures, ok := result["failures"].([]map[string]any)
	if !ok || len(failures) != 1 {
		t.Fatalf("ImportLocalDir() failures = %#v, want single failure", result["failures"])
	}
	if got := failures[0]["error"]; got != "source is inside skills root: "+sourceDir {
		t.Fatalf("ImportLocalDir() failure error = %#v", got)
	}
}

func TestImportLocalDirAcceptsSourceOutsideProjectRoot(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	skillsRoot := t.TempDir()
	outsideRoot := t.TempDir()
	sourceDir := filepath.Join(outsideRoot, "demo-skill")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, skillMainFile), []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{projectRoot: projectRoot, root: skillsRoot, http: &http.Client{}}

	out, err := svc.ImportLocalDir(context.Background(), importSkillDirParams{Path: sourceDir})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ImportLocalDir() result type = %T", out)
	}
	if got, present := result["failures"]; present {
		t.Fatalf("ImportLocalDir() failures = %#v, want no failures", got)
	}
	imported, ok := result["imported"].([]map[string]any)
	if !ok || len(imported) != 1 {
		t.Fatalf("ImportLocalDir() imported = %#v, want single import", result["imported"])
	}
	if gotName, _ := imported[0]["name"].(string); gotName != "demo-skill" {
		t.Fatalf("ImportLocalDir() imported name = %q, want demo-skill", gotName)
	}
	if _, err := os.Stat(filepath.Join(skillsRoot, "demo-skill", skillMainFile)); err != nil {
		t.Fatalf("ImportLocalDir() target SKILL.md stat err = %v", err)
	}
}

func TestImportLocalDirRejectsSourceInsideSkillsRoot(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	sourceDir := filepath.Join(skillsRoot, "demo-skill")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, skillMainFile), []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{projectRoot: t.TempDir(), root: skillsRoot, http: &http.Client{}}

	out, err := svc.ImportLocalDir(context.Background(), importSkillDirParams{Path: sourceDir})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ImportLocalDir() result type = %T", out)
	}
	failures, ok := result["failures"].([]map[string]any)
	if !ok || len(failures) != 1 {
		t.Fatalf("ImportLocalDir() failures = %#v, want single failure", result["failures"])
	}
	if got := failures[0]["error"]; got != "source is inside skills root: "+sourceDir {
		t.Fatalf("ImportLocalDir() failure error = %#v", got)
	}
}

func TestImportLocalDirRejectsExistingTarget(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	sourceDir := filepath.Join(projectRoot, "demo-skill")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, skillMainFile), []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	skillsRoot := t.TempDir()
	existingDir := filepath.Join(skillsRoot, "demo-skill")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(existing) error = %v", err)
	}
	svc := &service{projectRoot: projectRoot, root: skillsRoot, http: &http.Client{}}

	out, err := svc.ImportLocalDir(context.Background(), importSkillDirParams{Path: sourceDir})
	if err != nil {
		t.Fatalf("ImportLocalDir() error = %v", err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("ImportLocalDir() result type = %T", out)
	}
	failures, ok := result["failures"].([]map[string]any)
	if !ok || len(failures) != 1 {
		t.Fatalf("ImportLocalDir() failures = %#v, want single failure", result["failures"])
	}
	if got := failures[0]["error"]; got != "skill already exists: demo-skill" {
		t.Fatalf("ImportLocalDir() failure error = %#v", got)
	}
}

func TestReadRemoteHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
			_, _ = w.Write([]byte("# remote"))
		}
	}))
	defer server.Close()

	svc := newTestSkillService(t)
	svc.http = &http.Client{Timeout: 15 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := svc.ReadRemote(ctx, server.URL)
	if err == nil {
		t.Fatal("ReadRemote() error = nil, want context cancellation")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReadRemote() error = %v, want context deadline exceeded", err)
	}
}
