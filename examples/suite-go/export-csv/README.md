# export-csv

One CSV record, quoted only where quoting is required.

Bug class: CSV quoting. The seed state fails `row_test.go`; `reference.diff`
is the fix, and `mutants/` holds three plausible wrong fixes the test has to
reject.

```sh
./setup.sh && uzushio task doctor --task .
```
