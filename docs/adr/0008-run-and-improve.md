---
title: "run と improve：ハーネスは vault から描き、edit は対応ありの逐次検定で判定し、自律度に従って受理する"
status: accepted
date: 2026-09-05
depends-on: [0003, 0006, 0007]
---

# 0008: run と improve：ハーネスは vault から描き、edit は対応ありの逐次検定で判定し、自律度に従って受理する

## ステータス

Accepted

採択日: 2026-09-05

## 日付

2026-09-05

## コンテキスト

### 因果の欠落

0003 は edit / pattern / run の語彙を、0005〜0007 は verifier の健全性を定めた。しかし CMoA の提案者が
見るプロンプトは固定のテンプレートと Task の指示・ファイルだけで、vault の binding な edit は run.json に
記録されるだけで**中身がどこにも届かない**（`propose.go` はスナップショットを取った後 `prompt.Build(t)` に
それを渡していない）。この状態で `run` を書けば、同じハーネスを 2 回測って差を語ることになる。
Step 5 の第一の仕事は、edit が提案者の受け取るバイト列を変える経路を作ることである。

### 調査で分かったこと（2026-09-05）

- 自己改善の研究（AHE、Self-Harness、Meta-Harness、ACE、MCE）はいずれも、ループが編集する成果物と
  エージェントが読み込む成果物が同一である。AHE はコンポーネントごとのファイルを `code_agent.yaml` に
  登録して有効化する。どの研究もハーネスのファイルに diff を交換していない。ACE は追加専用の箇条書きを
  決定的にマージし、全面書き換えによる「文脈崩壊」を測っている。
- Claude Code の面は 7 面のうち 6 面がファイルとして書けば次回の起動で効く。`tool-description` は
  MCP の `tools/list` 由来で上書き口がなく、`tool-implementation` に潰れる。
- 小標本の統計：10〜30 Task × 1〜5 反復では 10 ポイントの差は検出できず、20 ポイントは対応あり設計で
  やっと検出できる。事後確率 P(θ>½) ≥ 0.95 を毎試行見る Bayes 規則は無変更の edit を 26% 採用する。
  不一致ペアに対する beta 混合 e-process（逐次 McNemar）は任意時点で妥当（Ville の不等式）で、
  `math.Lgamma` だけで書ける。Self-Harness の受理条件「両 split で非回帰 ∧ 一方で改善」は、
  非劣性（交差、補正不要）と優越（合併、補正が要る）に分解できる。
- DocDag の `append_only` は accepted / superseded / withdrawn の文書だけを凍結する。proposed な edit の
  diff は差し替えられ、accepted のものは差し替えられない——「後継が前任を置き換える」Gerrit 型の
  系譜が既に強制されている。`docdag query --binding` は id 順に返す。
- 今のトレースで掘れる失敗はすべて出力契約（diff の形式）の違反で、`system-prompt` 面に帰属する。

### 所有者の判断

Step 5 の対象は CMoA のコード面。ハーネス状態は git のベースラインではなく **vault から毎回合成**する。
edit の表現は**ハイブリッド**：加法的な面（memory、skill）は edit 文書の本文が内容、system-prompt だけ
サイドカーの diff。seed は空。Task コーパスは手書きの Go 課題 36 件（held-in 24 / held-out 12）。
統計は対応あり・逐次。受理は CMoA ADR 0008 の自律度に従う。Step 6 の制約は規範の clause と適合テスト。

## 決定

### D1. 汎用：描いたハーネスディレクトリを CMoA が読む

`uzushio harness render --vault --as-of [--with-edit] [--without-edit] --out <dir>` が binding な edit から
ディレクトリを描く：`system-prompt.md`（あれば CMoA のテンプレートの後に追記）、`memory/*.md`
（user プロンプトの Notes 節、ファイル名順）、`skills/<name>/SKILL.md`（名前と説明の一覧だけ。本文は
描かない——実在のハーネスの挙動に合わせる）。seed は空ディレクトリで、CMoA の既存テンプレートが土台。
CMoA は `cmoa propose --harness <dir>` で読み、読んだ木のハッシュ（`harness.render.tree_sha256`）を
run.json に記録する。CMoA は vault の中身を解釈しない。vault の `effective` 射影が AHE の
`code_agent.yaml` に当たる：ディレクトリにあるだけのファイルは効かず、binding な edit が置いたものだけが効く。

### D2. 汎用：edit の表現はハイブリッド

