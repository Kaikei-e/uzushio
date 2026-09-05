---
title: "帯域判定型 verifier の帯域は Task 側で許容区間として再中心化し、汎用ツールは bands.json を出すだけで、ゲート固有の形式は Task のアダプタが作る"
status: accepted
date: 2026-09-05
depends-on: [0006]
---

# 0007: 帯域判定型 verifier の帯域は Task 側で許容区間として再中心化し、汎用ツールは bands.json を出すだけで、ゲート固有の形式は Task のアダプタが作る

## ステータス

Accepted

採択日: 2026-09-05

## 日付

2026-09-05

## コンテキスト

### 最初の測定が言ったこと

0006 の最初の doctor 実行（`verifier/plecto-gate@2026-09-05`）は、無変更の Plecto が 5 回とも同じ 4 帯域を
外すことを示した。回ごとの散らばりは帯域の幅より一桁小さく、ノイズではなくホスト（コンテナ、CPU の
割当）のオフセットである。Plecto の `gate_tolerances.toml` 自身が「帯域はホスト相対。新ホストでは
2〜3 回回して value / ci_half を読み、lo / hi を再中心化する。半幅は観測したインターリーブ半レンジの
3 倍以上」と書いている。所有者は、この verifier を成立させる方法を見つけると決めた。

### 調査で分かったこと（2026-09-05）

- `run-perf.sh` は tolerances のパスをハードコードしており、環境変数も引数もない。`gate_verdict.py` は
  `argv[2]` で toml を受ける。
- **`ci_half` は回間の散らばりではない。** 3 ラウンドのラダーでは回間の散らばりを 3〜4 倍に過大評価し、
  2 ラウンドのレートリミット対では 2 倍に過小評価する。Plecto の「3 × ci_half」規則を n=5 に当てると、
  `apikey_cost_us` の帯域は [1.09, 3.02] になり、何も捕まえない。
- n=5 の `mean ± 3s` は 99.7% ではなく **95/73** の区間である。将来の 1 回の観測を確率 p で含む区間は
  許容区間（tolerance interval）で、正規分布なら `median ± k₂(n, p, γ) · s`、n=5、p=0.95、γ=0.90 で
  k₂ = 4.164（NBS Handbook 91 Table A-6 / ISO 16269-6）。ノンパラメトリックな許容区間は n=5 では
  成立しない（95/95 には n=93 が要る）。
- 変化点検出（Daly et al., MongoDB）は「1 回の新しい結果が回帰か」を決めるには適さない。定期的な
  ドリフト検査には合う。
- report.json を再解析すると、正直に再中心化した後に死ぬ mutant は 5 件中 2 件だけである。0002（余分な
  kv get 1 回）は apikey_cost_us を +0.128 µs（2.6σ）しか動かさず、無関係な mutant のノイズ振れ（+0.21 µs）
  より小さい。0005 は 0.7σ。0004 は ejection_transition_s を 1 バケット（1 s）動かすだけで帯域 [0, 2] の内側。
  0001 の 50 µs の sleep はワーカースレッド間で重なり、1.59 µs/req しか効かない——sleep の長さで mutant の
  寸法を測ってはいけない。
- `enforce_allowed_ratio` の中心は算術的に決まる（refill + capacity / window）。測定値を較正で中心に
  据えると、期待値ではなく観測値を規範化してしまう。
- コンテナは `PLECTO_CPUSET` の既定 `0-3` で動いた。CPU の割当は最も効くつまみで、変えれば較正は
  やり直しである。

### 所有者の訂正：汎用と固有を切り分ける

最初の実装は汎用のはずの `task calibrate` に実例のゲートの知識（TOML の形式、除外する不変式の名前、
tolerances ファイルのパス）を持ち込んでいた。uzushio の「汎用」は「どのハーネスにも適用できる規約」の
意味（0001 の非目標）であり、特定の OSS の都合を汎用ツールに入れるのは境界の侵犯である。

## 決定

### D1. 汎用：`uzushio task calibrate` は reference の run から帯域を導き、`bands.json` だけを出す

入力は doctor の `report.json`（複数可）の reference run の band 行。元の帯域は band 行の `band_lo` /
`band_hi` から読む（run 間で食い違えば拒否）。不変式ごとに：

