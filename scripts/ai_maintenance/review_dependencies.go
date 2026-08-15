package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const reviewFrontendDependencyValidator = `
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { pathToFileURL } from 'node:url';
const [appRoot, seedRoot] = process.argv.slice(1);
process.env.SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED = seedRoot;
const closure = await import(pathToFileURL(path.join(appRoot, 'scripts/frontend-execution-closure.mjs')).href);
const integrity = await import(pathToFileURL(path.join(appRoot, 'scripts/frontend-maintainability-dependency-integrity.mjs')).href);
const expected = JSON.parse(fs.readFileSync(path.join(appRoot, 'scripts/frontend-maintainability-dependencies.json'), 'utf8'));
integrity.validateDependencyIntegrity(expected);
integrity.dependencyEnvironmentProfile();
const packageLock = fs.readFileSync(path.join(appRoot, 'package-lock.json'));
if (createHash('sha256').update(packageLock).digest('hex') !== expected.packageLockSha256) {
  throw new Error('candidate package-lock does not match its dependency integrity identity');
}
closure.materializeImmutableDependencyOverlay(appRoot, seedRoot);
integrity.assertHiddenPackageLockClosure(appRoot);
const runtimeIdentity = integrity.dependencyTreeIntegrity(appRoot, {
  expectedOptionalLockSha256: expected.optionalLockSha256,
  runtimeOnly: true,
});
if (runtimeIdentity.binLockSha256 !== expected.binLockSha256) {
  throw new Error('frontend dependency seed bin closure mismatch');
}
for (const [relativePath, digest] of Object.entries(expected.requiredTools)) {
  const tool = fs.readFileSync(closure.nodeModulesPath(seedRoot, relativePath));
  if (createHash('sha256').update(tool).digest('hex') !== digest) {
    throw new Error('frontend dependency seed tool digest mismatch: ' + relativePath);
  }
}
`

// prepareReviewExecutionDependencies 只在前端 runner 命中时绑定并验证受控依赖 seed。
func prepareReviewExecutionDependencies(repoRoot, executionRoot string, plan gatePlan) (func() error, error) {
	if !reviewPlanNeedsFrontendDependencies(plan.RequiredGates) {
		return func() error { return nil }, nil
	}
	if err := validateReviewDependencyExecutionRoot(repoRoot, executionRoot); err != nil {
		return nil, err
	}
	seedRoot, cleanup, err := reviewFrontendDependencySeed(executionRoot)
	if err != nil {
		return nil, err
	}
	appRoot := filepath.Join(executionRoot, "frontend-app")
	err = runCommand(executionRoot, "node", "--input-type=module", "--eval", reviewFrontendDependencyValidator, appRoot, seedRoot)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("validate exact candidate frontend dependency seed: %w", err), cleanup())
	}
	return cleanup, nil
}

// validateReviewDependencyExecutionRoot 拒绝空路径、ambient cwd 和仓库 worktree 边界外目标。
func validateReviewDependencyExecutionRoot(repoRoot, executionRoot string) error {
	if strings.TrimSpace(executionRoot) == "" {
		return errors.New("exact candidate execution root is required for frontend dependencies")
	}
	repository, err := canonicalReviewRepository(repoRoot)
	if err != nil {
		return err
	}
	canonicalExecutionRoot, err := filepath.EvalSymlinks(executionRoot)
	if err != nil {
		return fmt.Errorf("canonicalize frontend dependency execution root: %w", err)
	}
	if err := validateReviewWorktreePath(filepath.Join(repository, ".worktrees"), canonicalExecutionRoot); err != nil {
		return fmt.Errorf("frontend dependency execution root is not an exact candidate: %w", err)
	}
	return nil
}

func reviewPlanNeedsFrontendDependencies(gates []string) bool {
	for _, gate := range gates {
		switch gate {
		case "frontend:static-guards", "frontend:lint", "frontend:typecheck-contracts", "frontend:changed-tests":
			return true
		}
	}
	return false
}

