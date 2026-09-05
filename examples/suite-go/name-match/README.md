# name-match

Matching two written names by case folding, not by lowering.

Bug class: Unicode case folding. The seed state fails `match_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
