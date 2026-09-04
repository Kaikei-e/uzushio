---
title: "ルールごとのフィクスチャと機械生成文書は型から書き、lint/ にコミットして CI で差分ゼロを検査する"
status: accepted
date: 2026-09-05
depends-on: [0002, 0003]
---

# 0004: ルールごとのフィクスチャと機械生成文書は型から書き、lint/ にコミットして CI で差分ゼロを検査する

## ステータス

Accepted

採択日: 2026-09-05

## 日付

2026-09-05

## コンテキスト

DocDag の `lint --fixtures` は「ルールが鳴れること」を要求する（DocDag ADR 0004）。ルールごとに
`ruleid/`（鳴らなければならない最小コーパス）と `ok/`（鳴ってはならない最小コーパス）を置き、
preset が出荷するルール以外は `missing_fixture` になる。名前は Semgrep の `ruleid:` / `ok:` 注釈から
借りている。

uzushio は 0003 で 9 ルールと 3 射影を足す。フィクスチャを YAML の隣に手で書くと、語彙が変わっても
追随しない。フィクスチャの中身は「edit が 1 つ、それを検証する run が 1 つ」のような小さな文書であり、
同じ形の文書を Step 5 の `uzushio run` と `uzushio improve` が本番の vault に書く。両方を同じ Go の型から
出せば、フィクスチャは機械生成文書のテストでもある。

## 決定

1. **`internal/doc` に機械生成文書の型を置く。** `Run` と `Pattern`（と edit の骨格）を構造体で表し、
   `Write()` がフロントマター（goccy/go-yaml）と本文を出し、`Path(vaultRoot)` が DocDag の規約どおりの
   ファイル名を返す。ID に `/` を含む kind（`fp/…`、`run/…`）は `id:` を必ずフロントマターに書き、
   ファイル名は ID の最後のセグメントにする（DocDag `newdoc.KindFilename` の規約）。語彙は
   `internal/vocab` の定数を使い、範囲外の値は構築時に拒否する。
2. **`internal/fixture` がルール・射影ごとの `ruleid/` と `ok/` を型から書く。** レイアウトは
   `lint/<name>/{ruleid,ok}/spec/<kind-dir>/<file>.md`。`make generate` が `lint/` に書き出し、
   コミットする。テストは再生成との差分ゼロと、`lint.Check(cfg, "", "lint")`（DNF＋フィクスチャ層）と
   `lint.Check(cfg, <repo>, "lint")`（全 3 層）が error も warn も返さないことを検査する。
3. **spec preset が出荷する 13 のフィクスチャ（ルール 10 と `modality_conflict` / `excepts_strict` /
   `stale_target` の検査）は DocDag v0.4.0 の `testdata/lint/spec` からバイト単位で複製して `lint/`
   に置く。** 射影 `has_inforce_successor` の `never_true` は複製では消えず、vault に本物の
   `supersedes` 系譜が要る。v0.4.0 への移行で `premise/docdag-v0-3-0` が事実として崩れたので、
   `premise/docdag-v0-4-0` が `reason: premise-collapse` で置き換える系譜をこの変更で書いた。 DocDag は preset ルールを
   `missing_fixture` から名前で免除するが、出荷フィクスチャを利用側の vault で参照はしない。若い
   vault では preset ルールがどこにも鳴らず、`lint --all` のコーパス層が `never_fired` を warn で
   報告して exit 2 になる。`ruleid/` フィクスチャがあれば info に下がる。複製元と版は `lint/README.md`
   に書く（Apache-2.0、DocDag と同じ）。生成器はこれらの複製ディレクトリには触れない。
4. **テストは in-process、CI はバイナリも回す。** `go test` は DocDag の公開 `lint.Check` を使い、
   `docdag` バイナリを要求しない。CI は別ステップで v0.4.0 のリリースバイナリを取り、`docdag validate`、
   `docdag lint --all`、`docdag validate --config docs/adr/docdag.yaml` を実行する。両者が一致する
   ことで、テストが見た設定と CLI が読む設定が同じであることを保証する。
