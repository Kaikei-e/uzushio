# BASELINE

What the local three-proposer fleet scored on every task in this suite,
measured once so that a later number has something to be compared with.

## How it was measured

For each task, `cmoa propose` was run against the fleet and `cmoa select`
then verified **every** candidate it produced, not just the first that
passed. Two numbers come out of that:

- the **union** rate: repetitions in which `select.json` recorded a
  `selected` candidate, that is, in which at least one of the three
  proposers produced a diff that applied and made the task's container
  exit 0;
- the **per-proposer** rate: for each proposer separately, the repetitions
  in which that proposer's own diff passed.

The union rate is what a run of the whole fleet achieves, and it saturates:
three tries at one problem mostly succeed. The per-proposer rate is the one
that says how hard a task is, and it is the column to read for that.

The fourteen tasks written first were measured over three repetitions and
the twenty-two added afterwards over two, to keep the wall clock down; the
`union` column carries the denominator. Proposers are named by the ids the
fleet configures. The models behind those ids, and the machine they ran on,
are not part of this record.

Every task was measured against a verifier `uzushio task doctor` had
already found **healthy**: the reference accepted on every run, all three
mutants killed, kill rate 1.00 against a floor of 0.80.

## Summary

- tasks measured: **36** (24 held-in, 12 held-out)
- per-proposer pass rate over the whole suite: granite **0.29**, qwen
  **0.58**, gemma **0.84**
- median per-task, per-proposer rate: **0.67**
- union rate: **74** of **86** repetitions
- tasks no proposer ever solved: **4** — `csv-import`, `iso-week`, `name-match`, `retry-backoff`
- tasks every proposer solved every time: **8** — `usage-share`, `permission-mask`, `session-cache`, `leaderboard-rank`, `report-builder`, `feed-producer`, `request-timeout`, `ingest-waitgroup`

The three proposers land at roughly 0.3, 0.55 and 0.85, which is the band
this suite was aimed at. The spread is between proposers as much as between
tasks, and that is the useful part: a task several proposers reach and one
does not is a task where a harness change has room to move the number.

## held-in

| task | bug class | union | granite | qwen | gemma | how the rest failed |
| --- | --- | --- | --- | --- | --- | --- |
| `inventory-reserve` | wrong comparison | 3/3 | 0/3 | 3/3 | 3/3 | apply_failed 3 |
| `usage-share` | integer vs float division | 2/2 | 2/2 | 2/2 | 2/2 | none |
| `tag-index` | nil map write | 2/2 | 0/2 | 2/2 | 2/2 | apply_failed 2 |
| `label-truncate` | rune vs byte | 3/3 | 0/3 | 3/3 | 3/3 | apply_failed 3 |
| `poll-interval` | time.Duration unit | 2/2 | 0/2 | 0/2 | 2/2 | apply_failed 2, fail 2 |
| `json-field-tag` | JSON field tag | 2/2 | 0/2 | 2/2 | 2/2 | fail 2 |
| `route-table` | wrong map key | 1/3 | 0/3 | 0/3 | 1/3 | apply_failed 5, no_diff 3 |
| `session-cache` | missing nil check | 3/3 | 3/3 | 3/3 | 3/3 | none |
| `config-loader` | error wrapping | 3/3 | 0/3 | 2/3 | 3/3 | apply_failed 2, fail 1, timeout 1 |
| `store-notfound` | error sentinel comparison | 2/2 | 0/2 | 2/2 | 2/2 | fail 2 |
| `object-key` | path joining | 2/2 | 0/2 | 0/2 | 2/2 | apply_failed 2, fail 2 |
| `name-match` | Unicode case folding | 0/2 | 0/2 | 0/2 | 0/2 | apply_failed 4, no_diff 2 |
| `leaderboard-rank` | sort stability | 3/3 | 3/3 | 3/3 | 3/3 | none |
| `report-builder` | builder reuse | 2/2 | 2/2 | 2/2 | 2/2 | none |
| `release-order` | comparator that is not an ordering | 2/2 | 0/2 | 2/2 | 2/2 | apply_failed 2 |
| `feed-producer` | channel never closed | 2/2 | 2/2 | 2/2 | 2/2 | none |
| `request-timeout` | context cancellation | 2/2 | 2/2 | 2/2 | 2/2 | none |
| `batch-close` | defer inside a loop | 2/2 | 0/2 | 2/2 | 2/2 | apply_failed 2 |
| `price-lookup` | off-by-one in a binary search | 2/2 | 0/2 | 2/2 | 0/2 | apply_failed 2, fail 2 |
| `callback-url` | URL query escaping | 2/2 | 0/2 | 0/2 | 2/2 | apply_failed 4 |
| `audit-buffer` | slice aliasing | 3/3 | 0/3 | 2/3 | 3/3 | apply_failed 3, fail 1 |
| `version-compare` | version comparison | 2/2 | 0/2 | 0/2 | 2/2 | fail 2, apply_failed 2 |
| `retry-backoff` | integer overflow guard | 0/3 | 0/3 | 0/3 | 0/3 | apply_failed 5, no_diff 3, fail 1 |
| `session-lru` | LRU eviction order | 2/2 | 0/2 | 2/2 | 2/2 | apply_failed 2 |

