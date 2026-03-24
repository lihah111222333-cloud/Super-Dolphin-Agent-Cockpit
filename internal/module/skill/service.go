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
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "skills")
}
