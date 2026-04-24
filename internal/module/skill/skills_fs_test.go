package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestWriteSkillContentRequiresSystemReview(t *testing.T) {
	t.Parallel()

	svc := newTestSkillService(t)
	_, err := svc.WriteSkillContent(context.Background(), "demo-skill", "# demo")
	if !errors.Is(err, ErrSkillSystemReviewRequired) {
		t.Fatalf("WriteSkillContent error = %v, want ErrSkillSystemReviewRequired", err)
	}
}

func TestReadLocalRejectsPathOutsideSkillsRoot(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	outsideRoot := t.TempDir()
	projectRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "SKILL.md")
	if err := os.WriteFile(outsidePath, []byte("# outside"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{projectRoot: projectRoot, root: skillsRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), http: &http.Client{}}

	_, err := svc.ReadLocal(skillTestContext(projectRoot), outsidePath)
	if err == nil || err.Error() != "path escapes skills root: "+outsidePath {
		t.Fatalf("ReadLocal() error = %v, want path escapes skills root", err)
	}
}

func TestListLocalFilesRejectsDirOutsideSkillsRoot(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	outsideRoot := t.TempDir()
	projectRoot := t.TempDir()
	svc := &service{projectRoot: projectRoot, root: skillsRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), http: &http.Client{}}

	_, err := svc.ListLocalFiles(skillTestContext(projectRoot), listSkillFilesParams{Dir: outsideRoot})
	if err == nil || err.Error() != "path escapes skills root: "+outsideRoot {
		t.Fatalf("ListLocalFiles() error = %v, want path escapes skills root", err)
	}
}

func TestWriteLocalRejectsPathOutsideSkillsRoot(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	outsideRoot := t.TempDir()
	projectRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "note.md")
	if err := os.WriteFile(outsidePath, []byte("before"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{projectRoot: projectRoot, root: skillsRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), http: &http.Client{}}

	_, err := svc.WriteLocal(skillTestContext(projectRoot), outsidePath, "after")
	if err == nil || err.Error() != "path escapes skills root: "+outsidePath {
		t.Fatalf("WriteLocal() error = %v, want path escapes skills root", err)
	}
}

func TestReadLocalAcceptsPathInsideProjectSkillsRoot(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := t.TempDir()
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	skillPath := filepath.Join(projectSkillsRoot, "demo", skillMainFile)
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{root: systemRoot, projectRoot: projectRoot, projectSkillsRoot: projectSkillsRoot, http: &http.Client{}}

	out, err := svc.ReadLocal(skillTestContext(projectRoot), skillPath)
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
	projectRoot := t.TempDir()
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	writeScopedSystemSkill(t, systemRoot, projectRoot, "from-system", "# system")
	writeTestSkill(t, projectSkillsRoot, "from-project", "# project")
	svc := &service{root: systemRoot, projectRoot: projectRoot, projectSkillsRoot: projectSkillsRoot, http: &http.Client{}}

	skills, err := svc.ListSkills(skillTestContext(projectRoot))
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

func TestExpandReturnsNotFoundForMissingSkill(t *testing.T) {
	t.Parallel()

	svc, _ := newExpandTestService(t)
	_, err := svc.Expand(expandTestContext(svc), skillExpandParams{Name: "ghost"})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Expand() error = %v, want os.ErrNotExist", err)
	}
	if err == nil || err.Error() != "skill not found: ghost" {
		t.Fatalf("Expand() error text = %v, want skill not found", err)
	}
}

func TestExpandRejectsPathEscapeSection(t *testing.T) {
	t.Parallel()

	svc, root := newExpandTestService(t)
	writeExpandTestSkill(t, root, "demo", "---\nname: demo\n---\nbody")

	_, err := svc.Expand(expandTestContext(svc), skillExpandParams{Name: "demo", Section: "../escape"})
	if !errors.Is(err, errInvalidSkillExpandParam) {
		t.Fatalf("Expand() error = %v, want invalid params", err)
	}
}

