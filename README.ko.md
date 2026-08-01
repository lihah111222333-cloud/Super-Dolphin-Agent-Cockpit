# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**AI가 작성하는 소프트웨어를 위한 자가 보호형 저장소.** AI 에이전트가 변경을 구현하고, 저장소가 소유한 map, contract, test, gate가 그 변경을 안전하게 유지할 수 있는지 결정합니다.

> [!IMPORTANT]
> **Maintainer 선언: 독창적 코드와 프로젝트 자체 문서는 100% AI가 작성하며, 인간이 방향을 정하고 저장소가 보호합니다.** Product code, test code, 프로젝트 자체 문서는 모두 AI 에이전트가 작성하거나 리팩토링합니다. Product intent, architecture decision, credential, release의 책임은 인간에게 있습니다. AI가 작성했다는 사실이 무결성을 뜻하지는 않습니다. 수용되는 모든 변경은 저장소가 소유한 evidence와 gate를 통과해야 합니다. 외부에서 가져온 법률 및 커뮤니티 표준 문서는 원래의 저작자 표시를 유지합니다.

**Local-first delivery enforcement.** 일상적인 commit과 push의 수용 여부는 version control에 포함된 [Git hooks](.githooks/README.md)가 강제하며, 유료 GitHub-hosted CI에 의존하지 않습니다. `pre-commit`은 staged snapshot, AI maintenance rule, 전체 repository guard, 영향받는 code를 검사합니다. `commit-msg`는 fix commit에 regression evidence를 요구하고, `pre-push`는 현재 `HEAD`의 push range, 영향받는 package와 contract, Go package nilness, 등록된 concurrent surface의 Race test를 검사합니다. Deferred Provider E2E, `gosec`/security scan, release check는 별도의 명시적 gate로 유지합니다.

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

Agent system의 capability는 고정되어 있지 않습니다. 이 프로젝트는 한 사람이 시작했고 주로 AI가 작성했으므로 한 maintainer의 시간, 경험, use case에는 한계가 있으며 Community의 공동 참여가 필요합니다. Contributor는 module, integration, UI, Skill, MCP, Provider, tool을 PR로 직접 제출할 수 있고 구현을 작성하지 않고 target scenario, specification, acceptance test, 실제 defect를 제공할 수도 있습니다. 이 engineering system은 Community code와 AI-generated code에 동일한 강한 제약을 적용하고 아직 구현되지 않은 요구를 AI engineering task로 바꾸어 architecture, contract, gate에 맞는 완전한 module을 생성하거나 수정하도록 강제합니다. Community collaboration과 AI 생성 속도를 함께 확대하여 한 maintainer가 모든 capability를 직접 작성하지 않고도 Hermes Agent와 OpenClaw를 빠르게 따라잡거나 넘어서는 방식입니다.

Hermes Agent와 OpenClaw는 autonomous execution, 폭넓은 tool integration, rapid iteration이 어디까지 도달할 수 있는지 보여 줍니다. 동시에 공통 과제도 분명하게 합니다. Feature, channel, runtime environment가 계속 확장되면 test와 국소 guard만으로 repository를 계속 이해하고 진화시키기 어렵습니다. 지나치게 큰 core module, 분산된 responsibility, 추적하기 어려운 impact path는 개별 feature가 계속 동작하더라도 system 전체의 maintenance cost를 계속 높일 수 있습니다.

이것이 Super Dolphin이 해결하도록 설계된 문제입니다. Code를 AI만 생산하든 AI와 뛰어난 human engineer가 함께 빠르게 생산하든, 지속 가능한 속도에는 repository가 강제하는 specification, contract, regression test, 실행 가능한 gate가 필요합니다. 이를 통해 architecture, 입증된 behavior, product intent를 일관되게 유지합니다.

Code가 사람보다 수십 배에서 수백 배 빠르게 생성되어도 사람의 review capacity는 같은 비율로 확장될 수 없습니다. Repository가 강제하는 specification, contract, regression test, 실행 가능한 gate가 없다면 전문 engineering team조차 점차 code에 대한 control을 잃습니다. 국소 기능은 계속 동작할 수 있지만 중복 path, lifecycle 모호성, hidden coupling, 검증되지 않은 assumption이 누적되어 system을 이해하고 test하고 delivery하고 유지하는 일이 계속 어려워집니다.

