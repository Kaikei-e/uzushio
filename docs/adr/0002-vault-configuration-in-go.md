---
title: "規範の vault の設定は Go の値として持ち、docdag.yaml は生成してコミットする"
status: accepted
date: 2026-09-05
depends-on: [0001]
---

# 0002: 規範の vault の設定は Go の値として持ち、docdag.yaml は生成してコミットする

## ステータス

Accepted

採択日: 2026-09-05

## 日付

2026-09-05

## コンテキスト

### 設定を YAML で手書きしたくない

uzushio は DocDag の spec preset に kind・辺・射影・ルールを足す。足す量は kind 3 つ、辺 2 つと既存辺の
端点拡張 4 つ、射影 3 つと既存射影の alt 1 つ、ルール 9 つである。これを `docdag.yaml` に手で書くと、
語彙（面の名前、verdict の値、ルール名）が YAML の中の文字列になり、綴りのずれが黙って検査を無効にする。
DocDag v0.4.0 の調査で、`Validate()` は重複するルール名も、存在しない射影名を `attr:` に書いた
ルールも拒否しないことが分かっている。どちらも「鳴らないルール」を作る。

### DocDag 側の前提が揃った

DocDag v0.4.0（2026-09-04）は `config` と `model` をリポジトリ直下の公開パッケージにし、
`yaml.Marshal(cfg)` → `Load` → `Validate` の往復を契約し、in-process の `lint.Check` を置いた
（DocDag ADR 0006）。`SpecPreset()` は Go の構造体リテラルで、uzushio はそれを起点に足すだけでよい。

### 面の語彙は CMoA が持つ

編集可能面（7 面）と自律度、読み取り専用 3 要素の語彙は CMoA のルートパッケージが export し、
`cmoa surfaces --format json` が同じものを出す（CMoA ADR 0008）。uzushio がこれを写して書くと
二重定義になる。

## 決定

1. **設定は `internal/vault` の Go の値。** `config.SpecPreset()` を起点に kind・辺・射影・ルールを
   足し、`cfg.Validate()` の後に自前の検査を通す。自前の検査は DocDag が見ないもの——ルール名の
   一意性、`attr:` に書かれたキーが射影名・`in_force`・`kind`・`status`・対象 kind の宣言フィールドの
   いずれかであること——に限る。
2. **`docdag.yaml` は `uzushio docdag-config` の出力をコミットする。** 先頭に「生成物、手で編集しない」
   のコメントを置く。CI は再生成との差分ゼロ（`--check`）を検査する。生成物をコミットする理由は、
   DocDag の CLI・GitHub Action・pre-commit フック・Claude Code プラグインが全部ファイルを読むためである。
3. **`preset: spec` は残し、リストは全部書き出す。** DocDag の `Merge` はリストを置換する（追記しない）。
   `preset:` を省くと ADR preset に合成され、`derived_edges` と top-level の `status_values` を継承して
   壊れる。`preset_version` は DocDag が比較しない値なので、`SpecPresetVersion` をそのまま書く。
4. **語彙は `internal/vocab` に一箇所。** kind 名、ディレクトリ、ID 正規表現と構築関数、status 語彙、
   フィールド名、辺名と属性、射影名、ルール名、STPA の 4 類型、verdict、split、expect / outcome /
   approval の語。設定の組み立てと文書の書き出し（0004）が同じ定数を読む。
5. **面の語彙は `cmoa surfaces --format json` の出力を `internal/surfaces/cmoa-surfaces.json` として
   コミットし、`go:embed` で読む。** `go generate` が再生成し、CI は `cmoa` を `go install` して
   差分ゼロを検査する。CMoA の Go パッケージは import しない。
6. **依存は DocDag v0.4.0、goccy/go-yaml（DocDag が固定する版）、cobra の 3 つ。** 生成 YAML は
   バイト単位で決定的であること（同じ入力 → 同じ出力）をテストで固定する。TAB と CR を含む文字列は
   marshal 前に拒否する（go-yaml v1.19.2 が黙って壊すことを実測で確認）。

## 根拠（調査結果・出典）

- DocDag v0.4.0 `config/load.go` `Merge`：リストは置換。`preset_version` は比較されない。
  `config.Preset("")` は ADR preset を返す。（DocDag 調査 2026-09-05、file:line 付き）
- DocDag `config/config.go` `Validate`：辺の端点 kind の未宣言は error、ルール名の重複と未知の射影名は
  検出しない。
- goccy/go-yaml v1.19.2：map キーは `fmt.Sprint` の辞書順、構造体フィールドは宣言順、
  `^he-\d{4}$` は `"^he-\\d{4}$"` で往復する。生の TAB は plain scalar として出力され復元で消える。
  （実測）
- CMoA ADR 0008：面と自律度は CMoA が export し、uzushio は Go の値として import しても CLI の JSON を
  読んでもよい。本記録は後者を選んだ。理由は Go の版への compile-time 依存を避けるため。
- DocDag ADR 0001：式言語を持ち込まないのは vault の語彙の話で、vault の外のホスト言語の生成器は
  対象外（Pulumi / cdk8s と同じ位置）。

## 検討した代替案

- **`docdag.yaml` を手書きし、Go は使わない。** 不採用。語彙が文字列になり、CMoA の面と綴りが
  ずれても誰も気づかない。
- **生成 YAML をコミットせず `--config <(uzushio docdag-config)` で渡す。** 不採用。Action・フック・
  プラグインがファイルを読む。
- **CMoA のルートパッケージを import する。** 不採用（所有者の判断）。JSON なら言語も版も跨げる。
- **`preset:` を省いて自己完結の設定を出す。** 不採用。ADR preset に合成される（上記 3）。
- **cobra を使わず標準ライブラリ `flag` だけにする。** 不採用（所有者の判断）。DocDag と揃える。

## 影響とトレードオフ

- 得るもの：語彙の変更がコンパイルエラーになる。設定の妥当性を `go test` が検査する。
  `docdag lint` の DNF 層を in-process で回せる。
- 失うもの：`docdag.yaml` を読んだ人が「直したい」と思っても直せない。ヘッダで生成元を示す。
- リスク：`Condition` は sum type ではない。不正な組合せ（`via` の中の `via` など）は型で防げず、
  `Validate` と `lint` の DNF 層に頼る。

## 関連ADR

- 0001（決定記録の場所）
- 0003（追加する語彙）— 本記録の生成器が組み立てる中身
- 0004（フィクスチャと機械生成文書）— 同じ `vocab` を読む