func TestExpandFullSkillContentHashUsesPreTruncationBytes(t *testing.T) {
	t.Parallel()

	svc, root := newExpandTestService(t)
	content := "---\nname: demo\nsummary: short\n---\n## Usage\nhello world"
	path := writeExpandTestSkill(t, root, "demo", content)

	res, err := svc.Expand(expandTestContext(svc), skillExpandParams{Name: "demo", MaxBytes: 10})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if res.Path != filepath.Join(path, skillMainFile) {
		t.Fatalf("Expand() path = %q", res.Path)
	}
	if res.Section != "" {
		t.Fatalf("Expand() section = %q, want empty", res.Section)
	}
	if !res.Truncated || res.Content != content[:10] {
		t.Fatalf("Expand() truncation = (%v, %q)", res.Truncated, res.Content)
	}
	if res.TotalBytes != int64(len(content)) {
		t.Fatalf("Expand() total_bytes = %d, want %d", res.TotalBytes, len(content))
	}
	wantHash := sha256.Sum256([]byte(content))
	if res.ContentHash != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("Expand() content_hash = %q", res.ContentHash)
	}
}

func TestExpandMarkdownSectionTruncatesAndHashesSelection(t *testing.T) {
	t.Parallel()

	svc, root := newExpandTestService(t)
	body := "---\nname: demo\nsummary: short\n---\n## Intro\nhello\n\n### Details\n" + strings.Repeat("x", 32) + "\n\n## Done\nbye"
	path := writeExpandTestSkill(t, root, "demo", body)
	selected := "### Details\n" + strings.Repeat("x", 32)

	res, err := svc.Expand(expandTestContext(svc), skillExpandParams{Name: "demo", Section: "### Details", MaxBytes: 12})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if res.Path != filepath.Join(path, skillMainFile) {
		t.Fatalf("Expand() path = %q", res.Path)
	}
	if res.Section != "### Details" {
		t.Fatalf("Expand() section = %q", res.Section)
	}
	if !res.Truncated || res.Content != selected[:12] {
		t.Fatalf("Expand() truncation = (%v, %q)", res.Truncated, res.Content)
	}
	if res.TotalBytes != int64(len(selected)) {
		t.Fatalf("Expand() total_bytes = %d, want %d", res.TotalBytes, len(selected))
	}
	wantHash := sha256.Sum256([]byte(selected))
	if res.ContentHash != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("Expand() content_hash = %q", res.ContentHash)
	}
}

func TestImportLocalDirRejectsSourceInsideProjectSkillsRoot(t *testing.T) {
	t.Parallel()

	systemRoot := t.TempDir()
	projectRoot := t.TempDir()
	projectSkillsRoot := defaultProjectSkillsRoot(projectRoot)
	sourceDir := filepath.Join(projectSkillsRoot, "demo-skill")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, skillMainFile), []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{root: systemRoot, projectRoot: projectRoot, projectSkillsRoot: projectSkillsRoot, http: &http.Client{}}

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: sourceDir})
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
	svc := &service{projectRoot: projectRoot, root: skillsRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), http: &http.Client{}}

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: sourceDir, Scope: "project"})
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
	if _, err := os.Stat(filepath.Join(projectRoot, ".agent", "skills", "demo-skill", skillMainFile)); err != nil {
		t.Fatalf("ImportLocalDir() target SKILL.md stat err = %v", err)
	}
}

func TestImportLocalDirRejectsSourceInsideSkillsRoot(t *testing.T) {
	t.Parallel()

	skillsRoot := t.TempDir()
	projectRoot := t.TempDir()
	sourceDir := filepath.Join(skillsRoot, "demo-skill")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, skillMainFile), []byte("# demo"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	svc := &service{projectRoot: projectRoot, root: skillsRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), http: &http.Client{}}

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: sourceDir})
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
	if got, want := failures[0]["error"], "source is inside skills root: "+sourceDir; got != want {
		t.Fatalf("ImportLocalDir() failure error = %#v, want %q", got, want)
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
	existingDir := filepath.Join(projectRoot, ".agent", "skills", "demo-skill")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(existing) error = %v", err)
	}
	svc := &service{projectRoot: projectRoot, root: skillsRoot, projectSkillsRoot: defaultProjectSkillsRoot(projectRoot), http: &http.Client{}}

	out, err := svc.ImportLocalDir(skillTestContext(projectRoot), importSkillDirParams{Path: sourceDir, Scope: "project"})
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
