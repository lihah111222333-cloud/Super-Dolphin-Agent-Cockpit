# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

**AI が書くソフトウェアのための自己防御型リポジトリ。** AI エージェントが変更を実装し、リポジトリが所有する map、contract、test、gate が、その変更を残せるほど安全かを判断します。

> [!IMPORTANT]
> **メンテナー宣言：オリジナルコードとプロジェクト固有ドキュメントは 100% AI が記述し、人間が方向を定め、リポジトリが守ります。** Product code、test code、プロジェクト固有ドキュメントは、すべて AI エージェントが記述またはリファクタリングしています。Product intent、architecture decision、credential、release の責任は人間が負います。AI が作者であることは無謬性を意味しません。受け入れられるすべての変更には、リポジトリが所有する evidence と gate が引き続き必要です。上流由来の法的文書およびコミュニティ文書は、元の帰属表示を維持します。

Super Dolphin Agent は、**AI ネイティブなソフトウェアガバナンスとマルチエージェント開発のコントロールプレーン**です。ローカルデスクトップ runtime、MCP orchestration、多言語 LSP navigation、Provider integration、永続 workflow、機械的に強制される engineering boundary を、一つの動作する参照実装に統合します。

英語版 [README.md](README.md) が正規の概要です。翻訳版は、同じ product scope、command、path、environment variable、repository identity、license を維持します。詳細な事実は [Architecture](docs/open-source/ARCHITECTURE.md)、[Governance in Action](docs/open-source/GOVERNANCE.md)、生成された [Code Map](docs/doc/codemap/README.md) を参照してください。

<!-- sd:why -->
## Super Dolphin が必要な理由

多くの Agent framework は task execution を最適化します。Super Dolphin はそれに加えて、完了した task が長期運用される software system にどのような変更を加えてよいかを統制します。

保守ループは次の 5 段階で構成されます。

1. **Orient**：生成された code map と capability contract で対象を特定します。
2. **Understand**：LSP を通じて definition、reference、call hierarchy、diagnostics を確認します。
3. **Change**：ownership が明確な狭い範囲だけを変更します。
4. **Constrain**：AST/SSA rule、dependency boundary、complexity budget、fail-fast contract で diff を制約します。
5. **Prove**：focused test、generated-artifact check、change-aware gate で結果を証明します。

### 境界づけられたコンテキストでの保守

このリポジトリは、通常の変更でコードベース全体を一つの model context に読み込む必要がないよう設計されています。生成された navigation、狭い contract、決定的な failure signal により、エージェントは関連範囲を見つけ、違反を迅速に修正できます。

すべての変更が局所的になるという保証ではありません。複数領域にまたがる作業には、より広い reference と impact analysis が必要です。受け入れられる変更には、対応する test と review evidence が引き続き求められます。

### 起源：AI code rot への対抗

Super Dolphin は 2026 年 3 月 19 日、`go-agent-v2` のクリーンスレートな後継として始まりました。V2 は、自動化されたクオンツ取引 workflow とマルチエージェントのデスクトップ制御を組み合わせた非公開 prototype でした。保守担当者の公開前記録によると、prototype は動作していましたが、soft constraint だけでは architecture の理解が次第に困難になりました。

- 80 を超える RPC method に、並行した binding、validation、capability、logging の経路が蓄積した。
- lifecycle ownership が複数の manager と非同期 side effect に分散した。
- 中央の event handler が 557 行に達した。
- 手動の application assembly が 200 行を超えた。

この状態を **AI code rot** と呼びます。局所的な変更は動作し続ける一方で、global contract、ownership boundary、可読性が劣化していく状態です。非公開の履歴そのものは公開 evidence ではありません。その代わり、公開リポジトリは、そこから生まれた guard、regression fixture、再現可能な command を提示します。

| V2 の failure mode | Super Dolphin の対応 |
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

### 現在の範囲

- Desktop application と、このリポジトリ固有の governance loop は実装済みです。
- `make guard` と関連 check が統制するのはこのリポジトリです。任意のリポジトリに使える汎用 scanner としては宣伝していません。
- Check-in 済みの public-source policy と validation primitive は release-readiness の基盤です。完全な source-export CLI、sealed receipt workflow、public CI gate、standalone guard distribution は、まだ公開済みの機能ではありません。
- この文書の canonical GitHub URL は公開先です。Clone、Issue、private reporting のリンクは、repository owner が release checklist を完了した後に利用可能になります。
- 現在の desktop Provider flow には Codex が必要です。Claude は、その Provider integration を明示的に対象とする作業でのみ使用します。

<!-- sd:quick-start -->
## クイックスタート

### 前提条件

- Go 1.25.7
- Node.js 20+ と npm
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
| Architecture test | <!-- BEGIN GENERATED ARCHTEST STATS -->Source AST: 329 runnable `Test*` functions across 127 `_test.go` files in `internal/archtest`<!-- END GENERATED ARCHTEST STATS --> |
| Architecture rule | [生成された backend boundary map](docs/doc/codemap/13-archtest-boundaries.md) |
| Test coverage | 現在の test run から再計算。固定の percentage は主張しません |
| CI | [GitHub Actions](.github/workflows/ci.yml) |

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
