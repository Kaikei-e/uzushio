---
title: "ハーネス編集の系譜を edit / pattern / run の 3 kind と predicts / validates の 2 辺で記録する"
status: accepted
date: 2026-09-05
depends-on: [0002]
---

# 0003: ハーネス編集の系譜を edit / pattern / run の 3 kind と predicts / validates の 2 辺で記録する

## ステータス

Accepted

採択日: 2026-09-05

## 日付

2026-09-05

## コンテキスト

### uzushio が埋める空白

ハーネス自己改善の研究（Agentic Harness Engineering = AHE, arXiv 2604.25850；Self-Harness,
arXiv 2606.09498；Weng 2026-07-04）は「どう進化させるか」を競う。それらが持たないのは、編集の系譜、
却下と予測外れの永続化、verifier の健全性検査と較正記録である。uzushio はそこを規約と参照実装で
埋める。地盤は DocDag の vault で、CMoA がトレースを書き、uzushio が失敗を掘り、編集を提案させ、
検証し、受理する。

### 一次資料の確認（2026-09-05）

- AHE の manifest は逐語で「the failure evidence, the inferred root cause, the targeted fix, and a
  predicted impact comprising both expected fixes and at-risk regressions」。7 コンポーネント。
  runs ディレクトリ・tracer・verifier・LLM 設定は読み取り専用。
- Self-Harness の弱点マイニング記録は「terminal verifier-level cause / causal status / abstract agent
  mechanism」。受理条件は Δin ≥ 0 ∧ Δho ≥ 0 ∧ max(Δin, Δho) > 0。「一方を犠牲にして他方を上げる
  提案は合計が増えても却下」。却下候補はログに残る。
- STPA Handbook（2018）§2.4：unsafe control action は 4 類型（not providing / providing causes hazard /
  too early, too late, or out of order / stopped too soon, applied too long）。「すべての UCA は
  どの文脈で危険かを明記しなければならない」。
- 両論文とも 1 つの編集を複数モデルで評価し、split はベンチマーク名に対して定義される。

### DocDag の語彙の制約（v0.4.0 調査）

`via` / `via_inbound` が読めるのは隣接文書の属性であって辺属性ではない。数値比較はない。
list 値のフィールドは `eq` / `not` / `required` から見ると「不在」に読まれ、`subset_of` だけが
list を扱う。存在検査の演算子はない。射影は `"true"` / `"false"` の文字列として全文書に現れる。

## 決定

### D1. kind

| kind | dir | ID | status | closed | append_only | fields |
| --- | --- | --- | --- | --- | --- | --- |
| `edit` | `spec/edits` | `he-NNNN` | proposed / accepted / rejected / superseded / withdrawn | yes | yes | `component`（7 面 one_of、必須）、`touches`（list）、`root_cause`、`approval`（human / auto、必須）、`approved_by`、`in_force_from`、`in_force_until` |
| `pattern` | `spec/patterns` | `fp/<slug>` | open / resolved / withdrawn | yes | yes | `category`（STPA 4 類型 one_of、必須）、`context`（必須）、`component`（7 面 one_of、必須）、`evidence`（CMoA のトレース run-id `YYYYMMDDTHHMMSSZ-<8 hex>` の list） |
| `run` | `spec/runs` | `run/<edit>@<day>-<model-slug>-<split>[-<n>]` | なし（機械が書く） | yes | yes | `split`（held-in / held-out、必須）、`verdict`（improve / hold / regress / inconclusive、必須）、`suite`（必須）、`trials`、`trace` |

`edit` は `period: {from: in_force_from, until: in_force_until}` を持つ。どちらも書かない edit は常に有効。
`run` の ID にモデルスラグを入れるのは、両論文が 1 編集を複数モデルで評価するため。`suite` は
「held-out が何に対して held-out か」を名指す。

### D2. 辺

| 辺 | from → to | 属性 | target |
| --- | --- | --- | --- |
| `predicts` | edit → pattern | `expect`（fix / at-risk、必須）、`outcome`（confirmed / refuted、任意） | — |
| `validates` | run → edit | `model`（必須）、`pass_rate`（number、必須）、`baseline_pass_rate`（number、必須） | なし（下記） |
| `supersedes`（既存） | from / to に edit を追加 | `reason` 必須（既存） | — |
| `premise`、`counterexample`、`about`（既存） | from に edit を追加 | — | — |

`validates` に `target: {leaf_of: supersedes}` を付けない。設計文書はそれを予定していたが、run は
append-only の履歴であり、`leaf_of` は「今も現行の葉を指しているか」という生存性の制約である。
両立させると、run が測った edit が supersede された瞬間にその run は `stale_target`（構造検査、
重大度を下げられない）の error になり、CI が run の変更も削除も拒否するため永久に消えない。
DocDag 自身が `internal/graph/target.go` でこのラチェットを名指しし、逃げ道は宣言側 kind の
`period` だとしている。測定に期限を持たせるのは誤りなので、target を外す方を選んだ。
`predicts` にも target は付けない（同じ理由）。

`expect: {fix, at-risk}` は AHE の「expected fixes and at-risk regressions」の省略形である。
`outcome` は予測の決算であり、Step 5 の `uzushio run` が書く。`baseline_pass_rate` は Self-Harness の
受理条件が差分で書かれているために要る。

### D3. 射影

- `validated_in`：held-in の run が `improve` か `hold`（非回帰）
- `validated_out`：held-out で同上
- `improved`：どちらかの split の run が `improve`
- `effective`（binding）に alt を追加：kind edit ∧ accepted ∧ validated_in ∧ validated_out ∧ improved ∧
  in_force ∧ 有効な後継がいない

