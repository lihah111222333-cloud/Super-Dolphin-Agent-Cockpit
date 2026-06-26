package skill

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

// newCreateSkillService 构造带空项目根和系统根的 skill service，供 CreateSkill 写入边界测试使用。
func newCreateSkillService(t *testing.T) (*service, string, string) {
	t.Helper()
	systemRoot := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "repo-a")
	return &service{
		root:              systemRoot,
		projectRoot:       projectRoot,
		projectSkillsRoot: defaultProjectSkillsRoot(projectRoot),
		http:              &http.Client{},
	}, projectRoot, systemRoot
}

func TestCreateSkillWritesToProjectScopeByDefault(t *testing.T) {
	t.Parallel()

	svc, projectRoot, systemRoot := newCreateSkillService(t)

	out, err := svc.CreateSkill(
		skillTestContext(projectRoot),
		createSkillParams{Name: "demo-skill", Content: "# demo", CWD: projectRoot},
	)
	if err != nil {
		t.Fatalf("CreateSkill error = %v", err)
	}

	got, _ := out.(map[string]any)["path"].(string)
	want := filepath.Join(projectRoot, ".agent", "skills", "demo-skill", skillMainFile)
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", got, err)
	}
	if string(data) != "# demo" {
		t.Fatalf("content = %q, want %q", string(data), "# demo")
	}
	// 项目作用域写入不能污染系统根目录。
	entries, _ := os.ReadDir(systemRoot)
	if len(entries) != 0 {
		t.Fatalf("system root must stay empty; got entries: %v", entries)
	}
}

func TestCreateSkillRejectsMissingCWD(t *testing.T) {
	t.Parallel()

	svc, projectRoot, _ := newCreateSkillService(t)

	// 上下文没有 WithCWD 且参数 CWD 为空时必须返回 ErrMissingCWD。
	_, err := svc.CreateSkill(
		context.Background(),
		createSkillParams{Name: "demo", Content: "# demo"},
	)
	if !errors.Is(err, ErrMissingCWD) {
		t.Fatalf("want ErrMissingCWD, got %v", err)
	}

	// 在 ctx 中提供 cwd 后同一调用应成功，防止缺失 cwd 时误回落到系统根。
	_, err = svc.CreateSkill(
		skillTestContext(projectRoot),
		createSkillParams{Name: "demo", Content: "# demo"},
	)
	if err != nil {
		t.Fatalf("cwd-scoped CreateSkill error = %v", err)
	}
}

func TestCreateSkillRejectsInvalidName(t *testing.T) {
	t.Parallel()

	svc, projectRoot, _ := newCreateSkillService(t)

	cases := []string{"", "   ", "_bad", "../escape", "bad/slash", "bad:name"}
	for _, name := range cases {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := svc.CreateSkill(
				skillTestContext(projectRoot),
				createSkillParams{Name: name, Content: "# body", CWD: projectRoot},
			)
			if !errors.Is(err, ErrInvalidSkillName) {
				t.Fatalf("name %q: want ErrInvalidSkillName, got %v", name, err)
			}
		})
	}
}

func TestCreateSkillRejectsEmptyContent(t *testing.T) {
	t.Parallel()

	svc, projectRoot, _ := newCreateSkillService(t)

	_, err := svc.CreateSkill(
		skillTestContext(projectRoot),
		createSkillParams{Name: "demo", Content: "   ", CWD: projectRoot},
	)
	if !errors.Is(err, ErrInvalidSkillName) {
		t.Fatalf("want ErrInvalidSkillName-wrapped content error, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "content is required") {
		t.Fatalf("err should mention missing content, got %v", err)
	}
}

func TestCreateSkillPublishesSkillsChanged(t *testing.T) {
	// 不并行：这里共享 event dispatcher，和同包技能写入事件测试保持一致。
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan uidto.SkillsChanged, 1)
	cancel := event.Subscribe(dispatcher, func(ev uidto.SkillsChanged) { got <- ev })
	defer cancel()

	svc, projectRoot, _ := newCreateSkillService(t)
	svc.bindDispatcher(dispatcher)

	if _, err := svc.CreateSkill(
		skillTestContext(projectRoot),
		createSkillParams{Name: "demo", Content: "# body", CWD: projectRoot},
	); err != nil {
		t.Fatalf("CreateSkill error = %v", err)
	}

	ev := mustReceiveSkillsChanged(t, got)
	if ev.Name != "demo" || ev.Action != "write" || ev.Count != 1 {
		t.Fatalf("skills changed event = %#v", ev)
	}
	if !reflect.DeepEqual(ev.Actions, []string{"write"}) {
		t.Fatalf("skills changed event actions = %#v", ev.Actions)
	}
	if ev.Cwd != "" {
		t.Fatalf("skills changed event leaked absolute cwd: %#v", ev)
	}
	if ev.RepoFingerprint == "" || ev.RelativePath == "" {
		t.Fatalf("skills changed event missing repo fingerprint / relative path: %#v", ev)
	}
}