Super Dolphin의 우위는 **sustainable iteration**입니다. Repository 자체를 control system으로 취급하여 community가 추가하는 새로운 capability를 흡수하면서도 codebase가 빠르게 유지 불가능한 architecture로 변하는 것을 막습니다. Specification이 intent를 정의하고, typed contract와 dependency boundary가 구현을 제한하며, test와 regression fixture가 입증된 behavior를 보존하고, AST/SSA guard와 change-aware gate가 알려진 bad smell을 거부합니다. 기능은 계속 성장할 수 있지만 repository의 executable specification을 만족하는 code만 수용됩니다.

### 선도 Agent와 동등해지고 넘어서는 capability route

AI가 빠르게 code를 생성하는 시대에는 capability code 자체가 희소 자원이 아닙니다. 희소한 것은 Community의 요구와 기여를 적합한 module로 안정적으로 바꾸는 engineering constraint입니다. Super Dolphin은 다음 route를 사용합니다.

1. **Community가 추격할 실제 scenario를 정의한다.** Hermes Agent나 OpenClaw가 해결했거나 아직 해결하지 못한 workflow, 기대 결과, failure case를 제공합니다.
2. **Scenario를 executable specification으로 만든다.** Code를 생성하거나 제출하기 전에 module ownership, typed contract, dependency direction, security boundary, acceptance test, delivery evidence를 정의합니다.
3. **Community PR과 AI generation의 두 경로로 구현한다.** Contributor는 완전한 module이나 제한된 integration을 PR할 수 있고, AI는 code map과 LSP로 backend, integration, 필요한 UI, test, documentation을 구현할 수 있습니다. 어느 쪽도 architecture를 우회할 수 없습니다.
4. **동일한 hard gate로 모든 code를 적합하게 만든다.** Build, test, E2E scenario, permission 및 lifecycle check, AST/SSA guard, dependency boundary, change-aware gate가 Community code와 AI-generated code를 동일하게 검사하고 부적합하면 Contributor 또는 AI에 수정을 강제합니다.
5. **실제 task로 parity를 증명한다.** “한 번 답했다”는 parity가 아닙니다. Target workflow와 failure path를 재현 가능하게 완료해야 validated capability가 됩니다.
6. **Community 사용 경험을 다음 생성 제약으로 바꾼다.** Production failure, regression, 반복 fix를 fixture, specification, 실행 가능한 guard로 만들어 이후 제출되거나 생성된 module이 기존 defect를 피하게 합니다.

이것은 한 maintainer가 모든 기능을 직접 작성하는 route가 아닙니다. Community는 code, 문제, evidence를 기여할 수 있고 이 engineering system은 Community code를 제약하며 AI가 module을 보완하고 수정하게 합니다. 제한된 유지보수 역량을 Community collaboration과 AI engineering throughput으로 확대해 유지 가능성을 지키면서 Hermes Agent와 OpenClaw를 빠르게 따라잡거나 넘어서는 것을 목표로 합니다.

### 제한된 컨텍스트를 이용한 유지보수

이 프로젝트의 성격상 codebase 전체를 통독할 필요가 없습니다. Engineer와 AI 모두 작업을 시작하기 전에 repository 전체를 이해할 필요가 없습니다. Target capability와 acceptance outcome에서 출발해 code map, module ownership, 좁은 contract, deterministic gate로 task에 필요한 context만 얻고, 그 제약 안에서 실행하며 위반을 수정하면 원하는 capability를 전달할 수 있습니다.

모든 변경이 국소적이라는 보장은 아닙니다. 여러 영역을 가로지르는 작업에는 reference와 impact analysis 범위를 넓혀야 하지만 필요한 context를 넓히는 것은 repository 전체를 통독하는 것과 다릅니다. 수용되는 모든 변경에는 관련 test와 review evidence가 계속 요구됩니다.

