# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**AI が書くソフトウェアのための自己防御型リポジトリ。** AI エージェントが変更を実装し、リポジトリが所有する map、contract、test、gate が、その変更を残せるほど安全かを判断します。

> [!IMPORTANT]
> **メンテナー宣言：オリジナルコードとプロジェクト固有ドキュメントは 100% AI が記述し、人間が方向を定め、リポジトリが守ります。** Product code、test code、プロジェクト固有ドキュメントは、すべて AI エージェントが記述またはリファクタリングしています。Product intent、architecture decision、credential、release の責任は人間が負います。AI が作者であることは無謬性を意味しません。受け入れられるすべての変更には、リポジトリが所有する evidence と gate が引き続き必要です。上流由来の法的文書およびコミュニティ文書は、元の帰属表示を維持します。

**Truth-image の delivery 検証。** version 管理された [Git hooks](.githooks/README.md)、手動の `make ci-l0` と `make ci-l1`、および release 検証は二つの責務を分離します。`pre-commit` は exact staged tree に対して repository code guard だけを実行し、remote CI を開始しません。`pre-push`、手動 L0-L1 command、release 検証は exact pushed tree または commit に対して fail-closed の remote ECI gate を実行します。GitHub は Git remote にすぎず、CI runner は提供しません。remote configuration、provenance、result authority、cleanup evidence の欠落、または push/release gate の失敗は delivery を拒否します。`commit-msg` は引き続き中国語の commit text と fix-test evidence を要求します。

Super Dolphin Agent は、**production-grade で AI-native な vibe-coding engineering system と multi-agent development control plane**です。ローカルデスクトップ runtime、MCP orchestration、多言語 LSP navigation、Provider integration、永続 workflow、機械的に強制される engineering boundary を、一つの動作する参照実装に統合します。

英語版 [README.md](README.md) が正規の概要です。翻訳版は、同じ product scope、command、path、environment variable、repository identity、license を維持します。詳細な事実は [Architecture](docs/open-source/ARCHITECTURE.md)、[Governance in Action](docs/open-source/GOVERNANCE.md)、生成された [Code Map](docs/doc/codemap/README.md) を参照してください。

<!-- sd:why -->
## Super Dolphin が必要な理由

多くの Agent framework は task execution を最適化します。Super Dolphin はそれに加えて、完了した task が長期運用される software system にどのような変更を加えてよいかを統制します。

保守ループは次の 6 段階で構成されます。

1. **Orient**：生成された code map と capability contract で対象を特定します。
2. **Understand**：LSP を通じて definition、reference、call hierarchy、diagnostics を確認します。
3. **Change**：ownership が明確な狭い範囲だけを変更します。
4. **Constrain**：AST/SSA rule、dependency boundary、complexity budget、fail-fast contract で diff を制約します。
5. **Prove**：focused test、generated-artifact check、change-aware gate で結果を証明します。

6. **Learn**：実証済みの修正から根本原因を抽出し、不変条件として一般化し、繰り返すパターンを regression evidence または実行可能な guard へ昇格させます。

### Vibe coding に guardrail を与える

AI は人間の数十倍から数百倍の速度でコードを生成できるため、ボトルネックはコード生成から test と信頼できる delivery へ移ります。同じ欠陥パターンが別の場所に残ったり、AI 生成コードに再び現れたりするなら、一件を直しただけでは完了ではありません。

Super Dolphin は、test または実利用で問題だと証明された bug fix evidence を定期的に統合し、再利用可能な engineering knowledge にします。安定したパターンは、リポジトリ所有の test、fixture、AST/SSA rule、dependency policy、その他の実行可能な gate に昇格されます。AI が既知の bad smell を再生成すると、gate が変更を拒否し、delivery の前に修正を強制します。

Skill と prompt は生成を導けますが、guard は何を受け入れられるかを強制します。候補となる guard には、再現可能な evidence、一般化可能な invariant、決定的な acceptance check が必要です。これは無制限な自己変更ではなく、evidence-driven な ratchet です。現在のリポジトリは自動 memory consolidation と広範な guard infrastructure を実装していますが、すべての修正を新しい実行可能な guard へ完全自動で end-to-end 昇格させることは、継続中の engineering direction であり、完全な網羅を主張するものではありません。

これは AI-native vibe coding が進む方向です。人間が intent、architecture、acceptance boundary を定義し、AI はその specification 内でのみコードを生成します。リポジトリは欠陥から学び、同種の bug を人手で何度も発見・処理することに頼らず、より堅牢で明快になっていきます。

