# Super Dolphin Agent

[English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**AI 네이티브 소프트웨어 거버넌스 및 멀티 에이전트 개발 제어 평면.**

Super Dolphin Agent는 AI 에이전트가 주요 유지보수 인력인 소프트웨어 프로젝트를 위해 설계되었습니다. 멀티 에이전트 세션, 도구 실행, MCP 오케스트레이션, 다국어 LSP, 스케줄링, 메모리, Provider 네이티브 스킬, 실시간 이벤트 스트리밍, 기계적으로 강제되는 엔지니어링 경계를 하나의 데스크톱 제어 평면에 통합합니다.

영문 [README.md](README.md)가 규범적 원본입니다. 제품 의미, 명령, 경로, 환경 변수, 규칙 ID 또는 라이선스 정보가 다를 경우 영문을 기준으로 합니다.

<!-- sd:why -->
## AI 유지보수를 전제로 한 설계

“AI 유지보수”는 검토 없는 변경이나 AI가 저장소 전체를 한 번에 이해해야 한다는 뜻이 아닙니다. AI가 주요 구현 인력으로 일하고, 저장소가 변경을 작고 감사 가능하게 유지하는 탐색 정보, 제약, 증거를 제공한다는 뜻입니다. 제품 목표, 고위험 의사결정, 자격 증명, 릴리스 책임은 사람이 유지합니다.

유지보수 루프는 제한된 컨텍스트를 중심으로 동작합니다.

1. 생성된 code map과 파일 단위 AI project map으로 변경 영역을 찾습니다.
2. capability contract와 명시적 아키텍처 규약으로 공개 동작을 이해합니다.
3. LSP 정의, 참조, call hierarchy, diagnostics로 작은 범위만 변경합니다.
4. AST, SSA, 의존성 방향, 복잡도 예산, fail-fast 정책으로 변경을 제한합니다.
5. 집중 테스트, 생성물 검사, 변경 감지 gate로 결과를 증명한 뒤 commit 또는 push합니다.

사람이나 AI가 코드베이스 전체를 계속 기억해야 한다는 취약한 전제를 제거합니다.

### 프로젝트의 기원: V2의 구조적 엔트로피에서 Super Dolphin으로

Super Dolphin Agent는 2026년 3월 19일 `go-agent-v2`에서 새로 시작하는 마이그레이션으로 출발했습니다. V2는 이미 에이전트 세션, 도구, Provider, 이벤트, 복구, 데스크톱 경험이라는 제품 가치를 증명했습니다. 문제는 기능 부족이 아니라, 성공적인 지역 기능 추가가 누적될수록 전체 시스템을 이해하기 어려워졌다는 점이었습니다.

V2에는 80개가 넘는 수동 RPC method가 있었고 binding, validation, capability check, logging, error mapping, 여러 registration path가 흩어져 있었습니다. lifecycle의 진실은 여러 manager file, 저장 상태 위의 유효 상태, 암시적 보조 상태 머신, 비동기 복구 부작용으로 나뉘었습니다. 중앙 event handler는 557줄까지 커졌고 bus에는 수십 개의 message/topic 상수가 쌓였으며 수동 application assembly는 200줄을 넘었습니다. 코드는 실행됐지만 “권위 있는 동작은 어디에 있는가?”라는 질문에 답하기가 점점 어려워졌습니다.

이 프로젝트가 말하는 **소프트웨어 부패**는 개발자를 비난하거나 시스템이 즉시 고장났다는 뜻이 아닙니다. 지역 기능은 동작하지만 contract, ownership, 변경 경계가 암묵 지식이 되는 상태입니다. 빠른 AI 반복은 그럴듯한 지역 patch마다 숨은 경로를 추가해 이 문제를 증폭합니다.

초기 V3 결정은 약 83,000줄의 기존 시스템에서 제자리 수술을 하지 않는 것이었습니다. 기존 코드는 행동 증거로 유지하고 기능은 함수 단위로 명시적 contract에 옮겼습니다.

| V2의 구조적 엔트로피 | Super Dolphin의 대응 |
|---|---|
| 수동 RPC와 분산된 횡단 로직 | typed request, 통합 contract, 명시적 middleware와 error semantics |
| 분산된 lifecycle 전이와 부작용 | 선언적 state transition, 타입 안전 event, owner가 분명한 runner |
| 수동 `New()` / `Close()` object graph | `fx` composition과 명시적 시작/종료 ownership |
| 비즈니스 모듈과 storage/adapter 결합 | onion boundary, Module-owned Port, anti-corruption adapter |
| 추상화 수준이 섞인 거대 함수 | composed method와 `80 / 4 / 10` 길이·중첩·복잡도 예산 |
| reviewer 기억에 의존하는 규약 | AST/SSA guard, map, manifest, hook, 재현 가능한 증거 |

따라서 V2는 숨길 역사가 아니라 Super Dolphin 거버넌스가 계속 방어하는 실패 모델입니다.

### 엔지니어링 부패 방지와 AI Code Rot

AI는 코드를 빠르게 만들지만 경계가 없으면 아키텍처 드리프트도 빠르게 키웁니다. Super Dolphin은 이를 **AI Code Rot**로 보고, 유입 지점 가까이에서 기계가 볼 수 있는 실패로 변환합니다.

| 부패 방지 계층 | 방지 대상 | 저장소의 근거 |
|---|---|---|
| 탐색의 규범적 진실 | 잘못된 하위 시스템 수정, 오래된 멘탈 모델 | `docs/doc/codemap`, project map, capability manifest |
| 아키텍처 경계 | Module이 Store, Provider, UI, Command 구현에 직접 의존 | 타입 기반 경계 레지스트리와 AST import 평가 |
| 의미적 guard | 오류 무시, silent fallback, 위험한 lifecycle, 과도하게 넓은 seam | AST guard와 priority SSA 분석 |
| 복잡도 예산 | 비즈니스, 인프라, 프로토콜, 영속화가 섞인 거대 함수 | 유효 함수 길이 `<= 80`, 중첩 `<= 4`, 순환 복잡도 `<= 10` |
| 부채 래칫 | 기존 부채 악화 또는 baseline 재생성으로 부채 은폐 | production/test freeze가 회귀를 거부하고 개선 시 축소 |
| 재현 가능한 gate | map, test, 생성물, 증거 없이 완료 주장 | pre-commit, pre-push, 변경 감지 AI maintenance gates |

80줄은 모든 시스템에 적용할 교리가 아닙니다. 이 저장소의 오케스트레이션 중심 작업에서는 같은 추상화 수준을 유지하는 composed method가 거대한 절차형 함수보다 안전하기 때문입니다. 핵심 규칙은 **정책을 보이게 하고, 세부 구현을 좁은 interface 뒤에 두며, 예외를 명시적이고 측정 가능하게 만드는 것**입니다.

### 일반 Agent Framework와 다른 점

| 일반 Agent Framework | Super Dolphin Agent |
|---|---|
| 작업 실행 최적화 | 작업이 실제 소프트웨어를 어떻게 바꾸는지 통제 |
| 더 많은 도구와 컨텍스트 제공 | 제한된 컨텍스트, capability contract, 허용된 의존 방향 제공 |
| run 종료를 성공으로 간주 | test, diagnostics, 생성 상태, Git 증거 요구 |
| prompt 규율에 주로 의존 | code, test, hook, manifest에서 불변식 강제 |
| retry 또는 default로 장애 은폐 | 설정, 상태, 의존성이 잘못되면 fail-fast |

```text
intent
  -> code map + capability contract
  -> LSP/MCP를 통한 제한된 AI 변경
  -> AST/SSA/architecture guards
  -> focused tests + generated artifact checks
  -> reviewable evidence
  -> accepted commit
```

<!-- sd:architecture -->
## 아키텍처 개요

```text
cmd/                 데스크톱 진입점, MCP 오케스트레이션, 다국어 LSP sidecar
frontend-app/        현재 React/Vite 데스크톱 UI
internal/contract/   모듈 간 interface와 DTO
internal/module/     Turn, Prompt, Cron, Memory, Skill 비즈니스 로직
internal/platform/   DB, RPC, 설정, 런타임 안전성
internal/provider/   Codex, Claude CLI 등 Provider adapter
internal/store/      sqlc 기반 영속성 adapter와 수동 wrapper
pkg/                 재사용 가능한 공개 라이브러리
```

핵심 비즈니스 계층은 내부 contract만 의존합니다. Store는 도메인과 SQL 구현 사이의 anti-corruption layer이며 Provider, MCP, UI는 바깥쪽에 위치합니다. `cmd/*`와 composition root가 명시적으로 조립합니다. 자세한 내용은 [code map](docs/doc/codemap/README.md)과 [onion architecture contract](docs/%E5%A5%91%E7%BA%A6/onion-architecture-convention.md)를 참조하세요.

## 핵심 기능

- 멀티 에이전트 session, resume, fork, schedule, 실시간 이벤트 스트리밍.
- MCP 오케스트레이션 sidecar와 범용 다국어 LSP peer.
- Cron, Memory, Prompt, Thread, Provider 네이티브 스킬.
- Codex 및 Claude CLI Provider adapter와 통합 contract.
- SQLite 영속성, Wails 데스크톱 호스트, React/Vite UI.
- code map, project map, capability contract, Archtest, AI maintenance gates.

<!-- sd:quick-start -->
## 빠른 시작

요구 사항: Go 1.25.7, Node.js 20+, 인증된 OpenAI Codex CLI, `gopls`, `typescript-language-server`, `typescript@5.9.3`. Claude Code CLI는 Claude Provider를 사용할 때만 필요합니다.

```bash
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks
( cd frontend-app && npm install )
./run-new-ui-desktop.sh
```

Windows PowerShell:

```powershell
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks
cd frontend-app; npm install; cd ..
.\run-new-ui-desktop.ps1
```

SQLite는 기본적으로 `SUPER_DOLPHIN_HOME/super-dolphin.db`를 사용하며 `SUPER_DOLPHIN_SQLITE_PATH`로 다른 로컬 파일을 지정할 수 있습니다. 규범적 스킬 위치는 `<workspace>/.agents/skills/`와 `~/.super-dolphin/skills/personal/{user,agent,imported}/`입니다.

<!-- sd:governance-demo -->
## 재현 가능한 거버넌스 증명

변경에 선택될 gate를 확인합니다.

```bash
./scripts/ai_maintenance_gates.sh --print-plan --base HEAD
```

핵심 부패 방지 검사를 실행합니다.

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

전체 검증:

```bash
make test
( cd frontend-app && npm run lint && npm test && npm run build )
```

검사 명령은 읽기 전용이며 규범적 생성물이 오래되면 실패합니다. 생성 진실을 의도적으로 바꿀 때만 해당 `*-refresh` target을 사용하세요.

<!-- sd:security -->
## 보안

- 자격 증명, Provider home, 로컬 DB, log, user memory, 장치별 설정을 commit하지 마세요.
- 설정 누락과 의존성 장애는 [fail-fast contract](docs/%E5%A5%91%E7%BA%A6/fail-fast-convention.md)를 따르며 silent fallback은 결함입니다.
- 공개 소스 exporter는 commit된 Git object만 읽고 default-deny policy로 내부 계획, archive, run evidence, local workspace, untracked file을 제외합니다.
- 민감한 취약점은 저장소 소유자에게 비공개로 보고하고 공개 Issue에 exploit, secret, user data를 포함하지 마세요.

<!-- sd:community -->
## 커뮤니티 및 기여

Issue와 범위가 명확한 Pull Request를 환영합니다. 변경을 작고 검증 가능하게 유지하고, 모듈 경계를 지키며, 버그 수정과 같은 commit에 regression test를 추가하고, 변경 영역에 맞는 gate를 실행하세요. 아키텍처 결정은 prompt뿐 아니라 contract와 실행 가능한 guard로 표현해야 합니다.

- [Code map](docs/doc/codemap/README.md)
- [Architecture contracts](docs/%E5%A5%91%E7%BA%A6/README.md)
- [Project Agent instructions](AGENTS.md)
- [Apache License 2.0](LICENSE)

## 라이선스

[Apache License 2.0](LICENSE)에 따라 제공됩니다. 저작권 고지는 [NOTICE](NOTICE)를 참조하세요.