CMoA が `docdag query --binding` で読む「今日有効なハーネス編集」はこの alt で決まる。

### D4. ルール

| 名前 | 重大度 | 条件（要旨） |
| --- | --- | --- |
| `accepted_unvalidated` | error | accepted な edit で validated_in / validated_out / improved のどれかが false |
| `edit_touches_readonly` | error | edit の `touches` が 7 面の部分集合でない（未記載も含む） |
| `edit_without_prediction` | error | 有効な edit に `predicts` がない |
| `edit_without_topic` | error | 有効な edit に `about` がない |
| `rejected_without_run` | error | rejected な edit に `validates` の inbound がない |
| `propose_only_accepted` | error | accepted な edit の component が propose-only の面 |
| `accepted_without_approver` | error | accepted な edit の component が human-approval の面で `approval: auto` |
| `pattern_resolved_unfixed` | warn | resolved な pattern を予測する accepted な edit がない |
| `predicts_withdrawn` | warn | proposed / accepted な edit が withdrawn な pattern を予測している |

`edit_touches_readonly` が「未記載も含む」のは DocDag の `subset_of` が不在を false に読むためで、
結果として `touches` は事実上必須である。機械が書く edit は常に `touches` を書く。
`edit_without_prediction` と `edit_without_topic` を `in_force` で絞るのは、失効中の文書が宣言する辺が
DocDag のインデックスから落ちる（`supersedes` を除く）ためで、未来日の edit が誤検知されないようにする。

### D5. 受理条件は 4 値の verdict で表す

「両 split で pass」は何も変えない編集を通す。Self-Harness の条件に合わせ、`hold`（非回帰・改善なし）と
`improve`（厳密な改善）を分け、受理には両 split の非回帰と少なくとも一方の `improve` を要求する。
数値の判定（何 pp 上がれば improve か、SPRT の閾値）は uzushio が行い、DocDag は語だけを読む。

### D6. 自律度の落とし方

CMoA の自律度（auto-accept / human-approval / propose-only）は面ごとに決まっている（CMoA ADR 0008）。
vault 側では `approval`（human / auto）を必須の閉じたフィールドにし、human-approval の面が `auto` で
accepted になっていれば error にする。`approved_by`（承認者）は自由文字列で任意。DocDag に自由文字列の
存在検査がないため、承認の事実は閉じた語彙 `approval` が担い、名前は記録に留める。

## 根拠（調査結果・出典）

- AHE arXiv 2604.25850 v4（2026-05-18、プレプリント）§3.1、§3.3、Table 3。
- Self-Harness arXiv 2606.09498 v3（2026-08-20、プレプリント）：受理条件の式と却下のログ。
- Weng, L. "Harness Engineering for Self-Improvement"（2026-07-04）：失敗を保存しやすくする、
  評価器と権限制御はループの外。
- STPA Handbook（Leveson & Thomas, 2018）§2.4。
- DocDag v0.4.0 `internal/graph/check.go` `matchAttr`：`subset_of` の不在は false、list は `eq` に不在。
  `internal/graph/check.go` `carriesWeight`：失効中の文書の辺は落ちる。`internal/graph/period.go`：
  どちらの日も書かない文書は常に有効。
- CMoA ADR 0008：7 面、自律度、読み取り専用 3 要素。

## 検討した代替案

- **verdict を pass / regress / inconclusive の 3 値のままにし、改善の要求は uzushio CLI だけが持つ。**
  不採用。vault を読む側（CMoA、人）が「受理された編集は本当に何かを改善したか」を辿れない。
- **`touches` を編集可能 6 面（propose-only を除く）に限る。** 不採用。propose-only の提案は記録できる
  べきで、受理だけを `propose_only_accepted` で止める。
- **run の ID を CMoA の run-id にする。** 不採用。ファイル名から edit と split が読めなくなる。
- **`approved_by` だけを宣言し、その存在を検査する。** 不可能（DocDag に自由文字列の存在検査がない）。
  `approval` の閉じた語彙で代替した。
- **split に `transfer`（凍結ハーネスを未見ベンチ・別モデルで）を足す。** 見送り。今のループでは
  使わない。
- **STPA 3 型を `wrong-order-or-timing` に改名。** 見送り。`wrong-timing` が順序誤りを含むことを
  規範の本文で定義する。
- **`validates` に `leaf_of: supersedes` を付ける（設計文書どおり）。** 不採用。append-only の run と
  組み合わせると最初の supersede で vault が壊れる（D2 の説明）。
- **rejected_without_run を warn に留める。** 不採用。Self-Harness と Weng の両方が却下の保存を
  一級の要件にしている。

## 影響とトレードオフ

- 得るもの：受理された編集は「予測があり、両 split で非回帰で、どこかで改善し、承認の種別が記録され、
  失効日を持てる」ことが `docdag validate` で機械検査される。却下も run を伴う。
- 失うもの：`touches` を書かない edit は書けない。`approval` を書き忘れた edit は `missing_field` で止まる。
- リスク：`edit_touches_readonly` は不在と範囲外を区別しない（メッセージで両方を言う）。
  `pattern_resolved_unfixed` は `expect: fix` を読めない（辺属性は `via_inbound` から見えない）ので、
  「accepted な edit が予測している」までしか言えない。
- STPA の 4 類型は uzushio 独自の持ち込みであり、両論文は使っていない。分類が合わなければ本記録を
  supersede して差し替える。

## 関連ADR

- 0002（設定を Go で持つ）
- 0004（フィクスチャと機械生成文書）— D1〜D4 の各ルールが鳴ることの証明
- CMoA ADR 0007（トレース）、0008（面と自律度）
