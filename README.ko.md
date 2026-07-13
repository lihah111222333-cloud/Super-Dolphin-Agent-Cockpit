# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**AI가 작성하는 소프트웨어를 위한 자가 보호형 저장소.** AI 에이전트가 변경을 구현하고, 저장소가 소유한 map, contract, test, gate가 그 변경을 안전하게 유지할 수 있는지 결정합니다.

> [!IMPORTANT]
> **Maintainer 선언: 독창적 코드와 프로젝트 자체 문서는 100% AI가 작성하며, 인간이 방향을 정하고 저장소가 보호합니다.** Product code, test code, 프로젝트 자체 문서는 모두 AI 에이전트가 작성하거나 리팩토링합니다. Product intent, architecture decision, credential, release의 책임은 인간에게 있습니다. AI가 작성했다는 사실이 무결성을 뜻하지는 않습니다. 수용되는 모든 변경은 저장소가 소유한 evidence와 gate를 통과해야 합니다. 외부에서 가져온 법률 및 커뮤니티 표준 문서는 원래의 저작자 표시를 유지합니다.

Super Dolphin Agent는 **production-grade AI-native vibe-coding engineering system 및 multi-agent development control plane**입니다. 로컬 데스크톱 runtime, MCP orchestration, 다국어 LSP navigation, Provider integration, 영속 workflow, 기계적으로 강제되는 engineering boundary를 하나의 동작하는 참조 구현에 통합합니다.

영문 [README.md](README.md)가 규범적 개요입니다. 번역본은 동일한 product scope, command, path, environment variable, repository identity, license를 유지합니다. 자세한 사실은 [Architecture](docs/open-source/ARCHITECTURE.md), [Governance in Action](docs/open-source/GOVERNANCE.md), 생성된 [Code Map](docs/doc/codemap/README.md)을 참조하세요.

<!-- sd:why -->
## Super Dolphin이 필요한 이유

대부분의 Agent framework는 task execution을 최적화합니다. Super Dolphin은 여기서 더 나아가 완료된 task가 장기간 유지되는 software system을 어떻게 변경할 수 있는지 통제합니다.

유지보수 루프는 여섯 단계로 구성됩니다.

1. **Orient**: 생성된 code map과 capability contract로 대상 영역을 찾습니다.
2. **Understand**: LSP를 통해 definition, reference, call hierarchy, diagnostics를 확인합니다.
3. **Change**: ownership이 명확한 좁은 영역만 변경합니다.
4. **Constrain**: AST/SSA rule, dependency boundary, complexity budget, fail-fast contract로 diff를 제한합니다.
5. **Prove**: focused test, generated-artifact check, change-aware gate로 결과를 증명합니다.

6. **Learn**: 검증된 수정에서 근본 원인을 추출하고 invariant로 일반화한 뒤, 반복되는 패턴을 regression evidence 또는 실행 가능한 guard로 승격합니다.

### Vibe coding을 위한 guardrail

AI는 사람이 작성하는 것보다 수십 배에서 수백 배 빠르게 코드를 생성할 수 있으므로 병목은 코드 생산에서 test와 신뢰할 수 있는 delivery로 이동합니다. 같은 결함 패턴이 다른 위치에 남아 있거나 AI 생성 코드에 다시 나타날 수 있다면, 한 사례만 수정한 것은 완료가 아닙니다.

Super Dolphin은 test 또는 실제 사용에서 잘못된 것으로 입증된 bug fix evidence를 주기적으로 통합해 재사용 가능한 engineering knowledge로 만듭니다. 안정된 패턴은 저장소가 소유한 test, fixture, AST/SSA rule, dependency policy 또는 기타 실행 가능한 gate로 승격됩니다. AI가 알려진 bad smell을 다시 생성하면 gate가 변경을 거부하고 delivery 전에 수정을 강제합니다.