// reviewFrontendDependencySeed 只接受显式物理 seed，并创建排除可变缓存的规范化视图。
func reviewFrontendDependencySeed(executionRoot string) (string, func() error, error) {
	configured, ok := os.LookupEnv("SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED")
	if !ok {
		return "", nil, errors.New("SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED is required; use a verified seed or canonical remote ECI")
	}
	if strings.TrimSpace(configured) == "" {
		return "", nil, errors.New("SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED is blank")
	}
	if !filepath.IsAbs(configured) {
		return "", nil, errors.New("SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED must be absolute")
	}
	info, err := os.Lstat(configured)
	if err != nil {
		return "", nil, fmt.Errorf("frontend dependency seed is unavailable: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("frontend dependency seed must be a physical directory: %s", configured)
	}
	return normalizeReviewFrontendDependencySeed(configured, filepath.Dir(executionRoot))
}

// normalizeReviewFrontendDependencySeed 建立轻量视图，避免复制或遍历完整依赖树。
func normalizeReviewFrontendDependencySeed(sourceRoot, worktreeRoot string) (string, func() error, error) {
	if runtime.GOOS == "windows" {
		return cleanWindowsReviewFrontendDependencySeed(sourceRoot)
	}
	seedRoot, err := os.MkdirTemp(worktreeRoot, ".ai-maintenance-frontend-seed-*")
	if err != nil {
		return "", nil, fmt.Errorf("reserve normalized frontend dependency seed: %w", err)
	}
	cleanup := func() error { return removeReviewDependencySeed(worktreeRoot, seedRoot) }
	if err := populateNormalizedReviewDependencySeed(sourceRoot, seedRoot); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	return seedRoot, cleanup, nil
}

// populateNormalizedReviewDependencySeed 排除 cache，并物化浅层 package root 骨架。
func populateNormalizedReviewDependencySeed(sourceRoot, seedRoot string) error {
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		return fmt.Errorf("read explicit frontend dependency seed: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == ".vite" || entry.Name() == ".vite-temp" {
			continue
		}
		sourcePath := filepath.Join(sourceRoot, entry.Name())
		targetPath := filepath.Join(seedRoot, entry.Name())
		if err := populateNormalizedReviewDependencyTopLevel(sourcePath, targetPath, entry); err != nil {
			return fmt.Errorf("normalize frontend dependency seed entry %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func populateNormalizedReviewDependencyTopLevel(sourcePath, targetPath string, entry os.DirEntry) error {
	if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
		return linkNormalizedReviewDependencyEntry(sourcePath, targetPath, entry)
	}
	return populateNormalizedReviewDependencyDirectory(sourcePath, targetPath, strings.HasPrefix(entry.Name(), "@"))
}

// populateNormalizedReviewDependencyDirectory 创建物理 package root，深层内容仍链接原 seed。
func populateNormalizedReviewDependencyDirectory(sourceRoot, targetRoot string, materializeChildDirectories bool) error {
	if err := os.Mkdir(targetRoot, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(sourceRoot, entry.Name())
		targetPath := filepath.Join(targetRoot, entry.Name())
		if materializeChildDirectories && entry.Type()&os.ModeSymlink == 0 && entry.IsDir() {
			if err := populateNormalizedReviewDependencyDirectory(sourcePath, targetPath, false); err != nil {
				return err
			}
			continue
		}
		if err := linkNormalizedReviewDependencyEntry(sourcePath, targetPath, entry); err != nil {
			return err
		}
	}
	return nil
}

func linkNormalizedReviewDependencyEntry(sourcePath, targetPath string, entry os.DirEntry) error {
	if entry.Type()&os.ModeSymlink == 0 {
		return os.Symlink(sourcePath, targetPath)
	}
	link, err := os.Readlink(sourcePath)
	if err != nil {
		return err
	}
	return os.Symlink(link, targetPath)
}

// cleanWindowsReviewFrontendDependencySeed 仅接受无 reserved cache 的物理 seed，避免不可靠的二次链接。
func cleanWindowsReviewFrontendDependencySeed(sourceRoot string) (string, func() error, error) {
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		return "", nil, fmt.Errorf("read Windows frontend dependency seed: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == ".vite" || entry.Name() == ".vite-temp" {
			return "", nil, errors.New("Windows frontend dependency seed contains reserved cache; use a canonical clean seed or remote ECI")
		}
	}
	return sourceRoot, func() error { return nil }, nil
}

func removeReviewDependencySeed(worktreeRoot, seedRoot string) error {
	if err := validateReviewWorktreePath(worktreeRoot, seedRoot); err != nil {
		return err
	}
	if err := os.RemoveAll(seedRoot); err != nil {
		return fmt.Errorf("remove normalized frontend dependency seed: %w", err)
	}
	return nil
}
