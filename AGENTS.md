# uzushio: agent instructions

このファイルはリポジトリ全体の作業指針。詳細はリンク先を必要な範囲だけ読む。
配下により具体的な `AGENTS.md` がある場合は、その対象範囲の指示も確認する。

## 構成と参照先

- [README.md](README.md): CLI、評価手順、セットアップ。関係する節を読む。
- `spec/`: 規範仕様と、提案・評価・校正の記録。`tests/conform/`: 仕様の実行可能な適合性テスト。
- [docs/adr/](docs/adr/README.md): 実装上の設計判断。規範仕様とは別のDocDag corpus。
- `cmd/uzushio/`: CLI。`internal/`: doctor、mutate、render、改善ループ、judge、統計、文書writerなど。
- `internal/vault/`: DocDag設定の生成元。`internal/doc/`: 型付き文書writer。`internal/fixture/`: lint fixtureの生成元。
- [lint/README.md](lint/README.md): 生成fixtureとDocDagからコピーしたfixtureの区別。

## 実装とレビューの要点

- 依存方向はuzushio → CMoA / DocDag。CMoAの実行・検証を既存のCLI連携で利用し、runtimeの選択処理やsurface/autonomy語彙を独自に複製しない。
- 仕様の変更では、該当する `spec/` の条項、適合性テスト、lint fixtureへの影響を確認する。実装上の設計判断はADRに記録する。
- 記録は追記と `supersedes` による履歴を保つ。acceptedな記録の決定内容を書き換えず、却下されたものを含む `spec/edits/`・`spec/patterns/` を削除しない。既存の `spec/runs/`・`spec/measures/` を編集・削除しない。
- verifierの不健康と測定不能を区別する。runner障害や未適用のmutantを検出成功として数えない。評価のheld-in/held-out、baselineとcandidate、tie handlingと標本数を混同しない。
- 自動採用・人間承認・提案のみというsurfaceの区分を維持する。これは改善ループの契約であり、通常のリポジトリ編集に追加の承認手順を課すものではない。
- Goは既存のパッケージと型付きwriterを使い、変更したファイルに `gofmt` を適用する。CLI/JSONや記録形式を変えた場合は、対応テストとREADMEを更新する。

## 生成物

- ルートの `docdag.yaml`、`internal/surfaces/cmoa-surfaces.json`、生成対象の `lint/` は生成元を変更して `make generate` で更新する。手編集で帳尻を合わせない。
- `make generate` はPATH上の `cmoa surfaces --format json` を使う。対象のCMoAバイナリを確認する。DocDagからコピーしたfixtureは [lint/README.md](lint/README.md) の手順で更新する。
- `make check` は再生成し、生成物を `git add -N` して差分を検出するため、作業ツリーとindexに作用する。生成物を変更する作業では `make generate` の結果をレビューし、コミットされた内容との一致を調べるときに `make check` を使う。未コミットの意図した生成差分でも非ゼロになる。

## ビルドと検証

コマンドはリポジトリルートから実行する。Goは `go.mod`、lintは `.golangci.yml`、
CIの詳細は `.github/workflows/` を参照する。

| 変更対象 | 検証 |
| --- | --- |
| Go実装 | 対象パッケージのテストで確認後、`make build test vet lint` |
| 生成元・surface連携 | `make generate` と生成差分のレビュー。コミット済み生成物の整合性は `make check` |
| 仕様・lint・DocDag設定・ADR | `make docdag`。条項や適合性テストを変えた場合は `make conform` も実行 |
| 案内文書のみ | 参照先・記載コマンド・差分を確認。Goテストや再生成は不要 |

- `make build` は `bin/uzushio` を作る。`./bin/uzushio --help` でCLIを確認する。
- `make lint` にはgolangci-lintが必要。DocDagはこのリポジトリの依存・CIに合わせたv0.4.0を使う。隣接するCMoAの指定バージョンとは区別する。
- `make docdag` は `docdag validate`、`docdag lint --all`、`docdag validate --config docs/adr/docdag.yaml` を実行する。ADR corpusには仕様用の `lint --all` を適用しない。
- 履歴を変更する作業では、`origin/main` が利用可能なら `docdag validate --immutable-since origin/main` とCIの履歴チェックも確認する。ブランチ間diffだけでは未コミット変更を検証できないため、作業ツリーの差分も読む。
- 通常の `go test ./...` は実E2Eを除外する。実CMoA・Dockerを通すdoctorの確認は `make e2e CMOA=/path/to/cmoa`。モデルを使う校正・改善の実行はREADMEの前提を満たす統合作業で行う。

## 作業の完了条件

- 着手時に `git status --short` を確認し、既存の変更を保持する。
- 変更した振る舞いを再現できるテストを追加・更新し、必要な検証が通ったら差分をレビューする。実行できなかった確認は理由を報告する。
- `git diff --check` を実行し、変更内容・検証結果・残る制約を簡潔に報告する。
- 生成された実行データや個人の設定を不用意にGitへ追加しない。fixtureは再現可能な合成データで作る。
- このファイルは継続的に有効な指示に絞る。一時的な作業計画や進捗は入れず、コマンドや契約を変更したら対応箇所を更新する。

作成時の公式資料（2026-09-07確認）:
[AGENTS.md](https://learn.chatgpt.com/docs/agent-configuration/agents-md)、
[Codex best practices](https://learn.chatgpt.com/guides/best-practices)。