## held-out

| task | bug class | union | granite | qwen | gemma | how the rest failed |
| --- | --- | --- | --- | --- | --- | --- |
| `csv-import` | string trimming | 0/3 | 0/3 | 0/3 | 0/3 | fail 9 |
| `cursor-pagination` | off-by-one at a boundary | 3/3 | 3/3 | 2/3 | 3/3 | fail 1 |
| `permission-mask` | bit mask off-by-one | 2/2 | 2/2 | 2/2 | 2/2 | none |
| `iso-week` | ISO week edge | 0/2 | 0/2 | 0/2 | 0/2 | apply_failed 4, no_diff 2 |
| `ledger-rounding` | rounding rule | 3/3 | 0/3 | 0/3 | 3/3 | apply_failed 3, no_diff 3 |
| `worker-fanout` | closure over a shared variable | 2/2 | 0/2 | 0/2 | 2/2 | apply_failed 2, no_diff 2 |
| `shift-scheduler` | time zone handling | 3/3 | 3/3 | 0/3 | 3/3 | fail 2, apply_failed 1 |
| `metrics-counter` | missing mutex | 3/3 | 0/3 | 3/3 | 3/3 | apply_failed 3 |
| `export-csv` | CSV quoting | 2/2 | 0/2 | 0/2 | 2/2 | apply_failed 4 |
| `event-ring` | ring buffer wraparound | 2/2 | 1/2 | 0/2 | 2/2 | no_diff 2, apply_failed 1 |
| `ingest-waitgroup` | WaitGroup.Add placement | 2/2 | 2/2 | 2/2 | 2/2 | none |
| `tariff-parser` | parser | 3/3 | 0/3 | 3/3 | 3/3 | apply_failed 3 |

## How a candidate fails

The `how the rest failed` column counts the outcomes that were not a pass,
and they are not all the same kind of failure.

- `fail` — the diff applied and the tests still said no. The proposer
  answered the wrong thing. This is the failure the task is about.
- `apply_failed` — the diff did not apply. The proposer may well have known
  the answer and mis-drew the patch: wrong context lines, wrong hunk
  header, spaces where the file has tabs.
- `no_diff` — the completion carried no unified diff at all.
- `timeout`, `http_error` — the proposer never answered.

`apply_failed` and `no_diff` are worth separating out, because they measure
the patch-emitting half of the loop rather than the reasoning half, and
they are what a harness change is most likely to move. `iso-week`,
`name-match`, `route-table` and `retry-backoff` are at or near zero almost
entirely on those two: their diffs did not apply. `csv-import` is the
opposite — every proposer produced an applying diff every time, and every
one of them was wrong.

## Retuning

Two groups sit at the ends of the range and are the first candidates for a
later pass:

- **too easy** (8): `usage-share`, `permission-mask`, `session-cache`, `leaderboard-rank`, `report-builder`, `feed-producer`, `request-timeout`, `ingest-waitgroup` — solved by all three proposers
  in every repetition, so they cannot show a regression.
- **too hard** (4): `csv-import`, `iso-week`, `name-match`, `retry-backoff` — solved by nobody, so they
  cannot show an improvement, though for three of the four the obstacle is
  the diff format rather than the bug.

They are recorded rather than changed: the numbers above are the first
measurement this suite has, and moving the tasks now would leave nothing to
compare a second measurement against.