### Agent の自律性だけではない production-grade vibe coding

[Hermes Agent](https://github.com/NousResearch/hermes-agent) や [OpenClaw](https://github.com/openclaw/openclaw) のような著名なプロジェクトは、自律実行、幅広い tool 利用、persistent memory、再利用可能な Skill の価値を示しています。Hermes は経験から Skill を作成・改善する learning loop を重視し、OpenClaw は OS、messaging platform、service を横断して動作する personal AI assistant を重視します。

Agent system の capability は固定ではありません。本プロジェクトは一人で立ち上げ、主に AI によって記述されているため、一人の maintainer の時間、経験、use case には限界があり、Community との共同開発が必要です。Contributor は module、integration、UI、Skill、MCP、Provider、tool の PR を直接提出でき、実装を書かずに target scenario、specification、acceptance test、実際の defect を提供することもできます。本 engineering system は Community code と AI-generated code に同じ強い制約を適用し、未実装の要求を AI の engineering task に変換して、architecture、contract、gate に適合する完全な module の生成または修正を強制します。これにより Community collaboration と AI の生成速度を同時に活かし、一人の maintainer が全 capability を手書きせずに Hermes Agent や OpenClaw に迅速に追いつき、超えることを目指せます。

Hermes Agent と OpenClaw は、autonomous execution、幅広い tool integration、rapid iteration がどこまで到達できるかを示しています。同時に、共通の課題も明らかにします。Feature、channel、runtime environment が拡大し続けると、test と局所的な guard だけでは repository の理解しやすさや進化可能性を維持できません。大きすぎる core module、分散した responsibility、追跡しにくい impact path により、個別 feature は動き続けても、system 全体の maintenance cost は上がり続けます。

これが Super Dolphin が解決する問題です。Code を AI だけで生産する場合でも、AI と優秀な human engineer が共同で高速に生産する場合でも、持続可能な速度には repository が強制する specification、contract、regression test、実行可能な gate が必要です。それらにより architecture、実証済みの behavior、product intent を一貫させます。

Code が人間の数十倍から数百倍の速度で生成されても、人間の review capacity は同じ比率では増やせません。Repository が強制する specification、contract、regression test、実行可能な gate がなければ、専門的な engineering team でさえ徐々に code の control を失います。局所機能は動き続けても、重複 path、lifecycle の曖昧さ、hidden coupling、未検証の assumption が蓄積し、system は理解、test、delivery、保守のすべてで難しくなります。

Super Dolphin の優位性は **sustainable iteration** です。Repository 自体を control system として扱うことで、community が追加する新しい capability を吸収しながら、codebase が急速に保守不能な architecture へ変わることを防ぎます。Specification が intent を定義し、typed contract と dependency boundary が実装を制限し、test と regression fixture が実証済みの behavior を保持し、AST/SSA guard と change-aware gate が既知の bad smell を拒否します。機能は増え続けられますが、repository の executable specification を満たす code だけが受け入れられます。

### 先進的な Agent に追いつき、超えるための capability route

AI が高速に code を生成する時代では、capability code 自体は希少資源ではありません。希少なのは、Community の要求と貢献を適合した module へ安定して変換する engineering constraint です。Super Dolphin は次の route を使います。

1. **Community が追うべき実 scenario を定義する。** Hermes Agent や OpenClaw が解決済み、または未解決の workflow、期待結果、failure case を提供します。
2. **Scenario を executable specification にする。** Code の生成または提出前に module ownership、typed contract、dependency direction、security boundary、acceptance test、delivery evidence を定義します。
3. **Community PR と AI generation の二つの経路で実装する。** Contributor は完全な module や限定的な integration を PR でき、AI は code map と LSP を使って backend、integration、必要な UI、test、documentation を実装できます。どちらも architecture を迂回できません。
4. **同じ hard gate ですべての code を適合させる。** Build、test、E2E scenario、permission と lifecycle check、AST/SSA guard、dependency boundary、change-aware gate が Community code と AI-generated code を同じように検査し、不適合なら Contributor または AI に修正を強制します。
5. **実 task で parity を証明する。** 「一度回答できた」は parity ではありません。Target workflow と failure path を再現可能に完了して初めて validated capability になります。
6. **Community の利用経験を次の生成制約へ変える。** Production failure、regression、反復 fix を fixture、specification、実行可能な guard にし、後続の提出・生成 module が既知の defect を避けるようにします。

これは一人の maintainer が全機能を手書きする route ではありません。Community は code、問題、evidence を貢献でき、本 engineering system は Community code を制約し、AI に module の補完と修正を実行させます。限られた保守能力を Community collaboration と AI engineering throughput へ拡大し、保守可能性を守りながら Hermes Agent や OpenClaw に迅速に追いつき、超えることを目指します。

### 境界づけられたコンテキストでの保守

このプロジェクトの性質上、codebase 全体を通読する必要はありません。Engineer も AI も、作業前に repository 全体を理解する必要はありません。Target capability と acceptance outcome から始め、code map、module ownership、狭い contract、deterministic gate を使って task に必要な context だけを取得し、その制約内で実行して違反を修正すれば、意図した capability を届けられます。

すべての変更が局所的になるという保証ではありません。複数領域にまたがる作業では reference と impact analysis の範囲を広げる必要がありますが、必要な context を広げることは repository 全体を通読することと同じではありません。受け入れられる変更には、対応する test と review evidence が引き続き求められます。

本プロジェクトの architecture standard では、変更の impact を知るために codebase 全体を通読しなければならない実装は、継続的な AI maintenance に適さず、system 全体の知識を持たない人間の engineer にとっても保守不能です。これは通常、module ownership、dependency direction、contract、guard が impact surface を明示できていないことを意味します。Engineering constraint は、一人の expert が system 全体を記憶することに依存せず、dependency と consequence を navigation、check、fail-fast が可能な machine signal に変える必要があります。

### AI-first maintenance と engineer が担う business semantics

この repository は、AI が制約の下で code を特定、理解、変更、検証しやすいよう意図的に構成されています。明示的な contract、狭い module、小さな function、生成された map、machine-readable boundary は、人が file を順番に読むと冗長または断片的に感じる場合があります。そのため、人間の線形な読みやすさだけが optimization target ではありません。ただし、重複や不明瞭な code を許すものではなく、追加 boundary は navigation、影響範囲の隔離、deterministic verification のいずれかを改善する必要があります。

Function が小さく symbol が多いことだけを問題とはみなしません。AI は長い file を物語として読むのではなく、definition、reference、call hierarchy、code map、test を通じて system を理解します。Contributor には、raw source だけで完全な mental model を作るのではなく、AI assistant と repository の navigation tool を組み合わせて読むことを推奨します。

したがって、従来の engineering のように file を一つずつ読み、call chain を人手で追う方法だけでこの repository を評価すべきではありません。より適切なのは、AI に code map、LSP、contract、test、gate を使わせ、実際の locate–understand–impact analysis–change–verify cycle を完了させたうえで、読みやすさ、変更しやすさ、保守コストを評価する方法です。

AI は明確に指定された design を実装できますが、business semantics を自ら所有・裁定することはできません。何を解決するのか、feature が何を意味するのか、module がどう動くべきか、最終的な user-visible outcome は何か、どの tradeoff を受け入れるかは、code generation ではなく方向を決める問題です。

人間が意思決定し、常に steering wheel を握ります。解くべき問題、business semantics、意図する user-visible outcome、受け入れられる tradeoff を決めます。AI が code を書いた後も、人間は feature が意図した need を実際に満たすかを検証しなければなりません。この責任を agent に委ねることはできません。

Code production が速くなっても、test が無料になったり指数関数的に安くなったりはしません。検証すべき change、組合せ、business-risk surface が増えるため、code を速く書くほど test と acceptance の負荷は上がります。本 architecture は、machine が検出または予防できる problem の約 90% を contract、static guard、test、gate の対象に置いています。残る約 10% は人間が検証します。requirement が正しく表現されたか、実際の利用で behavior が正しいか、product が正しい方向に進んでいるかです。

したがって engineer は本プロジェクトの中心に残ります。Engineer が product direction、business semantics、module responsibility、architecture、acceptance criteria、risk boundary を定義し、AI が repository gate の下でそれらを code、test、documentation、反復可能な maintenance work に変換します。目的は engineer を排除することではなく、すべての小さな function を手で読み書きする作業から、意味、evidence、system evolution の統制へ集中を移すことです。**Engineer は常に steering wheel を握ります。AI は engineering throughput を高めますが、business direction や delivery result の判断を置き換えることはできません。**

### AI 保守性の独立評価

2026 年 7 月 13 日、GPT-5.6 medium-thinking model を使用した 3 つの独立 Agent は、本 repository の純粋な AI code-maintenance capability を **95/100（A+）** と総合評価し、既存 score の参照を禁止された別の sub-agent も blind assessment で **95.6/100（A+）** を再現しました。Code map、LSP navigation、狭い contract、architecture guard、deterministic verification loop により、AI は codebase 全体を通読せずに特定、impact analysis、実装、検証を行え、人間は product direction、business semantics、acceptance decision、project documentation を担います。

**再現 prompt：** 純粋な AI maintenance の視点から本 repository を 100 点満点で評価し、人間は direction、business semantics、acceptance、documentation のみを担い、AI は design document の実行と code location、impact analysis、実装、test、diagnostics、delivery を担い、token cost は無視し、UI、release、commercial maturity は評価せず、README の既存 score を読まず、現在の evidence、分項 score、100 点ではない理由を示してください。

### 開発の歩み：なぜ Super Dolphin が生まれたのか

Super Dolphin は、連続する engineering evolution の第 3 の主要段階です。第 1 段階の V1、直接の前身 `go-agent-v2`（V2）、現在の V3 まで、この lineage は Agent feature を増やすだけのものではありません。すべての変更が bounded context 内で impact を示し、明示的な constraint の下で実行され、AI も人間も codebase 全体を通読・記憶せずに machine verification できるようにする、同じ engineering pain point を反復的に解決してきました。

1. **第 1 段階**は Python command-line multi-agent tool でした。Model が task を分割し、tool を通じて協調し、実際の engineering work を完了できることを検証しました。
2. **`go-agent-v2` はこのプロジェクトの直接の前身です。** 内部向け task dispatch tool から、クオンツ取引の自動 workflow、multi-agent desktop control、Provider integration、persistent execution を統合した実用的な engineering system へ発展しました。実際の業務で product direction の価値を証明したものであり、破棄を前提とした prototype ではありません。
3. **Super Dolphin / V3 は 2026 年 3 月 19 日に開始**され、新しい architecture generation となりました。前身で検証された capability と運用上の lesson を継承しながら、長期的な AI-driven development に必要な基盤を再構築しています。

V3 が必要になった理由は、前身が動かなかったからではありません。前身は動作し、機能を増やし続けました。しかし、AI が局所的な変更を生成する速度が、convention と人手の review に依存する architecture の安全な吸収速度を上回りました。個別の path を test で証明できても、system 全体の ownership、lifecycle、dependency direction、可読性は劣化し続けます。保守担当者の公開前記録では、その圧力が次の形で現れました。

- 80 を超える RPC method に、並行した binding、validation、capability、logging の経路が蓄積した。
- lifecycle ownership が複数の manager と非同期 side effect に分散した。
- 中央の event handler が 557 行に達した。
- 手動の application assembly が 200 行を超えた。

したがって V3 は単なる feature upgrade ではありません。Reviewer の記憶や prompt に置かれていた architecture knowledge を、リポジトリ所有の contract、code map、typed boundary、regression evidence、実行可能な gate へ移します。対象とする failure mode が **AI code rot**、つまり局所的な変更は動作していても global contract、ownership boundary、可読性が劣化する状態です。

前身の非公開な開発履歴は、公開 evidence ではなく maintainer 提供の context です。そのため公開リポジトリは、そこから得た lesson に基づく architecture response、guard、regression fixture、再現可能な command を提示します。

| 前身で観測された engineering pressure | Super Dolphin の対応 |
|---|---|
| 並行する手書き RPC path | typed request、単一の contract surface、明示的な middleware と error semantics |
| 分散した lifecycle side effect | 宣言的 transition、typed event、owner が明確な lifecycle runner |
| 手動の object graph | `fx` composition と明確な startup / shutdown ownership |
| business code と adapter の結合 | onion boundary、module-owned port、anti-corruption adapter |
| 抽象度が混在する巨大関数 | このリポジトリ固有の `80 / 4 / 10` による関数長、nesting、complexity budget |
| reviewer の記憶を policy とする運用 | AST/SSA guard、生成 map、manifest、hook、再現可能な evidence |

`80 / 4 / 10` budget は普遍的な style rule ではありません。オーケストレーション負荷の高いこのリポジトリ向けに段階的に厳格化される制約です。既定の実効関数長は `<= 80`、nesting は `<= 4`、cyclomatic complexity は `<= 10` です。

### リポジトリが強制するもの

| Layer | 防ぐもの | リポジトリ上の evidence |
|---|---|---|
| Navigation truth | 誤った subsystem の編集、古い project knowledge の使用 | `docs/doc/codemap`、project map、capability manifest |
| Architecture boundary | Domain code から Store、Provider、UI、Command 実装への越境 | typed backend-boundary registry と AST import evaluation |
| Semantic guard | error の無視、silent fallback、安全でない lifecycle path、wide service propagation | AST guard と priority SSA analysis |
| Complexity ratchet | 新しい code による既知の構造的負債の増加 | function、nesting、complexity、production/test freeze partition |
| Acceptance evidence | Agent の「done」状態を proof とみなすこと | focused test、generated-state check、Git hook、change-aware gate |

### 履歴に基づく事例

保守担当者は、現在では公開 regression evidence を持つ 5 件の公開前 incident を記録しています。誤った worktree に対する LSP scope、Provider identity の欠落、persistent Agent の runtime truth 欠落、非同期 UI failure の黙殺、architecture guard の type alias bypass です。

incident と evidence の境界を [Governance in Action](docs/open-source/GOVERNANCE.md) で確認し、保持されているすべての proof を実行してください。

### 一般的な Agent framework との違い

| 一般的な Agent framework | Super Dolphin Agent |
|---|---|
| task execution を最適化 | task が実際の software system をどう変更するかを統制 |
| Agent により多くの tool と context を提供 | Agent に bounded context と許可された dependency direction を提供 |
| run の完了を成功とみなす | test、diagnostics、generated-state check、Git evidence を要求 |
| 主に prompt discipline に依存 | code、test、hook、生成 manifest で invariant を強制 |
| retry や default で state 欠落を隠す | configuration、identity、ownership、dependency の欠落時に fail-fast |

<!-- sd:architecture -->
## アーキテクチャ

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

主要な依存ルールは inward ownership です。Module が必要な port を定義し、adapter がその port を実装します。Platform package と Provider package は、上位にある business module を import してはいけません。Backend boundary registry は architecture rule map を生成する単一の真実源です。

Component の責務、data flow、真実源、既知の範囲は [Architecture](docs/open-source/ARCHITECTURE.md) を参照してください。File 単位の navigation には、生成された [Code Map](docs/doc/codemap/README.md) を使用してください。

### データ伝達と計算の整合性

型付き境界を通過しただけでデータが信頼されることはありません。各境界は、自身が所有する不変条件を検証します。RPC binder は不正な wire value を拒否し、型付き DTO と narrow port はデータ形状と ownership を制約し、service は計算前に business rule を検証して入力を正規化します。Mapper の field guard は欠落または古い field を検出し、sqlc と SQLite constraint は永続化を保護します。長時間実行される workflow では、idempotency key、lease、claim token、CAS state transition も使用し、古い worker が現在の実行を上書きできないようにします。

前段で検証済みでも、計算は明示的に失敗できる設計です。Schedule、identity、configuration、retry、state transition のロジックは、データを暗黙に置き換えず error を返します。Cron path が具体例です。JSON-RPC parameter を型付き request に変換し、service validation の後に schedule を計算し、sqlc が制約された record を保存し、scheduler が期限到来 work を原子的に claim し、run、claim token、expected state が一致する場合にだけ turn result を commit します。Test と guard は宣言済みの境界を検証しますが、これは範囲のある evidence であり、将来のすべての business field が自動的に end-to-end 証明されるという意味ではありません。新しい cross-layer field には、対応する mapper、contract、schema、regression evidence の更新が必要です。

### 現在の範囲

- Desktop application と、このリポジトリ固有の governance loop は実装済みです。
- `make guard` と関連 check が統制するのはこのリポジトリです。任意のリポジトリに使える汎用 scanner としては宣伝していません。
- Check-in 済みの public-source policy と validation primitive は release-readiness の基盤です。完全な source-export CLI、sealed receipt workflow、public CI gate、standalone guard distribution は、まだ公開済みの機能ではありません。
- この文書の canonical GitHub URL は公開先です。Clone、Issue、private reporting のリンクは、repository owner が release checklist を完了した後に利用可能になります。
- 現在の desktop Provider flow には Codex が必要です。Claude は、その Provider integration を明示的に対象とする作業でのみ使用します。

<!-- sd:quick-start -->
## クイックスタート

### 前提条件

- Go 1.26.5
- Node.js `^20.19.0 || ^22.13.0 || >=24` と npm
- インストールおよび認証済みの OpenAI Codex CLI（`codex`）
- `gopls`
- `typescript-language-server` と TypeScript 5.9.3

以下の clone command は canonical public repository を対象とし、公開後に利用できます。それまでは、既存の maintainer は現在の authorized checkout を使用してください。

```bash
git clone https://github.com/lihah111222333-cloud/super-dolphin-agent.git
cd super-dolphin-agent
make install-hooks

go install golang.org/x/tools/gopls@latest
npm install -g typescript-language-server typescript@5.9.3
( cd frontend-app && npm ci )
```

現在の desktop development flow を起動します。

```bash
# macOS
./run-new-ui-desktop.sh

# Windows PowerShell
.\run-new-ui-desktop.ps1
```

単独で起動する `mcp-lsp` stdio server は fail-fast です。各 client の server 設定に次の起動 contract が必要です。

```text
SUPER_DOLPHIN_RUNTIME_MODE=dev
SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=<runtime-resource-root>
SUPER_DOLPHIN_DEPENDENCY_PROFILE=production
```

source build の runtime resource root は source checkout root、cross-platform `bin/LSP` package では artifact resource root です。target file から推測しません。Windows の platform semantics は実際に起動した binary から自動選択されます。native PowerShell は `mcp-lsp-windows-*.exe` と `G:/develop/project` 形式、WSL は `mcp-lsp-linux-*` と `/mnt/g/develop/project` 形式を使用し、両方を混在させないでください。三つの client の例と troubleshooting は追跡対象の[standalone client guide](docs/reference/mcp-lsp-standalone-clients.md)にあり、cross-platform binary package の `bin/LSP/README.md` と config skill にも同じ contract を収録します。

SQLite は `SUPER_DOLPHIN_HOME/super-dolphin.db` に自動作成されます。`SUPER_DOLPHIN_SQLITE_PATH` を設定すると別の local file を使用できます。PostgreSQL environment variable は product database の設定経路ではありません。

Build と test：

```bash
make build-plain
make test
make frontend-app-build && go test ./... -count=1
( cd frontend-app && npm run lint && npm test && npm run build )
```

Linked Git worktree を使う contributor は、編集前に worktree-local LSP peer を build・verify する必要があります。正確な command は [Contributing](CONTRIBUTING.md#worktree-and-lsp-readiness) を参照してください。

<!-- sd:governance-demo -->
## 再現可能なガバナンス証明

明示した変更ファイルに選択される gate を、実行せずに確認します。

```bash
./scripts/ai_maintenance_gates.sh --print-plan --changed-file README.md
```

このリポジトリの主要な governance check を実行します。

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

これらの command は、architecture rule、guard behavior、生成 navigation、project map drift、capability manifest を検証します。適用対象はこのリポジトリであり、古い真実源を暗黙に更新せず失敗します。所有する真実源を意図的に変更した場合に限り、明示的な `*-refresh` target を使用してください。

## コード品質

| Metric | 現在の真実源 |
|---|---|
| Architecture test | <!-- BEGIN GENERATED ARCHTEST STATS -->Source AST: 426 runnable `Test*` functions across 148 `_test.go` files in `internal/archtest`<!-- END GENERATED ARCHTEST STATS --> |
| Architecture rule | [生成された backend boundary map](docs/doc/codemap/13-archtest-boundaries.md) |
| Test coverage | 現在の test run から再計算。固定の percentage は主張しません |
| CI | [Alibaba Cloud ECI contract](docs/契约/remote-ci-eci-imagecache-contract.md) |

<!-- sd:security -->
## セキュリティ

credential、Provider home、local database、log、user memory、machine-specific configuration を commit しないでください。Identity、ownership、configuration、dependency が欠けている場合は、silent degradation ではなく fail-closed にする必要があります。

脆弱性は [Security Policy](SECURITY.md) の非公開手順で報告してください。公開 Issue に exploit detail、secret、trace payload、user data を記載しないでください。

<!-- sd:community -->
## コミュニティとコントリビューション

焦点の明確な Issue と Pull Request を歓迎します。まず以下を参照してください。

- [Contributing Guide](CONTRIBUTING.md)
- [Support](SUPPORT.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Roadmap](docs/open-source/ROADMAP.md)
- [Changelog](CHANGELOG.md)
- [Release Checklist](docs/open-source/RELEASE_CHECKLIST.md)

AI-assisted contribution を歓迎しますが、提出する diff、test、security、licensing、evidence については contributor が引き続き責任を負います。生成された回答や成功した Agent run は、repository gate の代わりにはなりません。

## ライセンス

[Apache License 2.0](LICENSE) の下で提供されます。Project および third-party attribution の指針は [NOTICE](NOTICE) を参照してください。