- 中心 = 観測値の中央値
- 半幅 = max(k₂(n, 0.95, 0.90) × 標本標準偏差、単位ごとの絶対床（µs は 0.1、ms は 0.005）)
- lo / hi = 中心 ∓ / ± 半幅（両側。`--lower keep|derive|zero`、既定は両側）
- 観測値が全部元の帯域の内側にある不変式は元の帯域をそのまま写す（ホスト非依存の不変式を広げない）
- `--keep <name>` で宣言された不変式は常に元の帯域を写す（中心が算術的に決まる場合など。名前は Task が
  宣言し、ツールは知らない）
- 整数値の不変式が帯域を外れていれば導出せず拒否する
- n < 5 は拒否。k₂ は n = 5〜12 の表で持つ

出力は `bands.json`：`task`、`rev`、`source_runs`、`day`、`n`、`rule`（統計量、p、γ、k₂、床、下限の方針）、
不変式ごとの `lo` / `hi` / `centre` / `half_width` / `three_ci_half`（比較用、使わない）/ `kept` / `reason`。
計算機の情報は書かない。ゲートが読む形式は知らない。

### D2. 固有：Task のアダプタが `bands.json` をゲートの形式に変える

Task はゲートの実行スクリプト（アダプタ）を持ち、実行直前に `bands.json` をそのゲートが読む形式に
描き、worktree 内の帯域ファイルを置き換える。worktree は候補のコピーなので、対象 OSS の帯域はそのまま
残り、対象 OSS には何も足さない（0006 D5）。ゲート固有のキー（例：パラメータ付きの表）を保つのは
アダプタの責務である。Task の環境（イメージ、ツール、CPU 割当）もアダプタも `examples/<task>/` に閉じる。
汎用コード（`internal/`、`cmd/`、ルート README）に対象 OSS の名前やファイル名が現れてはならない。

### D3. 汎用：mutant は帯域幅に対して寸法を決める

mutant が 1 回の実行で捕まるのは、その効果が半幅とノイズを足したものより大きいときである。規則として
**効果 ≥ 5 × 導いた半幅** を要求し、`note` に寸法の根拠を書く。効果量は帯域の単位で見積もる——sleep の
長さや呼び出し回数ではなく、測定量の変化で。較正で帯域が狭まれば、以前は捕まっていた mutant が
見えなくなることがあり、そのときは mutant を強めるのであって帯域を広げるのではない。

### D4. 汎用：環境の指紋と再較正の条件

`report.json` は環境の指紋（`verify` ブロック、compose ファイル、compose が Task ディレクトリから
マウントする全ファイル、`build` の Dockerfile、イメージ id）を持つ。`--reuse-reference` は Task の id と
rev と指紋が一致するときだけ前回の reference を再利用する。CPU 割当・イメージ・アダプタが変われば
指紋が変わり、reference は再測定され、帯域は再較正の対象になる。較正集合と同じ run で帯域を検証するのは
循環なので、検証は新しい実行で行う。

### D5. 実例：PlectoProxy の T1 ゲート（`examples/task-plecto-gate`）

- 較正集合は最初の doctor の reference 5 回（`PLECTO_CPUSET=0-3`）。k₂ = 4.164。導いた帯域：
  `dispatch_floor_us` 8.50〜9.03（元 2.0〜4.6）、`apikey_cost_us` 1.85〜2.26（元 0.3〜1.2）、
  `pooled_tail_p50_ms` 0.0098〜0.0416（元 0.04〜0.20）、`ratelimit_tax_us` 6.75〜12.80（元 2.2〜4.2）。
  他の 6 不変式は元のまま。`enforce_allowed_ratio` は `--keep` で宣言する。
- アウトオブサンプルの 6 回目で 10 不変式すべてが帯域内。較正後の doctor（`verifier/plecto-gate@2026-09-05-1`）
  は reference 5 回すべて帯域内（偽陽性 0）、mutant 4 件 killed、equivalent 2 件は生存（正しい）、0005 が生存。
  0005 の 32 回の `kv.get` は結果未使用でコンパイラに消され、効果ゼロだった——mutant の欠陥であり、
  verifier の欠陥ではない。`black_box` で観測可能にしても生存し、直接計測で `kv.get` 1 回は 14.5 ns
  （調査の推定 0.50 µs の約 1/35。1 回のノイズ読みから推定していた）と分かった。5 × 半幅 = 15.1 µs には
  1042 回が要るので 1200 回にし、単発で 28.0 µs（帯域上限 12.8）で killed を確認。「効果量は測って決める」
  （D3）の実例である。
