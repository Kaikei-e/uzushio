`TestTruncate` fails on non-ASCII labels. `Truncate` shortens a label to at
most n *characters* — runes, not bytes — and appends one horizontal ellipsis
"…" to what it kept when it had to shorten it. A label that already fits in
n characters comes back exactly as it was, with no ellipsis.

Counting bytes gets both halves wrong: "日本語" is three characters and must
survive a limit of 3 untouched, and "日本語のラベル" cut to 3 must come back
as "日本語…" rather than sliced in the middle of a character.

Fix `Truncate` in `truncate.go`. Do not change the test.
