---
title: "uzushio 自身の決定記録を docs/adr に置き、DocDag の adr preset で検査する"
status: accepted
date: 2026-09-05
---

# 0001: uzushio 自身の決定記録を docs/adr に置き、DocDag の adr preset で検査する

## ステータス

Accepted

採択日: 2026-09-05

## 日付

2026-09-05

## コンテキスト

uzushio のリポジトリには既に vault がある。`spec/` の下に clause / conform / deviation / measure /
premise / principle / pm / topic の 8 kind を持つ `preset: spec` のコーパスで、「AI が触るプロジェクトは
こう作れ」という規範の本体である。

一方で「uzushio という実装をなぜこう作ったか」という決定は、規範の条項ではない。条項（clause）は
BCP 14 の modality を持ち、適合テストに裏打ちされ、読者に義務を課す。決定記録は義務を課さず、
選択肢と理由と棄却を残す。二つを同じ kind に混ぜると、`orphan_must` のような条項向けのルールが
決定記録を誤検知するか、決定記録に modality を書く無意味な作業が生じる。

CMoA は同じ問題を `docs/adr/` に `preset: adr` の別 `docdag.yaml` を置いて解いた（CMoA ADR 0001）。
DocDag は `--config <path>` で設定ファイルを選べるので、1 リポジトリに 2 つの vault を置いて
別々に検査できる。

## 決定

1. uzushio 自身の決定記録は `docs/adr/NNNN-kebab-title.md` に置く。フロントマターは adr preset の
   語彙（`title`、`status`、`date`、`supersedes`、`depends-on`）に限る。
2. `docs/adr/docdag.yaml` に `preset: adr`、`dir: docs/adr`、`references.dangling: error`、
   `structural.missing_frontmatter: error` を書く。この 1 ファイルだけは手書きであり、
   生成物ではない（規範の vault の設定は 0002 のとおり生成する）。単一 kind の設定では `dir` が
   設定ファイルの位置ではなくプロセスのカレントディレクトリ基準で解決されるため、この vault は
   リポジトリのルートから `docdag validate --config docs/adr/docdag.yaml` として検査する。
3. CI は規範の vault（ルートの `docdag.yaml`）と決定記録の vault（`docs/adr/docdag.yaml`）を
   別ステップで `docdag validate` する。
4. 記録は日本語で書く。議論が日本語で行われたためであり、コードと README と規範の本文は英語のまま。
5. 決定を変えるときは新しい記録を書き、`supersedes:` を宣言して旧記録の status を `superseded` に
   する。記録の本文を書き換えて決定を変えない。`status_drift` がこの対応を検査する。

## 検討した代替案

- **spec vault の principle / premise / pm で表す。** 不採用。決定は「なぜそう実装したか」であって
  規範の根拠ではない。principle に置くと条項の `rationale` として参照可能になり、実装都合の判断が
  規範の根拠に昇格する。
- **spec preset に adr kind を足す。** 不採用。生成する設定に kind を 1 つ足せば済むが、規範の
  vault の語彙を実装都合で広げることになる。規範の語彙は「AI が触るプロジェクト全般」に適用できる
  ものだけにしたい。
- **記録を置かない（コミットメッセージと PR で足りる）。** 不採用。moka-1 の教訓は、前提が崩れた
  ことを記録する場がなかったことである。

## 影響とトレードオフ

- 1 リポジトリに 2 つの `docdag.yaml` がある。`docdag` を引数なしで実行すると探索でルートの設定が
  選ばれるので、決定記録側は常に `--config docs/adr/docdag.yaml` を明示する。
- 決定記録と規範の間に辺は張れない（コーパスをまたぐ辺は DocDag にない）。決定が条項に効く場合は
  条項側の premise として別に書く。

## 関連ADR

- CMoA ADR 0001（DocDag の採用）と同じ形。
- 0002（設定を Go で持つ）が、規範の vault の設定を生成物にする理由を述べる。
