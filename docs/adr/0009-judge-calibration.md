---
title: "審判の較正：信頼性と妥当性を別の κ で測り、扱いの名前を数値に添え、較正は期限付きで binding になる"
status: accepted
date: 2026-09-06
depends-on: [0003, 0005, 0008]
---

# 0009: 審判の較正：信頼性と妥当性を別の κ で測り、扱いの名前を数値に添え、較正は期限付きで binding になる

## ステータス

Accepted

採択日: 2026-09-06

## 日付

2026-09-06

## コンテキスト

### 推論的 grader が現れた

0005〜0007 は計算的 grader（テスト、帯域）の健全性を `task doctor` と `task calibrate` で測ることを決めた。
ロードマップの Step 7 で CMoA にチャット面が付き（CMoA ADR 0011）、選択器が LLM 審判になった。仕様の
grader の格（計算的 ＞ 推論的 ＞ 人間）では、推論的 grader は較正状況で上下し、単独で MUST を支えない。
つまり審判は「verifier がある」側に入るために、その健全性が測られていなければならない。ただし測る量は
kill rate ではない。審判に注入できる欠陥は無く、測れるのは**一致**である。

### 何を一致と呼ぶか

心理測定学の区別に従う。**信頼性**は審判が自分と一致するか——同じ 2 候補を逆順で見せたときの一致
（位置スワップ）、seed を変えて再実行したときの一致。**妥当性**は人間と一致するか。どちらも偶然一致を
除くために Cohen の κ で表す。3 つは 1 つの数に畳まない（arXiv 2606.19544：高信頼・低妥当の審判は
実在する）。

### 調査で分かったこと（2026-09-05〜06）

- **同点・棄権・不正出力の扱いは前処理ではなく別の量の推定**（Agreement Metrics 2606.00093）。扱いを
  変えるだけで報告精度が 0.55 から 0.90 に動き κ が 0 をまたいだ例がある。扱いの名前を全数値に添える。
- **標本数**。3 値の κ ≈ 0.6 で信頼区間 ±0.10 には約 170 項目、200 で ±0.09、50 では ±0.18。
- **公開データ**。人間ラベル・寛容ライセンス・同一プロンプトに 3 モデル以上の応答、の 3 条件を同時に
  満たすのは MT-Bench の人間判定（`lmsys/mt_bench_human_judgments`、CC-BY-4.0、80 問 × 6 モデル、
  3,355 判定）だけ。Arena 系はプロンプトごとに 1 ペアしか無く 3 値化できない。GPT-4 ラベルの
  データ（UltraFeedback 等）を妥当性の gold に使うのは審判を審判で測ることになる。
- **pairwise から 3 値項目を導く**には、3 ペアすべてに人間判定があり、多数決が非巡回な三つ組だけを
  採り、巡回は解決せず破棄して破棄率を記録する（真値が無い項目にラベルを付けると妥当性の κ にそのまま
  ノイズが乗る）。Bradley–Terry の強度差で層化しないと、易しい項目だけで κ が膨らむか難しい項目だけで
  0 に潰れる。
- **妥当性の測定が途絶える**のは Bainbridge の「自動化の皮肉」の入口で、警告が要る。DocDag には日付の
  算術が無いが、`period.in_force_until` と `--as-of` はある。

### 所有者の判断

公開データで種を撒き（英語で妥当性）、所有者はブラウザのページで日本語 30〜50 件にラベルを付ける
（スモーク）。較正は CMoA の外、uzushio の仕事。

## 決定

### D1. 汎用：kind `calibration`

`spec/calibrations/<judge>@<day>[-n].md`。closed、append_only、機械生成。fields は `judge`（モデルの
スラグ）、`pool`（候補を作った提案者プール、または `external`）、`window_from` / `window_to`、`n_items`、
`tie_handling`（one_of：`abstain-as-category` / `decided-only`）、`swap_kappa`、`rerun_kappa`、
`human_kappa`（`unmeasured` を許す）、`n_human`、`verdict`（one_of：`calibrated` / `uncalibrated` /
`unmeasured`）、`report`（レポートのパス）。`period.in_force_until` は `window_to` + 31 日（DocDag の `until` は排他なので、30 日目まで binding）を
書き手が導く——測定より長生きする較正文書は作れない。同じ審判の新しい較正は直前の binding な較正を
`supersedes` し、`effective` 射影は `has_inforce_successor: false` を条件に持つ——後の `uncalibrated` が前の
`calibrated` を退かせる。期限が来た較正は `effective` 射影から外れ、
`uzushio judge status` が「妥当性が N 日測られていない」、または binding な較正が `uncalibrated` /
`unmeasured` だと警告する（終了コード非 0）。

