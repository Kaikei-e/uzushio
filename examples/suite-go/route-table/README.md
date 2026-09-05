# route-table

Method matching folds case; path matching does not.

Bug class: wrong map key. The seed state fails `table_test.go`;
`reference.diff` is the fix, and `mutants/` holds three plausible wrong
fixes the test has to reject.

```sh
./setup.sh && uzushio task doctor --task .
```
