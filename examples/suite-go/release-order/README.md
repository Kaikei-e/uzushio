# release-order

Two-level ordering: newest day first, then by name.

Bug class: comparator that is not an ordering. The seed state fails
`order_test.go`; `reference.diff` is the fix, and `mutants/` holds three
plausible wrong fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
