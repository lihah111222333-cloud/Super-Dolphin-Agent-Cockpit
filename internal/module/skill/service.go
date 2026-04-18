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
	// approval 是 P20 Phase 1 新增的审批缓存指针。Phase 1 不涉及调用，预留给 Phase 6
	// skill_expand RPC 集成时使用 (s.approval.Lookup / Approve / Revoke)。初始化失败时
	// 降级为 nil；调用方必须先 nil-check。
	approval *ApprovalCache
}

var _ Service = (*service)(nil)

func NewService(projectRoot string) Service {
	pr := strings.TrimSpace(projectRoot)
	// P20 Phase 1: 尝试加载审批缓存。文件不存在时返回空 cache（正常）；文件损坏时
	// NewApprovalCache 返回空 cache + err，此处处于构造期，无法抓 err 回调日志；
	// 统一降级为空 cache——下次 skill_expand 调用会当作“未审批”重新弹审批流。
	approvalCache, _ := NewApprovalCache(DefaultApprovalCachePath())
	return &service{
		root:              defaultSkillsRoot(),
		projectRoot:       pr,
		projectSkillsRoot: defaultProjectSkillsRoot(pr),
		http:              &http.Client{Timeout: 15 * time.Second},
		approval:          approvalCache,
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