`judge_unmeasured_accepted`（チャット面の edit を、妥当性の測られた較正が無いのに受理したら error）は
**書かない**。edit は面を名指しできず、「vault 全体に binding な較正が 1 つも無い」は文書単位で評価される
DocDag のルールでは表せない。鳴れないルールを置くと「強制されている」と読まれる。理由は
`internal/vault` に注記した。代わりに UZ-C-009（MUST）：`calibration` 文書は `report:` を持ち、そのパスは
リポジトリに実在する——κ は周辺分布と n と区間が無ければ検査できない数で、それらはレポートにしかない。

### D2. 汎用：3 つの係数、2 つの扱い、全数値に n と区間

- 信頼性／スワップ：ペアごとに 2 順序の `choice_candidate` を 2 評定者と見て、p_o、両評定者の周辺、
  p_e、κ、PABAK、反転率、Wilson 区間。
- 信頼性／再実行：seed 0 の結末と後続 seed の結末（カテゴリは候補 id と `abstain`）。seed 0 を基準に
  片側でプールする——全順序なしペアをプールすると周辺が等しくなり κ が平坦化する。
- 妥当性：結末と gold（または `--labels` の人間ラベル。両方あれば人間ラベルが勝ち、別々にも報告）。
  `tie_handling` は主が `abstain-as-category`（`NoCandidate` も gold の `tie` も 1 カテゴリ）、副が
  `decided-only`（両者が決めた項目だけ）。同じ項目に 2 人以上のラベルがあれば human–human κ を天井として
  併記。ラベルの `all_bad` は `tie` に畳み、件数は別に数える。
- 区間は**項目を単位にした** jackknife（スワップと再実行の行は項目を共有するので、行単位では 3 倍ほど
  狭くなる——レビューで判明）。κ が未定義（周辺が退化）なら区間も `null` で書き、1 や [0, 0] にはしない。重み付き κ は
  置かない（3 値に順序が無い）。閾値は既定 0.667（Krippendorff）で `--min-kappa`。
- `no_candidate` の率をサブ理由別に、不正出力の再送率、遅延の中央値も報告する。
- レポートは `<vault>/calibrations/<judge>@<day>[-n]/report.json` と `items.jsonl`（項目ごとの結末・gold・
  ペアの両順序）。文書本文は人が読む要約で、**すべての数値に扱いの名前と n を添える**。

### D3. 汎用：`uzushio judge` コマンド群

```
uzushio judge import-mtbench --out <dir> [--target 200] [--min-judgments 3] [--seed N]
uzushio judge calibrate --suite <suite.json> --cmoa <bin> --config <cmoa.json> --vault . [--rerun N] [--labels <jsonl>...] [--dry-run]
uzushio judge status --vault .
```

`calibrate` は項目ごとに `cmoa judge --task <dir> --candidate c1.txt --candidate c2.txt --candidate c3.txt
--seed <k>` を呼び、`judge.json`（CMoA ADR 0011 D4 の記録）を読む。`--seed` は提示の nonce だけを動かす（CMoA ADR 0011 D4）。
審判の動かない試行（`judge_timeout` / `judge_failed`）は棄権に数えず、未測定として除き、件数を報告する。
`abstain` はプロトコルの `NoCandidate`（cycle / no_majority / all_draws / invalid_output）だけ。1 件の失敗で
数時間の run を捨てない：未測定のまま続け、未測定率が `--max-unmeasured`（既定 0.1）を超えたときだけ
非 0 で終わる。同じ日の 2 回目は `-n` 付きの別ディレクトリと文書になる。
測れないことが事前に分かる条件（suite が `face: chat` でない、`cmoa` に `judge` が無い、設定に `judge` が
無い）は何も使わずに exit 2。`--dry-run` は計画だけ出す。