- `edit` は `paths`（触るファイル。必須。DocDag は list の存在を検査できないので uzushio が検査）と
  `diff_sha256`（system-prompt 用）を持つ。`touches` は `paths` から導き、食い違いは拒否。
  `memory/**` → memory、`skills/**` → skill、`system-prompt.md` → system-prompt。他の 4 面は v0 に
  注入口がなく、`run` はその edit を拒否する。
- memory / skill の edit：文書の本文がそのファイルの内容。paths は 1 つ。既存の内容を変えるのは新しい
  edit で `supersedes` する（前任が binding から外れると、そのファイルは描かれなくなる）。衝突は構造的に
  起きない。
- system-prompt の edit：`spec/edits/<id>.diff` をサイドカーに持ち、`diff_sha256` をフロントマターに書く
  （閉じた文書の frontmatter を変えれば `immutable_violation`、これが改竄の tripwire）。id の昇順に seed に
  当てる。当たらなければ accepted なら hard error、候補なら run を `inconclusive`（`apply_failed_on_baseline`）
  にする。
- 描画は決定的で検証可能：`render.json` に as_of / at、edit の順序と各 sha256、seed の sha256、
  木の sha256、ファイル一覧、描画器の版を書く。

### D3. 汎用：`run` は対応ありの逐次検定

- 1 試行 = 同じ Task・同じ seed（`hash(task, k)`）・temperature 0 で、ベースライン（binding as-of 今日）と
  候補（ベースライン + edit）の両方を `cmoa propose --harness` → `cmoa select` で回し、pass = `selected`。
  ペアの結果は +1 / −1 / 0。ベースラインの結果は (task, seed, 木の sha256) でキャッシュする。
- 検定は不一致ペアに対する beta 混合 e-process（a = 1）、split × 方向で 4 本。α = 0.05。優越は
  split ごとに E ≥ 2/α（合併なので Bonferroni）、非劣性（マージン m）と有害は E ≥ 1/α（交差）。
  m = 0.15（screening）/ 0.10（確認）。cap は split あたり 60 / 150 ペア、反復 K = 3 / 5。
  カウントゲート：**反復をすべて終えて全反復で負けた Task が held-out に 1 つでもあれば**統計に関わらず
  regress（k = 0）。当初案の「正味 1 Task を超える回帰」は途中経過の Task も数えるため、無害な edit を
  不一致率 0.2 で約 8 割却下することがレビューのシミュレーションで分かり、改めた。判定は
  regress → improve → hold → inconclusive の順。毎試行後に評価し、決まれば止める。held-in を先に決め、
  示せない候補は held-out の予算を使わない。
- δ の信頼列は不一致率の点推定を代入せず、同時境界で作る（代入版は「全不一致ペアで負けた」edit を hold と
  読む）。その結果 m = 0.15 は 60 ペアでは証明できないことがあり、`hold` は証明できたときだけ出す——
  inconclusive が増えるのは正直な帰結で、マージンや cap を動かすのは別の判断。
- verifier が動かなかった試行（runner error、Docker 不在）はペアを「未測定」にし、負けにも勝ちにも数えず、
  キャッシュにも入れない。
- 最初に A/A 較正（ベースライン vs ベースライン、約 30 ペア）で不一致率 d̂₀ を測り、run のヘッダに
  記録する。d̂₀ > 0.4 なら inconclusive 以外の判定は意味を持たないと警告する。
- 記録：試行ごとの追記専用 JSONL（決定を再導出できる全項目）、run ヘッダ（パラメータと決定規則の
  文字列）、split ごとの `run` 文書（`validates` の model はプール名、pass_rate と baseline_pass_rate）。
  `--replay` は JSONL から判定を再計算し、一致しなければ失敗する。
- 事前登録：edit 文書の最終コミットを run に記録する（未コミットなら `uncommitted`）。
- 候補が複数のときは今は順次。successive halving は候補が増えてから。

### D4. 汎用：受理は自律度に従う

両 split で非回帰かつ一方で改善なら、面の自律度（`cmoa surfaces`）が auto-accept（memory、skill）の
edit は `status: accepted`、`approval: auto` にする。human-approval（system-prompt）は proposed のまま
残し、人が `approval: human` を書いて accepted にする。regress は常に rejected。proposed 文書の
frontmatter の変更は `append_only` に触れない。

### D5. 汎用：`improve` は決定的に掘り、`cmoa propose` に提案させる

トレースの状態語（candidates の status、verify の status、select の kind、apply_error の形）から
規則で `pattern` を作る。LLM は使わない。同じ形の失敗は 1 つの pattern に集約し、evidence に run-id を
足す。基盤の障害（接続拒否）は pattern にしない——どんな edit も直せない。
提案は `cmoa propose` を、描いたハーネスを repo とする Task で呼ぶ。指示は pattern と AHE の manifest
要件（根本原因・修正・予測）。候補の diff から paths と component を導き、memory / skill なら本文、
system-prompt ならサイドカー diff として `proposed` の edit を書く。`predicts` は `expect: fix`。
複数面に触る候補は捨てる。

