package skill

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
)

const (
	maxSkillFileBytes = 1 << 20
	skillMainFile     = "SKILL.md"
)

type service struct {
	root              string
	projectRoot       string
	http              *http.Client
	readConfigState   func(context.Context, string) (any, error)
	emitSkillsChanged skillsChangedEmitter
	skillsChangedMu   sync.Mutex
	skillsChangedNext uidto.SkillsChanged
	skillsChangedSeq  uint64
}

var _ Service = (*service)(nil)

func NewService(projectRoot string) Service {
	return &service{
		root:        defaultSkillsRoot(),
		projectRoot: strings.TrimSpace(projectRoot),
		http:        &http.Client{Timeout: 15 * time.Second},
	}
}

func defaultSkillsRoot() string {
	if root := strings.TrimSpace(os.Getenv("CODEX_HOME")); root != "" {
		return filepath.Join(root, "skills")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".codex", "skills")
	}
	// UserHomeDir 失败（如无 $HOME 的受限环境）时兜底到临时目录，
	// 避免 s.root 为空导致整个技能功能静默失效。
	return filepath.Join(os.TempDir(), "codex-skills")
}
