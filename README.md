# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**The self-guarding repository for AI-written software.** AI agents implement changes; repository-owned maps, contracts, tests, and gates decide whether those changes are safe enough to keep.

> [!IMPORTANT]
> **Maintainer declaration: 100% AI-written original code and project-authored documentation, human-directed, repository-guarded.** Product code, test code, and project-owned documentation are written or refactored by AI agents. Humans retain ownership of product intent, architecture decisions, credentials, and releases. Authorship does not imply infallibility: every accepted change remains subject to repository-owned evidence and gates. Upstream legal and community texts retain their original attribution.

**Truth-image delivery enforcement.** Versioned [Git hooks](.githooks/README.md), manual `make ci-l0`, `make ci-l1`, and `make ci-l2-claude`, release, and GitHub Actions use the fail-closed remote ECI gate. `pre-commit` and the manual L0-L2 commands check the exact staged tree; release and GitHub Actions check the exact commit. Missing remote configuration, provenance, result authority, cleanup evidence, or a failed gate rejects the action. `commit-msg` continues to require Chinese commit text and fix-test evidence.

Super Dolphin Agent is a **production-grade, AI-native vibe-coding engineering system and multi-agent development control plane**. It combines a local desktop runtime, MCP orchestration, multi-language LSP navigation, provider integrations, persistent workflows, and machine-enforced engineering boundaries in one working reference implementation.

The English README is the canonical overview. Translations preserve the same product scope, commands, paths, environment variables, repository identity, and license. Detailed facts live in [Architecture](docs/open-source/ARCHITECTURE.md), [Governance in Action](docs/open-source/GOVERNANCE.md), and the generated [Code Map](docs/doc/codemap/README.md).

<!-- sd:why -->
## Why Super Dolphin Exists

Most agent frameworks optimize task execution. Super Dolphin also governs what a completed task is allowed to change in a long-lived software system.

Its maintenance loop has six stages:

1. **Orient** with generated code maps and capability contracts.
2. **Understand** definitions, references, call hierarchies, and diagnostics through LSP.
3. **Change** a narrow, explicitly owned surface.
4. **Constrain** the diff with AST/SSA rules, dependency boundaries, complexity budgets, and fail-fast contracts.
5. **Prove** the result with focused tests, generated-artifact checks, and change-aware gates.

6. **Learn** from proven fixes: extract the root cause, generalize the invariant, and promote recurring patterns into regression evidence or executable guards.

### Guardrails for vibe coding

AI can generate code tens or hundreds of times faster than a person can write it, so the bottleneck moves from code production to testing and trustworthy delivery. Fixing one occurrence is not completion if the same defect pattern can remain elsewhere or return in AI-generated code.

Super Dolphin periodically consolidates evidence from bugs proven by tests or real use into reusable engineering knowledge. Stable patterns are promoted into repository-owned tests, fixtures, AST/SSA rules, dependency policies, or other executable gates. If an AI agent reproduces a known bad smell, the gate rejects the change and forces a repair before delivery.

Skills and prompts can guide generation; guards constrain what may be accepted. A candidate guard still requires reproducible evidence, a generalizable invariant, and deterministic acceptance checks—this is evidence-driven ratcheting, not blind self-modification. The repository currently implements automatic memory consolidation and extensive guard infrastructure; fully automatic end-to-end promotion of every fix into a new executable guard remains an engineering direction rather than a claim of complete coverage.

This is the direction of AI-native vibe coding: humans specify intent, architecture, and acceptance boundaries; AI generates within those specifications; the repository learns from defects and becomes progressively more robust and legible instead of depending on people to rediscover the same class of bug.

### Production-grade vibe coding, not only agent autonomy

