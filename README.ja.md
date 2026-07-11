# Super Dolphin Agent

🌐 [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [한국어](README.ko.md) | [Español](README.es.md) | [Deutsch](README.de.md)

> [!IMPORTANT]
> **🤖 100% AI 開発・保護**
> すべての Go バックエンドロジック、React フロントエンド、AST/SSA コンパイラレベルの保護ルール、およびこのドキュメントを含む本リポジトリ全体は、人間の設計指示のもとで**完全に AI エージェントのみによって記述・リファクタリング**されました。これは「自己保護型リポジトリパターン（Self-Guarding Repo Pattern）」のライブ実証として機能します。

**AI ネイティブなソフトウェアガバナンスと、マルチエージェント開発のコントロールプレーン。**

Super Dolphin Agent は、AI エージェントを主要な保守主体とするソフトウェアプロジェクトのために設計されています。マルチエージェントセッション、ツール実行、MCP オーケストレーション、多言語 LSP、スケジューリング、メモリ、Provider ネイティブスキル、リアルタイムイベントストリーミング、機械的に強制される設計境界を一つのデスクトップコントロールプレーンに統合します。

英語版 [README.md](README.md) が正規の情報源です。製品の意味、コマンド、パス、環境変数、ルール ID、ライセンス情報に差異がある場合は英語版を優先します。

## 具体的に何をするものなのか？

Super Dolphin Agent は、チャットボットや Cursor のプロンプトテンプレート、API ラッパーではありません。AI エージェントがコードベースを混乱させることなく自律的にソフトウェアを開発・保守できるように設計された、**ローカルデスクトップ実行環境およびコンパイラグレードのソフトウェアガバナンスファイアウォール**です。

システムを協調して動作する3つの部分に分割することで、**「ブラックボックス AI エントロピー」**の問題を解決します：

1. **ローカルコントロールセンター（デスクトップアプリ）**：Wails ベースのデスクトップインターフェース（`cmd/agent-terminal`）で、マルチエージェントセッションの実行と監視、ツール実行のモニタリング、自然言語による cron ジョブのスケジュール、SQLite バックエンドのベクトルメモリの管理、および AI ワークスペースログのリアルタイムストリーミングを可能にします。
2. **コードインテリジェンスエンジン（LSP ＆ MCP サイドカー）**：
   - **LSP サイドカー (`cmd/mcp-lsp`)**：リポジトリをインデックスし、脆弱なテキスト検索に代わって、正確で構造化されたコード定義、参照、および型階層を AI にフィードする、汎用多言語言語サーバープロトコルサイドカー。
   - **オーケストレーションサイドカー (`cmd/mcp-orch`)**：モデルコンテキストプロトコル（MCP）を調整し、ツール実行 DAG を管理。AI が自由な bash スクリプトではなく、安全で構造化されたインターフェースを介してファイルを読み書きできるようにします。
3. **免疫システム（AST/SSA ユニットテスト）**：Go テストスイート（`internal/archtest`）に直接組み込まれ、変更されたコードを Go コンパイラレベルの静的単一代入（SSA）中間表現にコンパイルしてデータフローチェックを実行。Git にコミットされる前に、よくある AI アンチパターン（エラーの握り潰し、デッドロックの導入、オニオンアーキテクチャ境界の違反）をブロックします。

### 生産レベルの参照モデル（コード防腐の学習）

Super Dolphin Agent は、本番環境に対応した生産レベル of マルチエージェントオーケストレーションシステムです。実際の運用負荷に合わせて設計されていますが、同時に開発者が以下を学ぶための優れた参照リポジトリとしても機能します：

1. **雰囲気プログラミングの痛みの解決（免疫ソフトウェアエンジニアリング）**：AI によるコードベースのエントロピー（コードの腐敗）からプロジェクトを保護するための、生産で検証済みの完全なブループリントを提供します。標準の Go ユニットテスト内で AST ルールを記述し、SSA コールグラフを構築し、自動収縮する品質ラチェットを実行する方法を示します。
2. **生産レベルのマルチエージェントアーキテクチャ**：コードベース自体は、マルチエージェントコントロールプレーンのクリーンで依存関係が注入された（`fx`）、コントラクトファースト（`internal/contract`）の実装です。以下の明確な参照パターンが含まれています：
   - 並行して動作するエージェントワーカーゴルーチンの起動、停止、および回復。
   - stdio MCP サイドカープロセスの起動と、生 JSON-RPC から型定義された Go 構造体への変換。
   - プロジェクトローカルの JSONL データベースでのスレッド履歴の保持。
   - SQLite ベースのベクトルメモリ検索の実装。

<!-- sd:why -->
## AI 保守を前提とした設計

「AI による保守」は、無審査の変更や、AI がリポジトリ全体を一度に理解することを意味しません。AI を主要な実装ワーカーとし、リポジトリ自身が変更を狭く、監査可能に保つためのナビゲーション、制約、証拠を提供するという意味です。製品目標、高影響の判断、認証情報、リリースの責任は人間が持ちます。

保守ループは限定されたコンテキストを中心に構成されます。

1. 生成されたコードマップとファイル単位の AI project map で対象を特定する。
2. capability contract と明示的なアーキテクチャ規約で公開動作を理解する。
3. LSP の定義、参照、call hierarchy、diagnostics を使って小さな範囲だけを変更する。
4. AST、SSA、依存方向、複雑度予算、fail-fast ポリシーで変更を制約する。
5. 集中的なテスト、生成物チェック、変更感知型 gate で証明してから commit / push する。

これにより、人間や AI がコードベース全体を記憶し続けるという脆い前提を排除します。

### クリーンな AI 閉ループ：プロジェクト全体の「全スキャン」は不要

従来の開発プロセスでは、開発者はプロジェクト全体のコードベースを AI エージェント의 コンテキストウィンドウに詰め込まなければならないと感じがちです。しかし、これはトークン消費を爆発させ、アテンションを飽和させ、AI のハルシネーション（幻覚）を増加させる原因になります。

Super Dolphin の自己保護アーキテクチャは、「最小限の知識（Zero-Knowledge）」原則の下で動作する、**クリーンで局所的なコード変更ループ**を作成します：
*   **狭いコンテキストのみ**: リポジトリにコンパイラで強制されたインターフェース、明確な境界ルール、自動更新されるプロジェクトマップがあるため、AI エージェントはターゲットファイルと直接接続されたコントラクトインターフェースのみをロードすれば十分です。
*   **リポジトリがエージェントをガイドする**: AI がアーキテクチャルールに違反したり技術的負債を導入しようとすると、AST/SSA 静的ゲートが即座にそれをブロックし、正確なコンパイラグレードの診断（Diagnostics）を提供します。
*   **自動的な自己修復（Self-Healing）**: エージェントはコンパイラの診断出力を読み取り、その場でコードを自己修正して、再度コミットを試みます。

これにより、**AI は安全で生産レベルの変更を行うためにプロジェクト全体を読み取る必要がなくなります**。コードベース自体が決定論的なコーディネーターとして機能します。


### プロジェクトの起源：V2 の構造的エントロピーから Super Dolphin へ

Super Dolphin Agent は 2026 年 3 月 19 日、`go-agent-v2` からのクリーンな移行として始まりました。V2 はすでに、エージェントセッション、ツール、Provider、イベント、復旧、デスクトップ体験という製品価値を証明していました。問題は機能不足ではなく、局所的な機能追加が成功するたびに、システム全体の理解可能性が少しずつ失われたことでした。

V2 では 80 を超える手書き RPC method に、binding、validation、capability check、logging、error mapping、複数の registration path が分散していました。lifecycle の真実は複数の manager file、有効状態、暗黙の副状態機械、非同期復旧副作用に分かれていました。中心 event handler は 557 行に達し、bus には数十の message/topic 定数が増え、手作業の application assembly は 200 行を超えていました。コードは動いていても、「権威ある動作はどこか」という問いが難しくなっていました。

本プロジェクトでいう**ソフトウェア腐敗**は、開発者への非難でも即時の故障でもありません。局所部分は動作していても、contract、ownership、変更境界が暗黙知になる状態です。高速な AI 開発は、もっともらしい局所 patch ごとに隠れた経路を増やし、この問題を加速します。

最初の V3 判断は、約 83,000 行の旧システムをその場で改造することを拒否しました。旧コードを動作証拠として残し、能力を関数単位で明示的な contract へ移行しました。

| V2 の構造的エントロピー | Super Dolphin の対応 |
|---|---|
| 手書き RPC と分散した横断処理 | typed request、統一 contract、明示的 middleware と error semantics |
| lifecycle と副作用の分散 | 宣言的 state transition、型安全 event、所有者が明確な runner |
| 手動 `New()` / `Close()` object graph | `fx` composition と明示的な起動・終了 ownership |
| 業務層から storage / adapter への結合 | onion boundary、Module-owned Port、anti-corruption adapter |
| 抽象度を混在させた巨大関数 | composed method と `80 / 4 / 10` の長さ・ネスト・複雑度予算 |
| reviewer の記憶に依存する規約 | AST/SSA guard、map、manifest、hook、再現可能な証拠 |

V2 は隠すべき歴史ではなく、Super Dolphin の governance が対抗する失敗モデルです。

### エンジニアリング防腐と AI Code Rot

AI はコードを高速に生成できますが、境界がなければ設計劣化も高速に拡大します。Super Dolphin はこの劣化を **AI Code Rot** とみなし、導入地点の近くで機械可視な失敗へ変換します。

| 防腐レイヤー | 防ぐもの | リポジトリ上の根拠 |
|---|---|---|
| ナビゲーションの正規情報 | 誤ったサブシステムの編集、古いメンタルモデル | `docs/doc/codemap`、project map、capability manifest |
| アーキテクチャ境界 | Module から Store、Provider、UI、Command 実装への直接依存 | 型付き境界レジストリと AST import 評価 |
| セマンティック guard | エラー無視、silent fallback、不安全なライフサイクル、広すぎる seam | AST guard と priority SSA 解析 |
| 複雑度予算 | 業務、インフラ、プロトコル、永続化を混在させた巨大関数 | 有効関数長 `<= 80`、ネスト `<= 4`、循環的複雑度 `<= 10` |
| 負債ラチェット | 既知の負債を悪化させる変更や、baseline 再作成による隠蔽 | production/test freeze が回帰を拒否し、改善時に縮小 |
| 再現可能な gate | map、test、生成物、証拠なしの完了宣言 | pre-commit、pre-push、変更感知型 AI maintenance gates |

80 行はすべてのシステムに適用する教義ではありません。このリポジトリのオーケストレーション中心の負荷では、同じ抽象度を保つ composed method が巨大な手続きより安全だからです。本質的な規則は、**ポリシーを見える状態にし、詳細を狭い interface の後ろへ置き、例外を明示的かつ測定可能にすること**です。

### 一般的な Agent Framework との違い

| 一般的な Agent Framework | Super Dolphin Agent |
|---|---|
| タスク実行を最適化 | タスクが実システムをどう変更するかを統治 |
| ツールとコンテキストを増やす | 限定コンテキスト、capability contract、依存方向を提供 |
| run の終了を成功とみなす | test、diagnostics、生成状態、Git 証拠を要求 |
| prompt の規律に依存 | code、test、hook、manifest で不変条件を強制 |
| retry や default で障害を隠す | 設定・状態・依存が不正なら fail-fast |

```text
intent
  -> code map + capability contract
  -> LSP/MCP による限定的な AI 変更
  -> AST/SSA/architecture guards
  -> focused tests + generated artifact checks
  -> reviewable evidence
  -> accepted commit
```

<!-- sd:architecture -->
## アーキテクチャ概要

```text
cmd/                 デスクトップ入口、MCP オーケストレーション、多言語 LSP sidecar
frontend-app/        現行 React/Vite デスクトップ UI
internal/contract/   モジュール間 interface と DTO
internal/module/     Turn、Prompt、Cron、Memory、Skill の業務ロジック
internal/platform/   DB、RPC、設定、ランタイム安全性
internal/provider/   Codex、Claude CLI などの Provider adapter
internal/store/      sqlc ベースの永続化 adapter と手書き wrapper
pkg/                 再利用可能な公開ライブラリ
```

業務層は内側の contract のみに依存し、Store はドメインと SQL 実装の間の anti-corruption layer になります。Provider、MCP、UI は外側に置かれ、`cmd/*` と composition root が明示的に組み立てます。詳細は[コードマップ](docs/doc/codemap/README.md)と[オニオンアーキテクチャ契約](docs/%E5%A5%91%E7%BA%A6/onion-architecture-convention.md)を参照してください。

## 主な機能

- マルチエージェントの session、resume、fork、schedule、リアルタイムイベント。
- MCP オーケストレーション sidecar と汎用多言語 LSP peer。
- Cron、Memory、Prompt、Thread、Provider ネイティブスキル。
- Codex / Claude CLI Provider adapter と統一 contract。
- SQLite、Wails デスクトップホスト、React/Vite UI。
- code map、project map、capability contract、Archtest、AI maintenance gates。

<!-- sd:quick-start -->
## クイックスタート

前提: Go 1.25.7、Node.js 20+、認証済み OpenAI Codex CLI、`gopls`、`typescript-language-server` と `typescript@5.9.3`。Claude Code CLI は Claude Provider を使う場合のみ必要です。

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

SQLite は既定で `SUPER_DOLPHIN_HOME/super-dolphin.db` を使います。`SUPER_DOLPHIN_SQLITE_PATH` で別のローカルファイルを指定できます。正規スキルは `<workspace>/.agents/skills/` と `~/.super-dolphin/skills/personal/{user,agent,imported}/` にあります。

<!-- sd:governance-demo -->
## 再現可能なガバナンス証明

変更に対して選択される gate を確認します。

```bash
./scripts/ai_maintenance_gates.sh --print-plan --base HEAD
```

主要な防腐チェックを実行します。

```bash
make guard
make codemap-check
make project-map-check
make capcontract-check
```

完全な検証:

```bash
make test
( cd frontend-app && npm run lint && npm test && npm run build )
```

check は読み取り専用で、正規情報が古い場合に失敗します。生成物を意図的に更新するときだけ対応する `*-refresh` target を使ってください。

<!-- sd:security -->
## セキュリティ

- 認証情報、Provider home、ローカル DB、log、user memory、端末固有設定を commit しないでください。
- 設定不足や依存障害は [fail-fast contract](docs/%E5%A5%91%E7%BA%A6/fail-fast-convention.md) に従い、silent fallback は欠陥として扱います。
- 公開ソース exporter は commit 済み Git object のみを読み、default-deny policy で内部計画、archive、run evidence、local workspace、untracked file を除外します。
- 機密性の高い脆弱性はリポジトリ所有者へ非公開で報告し、公開 Issue に exploit、secret、user data を含めないでください。

<!-- sd:community -->
## コミュニティとコントリビューション

Issue と焦点の明確な Pull Request を歓迎します。変更を小さく検証可能に保ち、モジュール境界を守り、修正には同じ commit で regression test を追加し、変更面に対応する gate を実行してください。アーキテクチャ判断は prompt だけでなく contract と実行可能な guard にしてください。

- [コードマップ](docs/doc/codemap/README.md)
- [アーキテクチャ契約](docs/%E5%A5%91%E7%BA%A6/README.md)
- [プロジェクト Agent 指示](AGENTS.md)
- [Apache License 2.0](LICENSE)

## ライセンス

[Apache License 2.0](LICENSE) の下で提供されます。著作権表示は [NOTICE](NOTICE) を参照してください。
