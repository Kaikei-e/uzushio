# price-lookup

Finding the volume tier a quantity falls in, by binary search.

Bug class: off-by-one in a binary search. The seed state fails
`tier_test.go`; `reference.diff` is the fix, and `mutants/` holds three
plausible wrong fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
