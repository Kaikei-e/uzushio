`TestRankKeepsTiesInOrder` fails. `Rank` orders a board by score, highest
first. Players who share a score are level, and a level board must not
reshuffle itself: entries with equal scores have to come out in the order
they went in, so that rebuilding the board from the same submissions gives
the same page every time.

`Rank` must also leave the slice it was given untouched. Fix `Rank` in
`rank.go`. Do not change the test.
