---
title: "task doctor は verifier の健全性を偽陽性と kill rate で測り、結果を verifier kind として vault に残す"
status: accepted
date: 2026-09-05
depends-on: [0002, 0003, 0004]
---

# 0005: task doctor は verifier の健全性を偽陽性と kill rate で測り、結果を verifier kind として vault に残す

## ステータス

Accepted

採択日: 2026-09-05

## 日付

2026-09-05

## コンテキスト

### 「verifier のない機能には着手しない」の verifier とは何か

uzushio の中心命題は、verifier のない機能に着手しないことである。しかし「テストがある」ことと
「テストが verifier である」ことは違う。既知の正解を落とすテストは verifier ではなく、欠陥を通す
テストも verifier ではない。前者が偽陽性、後者が検出漏れである。P4（verifier 健全性）はこの 2 つを
測って閾値未満の verifier を「無い」とみなす、と定めていた。測る手段が要る。

### ミューテーションテストの語彙

欠陥を注入して検出率を測る手法はミューテーションテストとして確立している（PIT、Stryker、Go では
gremlins）。語彙は killed（検出した）／survived（通した）／timeout／equivalent（意味が変わらない
mutant、分母から除く）で、score は killed / (killed + survived) が慣例である。LLM に mutant を
書かせる研究（Meta ACH、arXiv 2501.12862、FSE 2025）や、mutation でベンチマークのテストを強化して「解けた」
パッチの約 5 分の 1 が意味的に誤りだったと示す研究（SWE-ABS、arXiv 2603.00520、2026-02）もあるが、
LLM 生成は非決定的で、等価判定を要する。

### 検証の実体はどこにあるか

verifier の実体は CMoA の `internal/verify`（CMoA ADR 0005）で、CMoA ADR 0008 はそれを読み取り専用の
要素として名指しする。uzushio が再実装すると、健全性を測る層が測る対象と別の実装で測ることになる。
CMoA ADR 0009 が `cmoa verify` を足し、1 つの diff を 1 回検証して JSON を返す口を開けた。同時に
`task.json` が version 2 になり、reference solution・mutants・doctor の閾値を持つようになった。

## 決定

### D1. `uzushio task doctor` の手順

1. reference solution（`reference.diff`）を `reference_runs` 回（既定 3）検証する。1 回でも `fail` なら
   偽陽性。`timeout` / `runner_error` / `apply_failed` はその回を inconclusive とする。
2. 各 mutant について、`rev` の worktree に reference を当て、その上に mutant を当て、`rev` との差分を
   1 つの diff に合成して `cmoa verify` に渡す。mutant は正解に対して書かれているので、2 つの diff を
   連結しても一般には当たらないためである。
3. 分類：`fail` → killed、`pass` → survived、それ以外 → inconclusive。`expect: equivalent` の mutant は
   実行するが率から除く。
4. kill rate = killed / (killed + survived)（`expect: killed` の mutant のみ）。分母 0 は inconclusive。
5. 判定：reference が 1 回でも落ちた、`origin: hand` かつ `expect: killed` の mutant が 1 つでも生き残った、
   kill rate が `kill_rate_min`（既定 0.8）未満、のいずれかで **unhealthy**。それ以外で inconclusive が
   1 つでもあれば **inconclusive**。残りが **healthy**。

手書き mutant は「このテストなら必ず殺せるはず」と人が選んだものなので全死を要求する。生成 mutant
（D3）は等価や些末なものを含みうるので閾値で見る。timeout を killed に数えない（PIT は数える）のは、
mutant がテストをハングさせた事実と、verifier がそれを検出した事実を区別するためである。

### D2. 記録

