# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

> [!IMPORTANT]
> **🤖 100% AI 개발 및 보호**
> 모든 Go 백엔드 로직, React 프론트엔드, AST/SSA 컴파일러 수준의 보안 가드레일, 그리고 이 문서를 포함한 본 저장소 전체는 인간 설계자의 아키텍처 가이드라인에 따라 **오직 AI 에이전트에 의해서만 작성 및 리팩토링**되었습니다. 이는 "자가 보호형 저장소 패턴(Self-Guarding Repo Pattern)"의 실제 실증 사례로 작동합니다.

**AI 네이티브 소프트웨어 거버넌스 및 멀티 에이전트 개발 제어 평면.**

Super Dolphin Agent는 AI 에이전트가 주요 유지보수 인력인 소프트웨어 프로젝트를 위해 설계되었습니다. 멀티 에이전트 세션, 도구 실행, MCP 오케스트레이션, 다국어 LSP, 스케줄링, 메모리, Provider 네이티브 스킬, 실시간 이벤트 스트리밍, 기계적으로 강제되는 엔지니어링 경계를 하나의 데스크톱 제어 평면에 통합합니다.

영문 [README.md](README.md)가 규범적 원본입니다. 제품 의미, 명령, 경로, 환경 변수, 규칙 ID 또는 라이선스 정보가 다를 경우 영문을 기준으로 합니다.

## 구체적으로 무엇을 하는 프로젝트인가요?

Super Dolphin Agent는 챗봇, Cursor 프롬프트 템플릿 또는 단순 API 래퍼가 아닙니다. AI 에이전트가 코드베이스를 망가뜨리지 않고 자율적으로 소프트웨어를 개발하고 유지 관리할 수 있도록 설계된 **로컬 데스크톱 런타임 환경 및 컴파일러 레벨의 소프트웨어 거버넌스 방화벽**입니다.

시스템을 유기적으로 작동하는 세 부분으로 나누어 **"블랙박스 AI 엔트로피(코드 부패)"** 문제를 해결합니다:

1. **로컬 제어 센터(데스크톱 앱)**: Wails 기반의 데스크톱 인터페이스(`cmd/agent-terminal`)를 통해 멀티 에이전트 세션을 실행 및 모니터링하고, 도구 실행 체인을 관찰하고, 자연어로 크론 자동화 작업을 예약하고, SQLite 기반 벡터 메모리를 관리하며, AI 작업 공간 로그를 실시간으로 스트리밍합니다.
2. **코드 인텔리전스 엔진(LSP & MCP 사이드카)**:
   - **LSP 사이드카 (`cmd/mcp-lsp`)**: 저장소를 인덱싱하여 불안정한 텍스트 검색 대신 정확하고 구조화된 코드 정의, 참조 및 타입 계층 구조를 AI에 제공하는 범용 다국어 언어 서버 프로토콜 사이드카.
   - **오케스트레이션 사이드카 (`cmd/mcp-orch`)**: 모델 컨텍스트 프로토콜(MCP)을 조율하고 도구 실행 DAG를 관리하여, AI가 임의의 bash 스크립트 대신 안전하고 구조화된 인터페이스를 통해서만 파일을 읽고 쓸 수 있도록 강제합니다.
3. **면역 시스템(AST/SSA 단위 테스트)**: Go 테스트 제품군(`internal/archtest`)에 직접 내장되어 변경된 코드를 Go 컴파일러 레벨의 정적 단일 할당(SSA) 중간 표현으로 컴파일하여 데이터 흐름 분석을 수행합니다. 이를 통해 오류 삼키기, 데드락 유발, 양파 아키텍처 경계 위반 등 흔한 AI 안티패턴을 Git에 커밋되기 전에 원천 차단합니다.

### 운영 환경 수준의 참조 모델 (코드 부패 방지 학습)

Super Dolphin Agent는 실제 운영 환경에 적합한 생산 레벨의 멀티 에이전트 오케스트레이션 시스템입니다. 실제 프로덕션 워크로드를 위해 설계되었지만, 개발자가 다음을 학습할 수 있는 고표준 참조 저장소 역할도 수행합니다:

1. **분위기 프로그래밍(Vibe Coding)의 고통 해결 (면역 소프트웨어 공학)**: AI로 인한 코드 엔트로피(코드 부패)로부터 코드베이스를 보호할 수 있는 생산 검증된 완벽한 청사진을 제공합니다. 표준 Go 단위 테스트 내부에서 AST 규칙을 작성하고, SSA 호출 그래프를 구축하고, 자동으로 수축하는 품질 래칫을 실행하는 방법을 보여줍니다.
2. **운영 환경 수준의 멀티 에이전트 아키텍처**: 코드베이스 자체는 멀티 에이전트 제어 평면의 깔끔하고 의존성이 주입된(`fx` 프레임워크), 계약 우선(`internal/contract`) 설계 구현체입니다. 다음 기술 방향에 대한 명확한 참조 패턴을 포함하고 있습니다:
   - 병렬로 작동하는 에이전트 워커 고루틴의 시작, 중지 및 복구.
   - stdio MCP 사이드카 프로세스 실행 및 생 JSON-RPC를 타입 안전한 Go 구조체로 변환.
   - 프로젝트 로컬 JSONL 데이터베이스 내 세션 기록 유지 관리.
   - SQLite 기반 로컬 벡터 메모리 검색 구현.

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

### 깨끗한 AI 폐루프: 프로젝트 전체 "풀 스캔" 불필요

기존 개발 프로세스에서는 개발자가 AI 에이전트의 컨텍스트 창에 전체 프로젝트의 코드베이스를 쑤셔 넣어야만 한다고 느끼기 쉽습니다. 하지만 이는 토큰 소비를 폭증시키고, 에이전트의 주의를 분산시키며, AI 환각(Hallucination)을 증가시키는 주범이 됩니다.

Super Dolphin의 자가 보호 아키텍처는 "최소한의 지식(Zero-Knowledge)" 원칙 아래 동작하는 **매우 깨끗하고 국소적인 코드 변경 루프**를 제공합니다:
*   **좁은 컨텍스트만 사용**: 저장소 내 강제된 인터페이스 계약, 명확한 경계 규칙, 자동으로 업데이트되는 프로젝트 맵 덕분에 AI 에이전트는 타겟 파일과 직접 연결된 계약 인터페이스 정보만 로드하면 충분합니다.
*   **저장소 자체의 가이드 및 오류 정정**: AI가 아키텍처 규칙을 위반하거나 기술 부채를 도입하려고 하면, AST/SSA 정적 가드가 즉시 이를 차단하고 정확한 컴파일러 레벨의 진단(Diagnostics) 피드백을 제공합니다.
*   **자동 자가 치유(Self-Healing)**: 에이전트는 컴파일러의 진단 오류를 읽고 그 자리에서 코드를 스스로 수정하여 다시 제출을 시도합니다.

이로 인해 **AI는 안전한 운영 환경 수준의 변경을 수행하기 위해 프로젝트 전체를 통독할 필요가 없습니다**. 코드베이스 자체가 결정론적인 조율자 역할을 합니다.


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