이 프로젝트의 architecture standard에서 변경 impact를 알기 위해 codebase 전체를 통독해야 하는 구현은 지속적인 AI maintenance에 적합하지 않으며 system 전체 지식이 없는 사람 engineer에게도 유지보수 불가능합니다. 이는 대개 module ownership, dependency direction, contract, guard가 impact surface를 명시하지 못했다는 뜻입니다. Engineering constraint는 한 expert가 system 전체를 기억하는 데 의존하지 않고 dependency와 consequence를 navigation, check, fail-fast가 가능한 machine signal로 바꿔야 합니다.

### AI-first 유지보수와 engineer가 소유하는 business semantics

이 repository는 AI가 제약 아래에서 code를 찾고 이해하고 수정하고 검증하기 쉽도록 의도적으로 구성됩니다. 명시적 contract, 좁은 module, 작은 function, 생성된 map, machine-readable boundary는 사람이 file을 순서대로 읽을 때 더 장황하거나 분절되어 보일 수 있습니다. 따라서 사람의 선형적 가독성만이 유일한 optimization target은 아닙니다. 다만 중복이나 불명확한 code를 허용한다는 뜻은 아니며, 추가 boundary는 navigation, 영향 격리 또는 deterministic verification을 개선해야 합니다.

Function이 작고 symbol이 많다는 이유만으로 문제로 취급하지 않습니다. AI는 긴 file을 서사처럼 읽는 대신 definition, reference, call hierarchy, code map, test를 통해 system을 이해합니다. Contributor는 raw source만으로 완전한 mental model을 만들기보다 AI assistant와 repository navigation tool을 함께 사용해 읽는 것이 권장됩니다.

따라서 전통적인 engineering 방식처럼 file을 하나씩 읽고 call chain을 사람이 직접 추적하는 경험만으로 이 repository를 평가해서는 안 됩니다. 더 적절한 방법은 AI가 code map, LSP, contract, test, gate를 사용해 실제 locate–understand–impact analysis–change–verify cycle을 완료하게 한 뒤 repository의 읽기 편의성, 수정 편의성, 유지보수 비용을 평가하는 것입니다.

AI는 명확히 지정된 design을 구현할 수 있지만 business semantics를 스스로 소유하거나 결정할 수 없습니다. 어떤 문제를 해결할지, feature가 무엇을 의미하는지, module이 어떻게 동작해야 하는지, 최종 user-visible outcome이 무엇인지, 어떤 tradeoff를 허용할지는 code generation이 아니라 방향 결정의 문제입니다.

사람이 의사결정을 내리고 언제나 steering wheel을 잡습니다. 해결할 문제, business semantics, 의도한 user-visible outcome, 허용할 tradeoff를 결정합니다. AI가 code를 작성한 뒤에도 사람은 feature가 의도한 need를 실제로 충족하는지 검증해야 합니다. 이 책임은 agent에게 넘길 수 없습니다.

Code production이 빨라진다고 test가 무료가 되거나 기하급수적으로 저렴해지지는 않습니다. 검증할 change, 조합, business-risk surface가 더 많아지므로 code를 더 빨리 작성할수록 test와 acceptance의 부담은 커집니다. 이 architecture는 machine이 검출하거나 예방할 수 있는 problem의 약 90%를 contract, static guard, test, gate로 다룹니다. 남은 약 10%는 사람이 검증합니다. requirement가 올바르게 표현됐는지, 실제 사용에서 behavior가 맞는지, product가 여전히 올바른 방향으로 가는지입니다.

따라서 engineer는 여전히 이 프로젝트의 중심입니다. Engineer가 product direction, business semantics, module responsibility, architecture, acceptance criteria, risk boundary를 정의하고 AI는 repository gate 아래에서 이를 code, test, documentation, 반복 가능한 maintenance work로 변환합니다. 목표는 engineer를 제거하는 것이 아니라 모든 작은 function을 직접 읽고 작성하는 일에서 의미, evidence, system evolution을 통제하는 일로 집중을 옮기는 것입니다. **Engineer는 언제나 steering wheel을 잡습니다. AI는 engineering throughput을 높이지만 business direction이나 delivery result에 대한 판단을 대체하지는 못합니다.**

### AI 유지보수성 독립 평가