- `<task>/doctor/<run-id>/report.json`：各検証（label、種別、mutant の index・diff・expect・origin・operator、
  status、outcome、exit_code、duration_ms、project_name）、集計（reference_runs、reference_failures、
  reference_inconclusive、killed、survived、inconclusive、equivalent、kill_rate、kill_rate_min）、verdict、
  開始・終了時刻、cmoa の版。各検証の stdout / stderr は `cmoa verify --out` で同じディレクトリに残す。
  `cmoa verify` が返す `command` は記録しない。compose ファイルの絶対パス（ホームディレクトリ）を含み、
  公開リポジトリに置く記録に入れる理由がないためである。`examples/*/doctor/` は gitignore する。
- label は `cmoa verify --label` の制約（`^[a-z0-9][a-z0-9_-]{0,63}$`）に合わせて小文字化・置換する。
  mutant のファイル名は元の綴りのまま。
- `--vault <dir>` を与えたときは vault に **kind `verifier`** の文書を書く。ID は
  `verifier/<task>@<day>[-n]`、`spec/verifiers/`、closed、append_only、status なし。fields は
  `verdict`（healthy / unhealthy / inconclusive、必須）、`kill_rate`、`mutants`、`reference_runs`、`report`
  （レポートのパス）。辺とルールはまだ持たない。Step 5 で run が「健全な verifier に基づく」ことを辺で
  要求する。

DocDag は数値を比較できないので、`kill_rate` は記録であって判定ではない。判定は `verdict` の語に落ちて
いる（0003 の verdict と同じ考え方）。

### D3. `uzushio task mutate`：Go の AST から mutant を生成し、diff としてコミットする

演算子は `arith`（`+`↔`-`、`*`↔`/`）、`bound`（`<`↔`<=`、`>`↔`>=`、`==`↔`!=`）、`cond`（if 条件の否定）、
`const`（整数リテラル +1）、`stmt`（代入・式文の削除）、`ret`（return 式のゼロ値化。囲む関数の宣言された
単一の結果型を読み、分からなければ触らない）。1 箇所 1 mutant、名前は
`mutants/<NNNN>-<operator>-<file>-L<line>C<col>.diff` で決定的、同じ diff は重複排除する。生成した
mutant は `task.json` の `mutants` に `origin: generated` として追記する。`_test.go`、生成コード
（`Code generated … DO NOT EDIT`）、`.go` 以外は対象外。`arith` の文字列判定は構文的（リテラル、連結、
`string(...)`）で、変数に入った文字列は見分けられない——その mutant はコンパイルに失敗し killed になる。

変形は `go/parser` で位置を求めてトークンのバイト範囲を置換する。`go/printer` で再印字しないのは、
コメントが動き、ファイル全体が整形されて最小 diff にならないためである。diff は reference を当てた
worktree の index に対する `git diff` で作る（`a/<repo 相対パス>` / `b/<repo 相対パス>` がそのまま出る）。
Go の依存は足さない。

コンパイルできない mutant は killed ではなく **not viable** である（gremlins / PIT / Stryker とも分母から除く）。
`cmoa verify` は終了コードしか返さずビルド失敗とテスト失敗を区別できないので、除外は生成時に行う：
`task mutate` は候補ごとに reference を当てた worktree で `go build ./...` を走らせ、通らないものは捨てて
件数だけ報告する（`--keep-nonviable` で無効化）。手書き mutant は人が通ることを確かめる。

生成物を **コミットする**のは、`task doctor` を決定的に保つためである。生成を doctor の実行時に行うと、
Go のバージョンや演算子の実装が変わるたびに kill rate が動き、何を測ったのかが記録から再構成できない。
LLM による mutant 生成は非決定性と等価判定の問題があるので、この記録の範囲外とし、後の step で
`origin: llm` を足すかを決める。

### D4. 実行は `cmoa verify` の exec、レポートは uzushio の記録

uzushio は compose runner を持たない。`cmoa verify` の JSON（`status`、`exit_code`、`duration_ms`、
`command`）を読む。`cmoa` の版はレポートに記録する。テストは `Runner` インターフェースの偽実装で
Docker なしに通し、実 Docker の経路は `UZUSHIO_E2E=1` の下だけで回す。

