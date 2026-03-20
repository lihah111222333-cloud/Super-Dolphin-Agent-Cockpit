package skill

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
)

const (
	maxSkillFileBytes = 1 << 20
	skillMainFile     = "SKILL.md"
)

type service struct {
	cards           commandcardstore.Store
	root            string
	http            *http.Client
	readConfigState func(context.Context, string) (any, error)
}

var _ Service = (*service)(nil)

func NewService(cards commandcardstore.Store) Service {
	return &service{
		cards: cards,
		root:  defaultSkillsRoot(),
		http:  &http.Client{},
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
