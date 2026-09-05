---
title: "task doctor は verifier の健全性を偽陽性と kill rate で測る。帯域判定型の verifier も扱い、最初の外部 Task は uzushio 側に置いて GitHub Actions では回さない"
status: accepted
date: 2026-09-05
supersedes: [0005]
depends-on: [0002, 0003, 0004]
---

# 0006: task doctor は verifier の健全性を偽陽性と kill rate で測る。帯域判定型の verifier も扱い、最初の外部 Task は uzushio 側に置いて GitHub Actions では回さない

## ステータス

Accepted

採択日: 2026-09-05

本記録は 0005 を supersede する。0005 の D1〜D4（doctor の手順、記録、`task mutate`、`cmoa verify` の
exec）は文言を変えずに引き継ぎ、D5「帯域判定型（band）の grader は語彙だけ」を改め、最初の外部 Task に
ついての決定を足す。

## 日付

2026-09-05

## コンテキスト

0005 のコンテキストをそのまま引き継ぐ。

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

### その後に分かったこと：帯域判定型の verifier

0005 の `task doctor` はテスト通過型（exit-code）の verifier で確かめた。しかし verifier には別の形がある。
測定値が帯域 [lo, hi] に入るかで合否を決める性能ゲートで、所有者の別プロジェクト PlectoProxy
（Rust の L7 リバースプロキシ）が T1 ゲートとして持っている：`run-perf.sh gate` が oha と k6 で負荷を
かけ、`gate_verdict.py` が `gate_tolerances.toml` の帯域で不変式ごとに pass / fail / skipped を出し、
`gate.csv` に書く。所有者は「あえてやる」と判断した。テスト通過型だけを扱う doctor は、規範の
「grader の格」（P5）を半分しか確かめていないからである。

### 事前に分かっていたこと（2026-09-05 調査）

- ゲートは単一コンテナで動く。Rust 1.97.1、oha、k6（任意）、python3.11+。特権もホストネットワークも
  要らない。ただし `just gate` はビルドしない（ラッパーが要る）、target ディレクトリの位置が
  ハードコードされている、`taskset` を使うので `cpuset` で 2 CPU 以上を与える必要がある。
- k6 なしでも exit 0 になる（k6 の不変式は skipped）。ただしレートリミットのトークンバケットの正しさ
  （`enforce_allowed_ratio`）を失う。
- 帯域は参照ホスト基準で、Plecto 自身の README が **無変更コードで PASS / FAIL / PASS** を記録している。
  つまり偽陽性率が 1/4 前後の verifier であり、doctor が最初に測るべきものがそこにある。
- ゲートの exit 1 は「帯域を外れた」と「ハーネスが壊れた」を区別しない。`gate.csv` は既に
  `invariant,value,ci_half,band_lo,band_hi,verdict` の形で、区別に必要な情報を持っている。
- GitHub Actions の hosted runner は公開リポジトリで 4 vCPU、ベンチの分散は 2.7〜30% と報告され、
  負荷生成器と被測が同居する。GitHub は公開リポジトリでの self-hosted runner を推奨しない
  （fork の PR が runner を侵害しうる）。ジョブのログに CPU モデルが出る。

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

reference が 1 回でも落ちた（または 1 回も判定に達しなかった）ときは、kill rate を証拠として扱わない：
どの diff も reference と同じ理由で落ちるので、mutant の「killed」は検出を示さない。レポートは
`kill_rate_meaningful: false`、各 mutant に `outcome_note`、そして reference が外さなかった帯域を mutant が
外した場合だけ `bands_beyond_reference` を持ち、vault の記録は `kill_rate: n/a` と書く。
`task doctor --replay <report.json>` は既存のレポートから集計・注記・判定だけをやり直し、実行なしで
記録を書き直す（ライブ実行と同じ `Conclude` を通る）。

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

### D5. Task は uzushio の `examples/task-plecto-gate` に置く。PlectoProxy には何も足さない

Plecto の OSS に uzushio の Task（reference、mutant、閾値）が同居するのはおかしい、という所有者の判断。
Task は `setup.sh` が固定コミットを clone し、mutant はその固定コミットに対する diff である。
Plecto が進んでも Task は動き続け、Plecto を追うときは固定コミットと mutant を一緒に更新する。

### D6. `verify.kind: band` を CMoA に実装して使う

CMoA ADR 0009 が予約だけしていた `band` を実装する（同じ PR の中で、記録も改める）。コンテナは
`gate.csv` を標準出力に出し、`cmoa verify` が読む：`fail` 行があれば fail、CSV が無い・壊れている、
または fail 行なしで終了コードが非ゼロなら runner_error（ハーネスの故障）、それ以外は pass。
`skipped` 行は結果 JSON に列挙する。帯域はコンテナ内の Plecto 自身の toml をそのまま使い、CMoA は
再判定しない。exit-code 型で包むこともできたが、「帯域を外れた」と「ビルドが落ちた」を混ぜると
doctor の kill / inconclusive の区別が崩れる。

### D7. k6 を入れる。帯域は変えない。最初に偽陽性率を測る

