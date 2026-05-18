package skill

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/kelindar/event"
	"go.uber.org/fx"

	auditstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
)

// TODO(P7): event-driven auto-match. The current skills/match/preview RPC
// is the only trigger; we still need a thread.Started subscriber that
// performs auto-match + binds the result onto the session at runtime.
var Module = fx.Module("skill",
	fx.Provide(
		fx.Annotate(
			newService,
			fx.As(new(Service)),
			fx.As(new(contract.SkillMirrorReconciler)),
			fx.As(new(contract.SkillNativeReplacementSource)),
		),
		ProvideSkillLister,
		ProvideSkillCatalogSource,
		ProvideSkillHydrationSource,
	),
	fx.Provide(NewSkillHandlers),
	fx.Invoke(runBuiltinSkillSeed),
)

type serviceDeps struct {
	fx.In

	Config     *contract.Config
	Dispatcher *event.Dispatcher
	AuditStore auditstore.Store
}

type skillHandlerDeps struct {
	fx.In

	Service       Service
	Requester     contract.ApprovalRequester `optional:"true"`
	DreamExecutor contract.DreamExecutor     `optional:"true"`
}

func newService(deps serviceDeps) *service {
	projectRoot := ""
	if deps.Config != nil {
		projectRoot = strings.TrimSpace(deps.Config.ProjectRoot)
	}
	svc := NewService(projectRoot).(*service)
	svc.bindDispatcher(deps.Dispatcher)
	svc.auditStore = deps.AuditStore
	return svc
}

func ProvideSkillLister(svc Service) SkillLister { return svc }

func ProvideSkillCatalogSource(svc Service) SkillCatalogSource { return svc }

func ProvideSkillHydrationSource(svc Service) SkillHydrationSource { return svc }

const builtInSkillRoot = "embedded_skills"

func runBuiltinSkillSeed(lc fx.Lifecycle, svc Service) {
	seeder, ok := svc.(*service)
	if !ok || seeder == nil {
		return
	}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			_, err := seedBuiltInSkills(seeder.resolvedSuperDolphinHome())
			return err
		},
	})
}

func seedBuiltInSkills(superDolphinHome string) (int, error) {
	home := strings.TrimSpace(superDolphinHome)
	if home == "" {
		return 0, fmt.Errorf("super dolphin home is required")
	}
	names, err := listBuiltInSkillNames()
	if err != nil {
		return 0, err
	}
	written := 0
	hubRoot := filepath.Join(home, "skills", "personal", personalSkillTypeHub)
	for _, name := range names {
		ok, err := seedOneBuiltInSkill(hubRoot, name)
		if err != nil {
			return written, err
		}
		if ok {
			written++
		}
	}
	return written, nil
}

func listBuiltInSkillNames() ([]string, error) {
	entries, err := builtInSkillFS.ReadDir(builtInSkillRoot)
	if err != nil {
		return nil, fmt.Errorf("skill builtins: list embedded: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() && builtInSkillExists(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func builtInSkillExists(name string) bool {
	_, err := builtInSkillFS.ReadFile(builtInSkillRoot + "/" + name + "/" + skillMainFile)
	return err == nil
}

func seedOneBuiltInSkill(hubRoot, name string) (bool, error) {
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return false, fmt.Errorf("skill builtins: invalid embedded skill name %q", name)
	}
	targetDir := filepath.Join(hubRoot, name)
	if _, err := os.Stat(filepath.Join(targetDir, skillMainFile)); err == nil {
		return false, nil
	} else if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return copyBuiltInSkillDir(builtInSkillRoot+"/"+name, targetDir)
}

func copyBuiltInSkillDir(sourceRoot, targetRoot string) (bool, error) {
	wrote := false
	err := fs.WalkDir(builtInSkillFS, sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		ok, err := copyBuiltInSkillEntry(sourceRoot, targetRoot, path, entry)
		if ok {
			wrote = true
		}
		return err
	})
	return wrote, err
}

func copyBuiltInSkillEntry(sourceRoot, targetRoot, path string, entry fs.DirEntry) (bool, error) {
	rel, err := filepath.Rel(sourceRoot, path)
	if err != nil {
		return false, err
	}
	target := filepath.Join(targetRoot, rel)
	if entry.IsDir() {
		return false, os.MkdirAll(target, 0o755)
	}
	return writeBuiltInSkillFileIfMissing(path, target)
}

func writeBuiltInSkillFileIfMissing(source, target string) (bool, error) {
	data, err := builtInSkillFS.ReadFile(source)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, err
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := file.Write(data); err != nil {
		return false, closeBuiltInSkillFile(file, err)
	}
	return true, file.Close()
}

func closeBuiltInSkillFile(file *os.File, writeErr error) error {
	if closeErr := file.Close(); closeErr != nil {
		return fmt.Errorf("write built-in skill: %w; close: %v", writeErr, closeErr)
	}
	return writeErr
}

func (s *service) ReplacedNativeTools(ctx context.Context, cwd, provider string) []string {
	if s == nil {
		return nil
	}
	records, _, err := s.canonicalEffectiveSet(ctx, cwd)
	if err != nil {
		return nil
	}
	skills := make([]SkillInfo, 0, len(records))
	for _, record := range records {
		skills = append(skills, record.info)
	}
	return replacedNativeToolsForProvider(skills, provider)
}

func replacedNativeToolsForProvider(skills []SkillInfo, provider string) []string {
	seen := make(map[string]struct{})
	provider = strings.ToLower(strings.TrimSpace(provider))
	for _, info := range skills {
		collectReplacementTools(seen, info.ReplacesNative["*"])
		collectReplacementTools(seen, info.ReplacesNative[provider])
	}
	return sortedReplacementNames(seen)
}

func collectReplacementTools(seen map[string]struct{}, tools []string) {
	for _, name := range tools {
		if strings.TrimSpace(name) != "" {
			seen[name] = struct{}{}
		}
	}
}

func sortedReplacementNames(seen map[string]struct{}) []string {
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
