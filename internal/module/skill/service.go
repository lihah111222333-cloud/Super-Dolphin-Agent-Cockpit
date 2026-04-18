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
	projectSkillsRoot string
	http              *http.Client
	readConfigState   func(context.Context, string) (any, error)
	emitSkillsChanged skillsChangedEmitter
	skillsChangedMu   sync.Mutex
	skillsChangedNext uidto.SkillsChanged
	skillsChangedSeq  uint64
}

var _ Service = (*service)(nil)

func NewService(projectRoot string) Service {
	pr := strings.TrimSpace(projectRoot)
	return &service{
		root:              defaultSkillsRoot(),
		projectRoot:       pr,
		projectSkillsRoot: defaultProjectSkillsRoot(pr),
		http:              &http.Client{Timeout: 15 * time.Second},
	}
}

func defaultSkillsRoot() string {
	if override := strings.TrimSpace(os.Getenv("SKILLS_ROOT")); override != "" {
		return override
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".multi-agent", "skills")
	}
	// UserHomeDir 失败（如无 $HOME 的受限环境）时兜底到临时目录，
	// 避免 s.root 为空导致整个技能功能静默失效。
	return filepath.Join(os.TempDir(), "multi-agent-skills")
}

func defaultProjectSkillsRoot(projectRoot string) string {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return ""
	}
	return filepath.Join(projectRoot, ".agent", "skills")
}

// skillRoots 返回扫描/校验时按优先级排列的技能根目录：
// 项目根（若有）优先于系统根。空根会被过滤。
func (s *service) skillRoots() []string {
	roots := make([]string, 0, 2)
	if v := strings.TrimSpace(s.projectSkillsRoot); v != "" {
		roots = append(roots, v)
	}
	if v := strings.TrimSpace(s.root); v != "" {
		roots = append(roots, v)
	}
	return roots
}