5. **`docdag new` 用のテンプレートも同じ型から出す。** `internal/doc` の `Template(kind)` が edit /
   pattern / run の骨格を返す。
6. **追記専用の検査は二重にする。** `append_only: true` を edit / pattern / run に付け、CI で
   `docdag validate --immutable-since origin/main` を回す。ただし DocDag v0.4.0 の `--immutable-since`
   は committed status が accepted / superseded / withdrawn の文書しか見ない（status のない run と
   rejected な edit は対象外）。そのため CI は加えて `git diff --diff-filter=D` で `spec/edits` と
   `spec/patterns` の削除を、`--diff-filter=DM` で `spec/runs` と `spec/measures` の削除と変更を拒否する。
   この暫定を premise として vault に書き、DocDag 側が kind ごとの語彙を持ったら `retired_on` で
   引退させる。

## 根拠（調査結果・出典）

- DocDag v0.4.0 `docs/checks.md` "Layer 3"：フィクスチャのレイアウトと `missing_fixture` の免除規則。
  `internal/lint`：`missing_fixture` はルールにだけ要求され、射影にもフィクスチャは書ける。
- DocDag `lint/lint.go` `Check(cfg, vaultDir, fixturesDir)`：`vaultDir` 空でもフィクスチャ層は動く。
  kind の dir は相対でなければならない（絶対だと全フィクスチャが「鳴らない」に見える）。
- DocDag `internal/graph/immutable.go`：`immutableStatuses` は accepted / superseded / withdrawn に
  固定。status フィールドは例外。本文は行単位の接頭辞でなければならない。
- DocDag `internal/parse/parse.go` `KindFile`：`id:` があればそれ、なければファイル名の stem。
  `/` を含むパターンは stem を受けない。
- Semgrep のテスト規約：`ruleid:` / `ok:` 注釈を対象行の直前に書き、ルール 1 つにテストファイル 1 つ。
  https://semgrep.dev/docs/writing-rules/testing-rules

## 検討した代替案

- **フィクスチャを go test の一時ディレクトリだけに置く。** 不採用。CI の `docdag lint --all` が
  `missing_fixture` を報告し、README から読める最小コーパスもなくなる。
- **手書きで `lint/` にコミットする。** 不採用。語彙が変わっても追随しない。
- **`docdag new --fixture <rule>` の出力をそのまま使う。** 一部採用。生成器は user-defined ルールに
  対して動くが射影には動かず、出力は最小限で説明を持たない。型から書く方が機械生成文書と
  同じ経路を通る。
- **テストで `os/exec` により `docdag` バイナリを呼ぶ。** 不採用。バイナリがない環境でテストが
  skip される。CI 側でバイナリを回すことで補う。

## 影響とトレードオフ

- 得るもの：語彙を変えるとフィクスチャがコンパイルエラーになる。機械生成文書のフォーマットが
  Step 5 の前に DocDag で検証される。
- 失うもの：`lint/` にディレクトリが増える（uzushio のルール 9 ＋射影 5 ＋複製した preset の 13、
  それぞれ `ruleid/` と `ok/`）。
- `docdag lint --all` は `OK: no lint findings` にはならない。vault がまだ edit / pattern / run を
  1 つも持たないため、uzushio のルールは「対象 0 件」の info、宣言済みで未使用の辺も info として
  残る。Step 2 が到達するのは「0 errors, 0 warnings」であり、CI はそれで exit 0 になる。info が
  消えるのは Step 5 で本物の文書が積まれてからである。
- リスク：`--immutable-since` の保護範囲が狭い。git の diff-filter による代替は「main と比べる」ので、
  main 自身への直接 push は検査しない。

## 関連ADR

- 0002、0003
- DocDag ADR 0004（preset lint）、0006（公開 config と append_only）