### D5. 帯域判定型（band）の grader は語彙だけ

`task.json` v2 の `verify.kind` は `exit-code` と `band` を持つが、uzushio も CMoA も `band` を実装しない。
指定されれば「未実装」として明示的に拒否する。性能ゲート（測定値が [lo, hi] に入るか）を Task にする
step で実装する。

## 根拠（調査結果・出典）

- ミューテーションテストの語彙と score の定義：PIT、Stryker、mutmut、gremlins のドキュメント
  （2026-09-05 調査、`research-mutation` の要約を uzushio の README と本記録に反映）。
- LLM による mutant 生成：Meta ACH（arXiv 2501.12862、FSE 2025。等価判定エージェントの precision/recall は
  前処理なしで 0.79/0.47）。SWE-ABS（arXiv 2603.00520）：mutation 駆動の敵対的テストで SWE-Bench Verified を
  強化。設計文書が引いていた 2602.18550 は無関係の論文（履歴書スクリーニングの妥当性）で、引用を訂正した。
  非決定性と等価判定の扱いから D3 で範囲外とした。
- CMoA ADR 0005（verifier の経路）、0008（verifier は読み取り専用）、0009（`cmoa verify` と task.json v2）。
- Go の変形：`go/parser` で位置を求め、`go/printer` で再印字せずトークン位置でバイト置換する
  （`go/printer` はコメントを動かす：golang/go#20744）。diff は reference を当てた worktree の index に対する
  `git diff` で作り、Go の依存を足さない。
- gremlins の efficacy は killed / (killed + lived) で timeout と not-viable を分母から除く。Stryker の既定閾値は
  high 80 / low 60。PIT は timeout を検出に数えるが、本記録は数えない。

## 検討した代替案

- **uzushio が compose runner を再実装する。** 不採用（CMoA ADR 0009 と同じ理由）。
- **mutant を LLM で生成する（設計 R10）。** 見送り。非決定的で、等価 mutant の判定を別に要する。
  手書き＋AST 演算子で kill rate を測れることを先に確かめる。
- **mutant を doctor の実行時に生成する。** 不採用。決定性が失われ、記録から測定を再構成できない。
- **timeout を killed に数える（PIT 流）。** 不採用。mutant によるハングと verifier の検出を混ぜない。
- **全 mutant を 1 つの閾値で見る。** 不採用。手書き mutant は人が「殺せるはず」と選んだものなので、
  生き残りは verifier の欠陥そのものである。
- **doctor の結果を `measure` kind で表す。** 不採用。`measure` は条項の解釈一致率用で、`agreement` と
  `model` が必須。形が合わない。
- **結果を vault に書かず JSON のみ。** 不採用。「verifier がある」という主張を後から機械検査するには
  vault に事実が要る。
- **Plecto の性能ゲートを最初の Task にする。** 見送り。ライブ実行は数分かかりホスト依存で、
  最初の doctor の対象には重い。帯域判定は語彙だけ予約した（D5）。

## 影響とトレードオフ

- 得るもの：verifier の健全性が数値と語で記録され、Step 5 の run が「健全な verifier で測った」ことを
  辺で要求できるようになる。mutant の生成が決定的で、kill rate が再現できる。
- 失うもの：LLM 生成 mutant の多様性。AST 演算子は Go にしか効かない。
- リスク：`kill_rate_min` の既定 0.8 は慣例（Stryker の high 80）から借りたもので、この repo での実測に
  基づかない。最初の数 Task で見直す。`band` の予約語が長く実装されないと形骸化する。

## 関連ADR

- 0002（設定を Go で持つ）、0003（edit / pattern / run の語彙。verdict を語に落とす考え方）、
  0004（型から文書を書く。`verifier` も `internal/doc` が書く）
- CMoA ADR 0009