2026년 7월 13일, GPT-5.6 medium-thinking model을 사용한 3개 독립 Agent는 이 repository의 순수 AI code-maintenance capability를 **95/100(A+)**로 종합 평가했으며, 기존 score 열람이 금지된 별도 sub-agent도 blind assessment에서 **95.6/100(A+)**를 재현했습니다. Code map, LSP navigation, 좁은 contract, architecture guard, deterministic verification loop를 통해 AI는 codebase 전체를 통독하지 않고도 위치 파악, impact analysis, 구현, 검증을 수행할 수 있으며, 사람은 product direction, business semantics, acceptance decision, project documentation을 담당합니다.

**재현 prompt:** 순수 AI maintenance 관점에서 이 repository를 100점 만점으로 평가하되 사람은 direction, business semantics, acceptance, documentation만 담당하고 AI는 design document 실행과 code location, impact analysis, 구현, test, diagnostics, delivery를 담당하며 token cost는 무시하고 UI, release, commercial maturity는 평가하지 말고 README의 기존 score를 읽지 않은 채 현재 evidence, 항목별 score, 100점이 아닌 이유를 제시하십시오.

### 개발 과정: Super Dolphin이 만들어진 이유

Super Dolphin은 연속적인 engineering evolution의 세 번째 주요 단계입니다. 첫 단계 V1, 직접적인 전신 `go-agent-v2`(V2), 현재 V3까지 이 lineage는 Agent feature를 계속 추가하는 과정만이 아닙니다. 모든 변경이 bounded context 안에서 impact를 드러내고 명시적 constraint에 따라 실행되며 AI나 사람이 codebase 전체를 통독하고 기억하지 않아도 machine verification될 수 있게 하는 동일한 engineering pain point를 반복해서 해결해 왔습니다.

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

### 데이터 전달과 계산의 무결성

데이터는 typed boundary를 통과했다는 이유만으로 신뢰되지 않습니다. 각 boundary는 자신이 소유한 invariant를 검증합니다. RPC binder는 잘못된 wire value를 거부하고, typed DTO와 narrow port는 데이터 형태와 ownership을 제한하며, service는 계산 전에 business rule을 검증하고 입력을 정규화합니다. Mapper field guard는 누락되거나 오래된 field를 탐지하고, sqlc와 SQLite constraint는 persistence를 보호합니다. 장시간 실행되는 workflow는 idempotency key, lease, claim token, CAS state transition도 사용해 오래된 worker가 현재 실행을 덮어쓰지 못하게 합니다.

앞 단계에서 검증했더라도 계산은 명시적으로 실패할 수 있습니다. Schedule, identity, configuration, retry, state transition 로직은 데이터를 조용히 대체하지 않고 error를 반환합니다. Cron path가 구체적인 예입니다. JSON-RPC parameter를 typed request로 변환하고, service validation 후에 schedule을 계산하며, sqlc가 제약된 record를 저장하고, scheduler가 만기 work를 원자적으로 claim한 뒤, run·claim token·expected state가 여전히 일치할 때만 turn result를 commit합니다. Test와 guard는 선언된 boundary를 검증하지만 이는 범위가 정해진 evidence이며, 미래의 모든 business field가 자동으로 end-to-end 증명된다는 뜻은 아닙니다. 새로운 cross-layer field에는 해당 mapper, contract, schema, regression evidence의 갱신이 필요합니다.

### 현재 범위

- Desktop application과 이 저장소에 특화된 governance loop는 여기에 구현되어 있습니다.
- `make guard` 및 관련 check는 이 저장소를 통제합니다. 임의의 저장소에 사용할 수 있는 범용 scanner로 홍보하지 않습니다.
- Check-in된 public-source policy와 validation primitive는 release-readiness 기반입니다. 완전한 source-export CLI, sealed receipt workflow, public CI gate, standalone guard distribution은 아직 공개된 기능이 아닙니다.
- 이 문서의 canonical GitHub URL은 공개 대상입니다. Clone, Issue, private reporting 링크는 repository owner가 release checklist를 완료한 후에 사용할 수 있습니다.
- 현재 desktop Provider flow에는 Codex가 필요합니다. Claude는 해당 Provider integration을 명시적으로 대상으로 하는 작업에서만 사용됩니다.

<!-- sd:quick-start -->
## 빠른 시작

### 사전 요구 사항

- Go 1.26.5
- Node.js `^20.19.0 || ^22.13.0 || >=24` 및 npm
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