### D6. 実例：コーパスと最初の測定

Task コーパスは `examples/suite-go`（Go 課題 36 件、held-in 24 / held-out 12、`suite.json` に固定）。
各 Task は task-hello と同じ形で、`task doctor` が healthy であることを要求する。ローカルの提案者
プールの通過率を BASELINE に記録し、難易度の幅（おおよそ 0.2〜0.9）を確かめる。

### D7. Step 6：前のプロジェクトの制約を規範にする

UZ-C-006（テスト行数は製品行数の 3.0 倍を超えない。対象は uzushio と CMoA。実測 0.85 / 0.76）、
UZ-C-007（PR は roadmap の step 1 つに対応する。`Step N` の印を 1 つだけ）、UZ-C-008（held-out の Task は
この標準のツールチェーン自身のコードを使わない）。いずれも MUST で、適合テストが強制する。根拠は
pm-0002（測られなかった中核価値、成果物の形にしか効かなかった「ミニマル」、クリティカルパス上の
ドッグフーディング）。

## 根拠（調査結果・出典）

- AHE 2604.25850（`code_agent.yaml`、manifest の files、system-prompt 単独編集の回帰）、Self-Harness
  2606.09498（受理条件、宣言された空の面、K=2、split 固定）、Meta-Harness 2603.28052、ACE 2510.04618
  （文脈崩壊）、MCE 2601.21557。
- Miller 2411.00640、Bowyer ら 2503.01747、Wang 2512.21326（対応あり分析、小標本の限界）。
  Johari ら 1512.04922、Ramdas ら 2210.01948（任意時点妥当な推論）、Deng ら 1602.05549（Bayes 逐次の
  停止規則）、Berger 1982（交差合併検定）。
- DocDag v0.4.0 `internal/graph/immutable.go`（凍結される status）、`query --binding` の順序。
- CMoA `internal/propose/propose.go`（因果の欠落の箇所）、CMoA ADR 0008（自律度）、0009（`verify`）。
- Claude Code の文書（CLAUDE.md、skills、agents、hooks；MCP の `tools/list`）。

## 検討した代替案

- **Claude Code のハーネスを直接対象にし `claude -p` で検証する。** 見送り。課金と非決定性。
  CMoA のコード面で経路を作り、同じ形のファイルを後で渡す。
- **ハーネスを git で管理し accepted の edit を適用してコミットしたものをベースラインにする。** 不採用
  （所有者）。vault から合成する方が `--as-of` で過去のハーネスを再現でき、系譜と状態が一致する。
- **全部 diff。** 不採用。加法的な面まで diff にすると衝突と順序の問題を持ち込む。
- **全部本文。** 不採用。system-prompt の既存文の削除・書き換えが表せない。
- **事後確率の閾値で判定する。** 不採用。無変更の edit を 26% 採用する。
- **Wald の SPRT。** 不採用。効果量の事前指定と打ち切り時の劣化。e-process は同じ保証で peeking が自由。
- **skill の本文をプロンプトに入れる。** 不採用。実在するハーネスの挙動と違い、トークンも増える。
- **seed に出力契約の memory ノートを置く。** 不採用（所有者）。seed は空。最初の edit がそれを足す
  のはループの仕事で、auto-accept の面に自然に乗る。
- **既存リポジトリの履歴や Exercism から Task を切り出す。** 見送り（所有者）。手書き 36 件。
- **LLM でトレースをクラスタリングする。** 不採用。決定的な規則で始める。

## 影響とトレードオフ

- 得るもの：edit が提案者のプロンプトを実際に変え、その効果が対応あり・任意時点妥当な検定で判定され、
  受理が自律度に従って自動化される。ハーネスの状態は vault から日付付きで再現できる。
- 失うもの：小標本では判定の多くが inconclusive になる。1 試行に数十秒〜数分かかり、1 edit の screening
  で数十分〜数時間。system-prompt の edit は人の承認を待つ。
- リスク：d̂₀ が高ければ（fleet の非決定性）検出できる効果がさらに小さくなる。`cmoa propose` が書く edit
  の質は 8〜12B の提案者に依存する。36 Task の難易度分布が偏れば統計が効かない。

## 関連ADR

- 0003（語彙）、0006（band）、0007（較正）、CMoA ADR 0008（自律度）、0009（verify）、0010（harness 注入）
