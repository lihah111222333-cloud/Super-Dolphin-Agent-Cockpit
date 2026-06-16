---
name: releasing-go-projects
description: Use when preparing, reviewing, tagging, versioning, documenting, packaging, signing, or publishing Go releases, changelogs, release notes, Conventional Commits, Semantic Versioning, rollback plans, deployment readiness, or release CI workflows.
---

# Releasing Go Projects

## Overview

Release only from a verified, explainable state. A release must connect source changes, version semantics, changelog entries, artifacts, checksums, rollback notes, and guard evidence.

## Industry Baseline

Use these standards:

- Conventional Commits for commit message shape and changelog automation.
- Semantic Versioning for public API and artifact versions.
- Keep a Changelog for human-readable release notes.
- Git tags as immutable release pointers.
- Checksums, SBOMs, and signatures for release artifacts once binaries or images exist.
- SLSA provenance when release automation is introduced.

## Version Rules

| Change Type | Version Impact |
| --- | --- |
| Breaking public API or contract change | Major |
| Backward-compatible feature | Minor |
| Backward-compatible bug fix | Patch |
| Internal-only tooling or docs | Usually no release version |

Before `v1.0.0`, document compatibility expectations explicitly because SemVer allows more movement in `0.y.z`.

## Required Release Checklist

- Working tree is clean except allowed generated local files.
- `make guard-release` passes.
- Public API contract changes are documented.
- Database migrations have rollback or forward-fix strategy.
- Changelog or release notes explain user-visible changes.
- Artifacts are reproducible from the tagged source.
- Checksums are generated for binaries or archives.
- SBOM/provenance/signing are generated when release automation exists.
- Rollback or mitigation path is documented for risky changes.

## Commit And Changelog Rules

This repository requires Chinese Git commit messages. The commit subject and detail/body must both contain Chinese, and the detail/body is required.

Do not start commit subjects with English Conventional Commit prefixes such as:

- `feat:`
- `fix:`
- `docs:`
- `test:`
- `refactor:`
- `chore:`
- `ci:`
- `build:`
- `perf:`

Use Chinese commit subjects instead:

- `功能：新增用户登录`
- `修复：处理配置加载失败`
- `文档：补充接口契约说明`
- `守卫：强制提交信息使用中文`

If changelog automation later needs Conventional Commit data, add a mapping tool instead of requiring humans to write English commit subjects.

## Verification

Before tagging or publishing:

```bash
make guard-release
git status --short
```

Before committing:

```bash
git commit -m '中文主题' -m '中文详情，说明变更内容和原因。'
```

After tagging, verify the tag points to the intended commit:

```bash
git rev-parse HEAD
git rev-parse <tag>
```

## Common Mistakes

- Tagging before release guards pass.
- Calling a breaking contract change a patch.
- Publishing artifacts that cannot be reproduced from the tag.
- Omitting migration and rollback notes.
- Letting changelog text drift from actual commits.
