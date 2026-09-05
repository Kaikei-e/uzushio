# suite-chat-ja

Forty Japanese chat prompts, five in each of eight categories (writing, roleplay,
reasoning, math, coding, extraction, stem, humanities), six of them two-turn. They
are the owner's smoke test for the judge's validity in Japanese: the English
suite next door measures it, this one checks that nothing breaks in another
language.

Each item is a `task.json` version 3 chat task with its `conversation.json`, a
one-line `rubric.md`, and, for the ten reasoning and math items whose answer is
checkable, a `reference.md` the judge may read. `meta.json` carries the category.

The candidates are **not committed**: they are the local proposer pool's answers,
regenerated with `cmoa propose --task <item>` and copied to
`<item>/candidates/c1.txt` … `c3.txt` (with `candidates/mapping.json` recording
which proposer wrote which). Human labels come from the owner's labelling page
as a JSONL file (`item, labeler, presented, choice, at, duration_ms, note`) and
are passed to `uzushio judge calibrate --labels`.