Well-known projects such as [Hermes Agent](https://github.com/NousResearch/hermes-agent) and [OpenClaw](https://github.com/openclaw/openclaw) demonstrate the value of autonomous execution, broad tool use, persistent memory, and reusable skills. Hermes emphasizes a learning loop that creates and improves skills from experience; OpenClaw emphasizes a personal assistant that acts across operating systems, messaging platforms, and services.

Agent capabilities are not fixed. This project was initiated by one person and is written primarily by AI, so one maintainer's time, experience, and use cases are necessarily limited; it needs the community to grow. Contributors can submit PRs with modules, integrations, UI, Skills, MCP, Providers, and tools, or contribute target scenarios, specifications, acceptance tests, and real defects without writing the implementation. The engineering system applies the same hard constraints to community-written and AI-generated code, and turns unimplemented needs into engineering tasks that force AI to generate or repair complete modules conforming to the architecture, contracts, and gates. This combines community collaboration with AI generation speed to rapidly close the gap with or surpass Hermes Agent and OpenClaw without relying on one maintainer to hand-code every capability.

Hermes Agent and OpenClaw show how far autonomous execution, broad tool integration, and rapid iteration can go. They also make a shared challenge clear: as features, channels, and runtime environments expand, tests and local safeguards alone cannot keep a repository understandable and evolvable. Oversized core modules, dispersed responsibility, and hard-to-trace impact paths can leave individual features working while the cost of maintaining the whole system continues to rise.

That is the problem Super Dolphin is designed to solve. Whether code is produced by AI alone or by AI working with elite engineers, sustainable speed requires repository-enforced specifications, contracts, regression tests, and executable gates that keep architecture, proven behavior, and product intent aligned.

When code is produced tens or hundreds of times faster, human review capacity cannot scale at the same rate. Without repository-enforced specifications, contracts, regression tests, and executable gates, even an expert engineering team can gradually lose control of its code. Local features may keep working while duplicated paths, lifecycle ambiguity, hidden coupling, and untested assumptions accumulate until the system becomes progressively harder to understand, test, deliver, and maintain.

Super Dolphin's advantage is **sustainable iteration**. It treats the repository itself as the control system so new community capabilities can be absorbed without rapidly turning the codebase into unmaintainable architecture. Specifications define intent; typed contracts and dependency boundaries restrict implementation; tests and regression fixtures preserve demonstrated behavior; AST/SSA guards and change-aware gates reject known bad smells. Features can keep growing, but only code that satisfies the repository's executable specification is accepted.

### The route to match and surpass leading agents

With fast AI code generation, capability code is not the scarce resource. The scarce resource is an engineering constraint system that reliably turns community needs and contributions into qualified modules. Super Dolphin uses this route:

1. **The community defines the real scenario to pursue.** Contribute workflows, expected outcomes, and failure cases that Hermes Agent or OpenClaw already solves—or has not solved—not merely stars or feature checklists.
2. **Turn the scenario into an executable specification.** Before code is generated or submitted, define module ownership, typed contracts, dependency direction, security boundaries, acceptance tests, and delivery evidence so the desired result is machine-judgeable.
3. **Implement through two paths: community PRs and AI generation.** Contributors can submit complete modules or focused integrations; AI can use code maps and LSP to implement the backend, integrations, necessary UI, tests, and documentation inside the specified boundary. Neither source may bypass the architecture to splice together a one-off demo.
4. **Apply the same hard gates to make all code conform.** Compilation, tests, end-to-end scenarios, permission and lifecycle checks, AST/SSA guards, dependency boundaries, and change-aware gates assess community-written and AI-generated code alike. Nonconforming work is rejected and repaired by contributors or AI rather than leaving test and maintenance debt to the maintainer.
5. **Prove parity on real tasks.** “It answered once” is not parity. A capability becomes validated only when it reproducibly completes the target workflow and its failure paths.
6. **Turn community use into the next generation constraint.** Production failures, regressions, and repeated fixes become fixtures, specifications, and executable guards, so later submitted or generated modules avoid old defects by default and the engineering baseline strengthens as capabilities expand.

This is not a route for one maintainer to hand-write every feature. The community can contribute code, problems, and evidence; the engineering system constrains community code and drives AI to complete or repair modules. It amplifies limited maintainer capacity into community collaboration and AI engineering throughput, enabling rapid pursuit or surpassing of Hermes Agent and OpenClaw while preserving a maintainable codebase.

### Bounded-context maintenance

The nature of this project does not require anyone to read the entire codebase. Neither engineers nor AI need to understand the whole repository before starting work. Begin with the target capability and acceptance outcome, use code maps, module ownership, narrow contracts, and deterministic gates to obtain only the context required for the task, then execute within those constraints and repair violations to deliver the intended capability.

This is not a guarantee that every change is local. Cross-cutting work still requires broader reference and impact analysis, but expanding the necessary context is not the same as reading the entire repository; all accepted changes remain subject to the relevant tests and review evidence.

Under this project's architectural standard, any implementation that requires reading the entire codebase to discover the impact of a change is unsuitable for continuous AI maintenance and equally unmaintainable for human engineers who lack complete system knowledge. It usually means module ownership, dependency direction, contracts, or guards have failed to make the impact surface explicit. Engineering constraints must turn dependencies and consequences into navigable, checkable, fail-fast machine signals instead of relying on one expert to remember the whole system.

### AI-first maintainability, engineer-owned semantics

The repository is intentionally organized for AI to locate, understand, modify, and verify code under constraints. Explicit contracts, narrow modules, small functions, generated maps, and machine-readable boundaries can feel more verbose or fragmented to a person reading files linearly. Human reading convenience is therefore not the only optimization target. This is not permission for duplication or unclear code: every extra boundary must improve navigation, isolation, or deterministic verification.

Small functions are not treated as a problem merely because they increase the number of symbols. AI reads through definitions, references, call hierarchies, code maps, and tests rather than relying on one long file as a narrative. Contributors are encouraged to read this repository with an AI assistant and the repository's navigation tools instead of trying to build a complete mental model from raw source alone.

The repository should therefore not be judged only through the traditional practice of manually reading file after file and tracing every call chain. A more relevant evaluation is to have AI use the code maps, LSP, contracts, tests, and gates to perform a real locate–understand–impact-analyze–change–verify cycle, then assess how easy the repository is to read, modify, and maintain.

AI can implement a specified design, but it cannot independently own business meaning: which problem should be solved, what a feature means, how modules should behave, what the final user-visible outcome must be, and which tradeoffs are acceptable. These are direction-setting decisions, not code-generation tasks.

Humans make the decisions and keep hold of the steering wheel: they decide the problem to solve, the business meaning, the intended user-visible outcome, and the acceptable tradeoffs. After AI writes the code, humans must still verify that the feature actually satisfies the intended need. That responsibility cannot be outsourced to an agent.

Faster code production does not make testing free or exponentially cheaper. It creates more changes, combinations, and business-risk surface to validate, so the testing and acceptance burden rises as coding gets faster. This architecture already places about 90% of machine-detectable or machine-preventable problems behind contracts, static guards, tests, and gates; humans verify the remaining roughly 10%: whether the requirement was expressed correctly, whether behavior is right in real use, and whether the product is still heading in the right direction.

Engineers therefore remain at the center of this project. They define product direction, business semantics, module responsibilities, architecture, acceptance criteria, and risk boundaries; AI translates those decisions into code, tests, documentation, and repeatable maintenance work under repository gates. The aim is not to remove engineers, but to move their attention from reading and hand-writing every small function to governing meaning, evidence, and system evolution. **Engineers keep the steering wheel; AI increases engineering throughput but does not replace judgment about business direction or delivery results.**

### Independent AI maintainability assessment

On July 13, 2026, three independent agents using the GPT-5.6 medium-thinking model jointly rated this repository's pure AI code-maintenance capability at **95/100 (A+)**; a separate blind sub-agent, prohibited from reading the existing score, reproduced **95.6/100 (A+)**. Code maps, LSP navigation, narrow contracts, architecture guards, and deterministic verification allow AI to locate, analyze, implement, and verify changes without reading the entire codebase, while humans retain product direction, business semantics, acceptance decisions, and project documentation.

**Reproduction prompt:** From a pure AI-maintenance perspective, score this repository out of 100 assuming humans own only direction, semantics, acceptance, and documentation while AI executes design documents and owns code location, impact analysis, implementation, testing, diagnostics, and delivery, token cost is ignored, UI, release, and commercial maturity are excluded, the existing README score must not be read, and the answer must provide evidence, category scores, and reasons it is not 100.

### Development journey: why Super Dolphin exists

Super Dolphin is the third major stage in a continuous engineering lineage. From the first-stage V1 through the direct predecessor `go-agent-v2` (V2) to the current V3, the lineage is not merely a sequence of added agent features. It iteratively addresses one engineering pain point: every change should expose its impact within bounded context, execute under explicit constraints, and be machine-verified without requiring AI or humans to read and remember the entire codebase.

1. **The first stage** was a Python command-line multi-agent tool. It validated that models could split tasks, cooperate through tools, and complete real engineering work.
2. **`go-agent-v2` was the direct predecessor of this project.** It grew from internal task dispatch into a working engineering system that combined automated quantitative-trading workflows, multi-agent desktop controls, provider integration, and persistent execution. It proved that the product direction was useful in real work; it was not a disposable prototype.
3. **Super Dolphin / V3 began on March 19, 2026** as a new architectural generation. It carries forward the predecessor's capabilities and operational lessons while rebuilding the foundations required for long-term AI-driven development.

The reason for V3 was not that the predecessor could not work. The predecessor worked and kept accumulating features, but AI could generate local changes faster than a convention-driven architecture could safely absorb them. Tests could prove an individual path while ownership, lifecycle, dependency direction, and legibility still degraded across the system. According to the maintainers' pre-publication records, the pressure became visible in concrete forms:

- more than 80 RPC methods accumulated parallel binding, validation, capability, and logging paths;
- lifecycle ownership became distributed across managers and asynchronous side effects;
- a central event handler reached 557 lines;
- manual application assembly exceeded 200 lines.

This is why V3 was created as more than a feature upgrade. It moves architectural knowledge out of reviewer memory and prompts into repository-owned contracts, code maps, typed boundaries, regression evidence, and executable gates. We call the failure mode it addresses **AI code rot**: local changes continue to work while global contracts, ownership boundaries, and legibility decay.

The predecessor's private development history is maintainer-supplied context rather than public evidence. The public repository therefore exposes the architectural responses, guards, regression fixtures, and reproducible commands produced from those lessons.

| Pressure observed in the predecessor | Super Dolphin response |
|---|---|
| Parallel hand-written RPC paths | Typed requests, one contract surface, explicit middleware and error semantics |
| Distributed lifecycle side effects | Declarative transitions, typed events, and owned lifecycle runners |
| Manual object graphs | `fx` composition with explicit startup and shutdown ownership |
| Business code coupled to adapters | Onion boundaries, module-owned ports, and anti-corruption adapters |
| Giant mixed-abstraction functions | A repository-specific `80 / 4 / 10` function, nesting, and complexity budget |
| Reviewer memory as policy | AST/SSA guards, generated maps, manifests, hooks, and reproducible evidence |

The `80 / 4 / 10` budget is not a universal style rule. It is a ratcheted constraint for this orchestration-heavy repository: default effective function length `<= 80`, nesting `<= 4`, and cyclomatic complexity `<= 10`.

### What the repository enforces

| Layer | Protects against | Repository evidence |
|---|---|---|
| Navigation truth | Editing the wrong subsystem or using stale project knowledge | `docs/doc/codemap`, project map, capability manifest |
| Architecture boundaries | Domain code reaching into stores, providers, UI, or command implementations | Typed backend-boundary registry and AST import evaluation |
| Semantic guards | Ignored errors, silent fallback, unsafe lifecycle paths, and wide service propagation | AST guards and priority SSA analysis |
| Complexity ratchets | New code increasing known structural debt | Function, nesting, complexity, production, and test freeze partitions |
| Acceptance evidence | Treating an agent's “done” status as proof | Focused tests, generated-state checks, Git hooks, and change-aware gates |

### History-backed cases

The maintainers report five pre-publication incidents that now have public regression evidence: wrong-worktree LSP scope, missing provider identity, missing persistent-agent runtime truth, silent asynchronous UI failures, and a type-alias bypass in an architecture guard.

Read the incident/evidence boundary and run every retained proof in [Governance in Action](docs/open-source/GOVERNANCE.md).

### Why this is not another agent framework

| Typical agent framework | Super Dolphin Agent |
|---|---|
| Optimizes task execution | Governs how tasks change a real software system |
| Gives agents more tools and context | Gives agents bounded context and allowed dependency directions |
| Treats a completed run as success | Requires tests, diagnostics, generated-state checks, and Git evidence |
| Relies mainly on prompt discipline | Enforces invariants in code, tests, hooks, and generated manifests |
| Hides missing state behind retries or defaults | Fails fast on missing configuration, identity, ownership, or dependencies |

<!-- sd:architecture -->
## Architecture

```text
frontend-app/             React/Vite desktop UI
        |
cmd/agent-terminal/       Wails host and RPC boundary
        |
internal/app/             composition and anti-corruption adapters
        |
internal/contract/        stable ports and DTOs
        |
internal/module/          business capabilities
   |             |
internal/store/   internal/provider/
SQLite/sqlc       Codex and provider runtime integration

cmd/mcp-lsp/              generic multi-language LSP peer
cmd/mcp-orch/             orchestration, DAG, cron, and agent tools
```

The key dependency rule is inward ownership: modules define the ports they need; adapters implement those ports; platform and provider packages must not import upward into business modules. The backend boundary registry is the single source used to generate the architecture rule map.

See [Architecture](docs/open-source/ARCHITECTURE.md) for component responsibilities, data flow, truth sources, and known scope. Use the generated [Code Map](docs/doc/codemap/README.md) for file-level navigation.

### Data integrity across transport and computation

Data is not trusted merely because it crossed a typed boundary. Each boundary validates the invariant it owns: RPC binders reject malformed wire values; typed DTOs and narrow ports constrain shape and ownership; services validate business rules and normalize input before calculation; mapper field guards detect dropped or stale fields; and sqlc plus SQLite constraints protect persistence. Long-running workflows add idempotency keys, leases, claim tokens, and compare-and-swap state transitions so stale workers cannot overwrite the current execution.

Calculations remain fallible even after earlier validation. Schedule, identity, configuration, retry, and state-transition logic return explicit errors instead of silently substituting data. The cron path is a concrete example: JSON-RPC parameters become a typed request, service validation precedes schedule calculation, sqlc persists constrained records, the scheduler claims due work atomically, and turn results are committed only when the run, claim token, and expected state still agree. Tests and guards cover these declared boundaries, but they are bounded evidence rather than a claim that every future business field is automatically proven end to end; new cross-layer fields must extend the corresponding mapper, contract, schema, and regression evidence.

### Current scope

- The desktop application and its repository-specific governance loop are implemented here.
- `make guard` and the related checks govern this repository; they are not advertised as a general scanner for arbitrary repositories.
- The checked-in public-source policy and validation primitives are release-readiness foundations. A complete source-export CLI, sealed receipt workflow, public CI gate, and standalone guard distribution are not yet published capabilities.
- The canonical GitHub URL in this documentation is the publication target. Clone, issue, and private-reporting links become usable only after the repository owner completes the release checklist.
- Codex is required for the current desktop provider flow. Claude is used only by work that explicitly targets its provider integration.

<!-- sd:quick-start -->
## Quick Start

### Prerequisites

- Go 1.26.5
- Node.js matching `^20.19.0 || ^22.13.0 || >=24` and npm
- OpenAI Codex CLI (`codex`), installed and authenticated
- `gopls`
- `typescript-language-server` and TypeScript 5.9.3

The clone command below targets the canonical public repository and will work after publication. Existing maintainers should use their current authorized checkout until then.

```bash
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks

go install golang.org/x/tools/gopls@latest
npm install -g typescript-language-server typescript@5.9.3
( cd frontend-app && npm ci )
```

Run the current desktop development flow:

```bash
# macOS
./run-new-ui-desktop.sh

# Windows PowerShell
.\run-new-ui-desktop.ps1
```

SQLite is created automatically under `SUPER_DOLPHIN_HOME/super-dolphin.db`. Set `SUPER_DOLPHIN_SQLITE_PATH` to use a different local file. PostgreSQL environment variables are not a product-database configuration path.

Build and test:

```bash
make build-plain
make test
make frontend-app-build && go test ./... -count=1
( cd frontend-app && npm run lint && npm test && npm run build )
```

Contributors using linked Git worktrees must build and verify the worktree-local LSP peer before editing. See [Contributing](CONTRIBUTING.md#worktree-and-lsp-readiness) for the exact commands.

<!-- sd:governance-demo -->
## Reproducible Governance Proof

Inspect the gates selected for a known changed file without executing them:

```bash
./scripts/ai_maintenance_gates.sh --print-plan --changed-file README.md
```

Run the repository's core governance checks:

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

These commands validate architecture rules, guard behavior, generated navigation, project-map drift, and the capability manifest. They apply to this repository and fail on stale truth instead of silently refreshing it. Use explicit `*-refresh` targets only when the owning source has intentionally changed.

## Code Quality

| Metric | Value |
|--------|-------|
| Architecture Tests | <!-- BEGIN GENERATED ARCHTEST STATS -->Source AST: 409 runnable `Test*` functions across 141 `_test.go` files in `internal/archtest`<!-- END GENERATED ARCHTEST STATS --> |
| Architecture rules | [Generated backend boundary map](docs/doc/codemap/13-archtest-boundaries.md) |
| Test coverage | Recompute from a current test run; no static percentage is claimed |
| CI | [GitHub Actions](.github/workflows/ci.yml) |

<!-- sd:security -->
## Security

Do not commit credentials, provider homes, local databases, logs, user memory, or machine-specific configuration. Missing identity, ownership, configuration, or dependencies must fail closed rather than silently degrade.

Report vulnerabilities through the private process in [Security Policy](SECURITY.md). Do not put exploit details, secrets, trace payloads, or user data in a public issue.

<!-- sd:community -->
## Community and Contribution

Focused issues and pull requests are welcome. Start with:

- [Contributing Guide](CONTRIBUTING.md)
- [Support](SUPPORT.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Roadmap](docs/open-source/ROADMAP.md)
- [Changelog](CHANGELOG.md)
- [Release Checklist](docs/open-source/RELEASE_CHECKLIST.md)

AI-assisted contributions are welcome, but the contributor remains responsible for the submitted diff, tests, security, licensing, and evidence. A generated answer or a passing agent run is not a substitute for repository gates.

## License

Licensed under [Apache License 2.0](LICENSE). See [NOTICE](NOTICE) for project and third-party attribution guidance.