- 3 回目の doctor（`verifier/plecto-gate@2026-09-05-2`）：reference 5 回すべて帯域内、mutant 5 件 killed
  （0001 dispatch 10.5 µs、0002 apikey 3.07 µs、0003 ejection 6 s、0004 ejection 5 s、0005 ratelimit 26.0 µs）、
  equivalent 2 件生存、kill rate 1.00、**verdict healthy**。この環境で、このゲートは verifier として成立した。
- アダプタ `gate.sh` が `bands.json` から Plecto の `gate_tolerances.toml` を worktree 内に描く。
- mutant の寸法：0001 は 1.59 µs/req（半幅の 6.0 倍）でそのまま、0002 は kv get 1 回→8 回（5.0 倍）、
  0005 は観測可能な kv get 1200 回（実測 28 µs、5 倍超）、0004 は health tick の床 1200 ms → 3000 ms（transition が帯域上限 2 s を
  確実に超える）。
- CPU 割当は変えない。変えれば較正からやり直す。

## 根拠（調査結果・出典）

- PlectoProxy `bench/perf/gate_tolerances.toml` ヘッダ（再中心化の指針）、`run-perf.sh`（tolerances パスの
  ハードコード）、`gate_verdict.py`（`argv[2]`）。
- NBS Handbook 91 Table A-6、ISO 16269-6：正規分布の両側許容区間の係数 k₂。
- Daly et al., "Creating a Virtuous Cycle in Performance Testing at MongoDB"（変化点検出の適用範囲）。
- Bencher / Perfherder / benchstat の閾値の慣行（最小標本数、t 検定、境界）。
- doctor の `report.json`（20260905T002350Z-e13205e7）の再解析：ci_half と回間散らばりの乖離、mutant の効果量。

## 検討した代替案

- **汎用ツールがゲートの形式（TOML）を直接書く。** 不採用（所有者の訂正）。汎用ツールに対象 OSS の
  知識が入る。`bands.json` とアダプタに分けた。
- **除外する不変式をツールに名前で持たせる。** 不採用。`--keep` で Task が宣言する。
- **ゲート自身の「3 × ci_half」規則をそのまま使う。** 不採用。ci_half は回間の散らばりではなく、n=5 では
  捕まえない帯域を作る。
- **`mean ± 3s`。** 不採用。n=5 では 95/73 の区間で、名前ほどの保証がない。
- **ノンパラメトリック許容区間・MAD。** 不採用。n=5 では成立しない／自身の較正集合を拒否する。
- **中央値 ± 固定割合。** 不採用。不変式ごとのノイズの違いを無視する。
- **対象 OSS に tolerances パスの環境変数を足す PR。** 見送り。汎用の機能として別途提案する価値はある。
- **`PLECTO_CPUSET` を先に広げる。** 見送り（D5）。較正集合を作り直す別の作業。

## 影響とトレードオフ

- 得るもの：帯域判定型 verifier を、対象 OSS を変えずに、この環境で偽陽性 0 に較正できる規則と手順。
  汎用部分（許容区間、`bands.json`、指紋、mutant の寸法規則）は他のゲートにそのまま使える。`--only` と
  `--reuse-reference` で通常の運用は短くなる（mutant 1 件で約 7 分）。
- 失うもの：帯域が Task 固有になり、Plecto の帯域とは別物になる。較正のたびに約 35 分の測定と、
  検証のたびに約 1.5 時間がかかる。
- リスク：n=5 の許容区間は広く、小さな回帰は見逃す。強めた mutant は「大きな回帰は捕まえる」ことしか
  示さない。n を増やす（7〜10）と帯域は狭まる。CPU 割当を変えれば全部やり直しである。

## 関連ADR

- 0006（band verifier と最初の外部 Task）、0005（task doctor、supersede 済み）、0001（汎用の意味）
- CMoA ADR 0009（band の契約）
