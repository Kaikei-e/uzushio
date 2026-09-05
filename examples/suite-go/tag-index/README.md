# tag-index

An index whose zero value is usable, so the first Add builds the map.

Bug class: nil map write. The seed state fails `index_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