k6 は静的バイナリで導入は容易で、トークンバケットの正しさは失いたくない。帯域は Plecto のものを
そのまま使い、`reference_runs: 5` で無変更のツリーを 5 回回して、このコンテナ・この計算機での偽陽性率を
まず測る。unhealthy ならそれが事実として vault に残る。帯域を動かすかどうかはその後の別の判断。

### D8. mutant は手書き 7 件

調査が file:line 付きで挙げた 5 件（WASM dispatch への sleep、apikey フィルタへの余分な kv get、
ラウンドロビンの `fetch_add`→`load`、health tick の下限の引き上げ、レートリミットへの余分な kv probe）を
`expect: killed`、コールドパスと計測対象にリンクされないクレートの 2 件を `expect: equivalent` にする。
それぞれどの帯域を外すはずかを `note` に書く。3 つの tail 系帯域は幅が広く、微妙な mutant では
外せないことが分かっている——それも記録に残す。

### D9. 逐次実行。GitHub Actions では回さない

性能ゲートは CPU を共有すると測定が壊れるので doctor は `--parallel 1` で回す（band の Task では
既定で 1 に落とし、明示されたときだけ従う）。実行は所有者の計算機で行い、結果は vault の
`verifier` 文書として残す。GitHub Actions には入れない：hosted runner の分散は帯域の幅より大きく、
無変更でも外れる。self-hosted は公開リポジトリで推奨されず、ログに計算機の情報が出る。
「情報提供の定期ジョブ」という折衷はあるが、所有者はローカルのみと判断した。

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

- PlectoProxy `bench/perf/run-perf.sh`、`gate_verdict.py`、`gate_tolerances.toml`、`performance/README.md`
  （2026-09-05 読了、固定コミット 39778ec）。
- GitHub Docs：hosted runner の仕様、self-hosted runner と公開リポジトリの注意。
- Laaber et al., "Software microbenchmarking in the cloud. How bad is it really?"（EMSE 2019）：
  同一インスタンス上の A/B 同時計測なら 10% 以下の劣化も検出できる。CodSpeed（2025-07）：GitHub runner で
  CV 2.66%、2% のゲートでは偽陽性 45%。github-action-benchmark の既定閾値 200% とその理由（ネットワークや
  I/O を含むベンチは分散が大きい）。
- k6 docs：負荷生成器に 20% の CPU 余裕がないと応答時間が人工的に伸びる。
- CMoA ADR 0009 D3 は band を実装済みとして同じ PR の中で改めた。

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

- **PlectoProxy に Task を置く。** 不採用（所有者）。他プロジェクトの OSS に評価基盤の都合を持ち込む。
- **exit-code 型 + ラッパースクリプト。** 不採用。故障と逆脱が混ざる（D2）。
- **CMoA 側の tolerances で再判定する band。** 不採用。帯域が Plecto と二重になる。
- **k6 なし。** 不採用。短くなるが正しさの不変式を失う。
- **帯域をコンテナ基準に再中心化してから測る。** 不採用。先に測る。何を測ったか分からなくなる。
- **GitHub Actions の定期・非ブロッキングジョブ。** 見送り（所有者）。ローカルの結果を vault で共有する。
- **self-hosted runner。** 不採用。公開リポジトリでの安全性と、ログへの計算機情報の露出。

## 影響とトレードオフ

- 得るもの：verifier の健全性が数値と語で記録され、Step 5 の run が「健全な verifier で測った」ことを
  辺で要求できるようになる。mutant の生成が決定的で、kill rate が再現できる。
- 失うもの：LLM 生成 mutant の多様性。AST 演算子は Go にしか効かない。
- リスク：`kill_rate_min` の既定 0.8 は慣例（Stryker の high 80）から借りたもので、この repo での実測に
  基づかない。最初の数 Task で見直す。`band` の予約語が長く実装されないと形骸化する。

- 得るもの：doctor がテスト通過型と帯域判定型の両方で動くことの実証。verifier の偽陽性率が
  「README の逸話」から「vault の測定」になる。
- 失うもの：1 回の doctor に約 1.5 時間かかる。Task が固定コミットに縛られ、Plecto の更新に手で追随する。
- リスク：偽陽性率が高ければ、この Task の verifier は uzushio の規範上「無い」ことになる。それは
  失敗ではなく、この記録が測りたかった答えである。
- 最初の測定（2026-09-05、`verifier/plecto-gate@2026-09-05`）：reference 5 回すべてが同じ 4 帯域
  （`dispatch_floor_us`、`apikey_cost_us`、`ratelimit_tax_us` は上に、`pooled_tail_p50_ms` は下に）を外し、
  回ごとの散らばりは帯域の幅よりはるかに小さかった。つまりノイズではなくホストのオフセットであり、
  帯域はこの環境では再中心化しなければ verifier として使えない。reference が落ちる以上、mutant の
  「killed」は検出の証拠にならない——doctor はその場合 kill rate を証拠として扱わないことを明示する。
  ホスト非依存の 5 不変式（RR、ejection、token bucket）は 5 回とも帯域内だった。

## 関連ADR

- 0005（supersede 元）、0002（設定を Go で持つ）、0003（verdict を語に落とす考え方）、0004（型から文書を書く）
- CMoA ADR 0009（`cmoa verify`、task.json v2、band）