Skill과 prompt는 생성을 안내하지만 guard는 무엇을 수용할 수 있는지 강제합니다. 후보 guard에는 재현 가능한 evidence, 일반화 가능한 invariant, 결정적인 acceptance check가 필요합니다. 이는 통제되지 않은 자기 수정이 아니라 evidence-driven ratchet입니다. 현재 저장소는 자동 memory consolidation과 광범위한 guard infrastructure를 구현하고 있지만, 모든 수정을 새로운 실행 가능한 guard로 완전 자동 end-to-end 승격하는 것은 완전한 적용을 주장하는 기능이 아니라 계속 발전시키는 engineering direction입니다.

이것이 AI-native vibe coding이 나아갈 방향입니다. 인간은 intent, architecture, acceptance boundary를 정의하고 AI는 그 specification 안에서만 코드를 생성합니다. 저장소는 결함에서 학습하고 engineering baseline을 계속 강화하여, 같은 종류의 bug를 사람이 반복해서 찾아 처리하지 않아도 더 견고하고 명확해집니다.

### Agent 자율성을 넘어선 production-grade vibe coding

[Hermes Agent](https://github.com/NousResearch/hermes-agent)와 [OpenClaw](https://github.com/openclaw/openclaw) 같은 유명 프로젝트는 자율 실행, 폭넓은 tool 사용, persistent memory, 재사용 가능한 Skill의 가치를 보여 줍니다. Hermes는 경험에서 Skill을 만들고 개선하는 learning loop를 강조하며, OpenClaw는 OS, messaging platform, service 전반에서 작업하는 personal AI assistant를 강조합니다.

Agent system의 capability는 고정되어 있지 않습니다. Community는 Skill, MCP Server, Provider, tool, plugin, workflow를 계속 추가하여 Hermes Agent, OpenClaw 또는 현재의 다른 선도 Agent와 동등하거나 더 강한 기능을 만들 수 있습니다. 따라서 Super Dolphin은 기능 수가 가장 많거나 특정 시점의 단일 capability가 가장 강한 것을 장기적 우위로 정의하지 않습니다.

이 유명 프로젝트들은 Agent-first vibe coding의 고유한 한계도 보여 줍니다. 더 강한 autonomy, 더 많은 tool, persistent memory, 자기 개선 Skill만으로는 repository evolution이 통제 가능해지지 않습니다. AI Agent가 대규모 codebase를 계속 수정할 때 누가 architecture를 지키고, 중요한 property를 test하며, 이미 잘못된 것으로 입증된 pattern의 재발을 막고, 어떤 code를 남길 수 있는지 결정할 것인지라는 문제가 그대로 남습니다.

Code가 사람보다 수십 배에서 수백 배 빠르게 생성되어도 사람의 review capacity는 같은 비율로 확장될 수 없습니다. Repository가 강제하는 specification, contract, regression test, 실행 가능한 gate가 없다면 전문 engineering team조차 점차 code에 대한 control을 잃습니다. 국소 기능은 계속 동작할 수 있지만 중복 path, lifecycle 모호성, hidden coupling, 검증되지 않은 assumption이 누적되어 system을 이해하고 test하고 delivery하고 유지하는 일이 계속 어려워집니다.

Super Dolphin의 우위는 **sustainable iteration**입니다. Repository 자체를 control system으로 취급하여 community가 추가하는 새로운 capability를 흡수하면서도 codebase가 빠르게 유지 불가능한 architecture로 변하는 것을 막습니다. Specification이 intent를 정의하고, typed contract와 dependency boundary가 구현을 제한하며, test와 regression fixture가 입증된 behavior를 보존하고, AST/SSA guard와 change-aware gate가 알려진 bad smell을 거부합니다. 기능은 계속 성장할 수 있지만 repository의 executable specification을 만족하는 code만 수용됩니다.

### 제한된 컨텍스트를 이용한 유지보수

이 저장소는 일반적인 변경을 위해 전체 코드베이스를 하나의 model context에 넣지 않아도 되도록 설계되었습니다. 생성된 navigation, 좁은 contract, 결정적 failure signal은 에이전트가 관련 영역을 찾고 위반을 빠르게 수정하도록 돕습니다.

모든 변경이 국소적이라는 보장은 아닙니다. 여러 영역을 가로지르는 작업에는 더 넓은 reference 및 impact analysis가 필요하며, 수용되는 모든 변경에는 관련 test와 review evidence가 계속 요구됩니다.

### 개발 과정: Super Dolphin이 만들어진 이유

Super Dolphin은 연속적인 engineering evolution의 세 번째 주요 단계입니다.

1. **첫 번째 단계**는 Python command-line multi-agent tool이었습니다. Model이 task를 분할하고 tool을 통해 협업하며 실제 engineering work를 완료할 수 있는지 검증했습니다.
2. **`go-agent-v2`는 이 프로젝트의 직접적인 전신입니다.** 내부 task dispatch tool에서 자동화된 퀀트 트레이딩 workflow, multi-agent desktop control, Provider integration, persistent execution을 통합한 실제 동작하는 engineering system으로 발전했습니다. 실제 업무에서 product direction의 가치를 증명했으며, 폐기를 전제로 한 prototype이 아니었습니다.
3. **Super Dolphin / V3는 2026년 3월 19일 시작**된 새로운 architecture generation입니다. 전신에서 검증한 capability와 운영 lesson을 이어받으면서 장기적인 AI-driven development에 필요한 기반을 다시 구축했습니다.

V3가 필요해진 이유는 전신이 동작하지 않았기 때문이 아닙니다. 전신은 실제로 동작하며 기능을 계속 확장했습니다. 그러나 AI가 국소 변경을 생성하는 속도가 convention과 사람의 review에 의존하는 architecture가 안전하게 흡수할 수 있는 속도를 넘어섰습니다. 개별 path를 test로 증명하더라도 system 전체의 ownership, lifecycle, dependency direction, 가독성은 계속 약해질 수 있었습니다. 유지관리자의 공개 전 기록에서 이 압력은 다음과 같이 나타났습니다.

- 80개가 넘는 RPC method에 병렬 binding, validation, capability, logging 경로가 누적되었습니다.
- lifecycle ownership이 여러 manager와 비동기 side effect로 분산되었습니다.
- 중앙 event handler가 557줄까지 늘어났습니다.
- 수동 application assembly가 200줄을 넘었습니다.

따라서 V3는 단순한 feature upgrade가 아닙니다. Reviewer의 기억과 prompt에 있던 architecture knowledge를 저장소가 소유한 contract, code map, typed boundary, regression evidence, 실행 가능한 gate로 옮깁니다. 해결하려는 failure mode가 바로 **AI code rot**, 즉 국소 변경은 계속 동작하지만 global contract, ownership boundary, 가독성이 약해지는 상태입니다.

전신의 비공개 개발 이력은 공개 evidence가 아니라 maintainer가 제공한 context입니다. 따라서 공개 저장소는 그 lesson에서 만들어진 architecture response, guard, regression fixture, 재현 가능한 command를 제공합니다.

| 전신에서 관찰된 engineering pressure | Super Dolphin의 대응 |
|---|---|
| 병렬로 존재하는 수동 RPC path | typed request, 단일 contract surface, 명시적 middleware와 error semantics |
| 분산된 lifecycle side effect | 선언적 transition, typed event, owner가 명확한 lifecycle runner |
| 수동 object graph | `fx` composition과 명확한 startup / shutdown ownership |
| business code와 adapter의 결합 | onion boundary, module-owned port, anti-corruption adapter |
| 추상화 수준이 뒤섞인 거대 함수 | 이 저장소에 특화된 `80 / 4 / 10` 함수 길이, nesting, complexity budget |
| reviewer의 기억을 policy로 사용 | AST/SSA guard, 생성 map, manifest, hook, 재현 가능한 evidence |

`80 / 4 / 10` budget은 보편적인 style rule이 아닙니다. 오케스트레이션 비중이 높은 이 저장소에서 단계적으로 강화되는 제약입니다. 기본 유효 함수 길이는 `<= 80`, nesting은 `<= 4`, cyclomatic complexity는 `<= 10`입니다.

### 저장소가 강제하는 것

| Layer | 방지 대상 | 저장소 evidence |
|---|---|---|
| Navigation truth | 잘못된 subsystem 수정 또는 오래된 project knowledge 사용 | `docs/doc/codemap`, project map, capability manifest |
| Architecture boundary | Domain code가 Store, Provider, UI, Command 구현으로 넘어가는 것 | typed backend-boundary registry와 AST import evaluation |
| Semantic guard | error 무시, silent fallback, 안전하지 않은 lifecycle path, wide service propagation | AST guard와 priority SSA analysis |
| Complexity ratchet | 새 code가 알려진 구조적 부채를 늘리는 것 | function, nesting, complexity, production/test freeze partition |
| Acceptance evidence | Agent의 “done” 상태를 proof로 취급하는 것 | focused test, generated-state check, Git hook, change-aware gate |

### 기록에 근거한 사례

유지관리자는 현재 공개 regression evidence가 존재하는 다섯 건의 공개 전 incident를 기록했습니다. 잘못된 worktree의 LSP scope, Provider identity 누락, persistent Agent의 runtime truth 누락, 비동기 UI failure의 묵살, architecture guard의 type alias bypass입니다.

[Governance in Action](docs/open-source/GOVERNANCE.md)에서 incident와 evidence의 경계를 확인하고, 보존된 모든 proof를 실행해 보세요.

### 또 하나의 Agent framework가 아닌 이유

| 일반적인 Agent framework | Super Dolphin Agent |
|---|---|
| task execution 최적화 | task가 실제 software system을 어떻게 변경하는지 통제 |
| Agent에 더 많은 tool과 context 제공 | Agent에 bounded context와 허용된 dependency direction 제공 |
| run 완료를 성공으로 간주 | test, diagnostics, generated-state check, Git evidence 요구 |
| 주로 prompt discipline에 의존 | code, test, hook, 생성 manifest에서 invariant 강제 |
| retry 또는 default로 state 누락 은폐 | configuration, identity, ownership, dependency 누락 시 fail-fast |

<!-- sd:architecture -->
## 아키텍처

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

핵심 dependency rule은 inward ownership입니다. Module은 자신에게 필요한 port를 정의하고 adapter는 그 port를 구현합니다. Platform과 Provider package는 상위의 business module을 import해서는 안 됩니다. Backend boundary registry는 architecture rule map을 생성하는 단일 진실 공급원입니다.

Component 책임, data flow, 진실 공급원, 알려진 범위는 [Architecture](docs/open-source/ARCHITECTURE.md)를 참조하세요. File 수준 navigation에는 생성된 [Code Map](docs/doc/codemap/README.md)을 사용하세요.

### 현재 범위

- Desktop application과 이 저장소에 특화된 governance loop는 여기에 구현되어 있습니다.
- `make guard` 및 관련 check는 이 저장소를 통제합니다. 임의의 저장소에 사용할 수 있는 범용 scanner로 홍보하지 않습니다.
- Check-in된 public-source policy와 validation primitive는 release-readiness 기반입니다. 완전한 source-export CLI, sealed receipt workflow, public CI gate, standalone guard distribution은 아직 공개된 기능이 아닙니다.
- 이 문서의 canonical GitHub URL은 공개 대상입니다. Clone, Issue, private reporting 링크는 repository owner가 release checklist를 완료한 후에 사용할 수 있습니다.
- 현재 desktop Provider flow에는 Codex가 필요합니다. Claude는 해당 Provider integration을 명시적으로 대상으로 하는 작업에서만 사용됩니다.

<!-- sd:quick-start -->
## 빠른 시작

### 사전 요구 사항

- Go 1.25.7
- Node.js 20+ 및 npm
- 설치 및 인증을 완료한 OpenAI Codex CLI(`codex`)
- `gopls`
- `typescript-language-server` 및 TypeScript 5.9.3

아래 clone command는 canonical public repository를 대상으로 하며 공개 이후에 사용할 수 있습니다. 그전까지 기존 maintainer는 현재 승인된 checkout을 사용해야 합니다.

```bash
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks

go install golang.org/x/tools/gopls@latest
npm install -g typescript-language-server typescript@5.9.3
( cd frontend-app && npm ci )
```

현재 desktop development flow를 실행합니다.

```bash
# macOS
./run-new-ui-desktop.sh

# Windows PowerShell
.\run-new-ui-desktop.ps1
```

SQLite는 `SUPER_DOLPHIN_HOME/super-dolphin.db`에 자동 생성됩니다. `SUPER_DOLPHIN_SQLITE_PATH`를 설정하면 다른 local file을 사용할 수 있습니다. PostgreSQL environment variable은 product database 설정 경로가 아닙니다.

Build 및 test:

```bash
make build-plain
make test
make frontend-app-build && go test ./... -count=1
( cd frontend-app && npm run lint && npm test && npm run build )
```

Linked Git worktree를 사용하는 contributor는 편집하기 전에 worktree-local LSP peer를 build하고 verify해야 합니다. 정확한 command는 [Contributing](CONTRIBUTING.md#worktree-and-lsp-readiness)을 참조하세요.

<!-- sd:governance-demo -->
## 재현 가능한 거버넌스 증명

명시한 변경 파일에 선택되는 gate를 실행하지 않고 확인합니다.

```bash
./scripts/ai_maintenance_gates.sh --print-plan --changed-file README.md
```

이 저장소의 핵심 governance check를 실행합니다.

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

이 command는 architecture rule, guard behavior, 생성 navigation, project map drift, capability manifest를 검증합니다. 적용 대상은 이 저장소이며, 오래된 진실 공급원을 자동으로 갱신하지 않고 실패합니다. 소유 진실 공급원을 의도적으로 변경한 경우에만 명시적인 `*-refresh` target을 사용하세요.

## 코드 품질

| Metric | 현재 진실 공급원 |
|---|---|
| Architecture test | <!-- BEGIN GENERATED ARCHTEST STATS -->Source AST: 329 runnable `Test*` functions across 127 `_test.go` files in `internal/archtest`<!-- END GENERATED ARCHTEST STATS --> |
| Architecture rule | [생성된 backend boundary map](docs/doc/codemap/13-archtest-boundaries.md) |
| Test coverage | 현재 test run에서 다시 계산하며 고정 percentage를 주장하지 않음 |
| CI | [GitHub Actions](.github/workflows/ci.yml) |

<!-- sd:security -->
## 보안

Credential, Provider home, local database, log, user memory, machine-specific configuration을 commit하지 마세요. Identity, ownership, configuration, dependency가 누락되면 조용히 성능을 낮추지 말고 fail-closed해야 합니다.

취약점은 [Security Policy](SECURITY.md)의 비공개 절차를 통해 신고하세요. 공개 Issue에 exploit detail, secret, trace payload, user data를 포함하지 마세요.

<!-- sd:community -->
## 커뮤니티 및 기여

범위가 명확한 Issue와 Pull Request를 환영합니다. 다음 문서부터 확인하세요.

- [Contributing Guide](CONTRIBUTING.md)
- [Support](SUPPORT.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Roadmap](docs/open-source/ROADMAP.md)
- [Changelog](CHANGELOG.md)
- [Release Checklist](docs/open-source/RELEASE_CHECKLIST.md)

AI-assisted contribution을 환영하지만 제출한 diff, test, security, licensing, evidence에 대한 책임은 contributor에게 있습니다. 생성된 답변이나 성공한 Agent run은 repository gate를 대신할 수 없습니다.

## 라이선스

[Apache License 2.0](LICENSE)에 따라 제공됩니다. Project 및 third-party attribution 지침은 [NOTICE](NOTICE)를 참조하세요.
