# leaderboard-rank

Ordering by score without shuffling the players who are level.

Bug class: sort stability. The seed state fails `rank_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