`import-mtbench` はデータセット名を持つ唯一のファイル。`internal/pairwise` は「pairwise の人間判定」と
「rows エンドポイント」しか知らない。非巡回多数決の三つ組、Bradley–Terry（MM 反復）の強度差で
wide 40 / mid 40 / narrow 20 % に層化、`--seed` で決定的。巡回三つ組は破棄し、破棄率を DERIVATION.md に
書く。gold は**多数決トーナメントで誰にも負けていない唯一の候補**（1–1 のペアは未決で、敗北ではない）、
それが無いときだけ `tie`。2 ターン目の項目は、人間が判定したもの——各モデルの 1 ターン目の答え、共有の
2 ターン目の発話、2 ターン目の答え——を 1 つの候補（転写の続き）として渡す。当初の「共有 user 発話
2 つだけ」は、審判に見えない 3 つの別々の 1 ターン目への批評を並べることになり、推定量が人間のものと
違っていた（レビューで判明）。候補の本文は原文のまま。

### D4. 実例：`examples/suite-chat` と日本語のスモーク

レビュー後の導出（`--seed 1 --target 200`）：160 プロンプト、3,355 判定、三つ組 3,200 のうち 1,822 は誰も
比べていないペアを含み、32 が巡回、1,346 が適格。200 件を 112 プロンプトから層化抽出（wide 80 / mid 80 /
narrow 40、うち 2 ターン目 90 件）。**巡回率 2.3%**（完全な三つ組 1,378 中 32）——これは人間ラベルの
測定値で、どの審判も到達できない不一致の床。65 件は人間が決めなかった（`gold: tie`）、123 件は Condorcet
勝者、12 件は「負けていないが 1 ペアは未決」、13 件は多数決と BT の適合が食い違う「難項目」。
ATTRIBUTION.md に CC-BY-4.0 と出典（arXiv 2306.05685）。**モデルの応答（候補ファイル）はコミットしない**
（所有者の判断：各社の利用規約の対象。データセットのライセンスとは別）。リポジトリにあるのは設問・
人間ラベル・導出の記録と、応答を取り直す `import-mtbench` だけで、候補が無ければ `calibrate` は
何も使わずに exit 2 で「取り直せ」と言う。

日本語は所有者がブラウザのページで 40 件（MT-Bench の 8 カテゴリ × 5、うち 2 ターン 6 件、候補は
ローカルの提案者プールが生成）にラベルを付け、§1.5 の JSONL として `--labels` に渡す。件数から κ の
区間は広く（±0.2 前後）、スモークテストであって妥当性の測定ではない。

## 根拠（調査結果・出典）

- 信頼性／妥当性の分離 2606.19544、扱いは推定量 2606.00093、Cohen 1960、Krippendorff の閾値、
  Landis & Koch への批判、PABAK（Byrt 1993）。
- MT-Bench 2306.05685（人間判定の公開、GPT-4 のスワップ一致 65%）、Bradley–Terry の MM（Hunter 2004）。
- Bainbridge 1983「自動化の皮肉」。
- CMoA ADR 0011（`judge` コマンド、judge.json、`--seed` と `--judge-seed` の分離）。
- 実装：`internal/stats/kappa.go`（Cohen 1960 の例で検証）、`internal/pairwise`、`internal/judge`。

## 検討した代替案

- **κ を 1 つに畳む／加重 κ**。不採用。3 値に順序は無く、信頼性と妥当性は別の量。
- **巡回三つ組を BT で解決して採用**。不採用。真値が無い項目にラベルを付けることになる。BT は
  非巡回チェックと並走させ、食い違いを「難項目」と印す。
- **GPT-4 ラベルのデータを gold に使う**。不採用。審判を審判で測る（信頼性側には可）。
- **anytime-valid な κ**。見送り。既存文献に無い。将来は p_o の betting 信頼列を固定 p_e の κ に写す
  （PABAK 型）か、anchor-set の e-process（2606.15474）でドリフト警告にする。
- **`judge_unmeasured_accepted` ルール**。見送り（D1）。
- **`task calibrate` の名前空間に置く**。不採用。帯域の較正と審判の較正は別の道具。`judge` グループ。

## 影響とトレードオフ

- 得るもの：チャット面の審判が「どれだけ自分と一致し、どれだけ人間と一致するか」を、扱いの名前・n・
  区間付きで vault に残す。較正は期限付きで、測らなくなれば binding から外れる。
- 失うもの：200 項目 × seed 数 × 6 コールの審判時間（数時間）。人間ラベルは所有者の時間。
- リスク：κ の区間は 200 件でも ±0.09。日本語は件数不足で測定にならない。gold の 32% が `tie` で、
  `abstain-as-category` の κ は「決めない」一致に引かれうる——`decided-only` を必ず併記する理由。

## 関連ADR

- 0003（語彙）、0005〜0007（計算的 grader の健全性と較正）、0008（run と improve）、CMoA ADR 0011
